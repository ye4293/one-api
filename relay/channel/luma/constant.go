package luma

import "github.com/songquanpeng/one-api/relay/model"

var ModelList = []string{"luma-api"}

var ModelDetails = []model.APIModel{
	{
		Name:        "luma-api",
		Provider:    "Luma",
		Description: "Luma API - Fast and efficient for everyday tasks",
		Tags:        []string{"luma", "video"},
		PriceType:   "pay-per-token",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.25 /M tokens",
			"OutputTokens": "$1.25 /M tokens",
		},
	},
}
