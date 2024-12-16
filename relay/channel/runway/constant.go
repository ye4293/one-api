package runway

import "github.com/songquanpeng/one-api/relay/model"

var ModelDetails = []model.APIModel{
	{
		Provider:    "Runway",
		Name:        "gen3a_turbo",
		Tags:        []string{"video", "runway"},
		PriceType:   "pay-per-use",
		Description: "Runway Gen-3 text-to-video generation model",
		Prices: map[string]interface{}{
			"5s":  "$0.25",
			"10s": "$0.50",
		},
	},
}
