package common

const (
	SdActionGenerateUltra         = "GENERATE_Ultra"
	SdActionGenerateCore          = "GENERATE_CORE"
	SdActionGenerateSd3           = "GENERATE_SD3"
	SdActionGenerateSd3Turbo      = "GENERATE_SD3_TURBO"
	SdActionUpscaleConservative   = "UPSCALE_CONSERVATIVE"
	SdActionUpscaleCreative       = "UPSCALE_CREATIVE"
	SdActionUpscaleCreativeResult = "UPSCALE_CREATIVE_RESULT"
	SdActionEditErase             = "EDIT_Erase"
	SdActionEditInpaint           = "EDIT_INPAINT"
	SdActionEditOutpaint          = "EDIT_OUTPAINT"
	SdActionEditSearchReplace     = "EDIT_SEARCH_REPLACE"
	SdActionEditRemoveBackground  = "EDIT_REMOVE_BACKGROUND"
	SdActionControlSketch         = "CONTROL_SKETCH"
	SdActionControlStructure      = "CONTROL_STRUCTURE"
	SdActionImageToVideo          = "IMAGE_TO_VIDEO"
	SdActionVideoResult           = "VIDEO_RESULT"
)

var SdModel2Action = map[string]string{
	"generate_ultra":          SdActionGenerateUltra,
	"generate_core":           SdActionGenerateCore,
	"generate_sd3":            SdActionGenerateSd3,
	"generate_sd3_turbo":      SdActionGenerateSd3Turbo,
	"upscale_conservative":    SdActionUpscaleConservative,
	"upscale_creative":        SdActionUpscaleCreative,
	"upscale_creative_result": SdActionUpscaleCreativeResult,
	"edit_erase":              SdActionEditErase,
	"edit_inpaint":            SdActionEditInpaint,
	"edit_outpaint":           SdActionEditOutpaint,
	"edit_search_replace":     SdActionEditSearchReplace,
	"edit_remove_background":  SdActionEditRemoveBackground,
	"control_sketch":          SdActionControlSketch,
	"control_structure":       SdActionControlStructure,
	"image_to_video":          SdActionImageToVideo,
	"video_result":            SdActionVideoResult,
}
