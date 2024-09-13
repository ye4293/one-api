package stability

type SdGenerationRequest struct {
	Prompt         string  `json:"prompt" binding:"required,min=1,max=10000"`
	Mode           string  `json:"mode,omitempty" binding:"omitempty,oneof=text-to-image image-to-image"`
	Image          []byte  `json:"image,omitempty"`
	Strength       float64 `json:"strength,omitempty" binding:"omitempty,min=0,max=1"`
	AspectRatio    string  `json:"aspect_ratio,omitempty" binding:"omitempty,oneof=16:9 1:1 21:9 2:3 3:2 4:5 5:4 9:16 9:21"`
	Model          string  `json:"model,omitempty" binding:"omitempty,oneof=sd3-large sd3-large-turbo sd3-medium"`
	Seed           uint32  `json:"seed,omitempty" binding:"omitempty,max=4294967294"`
	OutputFormat   string  `json:"output_format,omitempty" binding:"omitempty,oneof=jpeg png"`
	NegativePrompt string  `json:"negative_prompt,omitempty" binding:"omitempty,max=10000"`
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
