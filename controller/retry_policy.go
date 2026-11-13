package controller

import (
	"context"
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/service"
)

type azureRetryRegionKey struct{}

func isAzureRetry(ctx context.Context) bool {
	_, restricted := ctx.Value(azureRetryRegionKey{}).(string)
	return restricted
}

// newRetryContext 保留入口为带签名请求设置的 Gemini 类型，以及已有 Azure 区域约束。
func newRetryContext(c *gin.Context) context.Context {
	ctx := c.Request.Context()
	if _, exists := dbmodel.ResponseOriginFromContext(ctx); !exists && dbmodel.ResponseOriginFallbackReason(ctx) == "" {
		if id := c.GetHeader("X-Response-ID"); id != "" {
			if origin, err := dbmodel.LookupResponseOrigin(id); err == nil {
				ctx = dbmodel.WithResponseOrigin(ctx, origin)
			}
		}
	}
	if origin, exists := dbmodel.ResponseOriginFromContext(ctx); exists {
		if origin.ChannelType == common.ChannelTypeAzure {
			ctx = context.WithValue(ctx, azureRetryRegionKey{}, origin.Region)
		}
		c.Request = c.Request.WithContext(ctx)
		return ctx
	}
	value, _ := c.Get("Config")
	cfg, _ := value.(dbmodel.ChannelConfig)
	channelType := c.GetInt("channel")
	path := strings.TrimRight(c.Request.URL.Path, "/")
	if path == "/v1/chat/completions" || path == "/v1/responses" || path == "/v1/responses/compact" {
		ctx = dbmodel.WithResponseOrigin(ctx, dbmodel.ResponseOrigin{ChannelID: c.GetInt("channel_id"), KeyIndex: c.GetInt("key_index"), ChannelType: channelType, Region: strings.TrimSpace(cfg.Region)})
	}
	if channelType == common.ChannelTypeAzure {
		ctx = context.WithValue(ctx, azureRetryRegionKey{}, strings.TrimSpace(cfg.Region))
	}
	c.Request = c.Request.WithContext(ctx)
	return ctx
}

// selectRetryChannelOnce 对 Gemini 过滤资源类型、对 Azure 过滤区域，再沿用现有优先级和权重。
func selectRetryChannelOnce(ctx context.Context, group, model string, failedChannelIds []int) (*dbmodel.Channel, error) {
	if origin, restricted := dbmodel.ResponseOriginFromContext(ctx); restricted {
		if origin.ChannelType == common.ChannelTypeAzure && strings.TrimSpace(origin.Region) == "" {
			return nil, errors.New("Azure response origin region is unavailable; retry stopped")
		}
		if origin.Region == "" {
			return dbmodel.CacheGetRandomSatisfiedChannelByType(ctx, group, model, origin.ChannelType, failedChannelIds)
		}
		return dbmodel.CacheGetRandomSatisfiedChannelWithCapability(ctx, group, model, func(ch *dbmodel.Channel, cfg dbmodel.ChannelConfig) bool {
			return ch.Type == origin.ChannelType && (origin.Region == "" || strings.EqualFold(strings.TrimSpace(cfg.Region), origin.Region))
		}, 0, "", failedChannelIds)
	}

	if region, restricted := ctx.Value(azureRetryRegionKey{}).(string); restricted {
		if region == "" {
			return nil, errors.New("Azure channel region is not configured; retry stopped")
		}
		return dbmodel.CacheGetRandomSatisfiedChannelWithCapability(
			ctx, group, model,
			func(channel *dbmodel.Channel, cfg dbmodel.ChannelConfig) bool {
				return channel.Type == common.ChannelTypeAzure && strings.EqualFold(strings.TrimSpace(cfg.Region), region)
			},
			0, "", failedChannelIds,
		)
	}
	if channelType, restricted := service.GeminiResourceType(ctx); restricted {
		return dbmodel.CacheGetRandomSatisfiedChannelByType(ctx, group, model, channelType, failedChannelIds)
	}
	channel, _, err := dbmodel.CacheGetRandomSatisfiedChannel(ctx, group, model, 0, "", failedChannelIds)
	return channel, err
}

func appendUniqueChannelID(channelIDs []int, channelID int) []int {
	if channelID <= 0 {
		return channelIDs
	}
	for _, existingChannelID := range channelIDs {
		if existingChannelID == channelID {
			return channelIDs
		}
	}
	return append(channelIDs, channelID)
}

// selectRetryChannel 选择重试渠道，始终从最高优先级开始（skipPriorityLevels=0），
// 依靠 CacheGetRandomSatisfiedChannel 内部 fallback 自然降级。
// 非 Azure 请求在所有渠道耗尽时从头循环；Azure 请求耗尽同区域渠道后停止。
func selectRetryChannel(ctx context.Context, group string, model string, failedChannelIds *[]int) (*dbmodel.Channel, error) {
	channel, err := selectRetryChannelOnce(ctx, group, model, *failedChannelIds)
	if origin, restricted := dbmodel.ResponseOriginFromContext(ctx); restricted && (origin.ResponseID != "" || origin.Region != "") {
		return channel, err
	}
	if isAzureRetry(ctx) {
		return channel, err
	}
	if err != nil {
		// 所有优先级均已耗尽，重置后从最高优先级重新开始
		*failedChannelIds = nil
		// Gemini 循环重试仍必须经过资源类型过滤。
		channel, err = selectRetryChannelOnce(ctx, group, model, nil)
	}
	return channel, err
}

// A failed filtered lookup must not be bypassed using an old channel snapshot:
// its type, enabled state or model/group permissions may have changed.
func retryRequiresAvailableChannel(ctx context.Context) bool {
	_, gemini := service.GeminiResourceType(ctx)
	_, origin := dbmodel.ResponseOriginFromContext(ctx)
	return origin || gemini || isAzureRetry(ctx)
}

func getLastRetryFallbackChannel(channelID int) *dbmodel.Channel {
	if channelID <= 0 {
		return nil
	}
	if channel, err := dbmodel.CacheGetChannel(channelID); err == nil {
		return channel
	}
	if channel, err := dbmodel.GetChannelById(channelID, true); err == nil {
		return channel
	}
	return nil
}
