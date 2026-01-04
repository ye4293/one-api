package kling

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// GenerateTaskID 生成唯一的任务ID
func GenerateTaskID() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return fmt.Sprintf("kling_%s", hex.EncodeToString(bytes))
}

// DetermineRequestType 从URL路径确定请求类型
func DetermineRequestType(path string) string {
	if strings.Contains(path, "/text2video") {
		return RequestTypeText2Video
	} else if strings.Contains(path, "/omni-video") {
		return RequestTypeOmniVideo
	} else if strings.Contains(path, "/image2video") && !strings.Contains(path, "/multi-") {
		return RequestTypeImage2Video
	} else if strings.Contains(path, "/multi-image2video") {
		return RequestTypeMultiImage2Video
	}
	return ""
}

// GetPromptFromRequest 从请求参数中提取提示词
func GetPromptFromRequest(params map[string]interface{}) string {
	if prompt, ok := params["prompt"].(string); ok {
		return prompt
	}
	return ""
}

// GetDurationFromRequest 从请求参数中提取视频时长
func GetDurationFromRequest(params map[string]interface{}) int {
	if duration, ok := params["duration"].(float64); ok {
		return int(duration)
	}
	if duration, ok := params["duration"].(int); ok {
		return duration
	}
	return 5 // 默认5秒
}

// GetAspectRatioFromRequest 从请求参数中提取宽高比
func GetAspectRatioFromRequest(params map[string]interface{}) string {
	if aspectRatio, ok := params["aspect_ratio"].(string); ok {
		return aspectRatio
	}
	return "16:9" // 默认16:9
}

// GetModelNameFromRequest 从请求参数中提取模型名称
func GetModelNameFromRequest(params map[string]interface{}) string {
	if model, ok := params["model"].(string); ok {
		return model
	}
	return ""
}

