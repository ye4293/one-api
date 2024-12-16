package ali

import "github.com/songquanpeng/one-api/relay/model"

var ModelList = []string{
	"qwen-turbo", "qwen-plus", "qwen-max", "qwen-max-longcontext",
	"text-embedding-v1",
}

var ModelDetails = []model.APIModel{
	{
		Provider:    "Anthropic Claude",
		Name:        "claude-3-haiku-20240307",
		Tags:        []string{"claude", "chat"},
		PriceType:   "pay-per-token",
		Description: "Claude 3 Haiku - Fast and efficient for everyday tasks",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.25 /M tokens",
			"OutputTokens": "$1.25 /M tokens",
		},
	}}
