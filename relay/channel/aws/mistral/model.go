package aws

type MistralRequest struct {
	Prompt      string   `json:"prompt"`
	MaxTokens   int      `json:"max_tokens"`
	Stop        []string `json:"stop"`
	Temperature float64  `json:"temperature"`
	TopP        float64  `json:"top_p"`
	TopK        int      `json:"top_k"`
}

type MistralResponse struct {
	Outputs []MistralOutput `json:"outputs"`
}

type MistralOutput struct {
	Text       string `json:"text"`
	StopReason string `json:"stop_reason"`
}

// Claude 的单个流式响应结构
type MistralStreamResponse struct {
	Outputs                        []MistralOutput    `json:"outputs"`
	AmazonBedrockInvocationMetrics *InvocationMetrics `json:"amazon-bedrock-invocationMetrics,omitempty"`
}

// AWS Bedrock 的调用指标
type InvocationMetrics struct {
	InputTokenCount   int `json:"inputTokenCount"`
	OutputTokenCount  int `json:"outputTokenCount"`
	InvocationLatency int `json:"invocationLatency"`
	FirstByteLatency  int `json:"firstByteLatency"`
}
