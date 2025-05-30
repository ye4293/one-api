package model

type ResponseFormat struct {
	Type       string         `json:"type,omitempty"`        // "json_object" 或 "json_schema"
	JSONSchema map[string]any `json:"json_schema,omitempty"` // 当 Type 为 "json_schema" 时使用
}

// StreamOptions 定义流式响应的选项
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type GeneralOpenAIRequest struct {
	Messages         []Message       `json:"messages,omitempty"`
	Model            string          `json:"model,omitempty"`
	FrequencyPenalty float64         `json:"frequency_penalty,omitempty"`
	MaxTokens        int             `json:"max_tokens,omitempty"`
	MaxInputTokens   int             `json:"max_input_tokens,omitempty"`
	N                int             `json:"n,omitempty"`
	PresencePenalty  float64         `json:"presence_penalty,omitempty"`
	ResponseFormat   *ResponseFormat `json:"response_format,omitempty"`
	Seed             float64         `json:"seed,omitempty"`
	Stream           bool            `json:"stream,omitempty"`
	StreamOptions    *StreamOptions  `json:"stream_options,omitempty"` // 新增字段
	Temperature      float64         `json:"temperature,omitempty"`
	TopP             float64         `json:"top_p,omitempty"`
	TopK             int             `json:"top_k,omitempty"`
	Tools            []Tool          `json:"tools,omitempty"`
	ToolChoice       any             `json:"tool_choice,omitempty"`
	FunctionCall     any             `json:"function_call,omitempty"`
	Functions        any             `json:"functions,omitempty"`
	User             string          `json:"user,omitempty"`
	Prompt           any             `json:"prompt,omitempty"`
	Input            any             `json:"input,omitempty"`
	EncodingFormat   string          `json:"encoding_format,omitempty"`
	Dimensions       int             `json:"dimensions,omitempty"`
	Instruction      string          `json:"instruction,omitempty"`
	Size             string          `json:"size,omitempty"`
	Stop             any             `json:"stop,omitempty"`
	AspectRatio      string          `json:"aspect_ratio,omitempty"`
	NumOutputs       int             `json:"num_outputs,omitempty"`
	OutputFormat     string          `json:"output_format,omitempty"`
	OutputQuality    int             `json:"output_quality,omitempty"`
	Modalities       []string        `json:"modalities,omitempty"`
	Audio            *AudioConfig    `json:"audio,omitempty"`
}

// 新增音频配置结构体
type AudioConfig struct {
	Voice  string `json:"voice,omitempty"`
	Format string `json:"format,omitempty"`
}

func (r GeneralOpenAIRequest) ParseInput() []string {
	if r.Input == nil {
		return nil
	}
	var input []string
	switch r.Input.(type) {
	case string:
		input = []string{r.Input.(string)}
	case []any:
		input = make([]string, 0, len(r.Input.([]any)))
		for _, item := range r.Input.([]any) {
			if str, ok := item.(string); ok {
				input = append(input, str)
			}
		}
	}
	return input
}

type GeneralVideoResponse struct {
	TaskId     string `json:"task_id"`
	TaskStatus string `json:"task_status"`
	Message    string `json:"message"`
}

type GeneralFinalVideoResponse struct {
	TaskId      string `json:"task_id"`
	VideoResult string `json:"video_result"`
	VideoId     string `json:"video_id"`
	TaskStatus  string `json:"task_status"`
	Message     string `json:"message"`
	Duration    string `json:"duration"`
}

type GeneralImageResponseAsync struct {
	TaskId     string `json:"task_id"`
	TaskStatus string `json:"task_status"`
	Message    string `json:"message"`
}

type GeneralFinalImageResponseAsync struct {
	TaskId     string   `json:"task_id"`
	ImageId    string   `json:"image_id"`
	TaskStatus string   `json:"task_status"`
	Message    string   `json:"message"`
	ImageUrls  []string `json:"image_urls"`
}
