package anthropic

import "github.com/songquanpeng/one-api/relay/model"

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
