package minimax

import "github.com/songquanpeng/one-api/relay/model"

var ModelList = []string{
	"abab5.5s-chat",
	"abab5.5-chat",
	"abab6-chat",
}

var ModelDetails = []model.APIModel{
	{
		Name:        "abab5.5s-chat",
		Provider:    "Minimax",
		Description: "abab5.5s-chat - Fast and efficient for everyday tasks",
		Tags:        []string{"minimax", "chat"},
		PriceType:   "pay-per-token",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.25 /M tokens",
			"OutputTokens": "$1.25 /M tokens",
		},
	},
}
