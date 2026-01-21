package cache

import (
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/anthropic"
)

// HandleClaudeCache 处理 Claude 缓存信息并记录到 Redis
// responseID: Claude 响应 ID
// usage: Claude Usage 信息
// c: gin context
func HandleClaudeCache(c *gin.Context, responseID string, usage *anthropic.Usage) {
	channelId := strconv.Itoa(c.GetInt("channel_id"))

	// 调试日志：函数入口
	logger.SysLog(fmt.Sprintf("[Claude Cache Debug] 进入handleClaudeCache - ResponseID: %s, ChannelID: %s", responseID, channelId))

	if usage == nil {
		logger.SysLog(fmt.Sprintf("[Claude Cache Debug] Usage为空，跳过缓存处理 - ResponseID: %s", responseID))
		return
	}

	// 调试日志：打印完整的 usage 信息
	logger.SysLog(fmt.Sprintf("[Claude Cache Debug] Usage详情 - ResponseID: %s, InputTokens: %d, OutputTokens: %d, CacheReadInputTokens: %d",
		responseID, usage.InputTokens, usage.OutputTokens, usage.CacheReadInputTokens))

	if usage.CacheCreation != nil {
		logger.SysLog(fmt.Sprintf("[Claude Cache Debug] CacheCreation存在 - ResponseID: %s, Ephemeral5m: %d, Ephemeral1h: %d",
			responseID, usage.CacheCreation.Ephemeral5mInputTokens, usage.CacheCreation.Ephemeral1hInputTokens))
	} else {
		logger.SysLog(fmt.Sprintf("[Claude Cache Debug] CacheCreation为空 - ResponseID: %s", responseID))
	}

	cacheInfo := make(map[string]interface{})
	expireTime := int64(5) // 默认5分钟
	shouldCache := false

	// 根据缓存类型设置过期时间
	if usage.CacheCreation != nil {
		if usage.CacheCreation.Ephemeral5mInputTokens > 0 {
			cacheInfo["cache_5m_tokens"] = usage.CacheCreation.Ephemeral5mInputTokens
			expireTime = 5
			shouldCache = true
			logger.SysLog(fmt.Sprintf("[Claude Cache Debug] 检测到5分钟缓存创建 - ResponseID: %s, Tokens: %d",
				responseID, usage.CacheCreation.Ephemeral5mInputTokens))
		}
		if usage.CacheCreation.Ephemeral1hInputTokens > 0 {
			cacheInfo["cache_1h_tokens"] = usage.CacheCreation.Ephemeral1hInputTokens
			expireTime = 60
			shouldCache = true
			logger.SysLog(fmt.Sprintf("[Claude Cache Debug] 检测到1小时缓存创建 - ResponseID: %s, Tokens: %d",
				responseID, usage.CacheCreation.Ephemeral1hInputTokens))
		}
	}

	// 检查缓存读取情况
	if usage.CacheReadInputTokens > 0 {
		cacheInfo["cache_read_tokens"] = usage.CacheReadInputTokens
		shouldCache = true
		logger.SysLog(fmt.Sprintf("[Claude Cache Debug] 检测到缓存读取 - ResponseID: %s, ReadTokens: %d",
			responseID, usage.CacheReadInputTokens))

		// 读取上次的缓存时长
		oldResponseID := c.GetHeader("X-Response-ID")
		logger.SysLog(fmt.Sprintf("[Claude Cache Debug] 尝试读取上次缓存时长 - NewResponseID: %s, OldResponseID: %s",
			responseID, oldResponseID))

		if oldResponseID != "" {
			cacheKey := fmt.Sprintf(common.CacheClaudeLength, oldResponseID)
			logger.SysLog(fmt.Sprintf("[Claude Cache Debug] Redis Key: %s", cacheKey))

			cacheLength, err := common.RedisGet(cacheKey)
			if err != nil {
				logger.Error(c.Request.Context(), fmt.Sprintf("[Claude Cache Debug] 读取缓存时长失败 - Key: %s, Error: %s",
					cacheKey, err.Error()))
			} else if cacheLength != "" {
				expireTime1, parseErr := strconv.ParseInt(cacheLength, 10, 64)
				if parseErr == nil {
					expireTime = expireTime1
					logger.SysLog(fmt.Sprintf("[Claude Cache Debug] 成功读取缓存时长 - OldResponseID: %s, ExpireTime: %d分钟",
						oldResponseID, expireTime))
				} else {
					logger.Error(c.Request.Context(), fmt.Sprintf("[Claude Cache Debug] 解析缓存时长失败 - Value: %s, Error: %s",
						cacheLength, parseErr.Error()))
				}
			} else {
				logger.SysLog(fmt.Sprintf("[Claude Cache Debug] Redis中未找到缓存时长 - Key: %s", cacheKey))
			}
		} else {
			logger.SysLog("[Claude Cache Debug] X-Response-ID header为空，无法读取上次缓存时长")
		}
		expireTime = 5
	}

	// 记录到 Redis
	if shouldCache {
		logger.SysLog(fmt.Sprintf("[Claude Cache Debug] 准备写入Redis - ResponseID: %s, ChannelID: %s, ExpireTime: %d分钟",
			responseID, channelId, expireTime))

		if err := dbmodel.SetClaudeCacheIdToRedis(responseID, channelId, expireTime); err != nil {
			logger.Error(c.Request.Context(), fmt.Sprintf("[Claude Cache] 写入Redis失败 - ResponseID: %s, Error: %s",
				responseID, err.Error()))
		} else {
			logger.SysLog(fmt.Sprintf("[Claude Cache] 成功写入Redis - RequestID: %s, CacheInfo: %v, ChannelID: %s, Expire: %dm",
				responseID, cacheInfo, channelId, expireTime))
		}
	} else {
		logger.SysLog(fmt.Sprintf("[Claude Cache Debug] 无需缓存 - ResponseID: %s, 原因: CacheCreation和CacheRead都为0", responseID))
	}
}
