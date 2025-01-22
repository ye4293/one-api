package pixverse

// PixverseRequest1 represents the request parameters for Pixverse API
type PixverseRequest1 struct {
	AspectRatio    string `json:"aspect_ratio" binding:"required"` // e.g. "16:9"
	Duration       int    `json:"duration" binding:"required"`     // video duration in seconds
	Model          string `json:"model" binding:"required"`        // e.g. "v1.5"
	MotionMode     string `json:"motion_mode,omitempty"`           // e.g. "normal", "fast"
	NegativePrompt string `json:"negative_prompt,omitempty"`       // max 2048 characters
	Prompt         string `json:"prompt" binding:"required"`       // max 2048 characters
	Quality        string `json:"quality" binding:"required"`      // e.g. "540p", "720p", "1080p"
	Seed           int    `json:"seed,omitempty"`                  // range: 0-2147483647
	Style          string `json:"style,omitempty"`                 // e.g. "anime", "3d_animation"
	TemplateId     int    `json:"template_id,omitempty"`           // template ID if needed
	WaterMark      bool   `json:"water_mark,omitempty"`            // default: false
}

// PixverseRequest2 represents the request parameters for Pixverse API with image input
type PixverseRequest2 struct {
	Duration       int    `json:"duration" binding:"required"` // video duration in seconds
	ImgId          int    `json:"img_id,omitempty"`            // Image ID from Upload Image API
	ImgUrl         string `json:"img_url,omitempty"`           // Image URL from Upload Image API
	Model          string `json:"model" binding:"required"`    // e.g. "v1.5"
	MotionMode     string `json:"motion_mode,omitempty"`       // e.g. "normal", "fast"
	NegativePrompt string `json:"negative_prompt,omitempty"`   // max 2048 characters
	Prompt         string `json:"prompt" binding:"required"`   // max 2048 characters
	Quality        string `json:"quality" binding:"required"`  // e.g. "540p", "720p", "1080p"
	Seed           int    `json:"seed,omitempty"`              // range: 0-2147483647
	Style          string `json:"style,omitempty"`             // e.g. "anime", "3d_animation"
	TemplateId     int    `json:"template_id,omitempty"`       // template ID if needed
	WaterMark      bool   `json:"water_mark,omitempty"`        // default: false
}

// UploadImageResponse represents the response structure for image upload API
type UploadImageResponse struct {
	ErrCode int              `json:"ErrCode,omitempty"` // error code
	ErrMsg  string           `json:"ErrMsg,omitempty"`  // error message
	Resp    *UploadImageResp `json:"resp,omitempty"`    // response data
}

// UploadImageResp represents the detailed response data for image upload
type UploadImageResp struct {
	ImgId  int    `json:"img_id,omitempty"`  // uploaded image ID
	ImgUrl string `json:"img_url,omitempty"` // uploaded image URL
}

// VideoResponse represents the response structure for video API
type PixverseVideoResponse struct {
	ErrCode    int        `json:"ErrCode,omitempty"` // error code
	ErrMsg     string     `json:"ErrMsg,omitempty"`  // error message
	Resp       *VideoResp `json:"resp,omitempty"`    // response data
	StatusCode int        `json:"status_code"`
}

// VideoResp represents the detailed response data for video
type VideoResp struct {
	VideoId int `json:"video_id,omitempty"` // video ID
}

// PixverseFinalResponse represents the final response structure for Pixverse API
type PixverseFinalResponse struct {
	ErrCode int                `json:"ErrCode,omitempty"` // error code
	ErrMsg  string             `json:"ErrMsg,omitempty"`  // error message
	Resp    *PixverseFinalResp `json:"resp,omitempty"`    // response data
}

// PixverseFinalResp represents the detailed response data for video generation
type PixverseFinalResp struct {
	CreateTime      string `json:"create_time,omitempty"`      // creation timestamp
	Id              int    `json:"id,omitempty"`               // video id
	ModifyTime      string `json:"modify_time,omitempty"`      // last update timestamp
	NegativePrompt  string `json:"negative_prompt,omitempty"`  // negative prompt used
	OutputHeight    int    `json:"outputHeight,omitempty"`     // height of video
	OutputWidth     int    `json:"outputWidth,omitempty"`      // width of video
	Prompt          string `json:"prompt,omitempty"`           // prompt used
	ResolutionRatio int    `json:"resolution_ratio,omitempty"` // video quality
	Seed            int    `json:"seed,omitempty"`             // seed used
	Size            int    `json:"size,omitempty"`             // video size
	Status          int    `json:"status,omitempty"`           // video status: 1=Generation successful, 5=Generating, 6=Deleted, 7=Generation failed
	Style           string `json:"style,omitempty"`            // style used
	Url             string `json:"url,omitempty"`              // video result URL
}
