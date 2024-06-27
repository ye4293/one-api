package stability

type SdGenerationRequest struct {
	Prompt          string  `json:"prompt" required:"true"`
	AspectRatio     string  `json:"aspect_ratio,omitempty"` // 默认值为"1:1"
	NegativePrompt  string  `json:"negative_prompt,omitempty"`
	Seed            int     `json:"seed,omitempty"` // 默认值为0
	StylePreset     string  `json:"style_preset,omitempty"`
	OutputFormat    string  `json:"output_format,omitempty"` // 默认值为"png"
	CFGSCALE        float64 `json:"cfg_scale,omitempty"`
	MontionBucketId int     `json:"motion_bucket_id,omitempty"`
}

type SdResponse struct {
	Video        string `json:"video,omitempty"`
	Image        string `json:"image,omitempty"`
	FinishReason string `json:"finish_reason,omitempty"`
	Seed         int64  `json:"seed,omitempty"`
	Id           string `json:"id,omitempty"`
	SDError      *SDErr `json:"error,omitempty"`
}

type SDErr struct {
	Errors []string `json:"errors"`
	ID     string   `json:"id"`
	Name   string   `json:"name"`
}
