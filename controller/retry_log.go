package controller

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

// recordFinalErrorLog 写一条聚合后的失败日志（LogTypeError），content 仅为最终错误消息（不含渠道信息），
// retryHistory 完整放进 other 字段供管理员展开。
// channelHistory 是扁平的渠道 ID 数组，作为 adminInfo 保留以兼容现有"重试"列。
// affinityTag 可选，亲和性标签。
func recordFinalErrorLog(
	ctx context.Context,
	c *gin.Context,
	bizErr *model.ErrorWithStatusCode,
	attempts []util.RetryAttempt,
	channelHistory []int,
	affinityTag string,
) {
	userId := c.GetInt("id")
	originalModel := c.GetString("original_model")
	tokenName := c.GetString("token_name")
	requestID := c.GetHeader("X-Request-ID")

	// 最终落到哪个渠道（取 attempts 最后一个，没有就退化到 c.GetInt("channel_id")）
	finalChannelId := c.GetInt("channel_id")
	if len(attempts) > 0 {
		finalChannelId = attempts[len(attempts)-1].ChannelId
	}

	// content 只保留最终错误消息，绝不拼 "渠道=xxx" 前缀
	content := ""
	if bizErr != nil {
		content = bizErr.Error.Message
	}

	// other：adminInfo + retryHistory（+ affinityTag）
	otherParts := []string{}
	if len(channelHistory) > 0 {
		if b, err := json.Marshal(channelHistory); err == nil {
			otherParts = append(otherParts, fmt.Sprintf("adminInfo:%s", string(b)))
		}
	}
	if len(attempts) > 0 {
		if b, err := json.Marshal(attempts); err == nil {
			otherParts = append(otherParts, fmt.Sprintf("retryHistory:%s", string(b)))
		}
	}
	if affinityTag != "" {
		otherParts = append(otherParts, affinityTag)
	}
	otherInfo := ""
	for i, p := range otherParts {
		if i > 0 {
			otherInfo += ";"
		}
		otherInfo += p
	}

	// 累计墙钟耗时（所有重试的 duration 之和），写入 Log.Duration 列以支持按耗时筛选
	var totalDuration float64
	for _, a := range attempts {
		totalDuration += a.Duration
	}

	dbmodel.RecordErrorLogWithRequestID(
		ctx,
		userId,
		finalChannelId,
		originalModel,
		tokenName,
		content,
		totalDuration,
		otherInfo,
		requestID,
	)

	logger.Infof(ctx, "Recorded final error log: requestID=%s, userId=%d, model=%s, attempts=%d, finalChannel=%d, totalDuration=%.3fs, error=%s",
		requestID, userId, originalModel, len(attempts), finalChannelId, totalDuration, content)
}
