package keling

type TextToVideoRequest struct {
	Model          string         `json:"model,omitempty"`
	Prompt         string         `json:"prompt"`
	NegativePrompt string         `json:"negative_prompt,omitempty"`
	CfgScale       float64        `json:"cfg_scale,omitempty"`
	Mode           string         `json:"mode,omitempty"`
	CameraControl  *CameraControl `json:"camera_control,omitempty"`
	AspectRatio    string         `json:"aspect_ratio,omitempty"`
	Duration       string         `json:"duration,omitempty"`
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
	Model          string  `json:"model,omitempty"`
	Image          string  `json:"image"`
	ImageTail      string  `json:"image_tail,omitempty"`
	Prompt         string  `json:"prompt,omitempty"`
	NegativePrompt string  `json:"negative_prompt,omitempty"`
	CfgScale       float64 `json:"cfg_scale,omitempty"`
	Mode           string  `json:"mode,omitempty"`
	Duration       string  `json:"duration,omitempty"`
	CallbackURL    string  `json:"callback_url,omitempty"`
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

// TaskData 表示任务的详细信息
type TaskData struct {
	TaskID        string     `json:"task_id"`
	TaskStatus    string     `json:"task_status"`
	TaskStatusMsg string     `json:"task_status_msg"`
	CreatedAt     int64      `json:"created_at"`
	UpdatedAt     int64      `json:"updated_at"`
	TaskResult    TaskResult `json:"task_result"`
}

// KelingVideoResponse 表示可灵 AI 视频生成 API 的响应
type KelingVideoResponse struct {
	Code       int      `json:"code"`
	Message    string   `json:"message"`
	RequestID  string   `json:"request_id"`
	Data       TaskData `json:"data"`
	StatusCode int      `json:"status_code"`
}
