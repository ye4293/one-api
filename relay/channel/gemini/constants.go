package gemini

import "github.com/songquanpeng/one-api/relay/model"

// https://ai.google.dev/models/gemini

var ModelList = []string{
	"gemini-1.5-flash", "gemini-1.5-pro", "gemini-1.5-pro-002", "gemini-2.0-flash", "gemini-2.5-flash", "gemini-2.5-flash-lite", "gemini-2.5-pro", "gemini-2.5-flash-image-preview",
	"gemini-2.5-flash-thinking", "gemini-2.5-pro-thinking", "gemini-2.5-flash-lite-thinking",
	"gemini-2.5-flash-nothinking", "gemini-2.5-flash-lite-thinking", "gemini-2.5-pro-nothinking", "gemini-2.5-flash-image", "gemini-3-pro-preview", "gemini-3-pro-preview-thinking", "gemini-3-pro-image-preview",
}

var ModelDetails = []model.APIModel{
	{
		Provider:    "Google",
		Name:        "gemini-1.5-flash",
		Tags:        []string{"gemini", "chat"},
		PriceType:   "pay-per-token",
		Description: "Gemini 1.5 Flash - Efficient model for quick responses",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.075 /M tokens",
			"OutputTokens": "$0.03 /M tokens",
		},
	},
	{
		Provider:    "Google",
		Name:        "gemini-1.5-pro",
		Tags:        []string{"gemini", "chat"},
		PriceType:   "pay-per-token",
		Description: "Gemini 1.5 Pro - Advanced model for complex tasks",
		Prices: map[string]interface{}{
			"InputTokens":  "$1.25 /M tokens",
			"OutputTokens": "$5.00 /M tokens",
		},
	},
	{
		Provider:    "Google",
		Name:        "gemini-1.5-pro-002",
		Tags:        []string{"gemini", "chat"},
		PriceType:   "pay-per-token",
		Description: "Gemini 1.5 Pro 002 - Enhanced version with improved capabilities",
		Prices: map[string]interface{}{
			"InputTokens":  "$1.25 /M tokens",
			"OutputTokens": "$5.00 /M tokens",
		},
	},
	{
		Provider:    "Google",
		Name:        "gemini-1.5-pro-001",
		Tags:        []string{"gemini", "chat"},
		PriceType:   "pay-per-token",
		Description: "Gemini 1.5 Pro 001 - Stable version for professional use",
		Prices: map[string]interface{}{
			"InputTokens":  "$1.25 /M tokens",
			"OutputTokens": "$5.00 /M tokens",
		},
	},
	{
		Provider:    "Google",
		Name:        "gemini-1.5-flash-8b",
		Tags:        []string{"gemini", "chat"},
		PriceType:   "pay-per-token",
		Description: "Gemini 1.5 Flash 8B - Lightweight and fast model",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.0375 /M tokens",
			"OutputTokens": "$0.15 /M tokens",
		},
	},
}
