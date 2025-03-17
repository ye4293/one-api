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
	RelayModelGenerateCore
	RelayModelGenerateSd3
	RelayModelGenerateUltra
	RelayModeUpscaleConservative
	RelayModeUpscaleCreative
	RelayModeUpscaleCreativeResult
	RelayModeEditErase
	RelayModeEditInpaint
	RelayModeEditOutpaint
	RelayModeEditSR //Search and Replace 搜索和替换
	RelayModeEditRB //Remove Background 删除背景
	RelayModeControlSketch
	RelayModeControlStructure
	RelayModeImageToVideo
	RelayModeVideoResult
	RelayMode3D
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
	} else if strings.HasPrefix(path, "/v1/images/generations") || strings.HasPrefix(path, "/v1/images/edits") {
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

	// 移除可能的模式前缀
	path = removeModePrefix(path)

	if strings.HasSuffix(path, "/mj/submit/action") {
		relayMode = RelayModeMidjourneyAction
	} else if strings.HasSuffix(path, "/mj/submit/modal") {
		relayMode = RelayModeMidjourneyModal
	} else if strings.HasSuffix(path, "/mj/submit/shorten") {
		relayMode = RelayModeMidjourneyShorten
	} else if strings.HasSuffix(path, "/mj/insight-face/swap") {
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

// 辅助函数：移除模式前缀
func removeModePrefix(path string) string {
	prefixes := []string{"/mj-fast", "/mj-turbo", "/mj-relax"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(path, prefix) {
			return strings.TrimPrefix(path, prefix)
		}
	}
	return path
}

func Path2RelayModeSd(path string) int {
	// 如果路径以 /sd/v2beta 开头，将其转换为 /v2beta 开头的路径
	path = strings.Replace(path, "/sd/v2beta", "/v2beta", 1)

	relayMode := RelayModeUnknown
	if strings.HasPrefix(path, "/v2beta/stable-image/generate/core") {
		relayMode = RelayModelGenerateCore
	} else if strings.HasPrefix(path, "/v2beta/stable-image/generate/ultra") {
		relayMode = RelayModelGenerateUltra
	} else if strings.HasPrefix(path, "/v2beta/stable-image/generate/sd3") {
		relayMode = RelayModelGenerateSd3
	} else if path == "/v2beta/stable-image/upscale/conservative" {
		relayMode = RelayModeUpscaleConservative
	} else if path == "/v2beta/stable-image/upscale/creative" {
		relayMode = RelayModeUpscaleCreative
	} else if strings.HasPrefix(path, "/v2beta/stable-image/upscale/creative/result") {
		relayMode = RelayModeUpscaleCreativeResult
	} else if strings.HasPrefix(path, "/v2beta/stable-image/edit/erase") {
		relayMode = RelayModeEditErase
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
	} else if strings.HasPrefix(path, "/v2beta/image-to-video/result") {
		relayMode = RelayModeVideoResult
	} else if strings.HasPrefix(path, "/v2beta/image-to-video") {
		relayMode = RelayModeImageToVideo
	} else if strings.HasPrefix(path, "/v2beta/3d/stable-fast-3d") {
		relayMode = RelayMode3D
	}

	return relayMode
}
