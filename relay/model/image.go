package model

type ImageRequest struct {
	Model           string        `json:"model"`
	Prompt          string        `json:"prompt" binding:"required"`
	N               int           `json:"n,omitempty"`
	StyleID         string        `json:"style_id,omitempty"`
	Style           string        `json:"style,omitempty"`
	Substyle        string        `json:"substyle,omitempty"`
	Size            string        `json:"size,omitempty"`
	Quality         string        `json:"quality,omitempty"`
	ResponseFormat  string        `json:"response_format,omitempty"`
	User            string        `json:"user,omitempty"`
	AspectRatio     string        `json:"aspect_ratio,omitempty"`
	NumOutputs      int           `json:"num_outputs,omitempty"`
	Seed            int           `json:"seed,omitempty"`
	OutputFormat    string        `json:"output_format,omitempty"`
	OutputQuality   int           `json:"output_quality,omitempty"`
	Controls        *Controls     `json:"controls,omitempty"`
	PromptOptimizer bool          `json:"prompt_optimizer,omitempty"`
	TextLayout      []interface{} `json:"text_layout,omitempty"`
	NegativePrompt  string        `json:"negative_prompt,omitempty"`
	Background      string        `json:"background,omitempty"`

	// Image 字段支持字符串或字符串数组
	Image interface{} `json:"image,omitempty"`

	// 新增字段：顺序图像生成相关
	SequentialImageGeneration        string                            `json:"sequential_image_generation,omitempty"`
	SequentialImageGenerationOptions *SequentialImageGenerationOptions `json:"sequential_image_generation_options,omitempty"`

	// 新增字段：流式响应
	Stream bool `json:"stream,omitempty"`
}

type Controls struct {
	Colors          string `json:"colors" binding:"required"`
	BackgroundColor string `json:"background_color,omitempty"`
}

// SequentialImageGenerationOptions 顺序图像生成选项
type SequentialImageGenerationOptions struct {
	MaxImages int `json:"max_images,omitempty"`
}
