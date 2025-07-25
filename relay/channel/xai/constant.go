package xai

import "github.com/songquanpeng/one-api/relay/model"

var modelList = []string{
	"grok-4-latest",
	"grok-4-latest-low",
	"grok-4-latest-medium",
	"grok-4-latest-high",

	"grok-4-0709",
	"grok-4-0709-low",
	"grok-4-0709-medium",
	"grok-4-0709-high",

	"grok-4-0709",
	"grok-4-0709-low",
	"grok-4-0709-medium",
	"grok-4-0709-high",
	"grok-4",
	"grok-4-low",
	"grok-4-medium",
	"grok-4-high",
	"grok-3",
	"grok-3-mini",
	"grok-3-mini-low",
	"grok-3-mini-medium",
	"grok-3-mini-high",
	"grok-3-fast",
	"grok-3-mini-fast",
	"grok-3-mini-fast-low",
	"grok-3-mini-fast-medium",
	"grok-3-mini-fast-high",
	"grok-2-vision-1212",
	"grok-2-image-1212",
}

var channelName = "xai"

var ModelDetails = []model.APIModel{
	{
		Provider:    "xAI",
		Name:        "grok-4-latest",
		Tags:        []string{"grok", "chat"},
		PriceType:   "pay-per-token",
		Description: "Grok 4 latest - Most advanced reasoning model",
		Prices: map[string]interface{}{
			"InputTokens":  "$15.00 /M tokens",
			"OutputTokens": "$75.00 /M tokens",
		},
	},
	{
		Provider:    "xAI",
		Name:        "grok-4",
		Tags:        []string{"grok", "chat"},
		PriceType:   "pay-per-token",
		Description: "Grok 4 - Advanced reasoning and analysis",
		Prices: map[string]interface{}{
			"InputTokens":  "$15.00 /M tokens",
			"OutputTokens": "$75.00 /M tokens",
		},
	},
	{
		Provider:    "xAI",
		Name:        "grok-3",
		Tags:        []string{"grok", "chat"},
		PriceType:   "pay-per-token",
		Description: "Grok 3 - Powerful language model with reasoning capabilities",
		Prices: map[string]interface{}{
			"InputTokens":  "$10.00 /M tokens",
			"OutputTokens": "$30.00 /M tokens",
		},
	},
	{
		Provider:    "xAI",
		Name:        "grok-3-mini",
		Tags:        []string{"grok", "chat"},
		PriceType:   "pay-per-token",
		Description: "Grok 3 Mini - Efficient and fast reasoning model",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.15 /M tokens",
			"OutputTokens": "$0.60 /M tokens",
		},
	},
	{
		Provider:    "xAI",
		Name:        "grok-2-vision-1212",
		Tags:        []string{"grok", "vision", "multimodal"},
		PriceType:   "pay-per-token",
		Description: "Grok 2 Vision - Multimodal model with image understanding",
		Prices: map[string]interface{}{
			"InputTokens":  "$2.00 /M tokens",
			"OutputTokens": "$10.00 /M tokens",
		},
	},
}
