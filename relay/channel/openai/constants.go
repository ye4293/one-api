package openai

import "github.com/songquanpeng/one-api/relay/model"

var ModelList = []string{
	"gpt-3.5-turbo", "gpt-3.5-turbo-0301", "gpt-3.5-turbo-0613", "gpt-3.5-turbo-1106", "gpt-3.5-turbo-0125",
	"gpt-3.5-turbo-16k", "gpt-3.5-turbo-16k-0613",
	"gpt-3.5-turbo-instruct",
	"gpt-4", "gpt-4-0314", "gpt-4-0613", "gpt-4-1106-preview", "gpt-4-0125-preview",
	"gpt-4-32k", "gpt-4-32k-0314", "gpt-4-32k-0613",
	"gpt-4-turbo-preview",
	"gpt-4-vision-preview", "chatgpt-4o-latest",
	"gpt-4o-2024-05-13", "gpt-4o-mini", "gpt-4o", "gpt-4o-mini-2024-07-18", "o1-preview", "o1-preview-2024-09-12", "o1-mini", "o1-mini-2024-09-12", "gpt-4o-2024-08-06", "gpt-4o-2024-11-20",
	"gpt-4o-audio-preview", "gpt-4o-audio-preview-2024-12-17", "gpt-4o-audio-preview-2024-10-01", "gpt-4o-mini-audio-preview", "gpt-4o-mini-audio-preview-2024-12-17",
	"text-embedding-ada-002", "text-embedding-3-small", "text-embedding-3-large",
	"text-curie-001", "text-babbage-001", "text-ada-001", "text-davinci-002", "text-davinci-003",
	"text-moderation-latest", "text-moderation-stable",
	"text-davinci-edit-001",
	"davinci-002", "babbage-002",
	"dall-e-2", "dall-e-3",
	"whisper-1", "o1", "o3-mini", "o3-mini-2025-01-31", "o1-2024-12-17", "gpt-4.1",
	"gpt-4.1-2025-04-14", "gpt-4.1-mini", "gpt-4.1-mini-2025-04-14", "gpt-4.1-nano", "gpt-4.1-nano-2025-04-14",
	"tts-1", "tts-1-1106", "tts-1-hd", "tts-1-hd-1106", "o4-mini", "o3-2025-04-16", "o4-mini-2025-04-16", "o3",
}

