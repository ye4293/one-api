package keling

import "github.com/songquanpeng/one-api/relay/model"

var ModelList = []string{
	"kling-v1-5", "kling-v1",
}

var ModelDetails = []model.APIModel{
	{
		Provider:    "KLING",
		Name:        "kling-v1",
		Tags:        []string{"video", "kling"},
		PriceType:   "pay-per-use",
		Description: "KLING Model V1.0 - Text to video generation with standard and professional modes",
		Prices: map[string]interface{}{
			"std_5s":  "$0.14",
			"std_10s": "$0.28",
			"pro_5s":  "$0.49",
			"pro_10s": "$0.98",
			"std_ext": "$0.14", // 标准模式扩展价格
			"pro_ext": "$0.49", // 专业模式扩展价格
		},
	},
	{
		Provider:    "KLING",
		Name:        "kling-v1-5",
		Tags:        []string{"video", "kling"},
		PriceType:   "pay-per-use",
		Description: "KLING Model V1.5 - Advanced text to video generation with improved quality and performance",
		Prices: map[string]interface{}{
			"std_5s":  "$0.28",
			"std_10s": "$0.56",
			"pro_5s":  "$0.49",
			"pro_10s": "$0.98",
		},
	},
}
