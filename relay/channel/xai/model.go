package xai

import "encoding/json"

// GrokVideoResponse Grok 视频生成/编辑响应
type GrokVideoResponse struct {
	RequestId  string          `json:"request_id,omitempty"` // 请求ID，用于轮询结果
	StatusCode int             `json:"-"`                    // HTTP状态码（内部使用）
	// 错误响应字段 (Grok API 格式，error 可能是 string 或 object)
	Code     string          `json:"code,omitempty"`
	RawError json.RawMessage `json:"error,omitempty"`
}

// GetError 提取错误信息字符串，兼容 string 和 object 两种格式
func (r *GrokVideoResponse) GetError() string {
	return extractErrorMessage(r.RawError)
}

// GrokVideoUsage 视频生成费用信息
type GrokVideoUsage struct {
	CostInUsdTicks int64 `json:"cost_in_usd_ticks,omitempty"`
}

// GrokVideoResult Grok 视频结果查询响应
type GrokVideoResult struct {
	Status   string          `json:"status,omitempty"`   // 状态：pending 或 done
	Video    *GrokVideoData  `json:"video,omitempty"`    // 视频数据（完成时）
	Model    string          `json:"model,omitempty"`    // 使用的模型
	Usage    *GrokVideoUsage `json:"usage,omitempty"`    // 费用信息
	Progress int             `json:"progress,omitempty"` // 进度 0-100
	// 错误响应字段 (error 可能是 string 或 object)
	Code     string          `json:"code,omitempty"`
	RawError json.RawMessage `json:"error,omitempty"`
}

// GetError 提取错误信息字符串，兼容 string 和 object 两种格式
func (r *GrokVideoResult) GetError() string {
	return extractErrorMessage(r.RawError)
}

// extractErrorMessage 从 json.RawMessage 中提取错误信息
// 支持纯字符串 "error message" 和对象 {"message": "error message"} 两种格式
func extractErrorMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// 尝试解析为字符串
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// 尝试解析为对象
	var obj struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Message != "" {
		return obj.Message
	}
	// 兜底：返回原始 JSON
	return string(raw)
}

// GrokVideoData 视频数据
type GrokVideoData struct {
	URL               string `json:"url,omitempty"`                // 视频URL
	Duration          int    `json:"duration,omitempty"`           // 视频时长（秒）
	RespectModeration bool   `json:"respect_moderation,omitempty"` // 内容审核标识
}

type XaiUsage struct {
	PromptTokens        int `json:"prompt_tokens,omitempty"`
	CompletionTokens    int `json:"completion_tokens,omitempty"`
	TotalTokens         int `json:"total_tokens,omitempty"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens,omitempty"`
		AudioTokens  int `json:"audio_tokens,omitempty"`
		TextTokens   int `json:"text_tokens,omitempty"`
		ImageTokens  int `json:"image_tokens,omitempty"`
	} `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails struct {
		ReasoningTokens          int `json:"reasoning_tokens,omitempty"`
		AudioTokens              int `json:"audio_tokens,omitempty"`
		AcceptedPredictionTokens int `json:"accepted_prediction_tokens,omitempty"`
		RejectedPredictionTokens int `json:"rejected_prediction_tokens,omitempty"`
	} `json:"completion_tokens_details,omitempty"`
}
