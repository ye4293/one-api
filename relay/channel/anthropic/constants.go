package anthropic

import (
	"regexp"
	"strconv"
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
	"claude-opus-4-8":   true,
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

// noSamplingModels 是显式覆盖表：命名不规则、或需强制视为 no-sampling 的特例。
// 常规判定由 IsNoSamplingModel 的版本规则（4.7+ 所有系列）完成，出新版本
// （4.9 / 5.x）无需改代码。这里留给规则覆盖不到的边角情况兜底，正常为空。
var noSamplingModels = map[string]bool{}

// claudeNewFmtVersionRe 解析 Claude 4+ 新命名（family 在前、版本在后）的版本段：
//
//	claude-<family>-<major>[-<minor>] ...
//
// family 是字母（opus/sonnet/haiku），major/minor 是数字段。用非锚定的
// FindStringSubmatch 以兼容前后缀：anthropic.claude-opus-4-8-v1、
// us.anthropic.claude-...、...-thinking 均能命中。
//
// 旧格式 claude-3-7-sonnet-...（版本在 family 前，claude- 后紧跟数字）不匹配本
// 正则，天然返回 ok=false —— 而它们 major=3、本就 <4.7 应排除，语义一致。
var claudeNewFmtVersionRe = regexp.MustCompile(`claude-[a-z]+-(\d+)(?:-(\d+))?`)

// parseClaudeMajorMinor 从模型名解析 (major, minor)，仅识别 Claude 4+ 新格式。
// minor 段若缺失、或为 >=5 位数字（8 位日期 YYYYMMDD 冒充 minor，如
// claude-opus-4-20250514 实为 4.0），一律记 minor=0。
func parseClaudeMajorMinor(modelName string) (major, minor int, ok bool) {
	m := claudeNewFmtVersionRe.FindStringSubmatch(strings.ToLower(modelName))
	if m == nil {
		return 0, 0, false
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, 0, false
	}
	if m[2] != "" && len(m[2]) < 5 { // 排除 8 位日期段冒充 minor
		minor, _ = strconv.Atoi(m[2])
	}
	return major, minor, true
}

// IsNoSamplingModel 判断模型是否完全不接受 temperature/top_p/top_k。
// 规则：Claude 4.7 及以上（所有系列，含未来 5.x）。官方文档：Opus 4.7 起 sampling
// 参数全部移除，传任何一个都会 400；4.6 及更早仍接受 temperature，必须排除。
// 传入名可含/不含 -thinking 后缀，也可为 AWS Bedrock 原生 ID / 区域前缀形式。
func IsNoSamplingModel(modelName string) bool {
	baseName := GetBaseModelName(modelName)
	if noSamplingModels[baseName] {
		return true
	}
	major, minor, ok := parseClaudeMajorMinor(baseName)
	if !ok {
		return false
	}
	return major > 4 || (major == 4 && minor >= 7)
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
	"claude-opus-4-8",
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
	"claude-opus-4-8-thinking",
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
