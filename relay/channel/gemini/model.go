package gemini

type ChatRequest struct {
	Contents          []ChatContent        `json:"contents"`
	SystemInstruction *SystemInstruction   `json:"systemInstruction,omitempty"`
	SafetySettings    []ChatSafetySettings `json:"safetySettings,omitempty"`
	GenerationConfig  ChatGenerationConfig `json:"generationConfig,omitempty"`
	Tools             []ChatTools          `json:"tools,omitempty"`
}

type SystemInstruction struct {
	Parts []Part `json:"parts"`
}

type InlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// FunctionCall represents a function call in Gemini response
type FunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

// FunctionResponse represents a function response in Gemini request
type FunctionResponse struct {
	Name     string `json:"name"`
	Response any    `json:"response"`
}

type Part struct {
	Text             string            `json:"text,omitempty"`
	InlineData       *InlineData       `json:"inlineData,omitempty"`
	Thought          bool              `json:"thought,omitempty"`          // 标识是否为思考内容
	ThoughtSignature string            `json:"thoughtSignature,omitempty"` // 思考签名（用于多轮对话）
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
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
	ResponseModalities []string        `json:"responseModalities,omitempty"`
	ThinkingConfig     *ThinkingConfig `json:"thinkingConfig,omitempty"`
	ImageConfig        *ImageConfig    `json:"imageConfig,omitempty"`
}

type ImageConfig struct {
	AspectRatio string `json:"aspectRatio,omitempty"`
	ImageSize   string `json:"imageSize,omitempty"`
}

type ThinkingConfig struct {
	// 使用指针类型以区分"未设置"和"设置为0"
	// 当设置为 0 时表示禁用思考，-1 表示动态思考
	ThinkingBudget  *int   `json:"thinkingBudget,omitempty"`
	IncludeThoughts bool   `json:"includeThoughts,omitempty"`
	ThinkingLevel   string `json:"thinkingLevel,omitempty"` // Gemini 3: "none", "minimal", "low", "medium", "high"
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
