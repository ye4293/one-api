package baidu

import "github.com/songquanpeng/one-api/relay/model"

var ModelList = []string{
	"ERNIE-Bot-4",
	"ERNIE-Bot-8K",
	"ERNIE-Bot",
	"ERNIE-Speed",
	"ERNIE-Bot-turbo",
	"Embedding-V1",
	"bge-large-zh",
	"bge-large-en",
	"tao-8k",
}

var ModelDetails = []model.APIModel{
	{
		Provider:    "Baidu",
		Name:        "ERNIE-Bot-4",
		Tags:        []string{"baidu", "chat"},
		PriceType:   "pay-per-token",
		Description: "ERNIE-Bot-4 - Fast and efficient for everyday tasks",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.25 /M tokens",
			"OutputTokens": "$1.25 /M tokens",
		},
	},
}
