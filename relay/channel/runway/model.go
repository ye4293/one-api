package runway

import (
	"encoding/json"
)

// PromptImage 表示输入图片的结构
type PromptImage struct {
	URI      string `json:"uri"`
	Position string `json:"position"`
}

// VideoGenerationRequest 定义请求结构
type VideoGenerationRequest struct {
	PromptImage json.RawMessage `json:"promptImage"` // 使用 json.RawMessage 处理多类型
	Model       string          `json:"model"`
	Seed        int             `json:"seed,omitempty"`
	PromptText  string          `json:"promptText,omitempty"`
	Watermark   bool            `json:"watermark,omitempty"`
	Duration    int             `json:"duration,omitempty"`
	Ratio       string          `json:"ratio,omitempty"`
}

type VideoResponse struct {
	Id         string `json:"id,omitempty"`
	StatusCode int    `json:"status_code,omitempty"`
	Error      string `json:"error"`
}

type VideoFinalResponse struct {
	ID        string   `json:"id"`
	Status    string   `json:"status"`
	CreatedAt string   `json:"createdAt"`
	Output    []string `json:"output,,omitempty"`
}
