package cohere

import "github.com/songquanpeng/one-api/relay/model"

var ModelList = []string{"command", "command-light", "command-nightly", "command-light-nightly", "command-r", "command-r-plus"}

func init() {
	num := len(ModelList)
	for i := 0; i < num; i++ {
		ModelList = append(ModelList, ModelList[i]+"-internet")
	}
}

var ModelDetails = []model.APIModel{
	{
		Name:        "command",
		Provider:    "Cohere",
		Description: "Command - Fast and efficient for everyday tasks",
		Tags:        []string{"cohere", "chat"},
		PriceType:   "pay-per-token",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.25 /M tokens",
			"OutputTokens": "$1.25 /M tokens",
		},
	},
	{
		Name:        "command-light",
		Provider:    "Cohere",
		Description: "Command-Light - Fast and efficient for everyday tasks",
		Tags:        []string{"cohere", "chat"},
		PriceType:   "pay-per-token",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.25 /M tokens",
			"OutputTokens": "$1.25 /M tokens",
		},
	},
	{
		Name:        "command-nightly",
		Provider:    "Cohere",
		Description: "Command-Nightly - Fast and efficient for everyday tasks",
		Tags:        []string{"cohere", "chat"},
		PriceType:   "pay-per-token",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.25 /M tokens",
			"OutputTokens": "$1.25 /M tokens",
		},
	},
	{
		Name:        "command-light-nightly",
		Provider:    "Cohere",
		Description: "Command-Light-Nightly - Fast and efficient for everyday tasks",
		Tags:        []string{"cohere", "chat"},
		PriceType:   "pay-per-token",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.25 /M tokens",
			"OutputTokens": "$1.25 /M tokens",
		},
	},
	{
		Name:        "command-r",
		Provider:    "Cohere",
		Description: "Command-R - Fast and efficient for everyday tasks",
		Tags:        []string{"cohere", "chat"},
		PriceType:   "pay-per-token",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.25 /M tokens",
			"OutputTokens": "$1.25 /M tokens",
		},
	},
	{
		Name:        "command-r-plus",
		Provider:    "Cohere",
		Description: "Command-R-Plus - Fast and efficient for everyday tasks",
		Tags:        []string{"cohere", "chat"},
		PriceType:   "pay-per-token",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.25 /M tokens",
			"OutputTokens": "$1.25 /M tokens",
		},
	},
}
