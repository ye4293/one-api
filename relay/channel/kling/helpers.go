package kling

import (
	"strings"
)

// IsSyncRequestType 判断是否为同步请求类型
// 同步接口会立即返回结果，不需要回调
func IsSyncRequestType(requestType string) bool {
	syncTypes := []string{
		RequestTypeCustomElements, // 自定义元素训练（同步）
		RequestTypeTTS,            // 语音合成（同步）
		RequestTypeImageRecognize, // 图像识别（同步）
	}

	for _, t := range syncTypes {
		if requestType == t {
			return true
		}
	}
	return false
}

// IsImageRequestType 判断是否为图片类请求类型
func IsImageRequestType(requestType string) bool {
	imageTypes := []string{
		RequestTypeImageGeneration,
		RequestTypeOmniImage,
		RequestTypeMultiImage2Image,
		RequestTypeImageExpand,
	}

	for _, t := range imageTypes {
		if requestType == t {
			return true
		}
	}
	return false
}

// IsSuccessMessage 判断 message 是否表示成功
// Kling API 可能返回 "success", "succeed", "SUCCESS", "SUCCEED" 等
func IsSuccessMessage(message string) bool {
	normalized := strings.ToLower(strings.TrimSpace(message))
	return normalized == "success" || normalized == "succeed"
}

// FormatDuration 格式化时长字符串（如 "5.0s" -> "5"）
func FormatDuration(durationStr string) string {
	return strings.TrimSuffix(durationStr, "s")
}

// RequiredModelNameMapping 需要使用固定 model_name 的接口映射
// 适用于无 model_name 请求参数的接口，通过 requestType 自动确定计费标识
// key: requestType, value: 固定的 model_name
var RequiredModelNameMapping = map[string]string{
	RequestTypeIdentifyFace:           "kling-identify-face",
	RequestTypeImageRecognize:         "kling-image-recognize",
	RequestTypeCustomVoices:           "kling-custom-voices",
	RequestTypeTTS:                    "kling-tts",
	RequestTypeTextToAudio:            "kling-text-to-audio",
	RequestTypeVideoToAudio:           "kling-video-to-audio",
	RequestTypeCustomElements:         "kling-custom-elements",
	RequestTypeAdvancedCustomElements: "kling-advanced-custom-elements",
	RequestTypeVideoExtend:            "kling-video-extend",
	RequestTypeVideoEffects:           "kling-video-effects",
	RequestTypeAvatarI2V:              "kling-avatar-image2video",
	RequestTypeImageExpand:            "kling-image-expand",
}

// DefaultModelMapping 用户未传 model_name 时的 Kling 官方默认值
// 适用于有 model_name 参数但用户未传的接口
var DefaultModelMapping = map[string]string{
	RequestTypeText2Video:       "kling-v1",
	RequestTypeImage2Video:      "kling-v1",
	RequestTypeOmniVideo:        "kling-video-o1",
	RequestTypeMultiImage2Video: "kling-v1-6",
	RequestTypeMotionControl:    "kling-v2-6",
}

// GetModelNameByRequestType 根据 requestType 获取应该使用的 model_name
// 优先级：固定映射表 > 用户传递的值 > 官方默认值
func GetModelNameByRequestType(requestType string, userModel string) string {
	if fixedModel, exists := RequiredModelNameMapping[requestType]; exists {
		return fixedModel
	}
	if userModel != "" {
		return userModel
	}
	if defaultModel, exists := DefaultModelMapping[requestType]; exists {
		return defaultModel
	}
	return ""
}
