package controller

// dynamic_priority.go — Master 节点定时计算动态优先级并落库。
//
// 流程（仅 Master 节点，每 DynamicPriorityCalcIntervalMinutes 分钟一次）：
//   1. 查所有 enabled 的 (channel_id, model, group) 去重三元组
//   2. 按 model 分组；同 model 内一次 pipeline 批量聚合各渠道窗口指标（按 channelId 去重）+ 读 UnitPrice
//   3. 调 dynamicprio.ScoreChannels 算分
//   4. BatchUpdateDynamicPriority 批量落库
//
// 无 Redis / 无数据时退化为：不更新分数，dynamic_priority 保持上一轮值或 0，
// 选渠道热路径回退到静态 Priority。不会因评分失败影响正常服务。

import (
	"context"
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
	// 定时任务无请求 ctx；用 Background 只是给底层 log 接口一个非 nil ctx，
	// 避免 log 里 ctx.Value(RequestID) 为 nil 打不到 request id 字段（此路径下本来就没有）。
	ctx := context.Background()

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
		logger.Error(ctx, "dynamic priority: load candidates failed: "+err.Error())
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

	// 一次批量拉取所有 enabled 渠道的 UnitPrice，避免 buildStatsForModel 里对每个
	// (model, channel) 组合发单条 GetChannelById 查询。
	//
	// 修复前的性能坑：`priceCache` 是 buildStatsForModel 的局部变量，每个 model 建一份空
	// cache。同一 channel 出现在 N 个 model 下就会被 GetChannelById 查询 N 次。
	// 生产 62K 渠道 × 544 model × 平均 ~300 channel/model → 十几万次单条 SELECT ×
	// 2ms RTT ≈ 5 分钟。这里改成一次批量 SELECT，把 O(N_models × N_channels) 降到 O(1)。
	//
	// 读失败不中止本轮：priceCache 空时 buildStatsForModel 的价格维度回退到中位分，
	// 只是本轮无价格偏好，成功率+延迟维度仍能评分。
	priceCache := make(map[int]float64)
	var priceRows []struct {
		Id        int     `gorm:"column:id"`
		UnitPrice float64 `gorm:"column:unit_price"`
	}
	if err := model.DB.Table("channels").
		Where("status = ?", common.ChannelStatusEnabled).
		Select("id, unit_price").
		Find(&priceRows).Error; err != nil {
		logger.Errorf(ctx, "dynamic priority: load unit prices failed (fallback to no price bias): %s", err.Error())
	} else {
		for _, p := range priceRows {
			priceCache[p.Id] = p.UnitPrice
		}
	}

	for modelName, groupCands := range byModel {
		stats, candIdx := buildStatsForModel(ctx, modelName, groupCands, window, priceCache)
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
		logger.Errorf(ctx, "dynamic priority: batch update failed (%d items): %s", len(allUpdates), err.Error())
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
//
// priceCache 由调用方（runDynamicPriorityCalcOnce）一次批量拉取传入，避免此处对每个
// channel 发单条 SQL —— 生产 60K+ 渠道 × 500+ model 场景下能把整轮耗时从数分钟压到秒级。
// 缺失 key（例如渠道刚新建、批量拉取时间点之后）视为未配置价格 UnitPrice=0，
// 价格维度自然回退到中位分。
func buildStatsForModel(ctx context.Context, modelName string, cands []abilityCandidate, window time.Duration, priceCache map[int]float64) ([]dynamicprio.ChannelStat, []int) {
	stats := make([]dynamicprio.ChannelStat, 0, len(cands))
	candIdx := make([]int, 0, len(cands))
	anyData := false

	// 批量读窗口：一次 pipeline 拿到该 model 下所有 channel 的样本，内部按 channelId 去重。
	// 消除「同 channel 多 group 各扫一次」的重复，并把 N×2 次串行 RTT 降到 2 次。
	channelIds := make([]int, 0, len(cands))
	for _, c := range cands {
		channelIds = append(channelIds, c.ChannelId)
	}
	samples, err := metrics.ScanAbilityWindowBatch(ctx, channelIds, modelName, window)
	if err != nil {
		// 整体读失败：本 model 全部按无数据处理，返回 nil 让调用方跳过（保留旧分，回退静态优先级）
		logger.Errorf(ctx, "dynamic priority: batch scan window model=%s: %s", modelName, err.Error())
		return nil, nil
	}

	for i, c := range cands {
		sample := samples[c.ChannelId] // 缺失 = 零值，等价无数据

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

		// 价格：从调用方批量拉取的 priceCache 里读，缺失视为未配置（0）
		if price, ok := priceCache[c.ChannelId]; ok {
			st.UnitPrice = price
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
