package stability

type SdGenerationRequest struct {
	Prompt         string `json:"prompt" required:"true"`
	AspectRatio    string `json:"aspect_ratio,omitempty"` // 默认值为"1:1"
	NegativePrompt string `json:"negative_prompt,omitempty"`
	Seed           int    `json:"seed,omitempty"` // 默认值为0
	StylePreset    string `json:"style_preset,omitempty"`
	OutputFormat   string `json:"output_format,omitempty"` // 默认值为"png"
}
