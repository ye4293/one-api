package openai

import "github.com/songquanpeng/one-api/relay/model"

type TextContent struct {
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
}

type ImageContent struct {
	Type     string          `json:"type,omitempty"`
	ImageURL *model.ImageURL `json:"image_url,omitempty"`
}

type ChatRequest struct {
	Model     string          `json:"model"`
	Messages  []model.Message `json:"messages"`
	MaxTokens int             `json:"max_tokens"`
}

type TextRequest struct {
	Model     string          `json:"model"`
	Messages  []model.Message `json:"messages"`
	Prompt    string          `json:"prompt"`
	MaxTokens int             `json:"max_tokens"`
	//Stream   bool      `json:"stream"`
}

// ImageRequest docs: https://platform.openai.com/docs/api-reference/images/create
type ImageRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt" binding:"required"`
	N              int    `json:"n,omitempty"`
	Size           string `json:"size,omitempty"`
	Quality        string `json:"quality,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
	Style          string `json:"style,omitempty"`
	User           string `json:"user,omitempty"`
	AspectRatio    string `json:"aspect_ratio,omitempty"`
	NumOutputs     int    `json:"num_outputs,omitempty"`
	Seed           int    `json:"seed,omitempty"`
	OutputFormat   string `json:"output_format,omitempty"`
	OutputQuality  int    `json:"output_quality,omitempty"`
}

type WhisperJSONResponse struct {
	Text string `json:"text,omitempty"`
}

type WhisperVerboseJSONResponse struct {
	Task     string    `json:"task,omitempty"`
	Language string    `json:"language,omitempty"`
	Duration float64   `json:"duration,omitempty"`
	Text     string    `json:"text,omitempty"`
	Segments []Segment `json:"segments,omitempty"`
}

type Segment struct {
	Id               int     `json:"id"`
	Seek             int     `json:"seek"`
	Start            float64 `json:"start"`
	End              float64 `json:"end"`
	Text             string  `json:"text"`
	Tokens           []int   `json:"tokens"`
	Temperature      float64 `json:"temperature"`
	AvgLogprob       float64 `json:"avg_logprob"`
	CompressionRatio float64 `json:"compression_ratio"`
	NoSpeechProb     float64 `json:"no_speech_prob"`
}

type TextToSpeechRequest struct {
	Model          string  `json:"model" binding:"required"`
	Input          string  `json:"input" binding:"required"`
	Voice          string  `json:"voice" binding:"required"`
	Speed          float64 `json:"speed"`
	ResponseFormat string  `json:"response_format"`
	StreamFormat   string  `json:"stream_format,omitempty"`
}

type UsageOrResponseText struct {
	*model.Usage
	ResponseText string
}

type SlimTextResponse struct {
	Choices     []TextResponseChoice `json:"choices"`
	model.Usage `json:"usage"`
	Error       model.Error `json:"error"`
}

type TextResponseChoice struct {
	Index         int `json:"index"`
	model.Message `json:"message"`
	FinishReason  string `json:"finish_reason"`
}

type TextResponse struct {
	Id          string               `json:"id"`
	Model       string               `json:"model,omitempty"`
	Object      string               `json:"object"`
	Created     int64                `json:"created"`
	Choices     []TextResponseChoice `json:"choices"`
	model.Usage `json:"usage"`
}

type EmbeddingResponseItem struct {
	Object    string    `json:"object"`
	Index     int       `json:"index"`
	Embedding []float64 `json:"embedding"`
}

type EmbeddingResponse struct {
	Object      string                  `json:"object"`
	Data        []EmbeddingResponseItem `json:"data"`
	Model       string                  `json:"model"`
	model.Usage `json:"usage"`
}

type ImageResponse struct {
	Created int `json:"created,omitempty"`
	Data    []struct {
		Url     string `json:"url,omitempty"`
		B64Json string `json:"b64_json,omitempty"`
	} `json:"data,omitempty"`
	Usage struct {
		InputTokens        int `json:"input_tokens,omitempty"`
		InputTokensDetails struct {
			ImageTokens int `json:"image_tokens,omitempty"`
			TextTokens  int `json:"text_tokens,omitempty"`
		} `json:"input_tokens_details,omitempty"`
		OutputTokens int `json:"output_tokens,omitempty"`
		TotalTokens  int `json:"total_tokens,omitempty"`
	} `json:"usage,omitempty"`
}

type ChatCompletionsStreamResponseChoice struct {
	Index        int           `json:"index"`
	Delta        model.Message `json:"delta"`
	FinishReason *string       `json:"finish_reason,omitempty"`
}

type ChatCompletionsStreamResponse struct {
	Id      string                                `json:"id"`
	Object  string                                `json:"object"`
	Created int64                                 `json:"created"`
	Model   string                                `json:"model"`
	Choices []ChatCompletionsStreamResponseChoice `json:"choices"`
	Usage   *model.Usage                          `json:"usage"`
}

type CompletionsStreamResponse struct {
	Choices []struct {
		Text         string `json:"text"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// SoraVideoRequest Sora 视频生成请求 (JSON 格式)
type SoraVideoRequest struct {
	Model          string `json:"model" binding:"required"`  // 模型名称 (sora-2, sora-2-pro)
	Prompt         string `json:"prompt" binding:"required"` // 视频描述
	Size           string `json:"size,omitempty"`            // 分辨率 (720x1280, 1280x720, 1024x1792, 1792x1024)
	Seconds        string `json:"seconds,omitempty"`         // 视频时长（秒）- 官方字段名，string 类型
	AspectRatio    string `json:"aspect_ratio,omitempty"`    // 宽高比
	Loop           bool   `json:"loop,omitempty"`            // 是否循环
	InputReference string `json:"input_reference,omitempty"` // 输入参考（URL/base64/dataURL）
}

// SoraRemixRequest Sora 视频 remix 请求
type SoraRemixRequest struct {
	Model   string `json:"model,omitempty"`             // 模型名称（用于路由识别，发送时会去掉）
	VideoID string `json:"video_id" binding:"required"` // 原视频ID
	Prompt  string `json:"prompt" binding:"required"`   // 新的描述
}

// SoraVideoResponse Sora 视频生成响应
type SoraVideoResponse struct {
	ID                 string `json:"id"`
	Object             string `json:"object"`
	Created            int64  `json:"created,omitempty"`
	CreatedAt          int64  `json:"created_at,omitempty"` // Remix 响应使用
	Model              string `json:"model"`
	Status             string `json:"status"`             // queued, processing, completed, failed
	Progress           int    `json:"progress,omitempty"` // Remix 响应使用
	Prompt             string `json:"prompt,omitempty"`
	Size               string `json:"size,omitempty"`
	Seconds            string `json:"seconds,omitempty"` // 视频时长（秒），string 类型
	VideoURL           string `json:"video_url,omitempty"`
	RemixedFromVideoID string `json:"remixed_from_video_id,omitempty"` // Remix 响应使用
	Error              *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
	StatusCode int `json:"status_code,omitempty"` // HTTP status code
}
