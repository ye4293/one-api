package metrics

// ability_window.go — 动态优先级的 Redis 滑动窗口数据源。
//
// 数据流：
//   relay 请求完成 → RecordAbilityMetric() → ZADD ability_metrics:{channelId}:{model}
//   Master 周期    → ScanAbilityWindow()  → ZRANGEBYSCORE 读窗口 + ZREMRANGEBYSCORE 清理
//
// 存储设计：
//   Key:    ability_metrics:{channelId}:{model}
//   Member: {success:0|1}:{duration_s}:{firstword_s}:{isStream:0|1}:{uuid}
//   Score:  Unix 时间戳（秒）
//
// 成员编码用冒号分隔而非 JSON：窗口内每条记录都会被 Redis 序列化传输，JSON 体积是
// 紧凑编码的 3~4 倍，对大流量渠道（单窗口上万条）的网络/内存开销不可忽略。
//
// 无 Redis 时所有写入静默丢弃、读取返回空——动态优先级退回默认 Priority，不影响选渠道。

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
)

// AbilityMetricKeyPrefix 是 Redis Sorted Set 的 key 前缀。
// 完整 key = {prefix}{channelId}:{model}
const AbilityMetricKeyPrefix = "ability_metrics:"

const minAbilityMetricKeyTTL = 30 * time.Minute
const abilityMetricKeyTTLBuffer = 10 * time.Minute

// AbilityMetric 是单次请求完成时记录到窗口的指标。
type AbilityMetric struct {
	ChannelId        int
	Model            string
	Success          bool      // 是否成功
	Duration         float64   // 端到端时长（秒）
	FirstWordLatency float64   // 首字延迟（秒）；非流式为 0
	IsStream         bool      // 是否流式
	OccurredAt       time.Time // 发生时间；零值时用 time.Now()
}

// AbilityMetricSample 是从窗口读回的聚合样本。
type AbilityMetricSample struct {
	SuccessCount int
	FailureCount int
	// 延迟按流式/非流式分别累计，评分时由 dynamicprio 选主指标
	StreamFirstWordSum float64 // 流式首字延迟之和（秒）
	StreamCount        int     // 流式样本数（first_word>0）
	NonStreamDurSum    float64 // 非流式端到端时长之和（秒）
	NonStreamCount     int     // 非流式样本数
}

// metricMemberID 用于成员去重。Redis ZSet member 必须唯一，否则同分同 member 会覆盖。
// 单窗口内同一渠道同一模型可能有上万条记录，必须带唯一后缀。
//
// 多进程安全：纯进程内 atomic 计数器在不同进程间会从 0 重新开始，多 worker 节点
// 同时打点时可能生成相同 member（同秒同 counter）导致 ZSet 覆盖丢数据。
// 故用 metricProcessTag 在启动时生成进程级唯一前缀（pid + 随机），member 编码为
// {success}:{dur}:{fw}:{isStream}:{processTag}:{counter}，跨进程也唯一。
var metricMemberID int64

// metricProcessTag 进程级唯一标识，member 去重的跨进程维度。
// 用 pid + 启动纳秒戳组合，同机多进程也不冲突。
var metricProcessTag = func() string {
	return fmt.Sprintf("%d_%d", os.Getpid(), time.Now().UnixNano())
}()

