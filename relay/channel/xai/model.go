package xai

// GrokVideoResponse Grok 视频生成/编辑响应
type GrokVideoResponse struct {
	RequestId  string `json:"request_id,omitempty"` // 请求ID，用于轮询结果
	StatusCode int    `json:"-"`                    // HTTP状态码（内部使用）
	// 错误响应字段 (Grok API 格式)
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
}

// GrokVideoResult Grok 视频结果查询响应
type GrokVideoResult struct {
	Status string         `json:"status,omitempty"` // 状态：pending 或 done
	Video  *GrokVideoData `json:"video,omitempty"`  // 视频数据（完成时）
	Model  string         `json:"model,omitempty"`  // 使用的模型
	// 错误响应字段
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
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
