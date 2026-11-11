package model

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
)

// Keep the existing Response-ID key/value format for both synchronous and
// asynchronous writers, including the companion cache-duration key.
func setResponseIDToRedis(ctx context.Context, client *redis.Client, id, channel string, expire int64, keyIndex int) error {
	if client == nil {
		return errors.New("redis unavailable")
	}
	if channel == "" {
		return errors.New("empty channel")
	}
	if id == "" {
		return errors.New("empty id")
	}
	if expire <= 0 {
		return errors.New("invalid expire time")
	}
	value := channel
	if keyIndex >= 0 {
		value = fmt.Sprintf("%s:%d", channel, keyIndex)
	}
	ttl := time.Duration(expire) * time.Minute
	pipe := client.Pipeline()
	pipe.Set(ctx, fmt.Sprintf(common.CacheClaudeRsID, id), value, ttl)
	pipe.Set(ctx, fmt.Sprintf(common.CacheClaudeLength, id), expire, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("failed to set response ID cache to redis: %w", err)
	}
	return nil
}

// No queue: a slow Redis must not hold up responses or accumulate goroutines.
var responseIDCacheWriteSlots = make(chan struct{}, 16)

// CacheResponseIdToChannelAsync is called after forwarding a Gemini response.
// Only immutable IDs and the Redis client are retained after the request ends.
// The mapping becomes available to X-Response-ID lookups when this write finishes.
func CacheResponseIdToChannelAsync(responseID string, channelID, keyIndex int, logPrefix string) {
	if responseID == "" || channelID <= 0 {
		return
	}
	client := common.RDB
	if !common.RedisEnabled || client == nil {
		return
	}
	select {
	case responseIDCacheWriteSlots <- struct{}{}:
		go func() {
			defer func() { <-responseIDCacheWriteSlots }()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.Errorf(ctx, "[%s] Response-ID cache panic channel=%d: %v", logPrefix, channelID, recovered)
				}
			}()
			if err := setResponseIDToRedis(ctx, client, responseID, strconv.Itoa(channelID), 1440, keyIndex); err != nil {
				logger.Warnf(ctx, "[%s] Response-ID cache write failed channel=%d keyIndex=%d: %v", logPrefix, channelID, keyIndex, err)
			}
		}()
	default:
		logger.SysLog(fmt.Sprintf("[%s] Response-ID cache write skipped: workers busy channel=%d", logPrefix, channelID))
	}
}
