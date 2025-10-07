package gemini

type ChatRequest struct {
	Contents          []ChatContent        `json:"contents"`
	SystemInstruction *SystemInstruction   `json:"system_instruction,omitempty"`
	SafetySettings    []ChatSafetySettings `json:"safety_settings,omitempty"`
	GenerationConfig  ChatGenerationConfig `json:"generation_config,omitempty"`
	Tools             []ChatTools          `json:"tools,omitempty"`
}

type SystemInstruction struct {
	Parts []Part `json:"parts"`
}

type InlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type Part struct {
	Text       string      `json:"text,omitempty"`
	InlineData *InlineData `json:"inlineData,omitempty"`
}

type ChatContent struct {
	Role  string `json:"role,omitempty"`
	Parts []Part `json:"parts"`
}

type ChatSafetySettings struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

type ChatTools struct {
	FunctionDeclarations any `json:"functionDeclarations,omitempty"`
}

type ChatGenerationConfig struct {
	Temperature        float64         `json:"temperature,omitempty"`
	TopP               float64         `json:"topP,omitempty"`
	TopK               float64         `json:"topK,omitempty"`
	MaxOutputTokens    int             `json:"maxOutputTokens,omitempty"`
	CandidateCount     int             `json:"candidateCount,omitempty"`
	StopSequences      []string        `json:"stopSequences,omitempty"`
	ResponseModalities []string        `json:"response_modalities,omitempty"`
	ThinkingConfig     *ThinkingConfig `json:"thinking_config,omitempty"`
	ImageConfig        *ImageConfig    `json:"imageConfig,omitempty"`
}

type ImageConfig struct {
	AspectRatio string `json:"aspectRatio,omitempty"`
}

type ThinkingConfig struct {
	ThinkingBudget  int  `json:"thinkingBudget,omitempty"`
	IncludeThoughts bool `json:"includeThoughts,omitempty"`
}

// type GeminiImageRequest struct {
// 	Model            string               `json:"model"`
// 	Contents         string               `json:"contents"`
// 	GenerationConfig ChatGenerationConfig `json:"generation_config,omitempty"`
// }

// type GeminiImg3 struct {
// 	Prompt            string `json:"prompt"`
// 	NumberOfImages    int    `json:"number_of_images,omitempty"`
// 	AspectRatio       string `json:"aspect_ratio,omitempty"`
// 	SafetyFilterLevel string `json:"safety_filter_level,omitempty"`
// 	PersonGeneration  string `json:"person_generation,omitempty"`
// }
