package deepseek

import "github.com/songquanpeng/one-api/relay/model"

var ModelList = []string{
	"deepseek-chat",
}

var ModelDetails = []model.APIModel{
	{
		Provider:    "Deepseek",
		Name:        "deepseek-chat",
		Tags:        []string{"deepseek", "chat"},
		PriceType:   "pay-per-token",
		Description: "Deepseek Chat - 64K context model with 4K output (8K Beta)",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.14 /M tokens",
			"OutputTokens": "$0.28 /M tokens",
		},
	},
}
