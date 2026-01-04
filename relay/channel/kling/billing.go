package kling

import (
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
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

