package baichuan

import "github.com/songquanpeng/one-api/relay/model"

var ModelList = []string{
	"Baichuan2-Turbo",
	"Baichuan2-Turbo-192k",
	"Baichuan-Text-Embedding",
}

var ModelDetails = []model.APIModel{
	{
		Name:        "Baichuan2-Turbo",
		Provider:    "Baidu",
		Description: "Baichuan2-Turbo - Fast and efficient for everyday tasks",
		Tags:        []string{"baidu", "chat"},
		PriceType:   "pay-per-token",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.25 /M tokens",
			"OutputTokens": "$1.25 /M tokens",
		},
	},
	{
		Name:        "Baichuan2-Turbo-192k",
		Provider:    "Baidu",
		Description: "Baichuan2-Turbo-192k - Fast and efficient for everyday tasks",
		Tags:        []string{"baidu", "chat"},
		PriceType:   "pay-per-token",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.25 /M tokens",
			"OutputTokens": "$1.25 /M tokens",
		},
	},
	{
		Name:        "Baichuan-Text-Embedding",
		Provider:    "Baidu",
		Description: "Baichuan-Text-Embedding - Fast and efficient for everyday tasks",
		Tags:        []string{"baidu", "chat"},
		PriceType:   "pay-per-token",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.25 /M tokens",
			"OutputTokens": "$1.25 /M tokens",
		},
	},
}
