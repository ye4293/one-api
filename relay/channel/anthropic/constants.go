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

// adaptiveThinkingModels 需要使用 adaptive thinking 的模型集合（4.6+）
// 官方文档：thinking.type="enabled" + budget_tokens 已在 Opus 4.6 / Sonnet 4.6 deprecated，
// 推荐使用 thinking.type="adaptive"；Opus 4.7 起彻底只接受 adaptive。
// 注意：新增此类模型时需同步更新此 map、ModelList 以及 claude-config.go 中的默认 MaxTokens
var adaptiveThinkingModels = map[string]bool{
	"claude-opus-4-7":   true,
	"claude-opus-4-6":   true,
	"claude-sonnet-4-6": true,
}

// IsAdaptiveThinkingModel 判断模型是否应使用 adaptive thinking（4.6+ 模型）
// 传入的 modelName 可以包含或不包含 -thinking 后缀
func IsAdaptiveThinkingModel(modelName string) bool {
	baseName := GetBaseModelName(modelName)
	return adaptiveThinkingModels[baseName]
}

// noSamplingModels 不接受 temperature/top_p/top_k 的模型集合（仅 4.7+）
// 官方文档：Opus 4.7 起 sampling 参数全部移除，传任何一个都会 400。
// 4.6 adaptive 仍然接受 temperature，不要把 4.6 加进来。
var noSamplingModels = map[string]bool{
	"claude-opus-4-7": true,
}

// IsNoSamplingModel 判断模型是否完全不接受 temperature/top_p/top_k
// 传入的 modelName 可以包含或不包含 -thinking 后缀
func IsNoSamplingModel(modelName string) bool {
	baseName := GetBaseModelName(modelName)
	return noSamplingModels[baseName]
}

// MapReasoningEffortToOutputEffort 将 OpenAI reasoning_effort 映射到 Claude output_config.effort
func MapReasoningEffortToOutputEffort(reasoningEffort string) string {
	switch reasoningEffort {
	case "none", "minimal", "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "xhigh":
		return "max"
	default:
		return "high"
	}
}

var ModelList = []string{
	// Claude 4 models
	"claude-sonnet-4-20250514",
	"claude-opus-4-20250514",
	"claude-opus-4-1-20250805",
	"claude-haiku-4-5-20251001",
	"claude-sonnet-4-5-20250929",
	"claude-opus-4-5-20251101",
	"claude-opus-4-6",
	"claude-sonnet-4-6",
	"claude-opus-4-7",
	// Claude thinking models
	"claude-3-7-sonnet-20250219-thinking",
	"claude-sonnet-4-20250514-thinking",
	"claude-opus-4-20250514-thinking",
	"claude-opus-4-1-20250805-thinking",
	"claude-haiku-4-5-20251001-thinking",
	"claude-sonnet-4-5-20250929-thinking",
	"claude-opus-4-5-20251101-thinking",
	"claude-opus-4-6-thinking",
	"claude-sonnet-4-6-thinking",
	"claude-opus-4-7-thinking",
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