var ModelDetails = []model.APIModel{
	// GPT-3.5 基础系列
	{
		Provider:    "OpenAI",
		Name:        "gpt-3.5-turbo",
		Tags:        []string{"openai", "chat"},
		PriceType:   "pay-per-token",
		Description: "Latest GPT-3.5 Turbo model",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.50 /M tokens",
			"OutputTokens": "$1.50 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "gpt-3.5-turbo-0301",
		Tags:        []string{"openai", "chat"},
		PriceType:   "pay-per-token",
		Description: "GPT-3.5 Turbo March 2023 version",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.50 /M tokens",
			"OutputTokens": "$1.50 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "gpt-3.5-turbo-0613",
		Tags:        []string{"openai", "chat"},
		PriceType:   "pay-per-token",
		Description: "GPT-3.5 Turbo June 2023 version",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.50 /M tokens",
			"OutputTokens": "$1.50 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "gpt-3.5-turbo-1106",
		Tags:        []string{"openai", "chat"},
		PriceType:   "pay-per-token",
		Description: "GPT-3.5 Turbo November 2023 version",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.50 /M tokens",
			"OutputTokens": "$1.50 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "gpt-3.5-turbo-0125",
		Tags:        []string{"openai", "chat"},
		PriceType:   "pay-per-token",
		Description: "GPT-3.5 Turbo January 2024 version",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.50 /M tokens",
			"OutputTokens": "$1.50 /M tokens",
		},
	},

	// GPT-3.5 16K 系列
	{
		Provider:    "OpenAI",
		Name:        "gpt-3.5-turbo-16k",
		Tags:        []string{"openai", "chat"},
		PriceType:   "pay-per-token",
		Description: "GPT-3.5 Turbo with 16K context window",
		Prices: map[string]interface{}{
			"InputTokens":  "$1.00 /M tokens",
			"OutputTokens": "$2.00 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "gpt-3.5-turbo-16k-0613",
		Tags:        []string{"openai", "chat"},
		PriceType:   "pay-per-token",
		Description: "GPT-3.5 Turbo 16K June 2023 version",
		Prices: map[string]interface{}{
			"InputTokens":  "$1.00 /M tokens",
			"OutputTokens": "$2.00 /M tokens",
		},
	},

	// GPT-3.5 Instruct
	{
		Provider:    "OpenAI",
		Name:        "gpt-3.5-turbo-instruct",
		Tags:        []string{"openai", "chat"},
		PriceType:   "pay-per-token",
		Description: "GPT-3.5 Turbo Instruct model",
		Prices: map[string]interface{}{
			"InputTokens":  "$1.50 /M tokens",
			"OutputTokens": "$2.00 /M tokens",
		},
	},
	// GPT-4 基础系列
	{
		Provider:    "OpenAI",
		Name:        "gpt-4",
		Tags:        []string{"openai", "chat"},
		PriceType:   "pay-per-token",
		Description: "Standard GPT-4 model",
		Prices: map[string]interface{}{
			"InputTokens":  "$30.00 /M tokens",
			"OutputTokens": "$60.00 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "gpt-4-0314",
		Tags:        []string{"openai", "chat"},
		PriceType:   "pay-per-token",
		Description: "GPT-4 March 2023 version",
		Prices: map[string]interface{}{
			"InputTokens":  "$30.00 /M tokens",
			"OutputTokens": "$60.00 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "gpt-4-0613",
		Tags:        []string{"openai", "chat"},
		PriceType:   "pay-per-token",
		Description: "GPT-4 June 2023 version",
		Prices: map[string]interface{}{
			"InputTokens":  "$30.00 /M tokens",
			"OutputTokens": "$60.00 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "gpt-4-1106-preview",
		Tags:        []string{"openai", "chat"},
		PriceType:   "pay-per-token",
		Description: "GPT-4 November 2023 preview version",
		Prices: map[string]interface{}{
			"InputTokens":  "$10.00 /M tokens",
			"OutputTokens": "$30.00 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "gpt-4-0125-preview",
		Tags:        []string{"openai", "chat"},
		PriceType:   "pay-per-token",
		Description: "GPT-4 January 2024 preview version",
		Prices: map[string]interface{}{
			"InputTokens":  "$10.00 /M tokens",
			"OutputTokens": "$30.00 /M tokens",
		},
	},

	// GPT-4 32K 系列
	{
		Provider:    "OpenAI",
		Name:        "gpt-4-32k",
		Tags:        []string{"openai", "chat"},
		PriceType:   "pay-per-token",
		Description: "GPT-4 with 32K context window",
		Prices: map[string]interface{}{
			"InputTokens":  "$60.00 /M tokens",
			"OutputTokens": "$120.00 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "gpt-4-32k-0314",
		Tags:        []string{"openai", "chat"},
		PriceType:   "pay-per-token",
		Description: "GPT-4 32K March 2023 version",
		Prices: map[string]interface{}{
			"InputTokens":  "$60.00 /M tokens",
			"OutputTokens": "$120.00 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "gpt-4-32k-0613",
		Tags:        []string{"openai", "chat"},
		PriceType:   "pay-per-token",
		Description: "GPT-4 32K June 2023 version",
		Prices: map[string]interface{}{
			"InputTokens":  "$60.00 /M tokens",
			"OutputTokens": "$120.00 /M tokens",
		},
	},

	// GPT-4 Turbo 和 Vision 系列
	{
		Provider:    "OpenAI",
		Name:        "gpt-4-turbo-preview",
		Tags:        []string{"openai", "chat"},
		PriceType:   "pay-per-token",
		Description: "GPT-4 Turbo preview version",
		Prices: map[string]interface{}{
			"InputTokens":  "$10.00 /M tokens",
			"OutputTokens": "$30.00 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "gpt-4-vision-preview",
		Tags:        []string{"openai", "chat", "vision"},
		PriceType:   "pay-per-token",
		Description: "GPT-4 with vision capabilities",
		Prices: map[string]interface{}{
			"InputTokens":  "$10.00 /M tokens",
			"OutputTokens": "$30.00 /M tokens",
		},
	},

	// OpenAI Assistant (o1) 系列
	{
		Provider:    "OpenAI",
		Name:        "chatgpt-4o-latest",
		Tags:        []string{"openai", "chat", "assistant"},
		PriceType:   "pay-per-token",
		Description: "Latest version of OpenAI Assistant",
		Prices: map[string]interface{}{
			"InputTokens":  "$10.00 /M tokens",
			"OutputTokens": "$30.00 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "gpt-4o-2024-05-13",
		Tags:        []string{"openai", "chat", "assistant"},
		PriceType:   "pay-per-token",
		Description: "OpenAI Assistant May 2024 version",
		Prices: map[string]interface{}{
			"InputTokens":  "$10.00 /M tokens",
			"OutputTokens": "$30.00 /M tokens",
		},
	},
	// 继续 OpenAI Assistant (o1) 系列
	{
		Provider:    "OpenAI",
		Name:        "gpt-4o-mini",
		Tags:        []string{"openai", "chat", "assistant"},
		PriceType:   "pay-per-token",
		Description: "Lightweight version of OpenAI Assistant",
		Prices: map[string]interface{}{
			"InputTokens":  "$10.00 /M tokens",
			"OutputTokens": "$30.00 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "gpt-4o",
		Tags:        []string{"openai", "chat", "assistant"},
		PriceType:   "pay-per-token",
		Description: "Standard OpenAI Assistant model",
		Prices: map[string]interface{}{
			"InputTokens":  "$10.00 /M tokens",
			"OutputTokens": "$30.00 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "gpt-4o-mini-2024-07-18",
		Tags:        []string{"openai", "chat", "assistant"},
		PriceType:   "pay-per-token",
		Description: "Mini Assistant July 2024 version",
		Prices: map[string]interface{}{
			"InputTokens":  "$10.00 /M tokens",
			"OutputTokens": "$30.00 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "o1-preview",
		Tags:        []string{"openai", "chat", "assistant"},
		PriceType:   "pay-per-token",
		Description: "Preview version of O1 Assistant",
		Prices: map[string]interface{}{
			"InputTokens":  "$10.00 /M tokens",
			"OutputTokens": "$30.00 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "o1-preview-2024-09-12",
		Tags:        []string{"openai", "chat", "assistant"},
		PriceType:   "pay-per-token",
		Description: "O1 Preview September 2024 version",
		Prices: map[string]interface{}{
			"InputTokens":  "$10.00 /M tokens",
			"OutputTokens": "$30.00 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "o1-mini",
		Tags:        []string{"openai", "chat", "assistant"},
		PriceType:   "pay-per-token",
		Description: "Lightweight O1 Assistant model",
		Prices: map[string]interface{}{
			"InputTokens":  "$10.00 /M tokens",
			"OutputTokens": "$30.00 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "o1-mini-2024-09-12",
		Tags:        []string{"openai", "chat", "assistant"},
		PriceType:   "pay-per-token",
		Description: "O1 Mini September 2024 version",
		Prices: map[string]interface{}{
			"InputTokens":  "$10.00 /M tokens",
			"OutputTokens": "$30.00 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "gpt-4o-2024-08-06",
		Tags:        []string{"openai", "chat", "assistant"},
		PriceType:   "pay-per-token",
		Description: "OpenAI Assistant August 2024 version",
		Prices: map[string]interface{}{
			"InputTokens":  "$10.00 /M tokens",
			"OutputTokens": "$30.00 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "gpt-4o-2024-11-20",
		Tags:        []string{"openai", "chat", "assistant"},
		PriceType:   "pay-per-token",
		Description: "OpenAI Assistant November 2024 version",
		Prices: map[string]interface{}{
			"InputTokens":  "$10.00 /M tokens",
			"OutputTokens": "$30.00 /M tokens",
		},
	},

	// Embedding 模型系列
	{
		Provider:    "OpenAI",
		Name:        "text-embedding-ada-002",
		Tags:        []string{"openai", "embedding"},
		PriceType:   "pay-per-token",
		Description: "Ada Embedding model",
		Prices: map[string]interface{}{
			"InputTokens": "$0.10 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "text-embedding-3-small",
		Tags:        []string{"openai", "embedding"},
		PriceType:   "pay-per-token",
		Description: "Small version of text embedding v3",
		Prices: map[string]interface{}{
			"InputTokens": "$0.02 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "text-embedding-3-large",
		Tags:        []string{"openai", "embedding"},
		PriceType:   "pay-per-token",
		Description: "Large version of text embedding v3",
		Prices: map[string]interface{}{
			"InputTokens": "$0.13 /M tokens",
		},
	},

	// 传统 GPT-3 系列
	{
		Provider:    "OpenAI",
		Name:        "text-curie-001",
		Tags:        []string{"openai", "completion"},
		PriceType:   "pay-per-token",
		Description: "Curie base model",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.20 /M tokens",
			"OutputTokens": "$0.20 /M tokens",
		},
	},
	// 继续传统 GPT-3 系列
	{
		Provider:    "OpenAI",
		Name:        "text-babbage-001",
		Tags:        []string{"openai", "completion"},
		PriceType:   "pay-per-token",
		Description: "Babbage base model",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.16 /M tokens",
			"OutputTokens": "$0.16 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "text-ada-001",
		Tags:        []string{"openai", "completion"},
		PriceType:   "pay-per-token",
		Description: "Ada base model",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.10 /M tokens",
			"OutputTokens": "$0.10 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "text-davinci-002",
		Tags:        []string{"openai", "completion"},
		PriceType:   "pay-per-token",
		Description: "Davinci-002 base model",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.20 /M tokens",
			"OutputTokens": "$0.20 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "text-davinci-003",
		Tags:        []string{"openai", "completion"},
		PriceType:   "pay-per-token",
		Description: "Davinci-003 base model",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.20 /M tokens",
			"OutputTokens": "$0.20 /M tokens",
		},
	},

	// Moderation 系列
	{
		Provider:    "OpenAI",
		Name:        "text-moderation-latest",
		Tags:        []string{"openai", "moderation"},
		PriceType:   "free",
		Description: "Latest content moderation model",
		Prices: map[string]interface{}{
			"Free": "$0.00",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "text-moderation-stable",
		Tags:        []string{"openai", "moderation"},
		PriceType:   "free",
		Description: "Stable content moderation model",
		Prices: map[string]interface{}{
			"Free": "$0.00",
		},
	},

	// Edit 模型
	{
		Provider:    "OpenAI",
		Name:        "text-davinci-edit-001",
		Tags:        []string{"openai", "edit"},
		PriceType:   "pay-per-token",
		Description: "Text editing model",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.20 /M tokens",
			"OutputTokens": "$0.20 /M tokens",
		},
	},

	// 基础模型
	{
		Provider:    "OpenAI",
		Name:        "davinci-002",
		Tags:        []string{"openai", "base"},
		PriceType:   "pay-per-token",
		Description: "Davinci base model version 2",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.20 /M tokens",
			"OutputTokens": "$0.20 /M tokens",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "babbage-002",
		Tags:        []string{"openai", "base"},
		PriceType:   "pay-per-token",
		Description: "Babbage base model version 2",
		Prices: map[string]interface{}{
			"InputTokens":  "$0.16 /M tokens",
			"OutputTokens": "$0.16 /M tokens",
		},
	},

	// DALL-E 系列
	{
		Provider:    "OpenAI",
		Name:        "dall-e-2",
		Tags:        []string{"openai", "image"},
		PriceType:   "pay-per-image",
		Description: "DALL-E 2 image generation model",
		Prices: map[string]interface{}{
			"1024x1024": "$0.018",
			"512x512":   "$0.018",
			"256x256":   "$0.016",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "dall-e-3",
		Tags:        []string{"openai", "image"},
		PriceType:   "pay-per-image",
		Description: "DALL-E 3 advanced image generation model",
		Prices: map[string]interface{}{
			"1024x1024": "$0.040",
			"1024x1792": "$0.080",
			"1792x1024": "$0.080",
		},
	},

	// Whisper 系列
	{
		Provider:    "OpenAI",
		Name:        "whisper-1",
		Tags:        []string{"openai", "audio"},
		PriceType:   "pay-per-minute",
		Description: "Whisper speech-to-text model",
		Prices: map[string]interface{}{
			"per minute": "$0.006",
		},
	},

	// TTS 系列
	{
		Provider:    "OpenAI",
		Name:        "tts-1",
		Tags:        []string{"openai", "audio"},
		PriceType:   "pay-per-character",
		Description: "Text-to-Speech standard model",
		Prices: map[string]interface{}{
			"1K characters": "$0.015",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "tts-1-1106",
		Tags:        []string{"openai", "audio"},
		PriceType:   "pay-per-character",
		Description: "Text-to-Speech November 2023 version",
		Prices: map[string]interface{}{
			"1K characters": "$0.015",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "tts-1-hd",
		Tags:        []string{"openai", "audio"},
		PriceType:   "pay-per-character",
		Description: "Text-to-Speech HD quality model",
		Prices: map[string]interface{}{
			"1K characters": "$0.030",
		},
	},
	{
		Provider:    "OpenAI",
		Name:        "tts-1-hd-1106",
		Tags:        []string{"openai", "audio"},
		PriceType:   "pay-per-character",
		Description: "Text-to-Speech HD November 2023 version",
		Prices: map[string]interface{}{
			"1K characters": "$0.030",
		},
	},
}
