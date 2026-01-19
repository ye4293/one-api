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
	} else if strings.Contains(path, "/identify-face") {
		return RequestTypeIdentifyFace
	} else if strings.Contains(path, "/advanced-lip-sync") {
		return RequestTypeAdvancedLipSync
	}

	// 视频类（新增6个）
	if strings.Contains(path, "/motion-control") {
		return RequestTypeMotionControl
	} else if strings.Contains(path, "/multi-elements") {
		return RequestTypeMultiElements
	} else if strings.Contains(path, "/video-extend") {
		return RequestTypeVideoExtend
	} else if strings.Contains(path, "/avatar/image2video") {
		return RequestTypeAvatarI2V
	} else if strings.Contains(path, "/effects") {
		return RequestTypeVideoEffects
	} else if strings.Contains(path, "/image-recognize") {
		return RequestTypeImageRecognize
	}

	// 音频类（新增3个）
	if strings.Contains(path, "/text-to-audio") {
		return RequestTypeTextToAudio
	} else if strings.Contains(path, "/video-to-audio") {
		return RequestTypeVideoToAudio
	} else if strings.Contains(path, "/tts") {
		return RequestTypeTTS
	}

	// 图片类（新增4个）
	if strings.Contains(path, "/generations") {
		return RequestTypeImageGeneration
	} else if strings.Contains(path, "/omni-image") {
		return RequestTypeOmniImage
	} else if strings.Contains(path, "/multi-image2image") {
		return RequestTypeMultiImage2Image
	} else if strings.Contains(path, "/editing/expand") {
		return RequestTypeImageExpand
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
		if duration < 5 {
			return 5
		}
		return int(duration)
	}
	if duration, ok := params["duration"].(int); ok {
		if duration < 5 {
			return 5
		}
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

// GetModeFromRequest 从请求参数中提取生成模式
// std: 标准模式（性价比高）
// pro: 专家模式（高品质）
func GetModeFromRequest(params map[string]interface{}) string {
	if mode, ok := params["mode"].(string); ok {
		// 验证是否为有效值
		if mode == "std" || mode == "pro" {
			return mode
		}
	}
	return "std" // 默认标准模式
}
