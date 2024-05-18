package constant

import "strings"

const (
	RelayModeUnknown = iota
	RelayModeChatCompletions
	RelayModeCompletions
	RelayModeEmbeddings
	RelayModeModerations
	RelayModeImagesGenerations
	RelayModeEdits
	RelayModeAudioSpeech
	RelayModeAudioTranscription
	RelayModeAudioTranslation
	RelayModeMidjourneyImagine
	RelayModeMidjourneyDescribe
	RelayModeMidjourneyBlend
	RelayModeMidjourneyChange
	RelayModeMidjourneySimpleChange
	RelayModeMidjourneyNotify
	RelayModeMidjourneyTaskFetch
	RelayModeMidjourneyTaskImageSeed
	RelayModeMidjourneyTaskFetchByCondition
	RelayModeMidjourneyAction
	RelayModeMidjourneyModal
	RelayModeMidjourneyShorten
	RelayModeSwapFace
)

func Path2RelayMode(path string) int {
	relayMode := RelayModeUnknown
	if strings.HasPrefix(path, "/v1/chat/completions") {
		relayMode = RelayModeChatCompletions
	} else if strings.HasPrefix(path, "/v1/completions") {
		relayMode = RelayModeCompletions
	} else if strings.HasPrefix(path, "/v1/embeddings") {
		relayMode = RelayModeEmbeddings
	} else if strings.HasSuffix(path, "embeddings") {
		relayMode = RelayModeEmbeddings
	} else if strings.HasPrefix(path, "/v1/moderations") {
		relayMode = RelayModeModerations
	} else if strings.HasPrefix(path, "/v1/images/generations") {
		relayMode = RelayModeImagesGenerations
	} else if strings.HasPrefix(path, "/v1/edits") {
		relayMode = RelayModeEdits
	} else if strings.HasPrefix(path, "/v1/audio/speech") {
		relayMode = RelayModeAudioSpeech
	} else if strings.HasPrefix(path, "/v1/audio/transcriptions") {
		relayMode = RelayModeAudioTranscription
	} else if strings.HasPrefix(path, "/v1/audio/translations") {
		relayMode = RelayModeAudioTranslation
	}
	return relayMode
}

func Path2RelayModeMidjourney(path string) int {
	relayMode := RelayModeUnknown
	if strings.HasSuffix(path, "/mj/submit/action") {
		// midjourney plus
		relayMode = RelayModeMidjourneyAction
	} else if strings.HasSuffix(path, "/mj/submit/modal") {
		// midjourney plus
		relayMode = RelayModeMidjourneyModal
	} else if strings.HasSuffix(path, "/mj/submit/shorten") {
		// midjourney plus
		relayMode = RelayModeMidjourneyShorten
	} else if strings.HasSuffix(path, "/mj/insight-face/swap") {
		// midjourney plus
		relayMode = RelayModeSwapFace
	} else if strings.HasSuffix(path, "/mj/submit/imagine") {
		relayMode = RelayModeMidjourneyImagine
	} else if strings.HasSuffix(path, "/mj/submit/blend") {
		relayMode = RelayModeMidjourneyBlend
	} else if strings.HasSuffix(path, "/mj/submit/describe") {
		relayMode = RelayModeMidjourneyDescribe
	} else if strings.HasSuffix(path, "/mj/notify") {
		relayMode = RelayModeMidjourneyNotify
	} else if strings.HasSuffix(path, "/mj/submit/change") {
		relayMode = RelayModeMidjourneyChange
	} else if strings.HasSuffix(path, "/mj/submit/simple-change") {
		relayMode = RelayModeMidjourneyChange
	} else if strings.HasSuffix(path, "/fetch") {
		relayMode = RelayModeMidjourneyTaskFetch
	} else if strings.HasSuffix(path, "/image-seed") {
		relayMode = RelayModeMidjourneyTaskImageSeed
	} else if strings.HasSuffix(path, "/list-by-condition") {
		relayMode = RelayModeMidjourneyTaskFetchByCondition
	}
	return relayMode
}

const (
	RelayModeUnknown2 = iota
	RelayModelGenerateCore
	RelayModelGenerateSd3
	RelayModeUpscaleCreative
	RelayModeUpscaleCreativeResult
	RelayModeEditInpaint
	RelayModeEditOutpaint
	RelayModeEditSR //Search and Replace 搜索和替换
	RelayModeEditRB //Remove Background 删除背景
	RelayModeControlSketch
	RelayModeControlStructure
)

func Path2RelayModeSd(path string) int {
	relayMode := RelayModeUnknown2
	if strings.HasPrefix(path, "/v2beta/stable-image/generate/core") {
		relayMode = RelayModelGenerateCore
	} else if strings.HasPrefix(path, "/v2beta/stable-image/generate/sd3") {
		relayMode = RelayModelGenerateSd3
	} else if path == "/v2beta/stable-image/upscale/creative" {
		relayMode = RelayModeUpscaleCreative
	} else if strings.HasPrefix(path, "/v2beta/stable-image/upscale/creative/result") {
		relayMode = RelayModeUpscaleCreativeResult
	} else if strings.HasPrefix(path, "/v2beta/stable-image/edit/inpaint") {
		relayMode = RelayModeEditInpaint
	} else if strings.HasPrefix(path, "/v2beta/stable-image/edit/outpaint") {
		relayMode = RelayModeEditOutpaint
	} else if strings.HasPrefix(path, "/v2beta/stable-image/edit/search-and-replace") {
		relayMode = RelayModeEditSR
	} else if strings.HasPrefix(path, "/v2beta/stable-image/edit/remove-background") {
		relayMode = RelayModeEditRB
	} else if strings.HasPrefix(path, "/v2beta/stable-image/control/sketch") {
		relayMode = RelayModeControlSketch
	} else if strings.HasPrefix(path, "/v2beta/stable-image/control/structure") {
		relayMode = RelayModeControlStructure
	}
	return relayMode
}
