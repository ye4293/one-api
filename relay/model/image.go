package model

// type ImageRequest struct {
// 	Model          string `json:"model"`
// 	Prompt         string `json:"prompt" binding:"required"`
// 	N              int    `json:"n,omitempty"`
// 	Size           string `json:"size,omitempty"`
// 	Quality        string `json:"quality,omitempty"`
// 	ResponseFormat string `json:"response_format,omitempty"`
// 	Style          string `json:"style,omitempty"`
// 	User           string `json:"user,omitempty"`
// 	AspectRatio    string `json:"aspect_ratio,omitempty"`
// 	NumOutputs     int    `json:"num_outputs,omitempty"`
// 	Seed           int    `json:"seed,omitempty"`
// 	OutputFormat   string `json:"output_format,omitempty"`
// 	OutputQuality  int    `json:"output_quality,omitempty"`
// }

type ImageRequest struct {
	Model           string    `json:"model"`
	Prompt          string    `json:"prompt" binding:"required"`
	N               int       `json:"n,omitempty"`
	StyleID         string    `json:"style_id,omitempty"`
	Style           string    `json:"style,omitempty"`
	Substyle        string    `json:"substyle,omitempty"`
	Size            string    `json:"size,omitempty"`
	Quality         string    `json:"quality,omitempty"`
	ResponseFormat  string    `json:"response_format,omitempty"`
	User            string    `json:"user,omitempty"`
	AspectRatio     string    `json:"aspect_ratio,omitempty"`
	NumOutputs      int       `json:"num_outputs,omitempty"`
	Seed            int       `json:"seed,omitempty"`
	OutputFormat    string    `json:"output_format,omitempty"`
	OutputQuality   int       `json:"output_quality,omitempty"`
	Controls        *Controls `json:"controls,omitempty"`
	PromptOptimizer bool      `json:"prompt_optimizer,omitempty"`
}

type Controls struct {
	Colors          string `json:"colors" binding:"required"`
	BackgroundColor string `json:"background_color,omitempty"`
}
