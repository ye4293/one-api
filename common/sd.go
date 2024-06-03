package common

const (
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
)

var SdModel2Action = map[string]string{
	"sd_generate_core":           SdActionGenerateCore,
	"sd_generate_sd3":            SdActionGenerateSd3,
	"sd_generate_sd3_turbo":      SdActionGenerateSd3Turbo,
	"sd_upscale_conservative":    SdActionUpscaleConservative,
	"sd_upscale_creative":        SdActionUpscaleCreative,
	"sd_upscale_creative_result": SdActionUpscaleCreativeResult,
	"sd_edit_erase":              SdActionEditErase,
	"sd_edit_inpaint":            SdActionEditInpaint,
	"sd_edit_outpaint":           SdActionEditOutpaint,
	"sd_edit_search_replace":     SdActionEditSearchReplace,
	"sd_edit_remove_background":  SdActionEditRemoveBackground,
	"sd_control_sketch":          SdActionControlSketch,
	"sd_control_structure":       SdActionControlStructure,
}
