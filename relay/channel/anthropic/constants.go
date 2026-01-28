package anthropic

import (
	"strings"

	"github.com/songquanpeng/one-api/relay/model"
)

// IsThinkingModel 判断模型是否是 thinking 模型
func IsThinkingModel(modelName string) bool {
	return strings.HasSuffix(modelName, "-thinking")
}

// GetBaseModelName 获取基础模型名称（去除 -thinking 后缀）
func GetBaseModelName(modelName string) string {
	if IsThinkingModel(modelName) {
		return strings.TrimSuffix(modelName, "-thinking")
	}
	return modelName
}

var ModelList = []string{
	"claude-3-haiku-20240307",
	"claude-3-sonnet-20240229",
	"claude-3-opus-20240229",
	"claude-3-5-sonnet-20240620",
	"claude-3-5-sonnet-20241022",
	"claude-3-5-haiku-20241022",
	"claude-3-7-sonnet-20250219",
	"claude-opus-4-20250514",
	"claude-sonnet-4-20250514",
	"claude-3-7-sonnet-20250219-thinking",
	"claude-opus-4-20250514-thinking",
	"claude-sonnet-4-20250514-thinking",
	"claude-opus-4-1-20250805",
	"claude-opus-4-1-20250805-thinking",
	"claude-haiku-4-5-20251001",
	"claude-haiku-4-5-20251001-thinking",
	"claude-sonnet-4-5-20250929-thinking",
	"claude-sonnet-4-5-20250929",
	"claude-opus-4-5-20251101-thinking",
	"claude-opus-4-5-20251101",
}

var ModelDetails = []model.APIModel{
	{
		Provider:    "Anthropic Claude",
		Name:        "claude-3-haiku-20240307",
		Tags:        []string{"claude", "chat"},
		PriceType:   "pay-per-token",
		Description: "Claude 3 Haiku - Fast and efficient for everyday tasks",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.25 /M tokens",
			"OutputTokens": "$1.25 /M tokens",
		},
	},
	{
		Provider:    "Anthropic Claude",
		Name:        "claude-3-sonnet-20240229",
		Tags:        []string{"claude", "chat"},
		PriceType:   "pay-per-token",
		Description: "Claude 3 Sonnet - Balanced performance and sophistication",
		Prices: map[string]interface{}{
			"InputTokens":  "$3.00 /M tokens",
			"OutputTokens": "$15.00 /M tokens",
		},
	},
	{
		Provider:    "Anthropic Claude",
		Name:        "claude-3-opus-20240229",
		Tags:        []string{"claude", "chat"},
		PriceType:   "pay-per-token",
		Description: "Claude 3 Opus - Most capable model for complex tasks",
		Prices: map[string]interface{}{
			"InputTokens":  "$15.00 /M tokens",
			"OutputTokens": "$75.00 /M tokens",
		},
	},
	{
		Provider:    "Anthropic Claude",
		Name:        "claude-3-5-sonnet-20240620",
		Tags:        []string{"claude", "chat"},
		PriceType:   "pay-per-token",
		Description: "Claude 3.5 Sonnet - Enhanced version with improved capabilities",
		Prices: map[string]interface{}{
			"InputTokens":  "$3.00 /M tokens",
			"OutputTokens": "$15.00 /M tokens",
		},
	},
	{
		Provider:    "Anthropic Claude",
		Name:        "claude-3-5-sonnet-20241022",
		Tags:        []string{"claude", "chat"},
		PriceType:   "pay-per-token",
		Description: "Claude 3.5 Sonnet - Latest version with further improvements",
		Prices: map[string]interface{}{
			"InputTokens":  "$3.00 /M tokens",
			"OutputTokens": "$15.00 /M tokens",
		},
	},
	{
		Provider:    "Anthropic Claude",
		Name:        "claude-3-5-haiku-20241022",
		Tags:        []string{"claude", "chat"},
		PriceType:   "pay-per-token",
		Description: "Claude 3.5 Haiku - Latest fast and efficient version",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.80 /M tokens",
			"OutputTokens": "$4.00 /M tokens",
		},
	},
}