// RecordAbilityMetric 把一次请求结果写入 Redis 滑动窗口。
// Redis 未启用时静默返回，调用方无需判空。
// ctx 用于 log 关联 request-id（异常路径），调用方一般传 gin.Context 或请求 ctx。
func RecordAbilityMetric(ctx context.Context, m AbilityMetric) {
	if !common.RedisEnabled || common.RDB == nil || m.ChannelId == 0 || m.Model == "" {
		return
	}

	ts := m.OccurredAt
	if ts.IsZero() {
		ts = time.Now()
	}
	score := float64(ts.Unix())

	succ := 0
	if m.Success {
		succ = 1
	}
	isStream := 0
	if m.IsStream {
		isStream = 1
	}

	// member: {success}:{duration}:{firstword}:{isStream}:{processTag}:{counter}
	// processTag 跨进程唯一，counter 进程内递增。两者组合保证多 worker 节点同时打点也不冲突。
	id := atomic.AddInt64(&metricMemberID, 1)
	member := fmt.Sprintf("%d:%.6f:%.6f:%d:%s:%d", succ, m.Duration, m.FirstWordLatency, isStream, metricProcessTag, id)

	key := AbilityMetricKeyPrefix + strconv.Itoa(m.ChannelId) + ":" + m.Model
	// ZADD + EXPIRE 用 pipeline 一次 RTT。
	//
	// 关于 TTL：至少 30 分钟；若动态优先级窗口被调大，则跟随窗口长度再加缓冲。
	// 因此 TTL 到期时 key 里所有 member 的 score 都已在窗口外，不会误伤有效数据。
	// 作用：**当某 (channel, model) 组合不再有请求写入**（例如渠道下线、model 名字改动、
	// 或历史脏数据）时，其 key 会在超过评分窗口后自动过期。否则会长期驻留 Redis
	// ——因为清理机制（Master 的 ZRemRangeByScore）只处理 abilities 表里能匹配到的
	// (channelId, model)，脏数据永远清不掉。
	//
	// 正常写入的 key 每次 ZADD 都刷新 TTL，永不过期。
	pipe := common.RDB.Pipeline()
	pipe.ZAdd(ctx, key, &redis.Z{Score: score, Member: member})
	pipe.Expire(ctx, key, abilityMetricKeyTTL())
	if _, err := pipe.Exec(ctx); err != nil {
		logger.Error(ctx, "ability metric ZAdd/Expire error: "+err.Error())
	}
}

func abilityMetricKeyTTL() time.Duration {
	windowMin := config.DynamicPriorityWindowMinutes
	if windowMin <= 0 {
		windowMin = 10
	}
	ttl := time.Duration(windowMin)*time.Minute + abilityMetricKeyTTLBuffer
	if ttl < minAbilityMetricKeyTTL {
		return minAbilityMetricKeyTTL
	}
	return ttl
}

// ScanAbilityWindow 读取指定 channel+model 在最近 window 内的窗口数据并聚合。
// 读取后顺带清理过期成员。返回的 sample 在无数据时为零值结构。
// ctx 一般来自定时任务的 context.Background()；用于 log 关联。
func ScanAbilityWindow(ctx context.Context, channelId int, model string, window time.Duration) (AbilityMetricSample, error) {
	var sample AbilityMetricSample
	if !common.RedisEnabled || common.RDB == nil || channelId == 0 || model == "" {
		return sample, nil
	}

	key := AbilityMetricKeyPrefix + strconv.Itoa(channelId) + ":" + model
	now := time.Now().Unix()
	minScore := float64(now) - window.Seconds()

	// 读窗口内全部成员（带 score，但 score 这里不用——成员本身已编码 success/duration）
	vals, err := common.RDB.ZRangeByScore(ctx, key, &redis.ZRangeBy{
		Min: strconv.FormatFloat(minScore, 'f', -1, 64),
		Max: strconv.FormatFloat(float64(now), 'f', -1, 64),
	}).Result()
	if err != nil {
		return sample, fmt.Errorf("ZRANGEBYSCORE %s: %w", key, err)
	}

	sample = aggregateSamples(vals)

	// 清理过期成员（窗口外的）。失败不影响本次评分，下轮再清。
	if err := common.RDB.ZRemRangeByScore(ctx, key, "-inf",
		strconv.FormatFloat(minScore, 'f', -1, 64)).Err(); err != nil {
		logger.Error(ctx, "ability metric ZRemRangeByScore error: "+err.Error())
	}

	return sample, nil
}

