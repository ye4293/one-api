package midjourney

import "github.com/songquanpeng/one-api/relay/model"

var ModelList = []string{
	"mj_imagine",
	"mj_variation",
	"mj_reroll",
	"mj_blend",
	"mj_modal",
	"mj_zoom",
	"mj_shorten",
	"mj_high_variation",
	"mj_low_variation",
	"mj_pan",
	"mj_inpaint",
	"mj_custom_zoom",
	"mj_describe",
	"mj_upscale",
	"swap_face",
}

var ModelDetails = []model.APIModel{
	{
		Name:        "mj_imagine",
		Provider:    "Midjourney",
		Description: "Midjourney API - Fast and efficient for everyday tasks",
		Tags:        []string{"midjourney", "video"},
		PriceType:   "pay-per-token",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.25 /M tokens",
			"OutputTokens": "$1.25 /M tokens",
		},
	},
}
