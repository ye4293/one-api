package luma

type LumaGenerationResponse struct {
	ID            string                `json:"id"`             // 生成任务的唯一标识符
	State         string                `json:"state"`          // 任务状态：queued, processing, completed, failed 等
	FailureReason *string               `json:"failure_reason"` // 失败原因，失败时才有值
	CreatedAt     string                `json:"created_at"`     // 创建时间
	Assets        interface{}           `json:"assets"`         // 资产信息，可能为 null
	Version       interface{}           `json:"version"`        // 版本信息，可能为 null
	Request       LumaGenerationRequest `json:"request"`        // 原始请求信息
	StatusCode    int                   `json:"status_code"`    // 状态码
}

// GenerationRequest 结构体保持不变，这里为完整性再列一遍
type LumaGenerationRequest struct {
	Prompt      string     `json:"prompt" binding:"required"`
	AspectRatio string     `json:"aspect_ratio,omitempty"`
	Loop        *bool      `json:"loop,omitempty"`
	Keyframes   *Keyframes `json:"keyframes,omitempty"`
	CallbackURL *string    `json:"callback_url,omitempty"`
}

type Keyframes struct {
	Frame0 *Frame `json:"frame0,omitempty"`
	Frame1 *Frame `json:"frame1,omitempty"`
}

type Frame struct {
	Type string `json:"type,omitempty"`
	URL  string `json:"url,omitempty"`
}
