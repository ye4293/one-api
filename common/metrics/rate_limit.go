package metrics

// rate_limit.go — 渠道级 429 即时反馈层。
//
// 存在意义：动态优先级评分是慢变信号（10 分钟滑动窗口 + 每分钟评分），无法秒级反应
// "某渠道刚开始被打爆"。选渠道热路径把流量集中到高分渠道后，上游 rate limit 触发
// 429，但 dp 分数还没来得及下调 —— 客户体验到间歇性 429，直到下一轮评分才缓解。
//
// 本模块提供**60 秒短窗口**的 429 计数：
//   - relay 失败路径拿到 status_code=429 时打点
//   - selectByDynamicPriority 在档内加权前批量读一次，命中阈值的渠道降权到最低
//   - 60 秒后自动过期，无需人工介入
//
// 与 ability_window 的关系：ability_window 记录所有请求（成功/失败），供 dp 评分；
// 本模块只记录 429 一类信号，供选渠道热路径**秒级**降权。两者独立，互不影响。

import (
	"context"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
)

// RateLimitKeyPrefix Redis 中 429 计数的 key 前缀。
// 完整 key = {prefix}{channel_id}
const RateLimitKeyPrefix = "channel_recent_429:"

// RateLimitWindow 429 反馈窗口。60 秒足够反应 rate limit burst，也让 60s
// 之后被 rate limit 缓解的渠道自动恢复到正常权重，无需人工重置。
const RateLimitWindow = 60 * time.Second

// RateLimitKeyTTL Redis key 的 TTL。稍长于窗口，避免边界 tick 期间旧 member
// 提前被删除影响 ZCount 精度。
const RateLimitKeyTTL = 90 * time.Second

// rlMemberID 进程内 atomic 计数器，用于生成 ZSet 唯一 member。
var rlMemberID int64

// RecordRateLimit 记录一次 429 事件到 Redis。
// Redis 未启用/RDB nil/channelId=0 时静默返回。
func RecordRateLimit(ctx context.Context, channelId int) {
	if !common.RedisEnabled || common.RDB == nil || channelId == 0 {
		return
	}
	ts := time.Now().Unix()
	// member 编码：{ts}:{processTag}:{counter}，跨进程唯一
	id := atomic.AddInt64(&rlMemberID, 1)
	member := fmt.Sprintf("%d:%s:%d", ts, metricProcessTag, id)
	key := RateLimitKeyPrefix + strconv.Itoa(channelId)

	pipe := common.RDB.Pipeline()
	pipe.ZAdd(ctx, key, &redis.Z{Score: float64(ts), Member: member})
	pipe.Expire(ctx, key, RateLimitKeyTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		logger.Error(ctx, "record rate limit ZAdd/Expire error: "+err.Error())
	}
}

// GetRecentRateLimits 批量获取多个渠道最近 RateLimitWindow 内的 429 次数。
// 用一次 pipeline 完成所有 ZCount 查询，选渠道热路径只增加 1 次 Redis RTT。
//
// 返回 map[channelId]count。Redis 未启用或全部查询失败时返回空 map（不报错），
// 调用方按"计数=0"处理，等价于关闭本机制 —— 不影响选渠道正常工作。
func GetRecentRateLimits(ctx context.Context, channelIds []int) map[int]int {
	result := make(map[int]int, len(channelIds))
	if !common.RedisEnabled || common.RDB == nil || len(channelIds) == 0 {
		return result
	}
	minScore := time.Now().Add(-RateLimitWindow).Unix()
	minStr := strconv.FormatInt(minScore, 10)

	// 一次 pipeline 批量 ZCount
	pipe := common.RDB.Pipeline()
	cmds := make(map[int]*redis.IntCmd, len(channelIds))
	seen := make(map[int]struct{}, len(channelIds))
	for _, id := range channelIds {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		key := RateLimitKeyPrefix + strconv.Itoa(id)
		cmds[id] = pipe.ZCount(ctx, key, minStr, "+inf")
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		logger.Errorf(ctx, "batch get rate limits pipeline error: %s", err.Error())
		return result
	}
	for id, cmd := range cmds {
		cnt, err := cmd.Result()
		if err != nil && err != redis.Nil {
			// 单个 key 失败不中断，其他继续
			continue
		}
		if cnt > 0 {
			result[id] = int(cnt)
		}
	}
	return result
}
