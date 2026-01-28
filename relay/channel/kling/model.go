package kling

import (
	"encoding/json"
	"fmt"
)

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
	Code      int         `json:"code"`
	Message   string      `json:"message"`
	RequestID string      `json:"request_id"`
	Data      interface{} `json:"data"` // 使用 interface{} 以支持不同类型的响应数据
}

// GetDataMap 获取 data 的 map 表示
func (r *KlingResponse) GetDataMap() (map[string]interface{}, error) {
	if r.Data == nil {
		return nil, fmt.Errorf("data is nil")
	}

	dataMap, ok := r.Data.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("data is not a map")
	}

	return dataMap, nil
}

// GetString 安全地从 data 中获取字符串字段
func (r *KlingResponse) GetString(key string) string {
	dataMap, err := r.GetDataMap()
	if err != nil {
		return ""
	}

	if val, ok := dataMap[key].(string); ok {
		return val
	}
	return ""
}

// GetInt64 安全地从 data 中获取 int64 字段
func (r *KlingResponse) GetInt64(key string) int64 {
	dataMap, err := r.GetDataMap()
	if err != nil {
		return 0
	}

	if val, ok := dataMap[key].(float64); ok {
		return int64(val)
	}
	return 0
}

// GetTaskID 获取 task_id 字段
func (r *KlingResponse) GetTaskID() string {
	return r.GetString("task_id")
}

// GetTaskStatus 获取 task_status 字段
func (r *KlingResponse) GetTaskStatus() string {
	return r.GetString("task_status")
}

// GetTaskData 获取 TaskData（用于异步接口）
func (r *KlingResponse) GetTaskData() (*TaskData, error) {
	dataMap, err := r.GetDataMap()
	if err != nil {
		return nil, err
	}

	// 将 map 转换为 TaskData 结构体
	jsonBytes, err := json.Marshal(dataMap)
	if err != nil {
		return nil, err
	}

	var taskData TaskData
	if err := json.Unmarshal(jsonBytes, &taskData); err != nil {
		return nil, err
	}

	return &taskData, nil
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
	TaskID             string                 `json:"task_id"`
	TaskStatus         string                 `json:"task_status"`
	TaskStatusMsg      string                 `json:"task_status_msg,omitempty"`
	TaskInfo           map[string]interface{} `json:"task_info,omitempty"`  // 任务创建时的参数信息
	CreatedAt          int64                  `json:"created_at,omitempty"` // Unix时间戳(ms)
	UpdatedAt          int64                  `json:"updated_at,omitempty"` // Unix时间戳(ms)
	TaskResult         TaskResult             `json:"task_result,omitempty"`
	ExternalTaskID     string                 `json:"external_task_id,omitempty"`     // 外部任务ID（系统内部ID）
	FinalUnitDeduction float64                `json:"final_unit_deduction,omitempty"` // 本次任务计费金额（人民币）
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

// ============ 视频类接口（新增6个） ============

// 镜头控制请求
type MotionControlRequest struct {
	KlingBaseRequest
	VideoID       string         `json:"video_id"`
	CameraControl *CameraControl `json:"camera_control,omitempty"`
	Duration      int            `json:"duration,omitempty"`
	Mode          string         `json:"mode,omitempty"`
}

// 多元素初始化选择请求
type MultiElementsRequest struct {
	KlingBaseRequest
	VideoID    string   `json:"video_id"`
	ElementIDs []string `json:"element_ids,omitempty"`
}

// 视频延长请求
type VideoExtendRequest struct {
	KlingBaseRequest
	VideoID   string `json:"video_id"`
	Duration  int    `json:"duration,omitempty"`
	Direction string `json:"direction,omitempty"` // before/after
}

// 数字人图生视频请求
type AvatarImage2VideoRequest struct {
	KlingBaseRequest
	Image       string `json:"image"`
	AudioFile   string `json:"audio_file,omitempty"`
	AudioID     string `json:"audio_id,omitempty"`
	Duration    int    `json:"duration,omitempty"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
	Mode        string `json:"mode,omitempty"`
}

// 视频效果应用请求
type VideoEffectsRequest struct {
	KlingBaseRequest
	VideoID      string                 `json:"video_id"`
	EffectType   string                 `json:"effect_type"`
	EffectParams map[string]interface{} `json:"effect_params,omitempty"`
}

// 图像识别请求
type ImageRecognizeRequest struct {
	KlingBaseRequest
	VideoID  string `json:"video_id,omitempty"`
	VideoURL string `json:"video_url,omitempty"`
	Image    string `json:"image,omitempty"`
}

// ============ 音频类接口（新增3个） ============

// 文本转音频请求
type TextToAudioRequest struct {
	KlingBaseRequest
	Text     string  `json:"text"`
	Voice    string  `json:"voice,omitempty"`
	Speed    float64 `json:"speed,omitempty"`
	Volume   float64 `json:"volume,omitempty"`
	Duration int     `json:"duration,omitempty"`
}

// 视频提取音频请求
type VideoToAudioRequest struct {
	KlingBaseRequest
	VideoID  string `json:"video_id,omitempty"`
	VideoURL string `json:"video_url,omitempty"`
}

// 文本转语音请求
type TTSRequest struct {
	KlingBaseRequest
	Text  string  `json:"text"`
	Voice string  `json:"voice,omitempty"`
	Speed float64 `json:"speed,omitempty"`
	Pitch float64 `json:"pitch,omitempty"`
}

// ============ 图片类接口（新增4个） ============

// 图片生成请求
type ImageGenerationRequest struct {
	KlingBaseRequest
	Prompt         string  `json:"prompt"`
	NegativePrompt string  `json:"negative_prompt,omitempty"`
	AspectRatio    string  `json:"aspect_ratio,omitempty"`
	N              int     `json:"n,omitempty"`
	Style          string  `json:"style,omitempty"`
	CfgScale       float64 `json:"cfg_scale,omitempty"`
}

// 全能图片请求
type OmniImageRequest struct {
	KlingBaseRequest
	Prompt         string  `json:"prompt"`
	Image          string  `json:"image,omitempty"`
	NegativePrompt string  `json:"negative_prompt,omitempty"`
	AspectRatio    string  `json:"aspect_ratio,omitempty"`
	Style          string  `json:"style,omitempty"`
	CfgScale       float64 `json:"cfg_scale,omitempty"`
}

// 多图转图请求
type MultiImage2ImageRequest struct {
	KlingBaseRequest
	Images         []ImageItem `json:"images"`
	Prompt         string      `json:"prompt"`
	NegativePrompt string      `json:"negative_prompt,omitempty"`
	AspectRatio    string      `json:"aspect_ratio,omitempty"`
}

// 图片扩展编辑请求
type ImageExpandRequest struct {
	KlingBaseRequest
	Image       string  `json:"image"`
	Prompt      string  `json:"prompt,omitempty"`
	Direction   string  `json:"direction,omitempty"` // top/bottom/left/right
	ExpandRatio float64 `json:"expand_ratio,omitempty"`
}

// ============ 通用类接口（新增2个） ============

// 自定义元素训练请求（同步接口）
type CustomElementsRequest struct {
	KlingBaseRequest
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Images      []string `json:"images"`                // 训练图片列表
	TrainSteps  int      `json:"train_steps,omitempty"` // 训练步数
}

// 自定义声音训练请求（异步接口）
type CustomVoicesRequest struct {
	KlingBaseRequest
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	AudioFiles  []string `json:"audio_files"`           // 训练音频文件列表
	TrainSteps  int      `json:"train_steps,omitempty"` // 训练步数
}
