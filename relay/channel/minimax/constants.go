package minimax

import "github.com/songquanpeng/one-api/relay/model"

var ModelList = []string{
	"abab5.5s-chat",
	"abab5.5-chat",
	"abab6-chat",
	"video-01",
	"video-01-live2d",
	"S2V-01",
	"T2V-01",
	"I2V-01",
	"T2V-01-Director",
	"I2V-01-Director",
	"I2V-01-live",
	"MiniMax-Hailuo-02",
	"MiniMax-Hailuo-2.3",
	"MiniMax-Hailuo-2.3-Fast",
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
