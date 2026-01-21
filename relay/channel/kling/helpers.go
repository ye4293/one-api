package kling

import (
	"fmt"
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

// RequiredModelNameMapping 按次计费接口必须使用的 model_name 映射
// key: requestType, value: 合法的 model_name 列表
var RequiredModelNameMapping = map[string][]string{
	RequestTypeIdentifyFace:   {"kling-identify-face"},
	RequestTypeImageRecognize: {"kling-image-recognize"},
	RequestTypeCustomVoices:   {"kling-custom-voices"},
	RequestTypeTTS:            {"kling-tts"},
	RequestTypeTextToAudio:    {"kling-text-to-audio"},
	RequestTypeVideoToAudio:   {"kling-video-to-audio"},
	RequestTypeCustomElements: {"kling-custom-elements"},
}

// ValidateModelName 验证按次计费接口的 model_name 是否合法
// 返回: (是否需要验证, 是否合法, 错误信息)
func ValidateModelName(requestType string, modelName string) (needValidate bool, isValid bool, errMsg string) {
	// 检查是否需要验证
	allowedNames, needValidate := RequiredModelNameMapping[requestType]
	if !needValidate {
		// 不在按次计费列表中，不需要验证
		return false, true, ""
	}

	// 需要验证，检查 model_name 是否为空
	if modelName == "" {
		return true, false, fmt.Sprintf("按次计费接口 %s 必须提供 model_name 参数，支持的值: %s", requestType, strings.Join(allowedNames, ", "))
	}

	// 检查 model_name 是否在允许列表中
	for _, allowed := range allowedNames {
		if strings.EqualFold(modelName, allowed) {
			return true, true, ""
		}
	}

	// model_name 不合法
	errMsg = fmt.Sprintf("model_name '%s' 不合法，接口 %s 仅支持: %s", modelName, requestType, strings.Join(allowedNames, ", "))
	return true, false, errMsg
}