// ScanAbilityWindowBatch 批量读取多个 channel 在同一 model、同一 window 内的窗口数据。
//
// 相比逐个调用 ScanAbilityWindow，本函数解决动态优先级评分的两个瓶颈:
//  1. 去重：channelIds 内部去重，同一 channel 只扫一次（调用方按 group 展开的重复由此消除）。
//  2. Pipeline：所有 ZRANGEBYSCORE 合并为一次往返，清理（ZREMRANGEBYSCORE）再合并为一次，
//     把「N×2 次串行 RTT」降到「2 次 RTT」，remote Redis 下收益尤其明显。
//
// 返回 map[channelId]sample；无数据/缺失 key 对应零值 sample。整体 Redis 出错时返回已聚合的
// 部分结果与 error，由调用方决定降级（评分链路会退回静态 Priority）。
func ScanAbilityWindowBatch(ctx context.Context, channelIds []int, model string, window time.Duration) (map[int]AbilityMetricSample, error) {
	result := make(map[int]AbilityMetricSample, len(channelIds))
	if !common.RedisEnabled || common.RDB == nil || model == "" || len(channelIds) == 0 {
		return result, nil
	}

	now := time.Now().Unix()
	minScore := float64(now) - window.Seconds()
	minStr := strconv.FormatFloat(minScore, 'f', -1, 64)
	maxStr := strconv.FormatFloat(float64(now), 'f', -1, 64)

	// 去重 channelId：调用方按 (channel, group) 展开，同 channel 会重复出现
	uniq := make([]int, 0, len(channelIds))
	seen := make(map[int]struct{}, len(channelIds))
	for _, id := range channelIds {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniq = append(uniq, id)
	}
	if len(uniq) == 0 {
		return result, nil
	}

	// 一次 pipeline 批量读窗口
	readPipe := common.RDB.Pipeline()
	cmds := make(map[int]*redis.StringSliceCmd, len(uniq))
	for _, id := range uniq {
		key := AbilityMetricKeyPrefix + strconv.Itoa(id) + ":" + model
		cmds[id] = readPipe.ZRangeByScore(ctx, key, &redis.ZRangeBy{Min: minStr, Max: maxStr})
	}
	if _, err := readPipe.Exec(ctx); err != nil && err != redis.Nil {
		return result, fmt.Errorf("pipeline ZRANGEBYSCORE model=%s: %w", model, err)
	}
	for id, cmd := range cmds {
		vals, err := cmd.Result()
		if err != nil && err != redis.Nil {
			logger.Errorf(ctx, "ability metric batch read ch=%d model=%s: %s", id, model, err.Error())
			continue
		}
		result[id] = aggregateSamples(vals)
	}

	// 一次 pipeline 批量清理过期成员；失败不影响本次评分，下轮再清
	cleanPipe := common.RDB.Pipeline()
	for _, id := range uniq {
		key := AbilityMetricKeyPrefix + strconv.Itoa(id) + ":" + model
		cleanPipe.ZRemRangeByScore(ctx, key, "-inf", minStr)
	}
	if _, err := cleanPipe.Exec(ctx); err != nil && err != redis.Nil {
		logger.Error(ctx, "ability metric batch ZRemRangeByScore error: "+err.Error())
	}

	return result, nil
}

// DeleteAbilityMetrics 删除某渠道某模型的全部窗口数据。
// 用于渠道/模型被删除时清理残留，避免向已删除渠道聚合数据。
func DeleteAbilityMetrics(ctx context.Context, channelId int, model string) {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	key := AbilityMetricKeyPrefix + strconv.Itoa(channelId) + ":" + model
	if err := common.RDB.Del(ctx, key).Err(); err != nil {
		logger.Error(ctx, "ability metric Del error: "+err.Error())
	}
}

// parseMetricMember 解析 member: {success}:{duration}:{firstword}:{isStream}:{counter}
func parseMetricMember(member string) (succ bool, dur, fw float64, isStream bool, ok bool) {
	parts := strings.Split(member, ":")
	if len(parts) < 4 {
		return false, 0, 0, false, false
	}
	s, err1 := strconv.Atoi(parts[0])
	d, err2 := strconv.ParseFloat(parts[1], 64)
	f, err3 := strconv.ParseFloat(parts[2], 64)
	st, err4 := strconv.Atoi(parts[3])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return false, 0, 0, false, false
	}
	return s == 1, d, f, st == 1, true
}

// aggregateSamples 把一批窗口成员聚合为 AbilityMetricSample。纯函数，便于单测。
//
// 延迟维度只统计成功样本：失败请求的 duration 是「到失败为止的耗时」，
// 不代表渠道正常服务的响应速度，混入会把慢失败拉低正常渠道的延迟分。
func aggregateSamples(members []string) AbilityMetricSample {
	var sample AbilityMetricSample
	for _, member := range members {
		succ, dur, fw, isStream, ok := parseMetricMember(member)
		if !ok {
			continue
		}
		if succ {
			sample.SuccessCount++
		} else {
			sample.FailureCount++
		}
		if !succ {
			continue
		}
		if isStream && fw > 0 {
			sample.StreamFirstWordSum += fw
			sample.StreamCount++
		} else if !isStream && dur > 0 {
			sample.NonStreamDurSum += dur
			sample.NonStreamCount++
		}
	}
	return sample
}
