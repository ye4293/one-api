package recraft

import "github.com/songquanpeng/one-api/relay/model"

var ModelDetails = []model.APIModel{
	{
		Provider:    "Recraft",
		Name:        "recraft-api",
		Tags:        []string{"image", "recraft"},
		PriceType:   "pay-per-use",
		Description: "Recraft text-to-video generation model",
		Prices: map[string]interface{}{
			"5s":  "$0.25",
			"10s": "$0.50",
		},
	},
}
