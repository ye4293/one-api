package lingyiwanwu

import "github.com/songquanpeng/one-api/relay/model"

// https://platform.lingyiwanwu.com/docs

var ModelList = []string{
	"yi-34b-chat-0205",
	"yi-34b-chat-200k",
	"yi-vl-plus",
}

var ModelDetails = []model.APIModel{
	{
		Name:        "yi-34b-chat-0205",
		Provider:    "LingyiWanwu",
		Description: "yi-34b-chat-0205 - Fast and efficient for everyday tasks",
		Tags:        []string{"lingyiwanwu", "chat"},
		PriceType:   "pay-per-token",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.25 /M tokens",
			"OutputTokens": "$1.25 /M tokens",
		},
	},
	{
		Name:        "yi-34b-chat-200k",
		Provider:    "LingyiWanwu",
		Description: "yi-34b-chat-200k - Fast and efficient for everyday tasks",
		Tags:        []string{"lingyiwanwu", "chat"},
		PriceType:   "pay-per-token",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.25 /M tokens",
			"OutputTokens": "$1.25 /M tokens",
		},
	},
}
