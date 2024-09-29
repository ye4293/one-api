package aws

type AI21Request struct {
	// 共用字段
	MaxTokens   int     `json:"max_tokens,omitempty"` // 用于 jamba-instruct
	MaxTokens2  int     `json:"maxTokens,omitempty"`  // 用于 j2-ultra 和 j2-mid
	Temperature float64 `json:"temperature,omitempty"`
	TopP        float64 `json:"top_p,omitempty"` // 用于 jamba-instruct
	TopP2       float64 `json:"topP,omitempty"`  // 用于 j2-ultra 和 j2-mid

	// jamba-instruct 特有字段
	Messages []Message `json:"messages,omitempty"`

	// j2-ultra 和 j2-mid 特有字段
	Prompt           string   `json:"prompt,omitempty"`
	StopSequences    []string `json:"stopSequences,omitempty"`
	CountPenalty     *Penalty `json:"countPenalty,omitempty"`
	PresencePenalty  *Penalty `json:"presencePenalty,omitempty"`
	FrequencyPenalty *Penalty `json:"frequencyPenalty,omitempty"`
}

// AI21 Jamba Instruct
type AI21JambaInstructRequest struct {
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens"`
	TopP        float64   `json:"top_p"`
	Temperature float64   `json:"temperature"`
}

type Message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// AI21 J2 Ultra and J2 Mid
type AI21J2Request struct {
	Prompt           string   `json:"prompt"`
	MaxTokens        int      `json:"maxTokens"`
	Temperature      float64  `json:"temperature"`
	TopP             float64  `json:"topP"`
	StopSequences    []string `json:"stopSequences"`
	CountPenalty     Penalty  `json:"countPenalty"`
	PresencePenalty  Penalty  `json:"presencePenalty"`
	FrequencyPenalty Penalty  `json:"frequencyPenalty"`
}

type Penalty struct {
	Scale float64 `json:"scale"`
}

type GenericResponse struct {
	ID      interface{} `json:"id"`
	Choices []Choice    `json:"choices"`
	Usage   Usage       `json:"usage"`
}

type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ai21StreamResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []StreamChoice `json:"choices"`
	Usage   *Usage         `json:"usage,omitempty"`
}

type StreamChoice struct {
	Index        int         `json:"index"`
	Delta        StreamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason,omitempty"`
}

type StreamDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type Prompt struct {
	Text   string  `json:"text"`
	Tokens []Token `json:"tokens"`
}

type Token struct {
	GeneratedToken GeneratedToken `json:"generatedToken"`
	TopTokens      interface{}    `json:"topTokens"`
	TextRange      TextRange      `json:"textRange"`
}

type GeneratedToken struct {
	Token      string  `json:"token"`
	LogProb    float64 `json:"logprob"`
	RawLogProb float64 `json:"raw_logprob"`
}

type TextRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type Completion struct {
	Data         CompletionData `json:"data"`
	FinishReason FinishReason   `json:"finishReason"`
}

type CompletionData struct {
	Text   string  `json:"text"`
	Tokens []Token `json:"tokens"`
}

type FinishReason struct {
	Reason string `json:"reason"`
	Length int    `json:"length"`
}

type Response struct {
	ID          int          `json:"id"`
	Prompt      Prompt       `json:"prompt"`
	Completions []Completion `json:"completions"`
}
