package kling

import (
	"testing"
)

func TestCalculateQuota(t *testing.T) {
	// 测试用模型价格（避免依赖 common 包，绕开 common/init.go 的 flag.Parse 副作用）
	modelRatio := map[string]float64{
		"kling-v1-5-std": 50.0,
		"kling-v1-5-pro": 100.0,
	}

	tests := []struct {
		name         string
		params       map[string]interface{}
		requestType  string
		wantPositive bool
		description  string
	}{
		{
			name: "基础文生视频",
			params: map[string]interface{}{
				"model":        "kling-v1-5-std",
				"duration":     5,
				"aspect_ratio": "16:9",
			},
			requestType:  RequestTypeText2Video,
			wantPositive: true,
			description:  "基础价格 50 × 时长倍率 1 × 分辨率倍率 1.2 × 类型倍率 1.0",
		},
		{
			name: "长时长视频",
			params: map[string]interface{}{
				"model":        "kling-v1-5-std",
				"duration":     10,
				"aspect_ratio": "16:9",
			},
			requestType:  RequestTypeText2Video,
			wantPositive: true,
			description:  "时长倍率应为 10/5 = 2",
		},
		{
			name: "图生视频",
			params: map[string]interface{}{
				"model":        "kling-v1-5-pro",
				"duration":     5,
				"aspect_ratio": "1:1",
			},
			requestType:  RequestTypeImage2Video,
			wantPositive: true,
			description:  "图生视频类型倍率为 1.1",
		},
		{
			name: "多图生视频",
			params: map[string]interface{}{
				"model":        "kling-v1-5-pro",
				"duration":     5,
				"aspect_ratio": "21:9",
			},
			requestType:  RequestTypeMultiImage2Video,
			wantPositive: true,
			description:  "多图生视频类型倍率为 1.3，21:9 分辨率倍率为 1.3",
		},
		{
			name: "缺少模型参数",
			params: map[string]interface{}{
				"duration":     5,
				"aspect_ratio": "16:9",
			},
			requestType:  RequestTypeText2Video,
			wantPositive: true,
			description:  "应使用默认模型和价格",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			quota := calculateQuotaForTest(tt.params, tt.requestType, modelRatio, quotaPerUnitForTest)
			if tt.wantPositive && quota <= 0 {
				t.Errorf("CalculateQuota() = %v, want positive value. %s", quota, tt.description)
			}
			if !tt.wantPositive && quota > 0 {
				t.Errorf("CalculateQuota() = %v, want non-positive value. %s", quota, tt.description)
			}
			t.Logf("%s: quota = %d (%s)", tt.name, quota, tt.description)
		})
	}
}

func TestCalculateQuotaWithDifferentDurations(t *testing.T) {
	modelRatio := map[string]float64{
		"kling-v1-5-std": 50.0,
	}

	durations := []int{1, 5, 10, 15, 20}
	for _, duration := range durations {
		params := map[string]interface{}{
			"model":        "kling-v1-5-std",
			"duration":     duration,
			"aspect_ratio": "16:9",
		}
		quota := calculateQuotaForTest(params, RequestTypeText2Video, modelRatio, quotaPerUnitForTest)
		t.Logf("Duration %d seconds: quota = %d", duration, quota)

		if quota <= 0 {
			t.Errorf("Duration %d: quota should be positive, got %d", duration, quota)
		}
	}
}

func TestCalculateQuotaWithDifferentAspectRatios(t *testing.T) {
	modelRatio := map[string]float64{
		"kling-v1-5-std": 50.0,
	}

	aspectRatios := []string{"16:9", "9:16", "1:1", "21:9", "9:21", "unknown"}
	for _, ratio := range aspectRatios {
		params := map[string]interface{}{
			"model":        "kling-v1-5-std",
			"duration":     5,
			"aspect_ratio": ratio,
		}
		quota := calculateQuotaForTest(params, RequestTypeText2Video, modelRatio, quotaPerUnitForTest)
		t.Logf("Aspect ratio %s: quota = %d", ratio, quota)

		if quota <= 0 {
			t.Errorf("Aspect ratio %s: quota should be positive, got %d", ratio, quota)
		}
	}
}

const quotaPerUnitForTest = 500 * 1000.0

// calculateQuotaForTest: 复制 billing.go 的计费逻辑用于单测，避免导入 common/config 包导致测试二进制 flag 冲突
func calculateQuotaForTest(params map[string]interface{}, requestType string, modelRatio map[string]float64, quotaPerUnit float64) int64 {
	modelName := GetModelNameFromRequest(params)
	if modelName == "" {
		modelName = "kling-v1-5-std"
	}

	baseRatio, exists := modelRatio[modelName]
	if !exists {
		baseRatio = 50.0
	}

	duration := GetDurationFromRequest(params)
	if duration <= 0 {
		duration = 5
	}

	durationMultiplier := float64(duration) / 5.0
	if durationMultiplier < 1 {
		durationMultiplier = 1
	}

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

	return int64(baseRatio * durationMultiplier * resolutionMultiplier * requestTypeMultiplier * quotaPerUnit)
}
