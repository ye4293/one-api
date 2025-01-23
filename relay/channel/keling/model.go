package keling

type TextToVideoRequest struct {
	Model          string         `json:"model,omitempty"`
	ModelName      string         `json:"model_name,omitempty"`
	Prompt         string         `json:"prompt"`
	NegativePrompt string         `json:"negative_prompt,omitempty"`
	CfgScale       float64        `json:"cfg_scale,omitempty"`
	Mode           string         `json:"mode,omitempty"`
	CameraControl  *CameraControl `json:"camera_control,omitempty"`
	AspectRatio    string         `json:"aspect_ratio,omitempty"`
	Duration       interface{}    `json:"duration,omitempty"`
	CallbackURL    string         `json:"callback_url,omitempty"`
}

type CameraControl struct {
	Type   string        `json:"type,omitempty"`
	Config *CameraConfig `json:"config,omitempty"`
}

type CameraConfig struct {
	Horizontal float64 `json:"horizontal,omitempty"`
	Vertical   float64 `json:"vertical,omitempty"`
	Pan        float64 `json:"pan,omitempty"`
	Tilt       float64 `json:"tilt,omitempty"`
	Roll       float64 `json:"roll,omitempty"`
	Zoom       float64 `json:"zoom,omitempty"`
}

type ImageToVideoRequest struct {
	Model          string      `json:"model,omitempty"`
	ModelName      string      `json:"model_name,omitempty"`
	Image          string      `json:"image"`
	ImageTail      string      `json:"image_tail,omitempty"`
	Prompt         string      `json:"prompt,omitempty"`
	NegativePrompt string      `json:"negative_prompt,omitempty"`
	CfgScale       float64     `json:"cfg_scale,omitempty"`
	Mode           string      `json:"mode,omitempty"`
	Duration       interface{} `json:"duration,omitempty"`
	CallbackURL    string      `json:"callback_url,omitempty"`
}

// Video 表示单个视频的信息
type Video struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Duration string `json:"duration"`
}

// TaskResult 表示任务结果
type TaskResult struct {
	Videos []Video `json:"videos"`
}

type TaskData struct {
	TaskID        string     `json:"task_id,omitempty"`
	TaskStatus    string     `json:"task_status,omitempty"`
	TaskStatusMsg string     `json:"task_status_msg,omitempty"`
	CreatedAt     int64      `json:"created_at,omitempty"`
	UpdatedAt     int64      `json:"updated_at,omitempty"`
	TaskResult    TaskResult `json:"task_result,omitempty"`
	TaskInfo      TaskInfo   `json:"task_info,omitempty"`
}

type TaskInfo struct {
	ParentVideo    ParentVideo `json:"parent_video,omitempty"`
	ExternalTaskID string      `json:"external_task_id,omitempty"` // 客户自定义任务ID
}

type ParentVideo struct {
	ID       string `json:"id,omitempty"`       // 原视频ID；全局唯一
	URL      string `json:"url,omitempty"`      // 原视频的URL
	Duration string `json:"duration,omitempty"` // 原视频总时长，单位s
}

// KelingVideoResponse 表示可灵 AI 视频生成 API 的响应
type KelingVideoResponse struct {
	Code       int      `json:"code"`
	Message    string   `json:"message"`
	RequestID  string   `json:"request_id"`
	Data       TaskData `json:"data"`
	StatusCode int      `json:"status_code"`
}

type KlingLipRequest struct {
	Input struct {
		VideoId       string  `json:"video_id"`                 // 通过可灵AI生成的视频的ID
		Mode          string  `json:"mode"`                     // 生成视频的模式：text2video, audio2video
		Text          string  `json:"text,omitempty"`           // 生成对口型视频的文本内容，最大长度120
		VoiceId       string  `json:"voice_id,omitempty"`       // 音色ID
		VoiceLanguage string  `json:"voice_language,omitempty"` // 音色语种，默认值：zh
		VoiceSpeed    float64 `json:"voice_speed,omitempty"`    // 语速，范围：0.8~2.0，默认值：1.0
		AudioType     string  `json:"audio_type,omitempty"`     // 音频类型：file, url
		AudioFile     string  `json:"audio_file,omitempty"`     // 音频文件内容（base64编码）
		AudioUrl      string  `json:"audio_url,omitempty"`      // 音频文件URL
	} `json:"input"`
	CallbackUrl string `json:"callback_url,omitempty"` // 回调通知地址
}
