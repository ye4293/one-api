package model

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/env"
	"github.com/songquanpeng/one-api/common/logger"
)

// ResponseIdRegionInfo 缓存值的三段结构：channelId:keyIndex:region
// region 为第三段（可选），用于 Azure 等加密内容绑定资源区域时做同区域兜底。
// 旧缓存值兼容：只有 channelId 或 channelId:keyIndex 两段时 region 为空。
type ResponseIdRegionInfo struct {
	ChannelID int
	KeyIndex  int // -1 表示未记录
	Region    string
}

// ResponsesAffinityEnabled responses 区域亲和总开关。
// 默认开启：本特性 fail-open（查询失败/未命中仅退回随机选渠，不拒绝请求），
// 关闭后写入路径短路，行为回到改动前。
var ResponsesAffinityEnabled = env.Bool("RESPONSES_AFFINITY_ENABLED", true)

// ResponsesAffinityTTLMinutes response_id -> 渠道/区域 映射的 Redis TTL（分钟）。
// 默认 1440（24h），与历史行为一致。
var ResponsesAffinityTTLMinutes = env.Int("RESPONSES_AFFINITY_TTL_MINUTES", 1440)

// SetResponseIdRegionToRedis 写入 response_id -> channelId:keyIndex:region 映射。
// 与 SetClaudeCacheIdToRedis 同 key（cache_response_id_%s），值格式向后兼容。
// region 为空时退化为两段格式，与旧写入完全一致。
func SetResponseIdRegionToRedis(id string, channelId int, keyIndex int, region string, expireMinutes int64) error {
	if !common.RedisEnabled {
		return errors.New("redis disabled")
	}
	if id == "" {
		return errors.New("empty id")
	}
	if channelId <= 0 {
		return errors.New("invalid channel id")
	}
	if expireMinutes <= 0 {
		expireMinutes = 1440
	}

	cacheKey := fmt.Sprintf(common.CacheClaudeRsID, id)
	// 缓存值格式：channelId / channelId:keyIndex / channelId:keyIndex:region
	cacheValue := fmt.Sprintf("%d", channelId)
	if keyIndex >= 0 {
		cacheValue = fmt.Sprintf("%s:%d", cacheValue, keyIndex)
	}
	if region != "" {
		cacheValue = fmt.Sprintf("%s:%s", cacheValue, region)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := common.RDB.Set(ctx, cacheKey, cacheValue, time.Duration(expireMinutes)*time.Minute).Err(); err != nil {
		return fmt.Errorf("failed to set response id mapping: %w", err)
	}
	// 命中续期由写入承担：活跃会话每轮都会写新 id，无需额外 EXPIRE。
	return nil
}

// GetResponseIdRegionFromRedis 读取 response_id 映射（含 region 三段解析）。
// 返回 channelId、keyIndex、region；解析失败或 key 不存在时返回 0, -1, ""。
func GetResponseIdRegionFromRedis(id string) (int, int, string) {
	if !common.RedisEnabled || id == "" {
		return 0, -1, ""
	}
	cacheKey := fmt.Sprintf(common.CacheClaudeRsID, id)
	value, err := common.RedisGet(cacheKey)
	if err != nil || value == "" {
		return 0, -1, ""
	}
	info := ParseResponseIdCacheValue(value)
	if info.ChannelID <= 0 {
		return 0, -1, ""
	}
	return info.ChannelID, info.KeyIndex, info.Region
}

// ParseResponseIdCacheValue 解析缓存值，兼容历史两段/单段格式。
// 格式：channelId | channelId:keyIndex | channelId:keyIndex:region
// region 为字母数字与连字符（Azure region 如 eastus、southeast-asia），不含 ":"，
// SplitN 不会切破。
func ParseResponseIdCacheValue(value string) ResponseIdRegionInfo {
	info := ResponseIdRegionInfo{ChannelID: 0, KeyIndex: -1, Region: ""}
	if value == "" {
		return info
	}
	parts := strings.SplitN(value, ":", 3)
	channelID, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || channelID <= 0 {
		return info
	}
	info.ChannelID = channelID
	if len(parts) >= 2 {
		if idx, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
			info.KeyIndex = idx
		}
	}
	if len(parts) == 3 {
		info.Region = strings.TrimSpace(parts[2])
	}
	return info
}

// CacheResponseIdWithRegionToChannel 带区域的通用缓存入口。
// 供 relay 层各响应 handler 调用：region 为空时行为与 CacheResponseIdToChannel 一致。
func CacheResponseIdWithRegionToChannel(responseId string, channelId int, keyIndex int, region string, logPrefix string) {
	if responseId == "" || channelId <= 0 {
		return
	}
	if !ResponsesAffinityEnabled {
		return
	}
	region = sanitizeRegion(region)
	expireMinutes := int64(ResponsesAffinityTTLMinutes)
	if err := SetResponseIdRegionToRedis(responseId, channelId, keyIndex, region, expireMinutes); err != nil {
		// Redis 写入失败不影响主流程，只记录日志
		logger.SysLog(fmt.Sprintf("[%s] Failed to cache response_id=%s to channel_id=%d keyIndex=%d region=%s: %v",
			logPrefix, responseId, channelId, keyIndex, region, err))
	} else {
		logger.SysLog(fmt.Sprintf("[%s] Cached response_id=%s -> channel_id=%d keyIndex=%d region=%s (TTL: %dm)",
			logPrefix, responseId, channelId, keyIndex, region, ResponsesAffinityTTLMinutes))
	}
}

// sanitizeRegion 清洗 region，去掉不可入缓存值的字符（防 ":" 注入破坏三段格式）。
func sanitizeRegion(region string) string {
	region = strings.TrimSpace(region)
	if region == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range region {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ResolveRelayRegion 解析本次请求实际命中的上游区域（供响应侧写入 Redis 三段缓存）。
// 优先读上游响应头 x-ms-region（Azure 全局返回，代理透传可读）；
// 为空时回退渠道 Config JSON 的 region 字段（CRM 建渠道时写入）。
// gin context 的 channel_id 在重试后由 SetupContextForSelectedChannel 更新为最终成功渠道，
// 因此重试场景下也能解析到正确区域。
func ResolveRelayRegion(c *gin.Context, resp *http.Response) string {
	if resp != nil {
		if region := sanitizeRegion(resp.Header.Get("x-ms-region")); region != "" {
			return region
		}
	}
	if c != nil {
		if ch, err := CacheGetChannel(c.GetInt("channel_id")); err == nil && ch != nil {
			return sanitizeRegion(GetAzureChannelRegion(ch))
		}
	}
	return ""
}
