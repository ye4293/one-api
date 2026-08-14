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
	"github.com/songquanpeng/one-api/common/logger"
)

// AbilityMetricKeyPrefix 是 Redis Sorted Set 的 key 前缀。
// 完整 key = {prefix}{channelId}:{model}
const AbilityMetricKeyPrefix = "ability_metrics:"

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
func RecordAbilityMetric(m AbilityMetric) {
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
	ctx := context.Background()
	// 不设 TTL：由 Master 周期 ZREMRANGEBYSCORE 清理。若设 TTL 反而会在清理前过早整 key 过期，
	// 丢失还在窗口期内的数据——尤其低频模型可能几小时才一次请求。
	if err := common.RDB.ZAdd(ctx, key, &redis.Z{Score: score, Member: member}).Err(); err != nil {
		logger.SysError("ability metric ZAdd error: " + err.Error())
	}
}

// ScanAbilityWindow 读取指定 channel+model 在最近 window 内的窗口数据并聚合。
// 读取后顺带清理过期成员。返回的 sample 在无数据时为零值结构。
func ScanAbilityWindow(channelId int, model string, window time.Duration) (AbilityMetricSample, error) {
	var sample AbilityMetricSample
	if !common.RedisEnabled || common.RDB == nil || channelId == 0 || model == "" {
		return sample, nil
	}

	key := AbilityMetricKeyPrefix + strconv.Itoa(channelId) + ":" + model
	ctx := context.Background()
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
		logger.SysError("ability metric ZRemRangeByScore error: " + err.Error())
	}

	return sample, nil
}

// DeleteAbilityMetrics 删除某渠道某模型的全部窗口数据。
// 用于渠道/模型被删除时清理残留，避免向已删除渠道聚合数据。
func DeleteAbilityMetrics(channelId int, model string) {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	key := AbilityMetricKeyPrefix + strconv.Itoa(channelId) + ":" + model
	if err := common.RDB.Del(context.Background(), key).Err(); err != nil {
		logger.SysError("ability metric Del error: " + err.Error())
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
