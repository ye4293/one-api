package kling

// 通用请求参数
type KlingBaseRequest struct {
	Model          string `json:"model"`                      // 模型名称
	CallbackURL    string `json:"callback_url,omitempty"`     // 回调地址
	ExternalTaskID string `json:"external_task_id,omitempty"` // 外部任务ID
}

// 文生视频请求
type Text2VideoRequest struct {
	KlingBaseRequest
	Prompt         string  `json:"prompt"`                    // 提示词
	NegativePrompt string  `json:"negative_prompt,omitempty"` // 负面提示词
	Duration       int     `json:"duration,omitempty"`        // 视频时长(秒)
	AspectRatio    string  `json:"aspect_ratio,omitempty"`    // 宽高比
	CfgScale       float64 `json:"cfg_scale,omitempty"`       // CFG 强度
	Mode           string  `json:"mode,omitempty"`            // 生成模式
}

// 全能视频请求（支持文本+图片）
type OmniVideoRequest struct {
	KlingBaseRequest
	Prompt         string         `json:"prompt"`                    // 提示词
	NegativePrompt string         `json:"negative_prompt,omitempty"` // 负面提示词
	Image          string         `json:"image,omitempty"`           // 首帧图片URL或base64
	ImageTail      string         `json:"image_tail,omitempty"`      // 尾帧图片
	Duration       int            `json:"duration,omitempty"`        // 视频时长
	AspectRatio    string         `json:"aspect_ratio,omitempty"`    // 宽高比
	CameraControl  *CameraControl `json:"camera_control,omitempty"`  // 镜头控制
	Mode           string         `json:"mode,omitempty"`            // 生成模式
	CfgScale       float64        `json:"cfg_scale,omitempty"`       // CFG 强度
}

// 图生视频请求
type Image2VideoRequest struct {
	KlingBaseRequest
	Image          string  `json:"image"`                     // 图片URL或base64
	ImageTail      string  `json:"image_tail,omitempty"`      // 尾帧图片
	Prompt         string  `json:"prompt,omitempty"`          // 提示词
	NegativePrompt string  `json:"negative_prompt,omitempty"` // 负面提示词
	Duration       int     `json:"duration,omitempty"`        // 视频时长
	CfgScale       float64 `json:"cfg_scale,omitempty"`       // CFG 强度
	Mode           string  `json:"mode,omitempty"`            // 生成模式
}

// 多图生视频请求
type MultiImage2VideoRequest struct {
	KlingBaseRequest
	ImageList      []ImageItem `json:"image_list"`                // 图片列表
	Prompt         string      `json:"prompt"`                    // 提示词
	NegativePrompt string      `json:"negative_prompt,omitempty"` // 负面提示词
	Duration       int         `json:"duration,omitempty"`        // 视频时长
	AspectRatio    string      `json:"aspect_ratio,omitempty"`    // 宽高比
	Mode           string      `json:"mode,omitempty"`            // 生成模式
}

// 图片项
type ImageItem struct {
	Image string `json:"image"` // 图片URL或base64
}

// 镜头控制
type CameraControl struct {
	Type   string        `json:"type"`   // 镜头类型
	Config *CameraConfig `json:"config"` // 镜头配置
}

// 镜头配置
type CameraConfig struct {
	Horizontal float64 `json:"horizontal,omitempty"` // 水平移动
	Vertical   float64 `json:"vertical,omitempty"`   // 垂直移动
	Pan        float64 `json:"pan,omitempty"`        // 平移
	Tilt       float64 `json:"tilt,omitempty"`       // 倾斜
	Roll       float64 `json:"roll,omitempty"`       // 滚转
	Zoom       float64 `json:"zoom,omitempty"`       // 缩放
}

// API 响应
type KlingResponse struct {
	Code      int      `json:"code"`
	Message   string   `json:"message"`
	RequestID string   `json:"request_id"`
	Data      TaskData `json:"data"`
}

// 任务数据
type TaskData struct {
	TaskID        string                 `json:"task_id"`
	TaskStatus    string                 `json:"task_status"` // submitted/processing/succeed/failed
	TaskStatusMsg string                 `json:"task_status_msg,omitempty"`
	TaskInfo      map[string]interface{} `json:"task_info,omitempty"` // 任务创建时的参数信息（如 parent_video）
	CreatedAt     int64                  `json:"created_at"`
	UpdatedAt     int64                  `json:"updated_at"`
	TaskResult    TaskResult             `json:"task_result,omitempty"`
}

// 任务结果
type TaskResult struct {
	Videos []Video `json:"videos"`
}

// 视频信息
type Video struct {
	ID       string `json:"id"`       // 视频ID
	URL      string `json:"url"`      // 视频URL
	Duration string `json:"duration"` // 视频时长
}

// 回调通知结构（完整协议）
type CallbackNotification struct {
	TaskID         string                 `json:"task_id"`
	TaskStatus     string                 `json:"task_status"`
	TaskStatusMsg  string                 `json:"task_status_msg,omitempty"`
	TaskInfo       map[string]interface{} `json:"task_info,omitempty"`  // 任务创建时的参数信息
	CreatedAt      int64                  `json:"created_at,omitempty"` // Unix时间戳(ms)
	UpdatedAt      int64                  `json:"updated_at,omitempty"` // Unix时间戳(ms)
	TaskResult     TaskResult             `json:"task_result,omitempty"`
	ExternalTaskID string                 `json:"external_task_id,omitempty"` // 外部任务ID（系统内部ID）
}

// 查询任务状态响应
type QueryTaskResponse struct {
	Code      int      `json:"code"`
	Message   string   `json:"message"`
	RequestID string   `json:"request_id"`
	Data      TaskData `json:"data"`
}

// 人脸识别请求
type IdentifyFaceRequest struct {
	VideoID  string `json:"video_id,omitempty"`  // 可灵视频ID
	VideoURL string `json:"video_url,omitempty"` // 视频URL
}

// 人脸识别响应
type IdentifyFaceResponse struct {
	Code      int              `json:"code"`
	Message   string           `json:"message"`
	RequestID string           `json:"request_id"`
	Data      IdentifyFaceData `json:"data"`
}

type IdentifyFaceData struct {
	SessionID string     `json:"session_id"`
	FaceData  []FaceInfo `json:"face_data"`
}

type FaceInfo struct {
	FaceID    string `json:"face_id"`
	FaceImage string `json:"face_image"`
	StartTime int64  `json:"start_time"`
	EndTime   int64  `json:"end_time"`
}

// 对口型请求
type AdvancedLipSyncRequest struct {
	KlingBaseRequest
	SessionID  string       `json:"session_id"`
	FaceChoose []FaceChoose `json:"face_choose"`
}

type FaceChoose struct {
	FaceID              string  `json:"face_id"`
	AudioID             string  `json:"audio_id,omitempty"`
	SoundFile           string  `json:"sound_file,omitempty"`
	SoundStartTime      int64   `json:"sound_start_time"`
	SoundEndTime        int64   `json:"sound_end_time"`
	SoundInsertTime     int64   `json:"sound_insert_time"`
	SoundVolume         float64 `json:"sound_volume,omitempty"`
	OriginalAudioVolume float64 `json:"original_audio_volume,omitempty"`
}
