package kling

import "strings"

// IsSyncRequestType 判断是否为同步请求类型
// 同步接口会立即返回结果，不需要回调
func IsSyncRequestType(requestType string) bool {
	syncTypes := []string{
		RequestTypeCustomElements, // 自定义元素训练（同步）
		// 可以在这里添加其他同步接口
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
