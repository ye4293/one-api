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
// 这些接口通过 requestType 自动确定 model_name，用于计费识别
// key: requestType, value: 固定的 model_name
var RequiredModelNameMapping = map[string]string{
	RequestTypeIdentifyFace:     "kling-identify-face",
	RequestTypeImageRecognize:   "kling-image-recognize",
	RequestTypeCustomVoices:     "kling-custom-voices",
	RequestTypeTTS:              "kling-tts",
	RequestTypeTextToAudio:      "kling-text-to-audio",
	RequestTypeVideoToAudio:     "kling-video-to-audio",
	RequestTypeCustomElements:   "kling-custom-elements",
	RequestTypeMotionControl:    "kling-motion-control",
	RequestTypeVideoExtend:      "kling-video-extend",
	RequestTypeVideoEffects:     "kling-video-effects",
	RequestTypeAvatarI2V:        "kling-avatar-image2video",
	RequestTypeImageExpand:      "kling-image-expand",
}

// GetModelNameByRequestType 根据 requestType 获取应该使用的 model_name
// 如果 requestType 在映射表中，返回固定的 model_name；否则返回用户传递的 model
func GetModelNameByRequestType(requestType string, userModel string) string {
	if fixedModel, exists := RequiredModelNameMapping[requestType]; exists {
		return fixedModel
	}
	return userModel
}
