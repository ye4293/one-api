package moonshot

import "github.com/songquanpeng/one-api/relay/model"

var ModelList = []string{
	"moonshot-v1-8k",
	"moonshot-v1-32k",
	"moonshot-v1-128k",
}

var ModelDetails = []model.APIModel{
	{
		Name:        "moonshot-v1-8k",
		Provider:    "Moonshot",
		Description: "Moonshot API - Fast and efficient for everyday tasks",
		Tags:        []string{"moonshot", "chat"},
		PriceType:   "pay-per-token",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.25 /M tokens",
			"OutputTokens": "$1.25 /M tokens",
		},
	},
}
