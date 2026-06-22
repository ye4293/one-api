package kling

import (
	"context"
	"fmt"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
)

// CalculateQuota 计算 Kling 视频生成费用（后扣费模式）
// 提交任务时仅验证余额，成功后才实际扣费
func CalculateQuota(params map[string]interface{}, requestType string) int64 {
	// 获取模型名称
	modelName := GetModelNameFromRequest(params)
	if modelName == "" {
		modelName = "kling-v1-5-std" // 默认模型
	}

	// 获取基础价格
	baseRatio, exists := common.ModelRatio[modelName]
	if !exists {
		// 如果模型不存在，使用默认价格
		baseRatio = 50.0
	}

	// 获取视频时长（秒）
	duration := GetDurationFromRequest(params)
	if duration <= 0 {
		duration = 5 // 默认5秒
	}

	// 根据时长计算倍率（每5秒为一个计费单位）
	durationMultiplier := float64(duration) / 5.0
	if durationMultiplier < 1 {
		durationMultiplier = 1
	}

	// 分辨率加成
	aspectRatio := GetAspectRatioFromRequest(params)
	resolutionMultiplier := 1.0
	switch aspectRatio {
	case "16:9", "9:16":
		resolutionMultiplier = 1.2
	case "1:1":
		resolutionMultiplier = 1.0
	case "21:9", "9:21":
		resolutionMultiplier = 1.3
	default:
		resolutionMultiplier = 1.0
	}

	// 请求类型加成
	requestTypeMultiplier := 1.0
	switch requestType {
	case RequestTypeText2Video:
		requestTypeMultiplier = 1.0
	case RequestTypeImage2Video:
		requestTypeMultiplier = 1.1
	case RequestTypeOmniVideo:
		requestTypeMultiplier = 1.2
	case RequestTypeMultiImage2Video:
		requestTypeMultiplier = 1.3
	}

	// 计算总费用
	totalQuota := int64(baseRatio * durationMultiplier * resolutionMultiplier * requestTypeMultiplier * float64(config.QuotaPerUnit))

	return totalQuota
}

// ChargeVideoOnSuccess 视频/音频任务成功时的完整计费流程
func ChargeVideoOnSuccess(ctx context.Context, video *model.Video, quota int64) error {
	if quota <= 0 {
		return fmt.Errorf("invalid quota: %d", quota)
	}

	if err := model.DecreaseUserQuota(video.UserId, quota); err != nil {
		return fmt.Errorf("扣费失败: %w", err)
	}

	model.UpdateUserUsedQuotaAndRequestCount(video.UserId, quota)
	model.UpdateChannelUsedQuota(video.ChannelId, quota)

	logContent := fmt.Sprintf("Kling 任务成功，扣费 quota=%d, task_id=%s, type=%s", quota, video.TaskId, video.Type)
	model.RecordConsumeLog(
		ctx,
		video.UserId,
		video.ChannelId,
		0, 0,
		video.Model,
		"",
		quota,
		logContent,
		float64(video.TotalDuration),
		"", "",
		false,
		0.0,
	)

	logger.Infof(ctx, "[kling-billing] 成功扣费 user_id=%d channel_id=%d quota=%d model=%s task_id=%s",
		video.UserId, video.ChannelId, quota, video.Model, video.TaskId)
	return nil
}

// ChargeImageOnSuccess 图片任务成功时的完整计费流程
func ChargeImageOnSuccess(ctx context.Context, image *model.Image, quota int64) error {
	if quota <= 0 {
		return fmt.Errorf("invalid quota: %d", quota)
	}

	if err := model.DecreaseUserQuota(image.UserId, quota); err != nil {
		return fmt.Errorf("扣费失败: %w", err)
	}

	model.UpdateUserUsedQuotaAndRequestCount(image.UserId, quota)
	model.UpdateChannelUsedQuota(image.ChannelId, quota)

	logContent := fmt.Sprintf("Kling 图片任务成功，扣费 quota=%d, task_id=%s", quota, image.TaskId)
	model.RecordConsumeLog(
		ctx,
		image.UserId,
		image.ChannelId,
		0, 0,
		image.Model,
		"",
		quota,
		logContent,
		float64(image.TotalDuration),
		"", "",
		false,
		0.0,
	)

	logger.Infof(ctx, "[kling-billing] 图片扣费成功 user_id=%d channel_id=%d quota=%d model=%s task_id=%s",
		image.UserId, image.ChannelId, quota, image.Model, image.TaskId)
	return nil
}
