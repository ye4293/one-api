package kling

const (
	// 任务状态
	TaskStatusPending    = "pending"
	TaskStatusSubmitted  = "submitted"
	TaskStatusProcessing = "processing"
	TaskStatusSucceed    = "succeed"
	TaskStatusFailed     = "failed"

	// 请求类型
	RequestTypeText2Video       = "text2video"
	RequestTypeOmniVideo        = "omni-video"
	RequestTypeImage2Video      = "image2video"
	RequestTypeMultiImage2Video = "multi-image2video"
	RequestTypeIdentifyFace     = "identify-face"
	RequestTypeAdvancedLipSync  = "advanced-lip-sync"

	// 视频相关（新增6个）
	RequestTypeMotionControl   = "motion-control"
	RequestTypeMultiElements   = "multi-elements"
	RequestTypeVideoExtend     = "video-extend"
	RequestTypeAvatarI2V       = "avatar-image2video"
	RequestTypeVideoEffects    = "video-effects"
	RequestTypeImageRecognize  = "image-recognize"

	// 音频相关（新增3个）
	RequestTypeTextToAudio  = "text-to-audio"
	RequestTypeVideoToAudio = "video-to-audio"
	RequestTypeTTS          = "tts"

	// 图片相关（新增4个）
	RequestTypeImageGeneration  = "image-generation"
	RequestTypeOmniImage        = "omni-image"
	RequestTypeMultiImage2Image = "multi-image2image"
	RequestTypeImageExpand      = "image-expand"

	// 通用类（新增2个）
	RequestTypeCustomElements = "custom-elements"
	RequestTypeCustomVoices   = "custom-voices"

	// 通用类 - 查询和管理接口
	RequestTypePresetsElements = "presets-elements"
	RequestTypeDeleteElements  = "delete-elements"
	RequestTypePresetsVoices   = "presets-voices"
	RequestTypeDeleteVoices    = "delete-voices"
)
