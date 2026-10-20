package controller

import (
	"context"
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	dbmodel "github.com/songquanpeng/one-api/model"
)

type azureRetryRegionKey struct{}

func isAzureRetry(ctx context.Context) bool {
	_, restricted := ctx.Value(azureRetryRegionKey{}).(string)
	return restricted
}

// newRetryContext 固定初始渠道的区域，避免重试切换渠道后改变区域限制。
func newRetryContext(c *gin.Context) context.Context {
	ctx := c.Request.Context()
	if c.GetInt("channel") != common.ChannelTypeAzure {
		return ctx
	}
	value, _ := c.Get("Config")
	cfg, _ := value.(dbmodel.ChannelConfig)
	return context.WithValue(ctx, azureRetryRegionKey{}, strings.TrimSpace(cfg.Region))
}

// selectRetryChannelOnce 对 Azure 先过滤区域，再沿用现有优先级和权重选渠。
func selectRetryChannelOnce(ctx context.Context, group, model string, failedChannelIds []int) (*dbmodel.Channel, error) {
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
	if isAzureRetry(ctx) {
		return channel, err
	}
	if err != nil {
		// 所有优先级均已耗尽，重置后从最高优先级重新开始
		*failedChannelIds = nil
		channel, _, err = dbmodel.CacheGetRandomSatisfiedChannel(
			ctx, group, model, 0, "", nil,
		)
	}
	return channel, err
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
