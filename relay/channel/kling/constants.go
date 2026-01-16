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
)
