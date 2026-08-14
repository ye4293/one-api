package controller

// dynamic_priority.go — Master 节点定时计算动态优先级并落库。
//
// 流程（仅 Master 节点，每 DynamicPriorityCalcIntervalMinutes 分钟一次）：
//   1. 查所有 enabled 的 (channel_id, model, group) 去重三元组
//   2. 按 model 分组；同 model 内对每个渠道 ScanAbilityWindow 聚合窗口指标 + 读 UnitPrice
//   3. 调 dynamicprio.ScoreChannels 算分
//   4. BatchUpdateDynamicPriority 批量落库
//
// 无 Redis / 无数据时退化为：不更新分数，dynamic_priority 保持上一轮值或 0，
// 选渠道热路径回退到静态 Priority。不会因评分失败影响正常服务。

import (
	"fmt"
	"sync"
	"time"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/dynamicprio"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/common/metrics"
	"github.com/songquanpeng/one-api/model"
)

// abilityCandidate 是参与评分的候选渠道（去重后的 channel+model+group 三元组）。
type abilityCandidate struct {
	ChannelId int
	Model     string
	Group     string
}

var dynamicPriorityTaskOnce sync.Once

// StartDynamicPriorityTask 启动动态优先级定时计算（仅 Master 节点）。
func StartDynamicPriorityTask() {
	dynamicPriorityTaskOnce.Do(func() {
		if !config.IsMasterNode {
			return
		}
		common.SafeGoroutine(func() {
			// 启动后等一个周期再首次执行：给 Redis 窗口积累数据的时间，
			// 也避免与启动期的 ChannelCache 初始化等任务抢资源。
			const pollTick = 15 * time.Second
			ticker := time.NewTicker(pollTick)
			defer ticker.Stop()

			lastRun := time.Time{}
			for range ticker.C {
				if !config.DynamicPriorityEnabled {
					continue
				}
				intervalMin := config.DynamicPriorityCalcIntervalMinutes
				if intervalMin <= 0 {
					intervalMin = 5
				}
				targetInterval := time.Duration(intervalMin) * time.Minute
				// 首次执行：lastRun 零值时直接跑一次
				if lastRun.IsZero() || time.Since(lastRun) >= targetInterval {
					lastRun = time.Now()
					runDynamicPriorityCalcOnce()
				}
			}
		})
	})
}

// dynamicPriorityCalcLockKey 计算器分布式锁 key。多 Master 节点（误配或滚动更新期间
// 两个 master 同时存活）会各自跑定时器，无锁会重复 SCAN/计算/UPDATE 虽幂等但浪费且
// 可能造成分数短暂抖动。锁 TTL 5 分钟覆盖单轮计算（候选通常秒级完成）。
const dynamicPriorityCalcLockKey = "dynamic_priority:calc:lock"
const dynamicPriorityCalcLockTTL = 5 * time.Minute

// runDynamicPriorityCalcOnce 执行一轮评分计算。
func runDynamicPriorityCalcOnce() {
	// 分布式锁：仅一个 Master 节点执行本轮计算。拿不到锁直接跳过，下轮再来。
	// Redis 未启用时 RedisLockAcquire 返回 "local"，等价于「拿到锁」（单机模式无并发问题）。
	token := common.RedisLockAcquire(dynamicPriorityCalcLockKey, dynamicPriorityCalcLockTTL)
	if token == "" {
		logger.SysLog("dynamic priority: calc lock held by another node, skip this round")
		return
	}
	defer common.RedisLockRelease(dynamicPriorityCalcLockKey, token)

	start := time.Now()

	candidates, err := loadAbilityCandidates()
	if err != nil {
		logger.SysError("dynamic priority: load candidates failed: " + err.Error())
		return
	}
	if len(candidates) == 0 {
		return
	}

	// 按 model 分组：评分是「同 model 内多渠道相对排名」，必须按 model 切分。
	byModel := make(map[string][]abilityCandidate)
	for _, c := range candidates {
		byModel[c.Model] = append(byModel[c.Model], c)
	}

	window := time.Duration(config.DynamicPriorityWindowMinutes) * time.Minute
	if window <= 0 {
		window = 10 * time.Minute
	}
	weights := dynamicprio.Weights{
		Success: config.DynamicPriorityWeightSuccess,
		Latency: config.DynamicPriorityWeightLatency,
		Price:   config.DynamicPriorityWeightPrice,
	}

	var allUpdates []model.DynamicPriorityUpdate
	var channelsScored, channelsSkipped int

	for modelName, groupCands := range byModel {
		stats, candIdx := buildStatsForModel(modelName, groupCands, window)
		if len(stats) == 0 {
			channelsSkipped += len(groupCands)
			continue
		}
		scores := dynamicprio.ScoreChannels(stats, weights)

		for i, sc := range scores {
			cand := groupCands[candIdx[i]]
			dp := int64(sc.Score)
			if !sc.HasData {
				// 无数据渠道：写 0，让选渠道热路径回退到静态 Priority。
				// 用 0 而非保留旧值：旧值可能来自已被禁用前的数据，过期且不可信。
				dp = 0
			}
			allUpdates = append(allUpdates, model.DynamicPriorityUpdate{
				ChannelId:       cand.ChannelId,
				Model:           cand.Model,
				Group:           cand.Group,
				DynamicPriority: dp,
			})
			channelsScored++
		}
	}

	if len(allUpdates) == 0 {
		return
	}

	if err := model.BatchUpdateDynamicPriority(allUpdates); err != nil {
		logger.SysError(fmt.Sprintf("dynamic priority: batch update failed (%d items): %s", len(allUpdates), err.Error()))
		return
	}

	logger.SysLog(fmt.Sprintf(
		"dynamic priority calc done: models=%d channels_scored=%d channels_skipped=%d updates=%d cost=%s",
		len(byModel), channelsScored, channelsSkipped, len(allUpdates), time.Since(start),
	))
}

