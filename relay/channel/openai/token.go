package openai

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/pkoukk/tiktoken-go"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/image"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/relay/model"
)

// tokenEncoderMap won't grow after initialization
var tokenEncoderMap = map[string]*tiktoken.Tiktoken{}
var defaultTokenEncoder *tiktoken.Tiktoken

func InitTokenEncoders() {
	logger.SysLog("initializing token encoders")
	gpt35TokenEncoder, err := tiktoken.EncodingForModel("gpt-3.5-turbo")
	if err != nil {
		logger.FatalLog(fmt.Sprintf("failed to get gpt-3.5-turbo token encoder: %s", err.Error()))
	}
	defaultTokenEncoder = gpt35TokenEncoder
	gpt4oTokenEncoder, err := tiktoken.EncodingForModel("gpt-4o")
	if err != nil {
		logger.FatalLog(fmt.Sprintf("failed to get gpt-4o token encoder: %s", err.Error()))
	}
	gpt4TokenEncoder, err := tiktoken.EncodingForModel("gpt-4")
	if err != nil {
		logger.FatalLog(fmt.Sprintf("failed to get gpt-4 token encoder: %s", err.Error()))
	}
	for model := range common.ModelRatio {
		if strings.HasPrefix(model, "gpt-3.5") {
			tokenEncoderMap[model] = gpt35TokenEncoder
		} else if strings.HasPrefix(model, "gpt-4o") {
			tokenEncoderMap[model] = gpt4oTokenEncoder
		} else if strings.HasPrefix(model, "gpt-4") {
			tokenEncoderMap[model] = gpt4TokenEncoder
		} else {
			tokenEncoderMap[model] = nil
		}
	}
	logger.SysLog("token encoders initialized")
}
func getTokenEncoder(model string) *tiktoken.Tiktoken {
	tokenEncoder, ok := tokenEncoderMap[model]
	if ok && tokenEncoder != nil {
		return tokenEncoder
	}
	if ok {
		tokenEncoder, err := tiktoken.EncodingForModel(model)
		if err != nil {
			logger.SysError(fmt.Sprintf("failed to get token encoder for model %s: %s, using encoder for gpt-3.5-turbo", model, err.Error()))
			tokenEncoder = defaultTokenEncoder
		}
		tokenEncoderMap[model] = tokenEncoder
		return tokenEncoder
	}
	return defaultTokenEncoder
}

func getTokenNum(tokenEncoder *tiktoken.Tiktoken, text string) int {
	if config.ApproximateTokenEnabled {
		return int(float64(len(text)) * 0.38)
	}
	return len(tokenEncoder.Encode(text, nil, nil))
}

func CountTokenMessages(messages []model.Message, model string) int {
	tokenEncoder := getTokenEncoder(model)
	if tokenEncoder == nil {
		return 0
	}

	var tokensPerMessage, tokensPerName int
	if model == "gpt-3.5-turbo-0301" {
		tokensPerMessage = 4
		tokensPerName = -1
	} else {
		tokensPerMessage = 3
		tokensPerName = 1
	}

	tokenNum := 0
	for _, message := range messages {
		tokenNum += tokensPerMessage

		// 处理 Content
		if message.Content != nil {
			switch v := message.Content.(type) {
			case string:
				tokenNum += getTokenNum(tokenEncoder, v)
			case []any:
				for _, it := range v {
					m, ok := it.(map[string]any)
					if !ok {
						continue
					}

					contentType, ok := m["type"].(string)
					if !ok {
						continue
					}

					switch contentType {
					case "text":
						if textValue, ok := m["text"]; ok {
							if textString, ok := textValue.(string); ok {
								tokenNum += getTokenNum(tokenEncoder, textString)
							}
						}
					case "image_url":
						// 默认跳过图片token计算以节省服务器资源
						// 媒体token计算消耗性能，且响应中会包含准确的token信息
						logger.SysLog(fmt.Sprintf("Skipping image token calculation for performance optimization - model: %s", model))
						continue
					case "audio_url", "video_url", "input_audio", "file_url":
						// 默认跳过音频、视频、文档token计算以节省服务器资源
						// 媒体token计算消耗性能，且响应中会包含准确的token信息
						logger.SysLog(fmt.Sprintf("Skipping %s token calculation for performance optimization - model: %s", contentType, model))
						continue
					}
				}
			}
		}

		// 处理 Role
		if message.Role != "" {
			tokenNum += getTokenNum(tokenEncoder, message.Role)
		}

		// 处理 Name
		if message.Name != nil {
			tokenNum += tokensPerName
			tokenNum += getTokenNum(tokenEncoder, *message.Name)
		}
	}

	tokenNum += 3 // Every reply is primed with <|start|>assistant<|message|>
	return tokenNum
}

const (
	lowDetailCost         = 85
	highDetailCostPerTile = 170
	additionalCost        = 85
)

func countImageTokens(url string, detail string) (int, error) {
	if detail == "" || detail == "auto" {
		detail = "high"
	}

	switch detail {
	case "low":
		return lowDetailCost, nil
	case "high":
		width, height, err := image.GetImageSize(url)
		if err != nil {
			return 0, err
		}

		if width > 2048 || height > 2048 {
			ratio := float64(2048) / math.Max(float64(width), float64(height))
			width = int(float64(width) * ratio)
			height = int(float64(height) * ratio)
		}
		if width > 768 && height > 768 {
			ratio := float64(768) / math.Min(float64(width), float64(height))
			width = int(float64(width) * ratio)
			height = int(float64(height) * ratio)
		}
		numSquares := int(math.Ceil(float64(width)/512) * math.Ceil(float64(height)/512))
		return numSquares*highDetailCostPerTile + additionalCost, nil
	default:
		return 0, errors.New("invalid detail option")
	}
}

func CountTokenInput(input any, model string) int {
	switch v := input.(type) {
	case string:
		return CountTokenText(v, model)
	case []string:
		text := ""
		for _, s := range v {
			text += s
		}
		return CountTokenText(text, model)
	}
	return 0
}

func CountTokenText(text string, model string) int {
	tokenEncoder := getTokenEncoder(model)
	return getTokenNum(tokenEncoder, text)
}
