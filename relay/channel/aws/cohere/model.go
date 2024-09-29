package aws

type awsCohereRequest struct {
	Message           string        `json:"message"`
	ChatHistory       []ChatMessage `json:"chat_history"`
	Documents         []Document    `json:"documents"`
	SearchQueriesOnly bool          `json:"search_queries_only"`
	Preamble          string        `json:"preamble"`
	MaxTokens         int           `json:"max_tokens"`
	Temperature       float64       `json:"temperature"`
	P                 float64       `json:"p"`
	K                 float64       `json:"k"`
	PromptTruncation  string        `json:"prompt_truncation"`
	FrequencyPenalty  float64       `json:"frequency_penalty"`
	PresencePenalty   float64       `json:"presence_penalty"`
	Seed              int           `json:"seed"`
	ReturnPrompt      bool          `json:"return_prompt"`
	Tools             []Tool        `json:"tools"`
	ToolResults       []ToolResult  `json:"tool_results"`
	StopSequences     []string      `json:"stop_sequences"`
	RawPrompting      bool          `json:"raw_prompting"`
}

type ChatMessage struct {
	Role    string `json:"role"`
	Message string `json:"message"`
}

type Document struct {
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
}

type Tool struct {
	Name                 string                   `json:"name"`
	Description          string                   `json:"description"`
	ParameterDefinitions map[string]ParameterSpec `json:"parameter_definitions"`
}

type ParameterSpec struct {
	Description string `json:"description"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
}

type ToolResult struct {
	Call    ToolCall `json:"call"`
	Outputs []struct {
		Text string `json:"text"`
	} `json:"outputs"`
}

type ToolCall struct {
	Name       string                 `json:"name"`
	Parameters map[string]interface{} `json:"parameters"`
}