// buildStatsForModel 为某 model 下的所有候选渠道构造 ChannelStat。
// 返回 stats 和对应的 candidate 索引（candIdx[i] 是 stats[i] 对应的候选）。
// 只返回有至少一个有效样本的渠道——纯无数据的渠道也要保留（写 0 分回退静态优先级），
// 但若整组 Redis 全无数据则返回空，跳过该 model。
func buildStatsForModel(modelName string, cands []abilityCandidate, window time.Duration) ([]dynamicprio.ChannelStat, []int) {
	stats := make([]dynamicprio.ChannelStat, 0, len(cands))
	candIdx := make([]int, 0, len(cands))
	anyData := false

	// UnitPrice 按 channelId 去重读取，避免同渠道多 group 重复查
	priceCache := make(map[int]float64)

	for i, c := range cands {
		sample, err := metrics.ScanAbilityWindow(c.ChannelId, modelName, window)
		if err != nil {
			logger.SysError(fmt.Sprintf("dynamic priority: scan window ch=%d model=%s: %s", c.ChannelId, modelName, err.Error()))
			continue
		}

		total := sample.SuccessCount + sample.FailureCount
		if total > 0 {
			anyData = true
		}

		st := dynamicprio.ChannelStat{
			ChannelId:    c.ChannelId,
			SuccessCount: sample.SuccessCount,
			FailureCount: sample.FailureCount,
			TotalCount:   total,
		}
		if total > 0 {
			st.SuccessRate = float64(sample.SuccessCount) / float64(total)
		}
		// 延迟：流式用平均首字，非流式用平均端到端。窗口内可能两种都有，
		// 评分函数会按多数派选主指标，这里两个都填。
		if sample.StreamCount > 0 {
			st.AvgFirstTokenMs = sample.StreamFirstWordSum / float64(sample.StreamCount) * 1000
		}
		if sample.NonStreamCount > 0 {
			st.AvgLatencyMs = sample.NonStreamDurSum / float64(sample.NonStreamCount) * 1000
		}

		// 价格：同 channel 多 group 共享一个价格，缓存
		if price, ok := priceCache[c.ChannelId]; ok {
			st.UnitPrice = price
		} else {
			ch, perr := model.GetChannelById(c.ChannelId, false)
			if perr == nil && ch != nil {
				st.UnitPrice = ch.UnitPrice
				priceCache[c.ChannelId] = ch.UnitPrice
			}
		}

		stats = append(stats, st)
		candIdx = append(candIdx, i)
	}

	// 整组无任何数据：返回空，调用方跳过该 model（不写库，保留旧分）
	if !anyData {
		return nil, nil
	}
	return stats, candIdx
}

// loadAbilityCandidates 查所有 enabled 的 (channel_id, model, group) 去重三元组。
// 被禁用的 Ability（enabled=false）不参与评分——这是与「模型级禁用」特性的硬隔离。
func loadAbilityCandidates() ([]abilityCandidate, error) {
	groupCol := "`group`"
	if common.UsingPostgreSQL {
		groupCol = `"group"`
	}
	var rows []abilityCandidate
	err := model.DB.Model(&model.Ability{}).
		Select("DISTINCT channel_id, model, "+groupCol+" as \"group\"").
		Where("enabled = ?", true).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}
