package minimax

type MinimaxResponse struct {
	ID                  string   `json:"id"`
	Choices             []Choice `json:"choices"`
	Created             int64    `json:"created"`
	Model               string   `json:"model"`
	Object              string   `json:"object"`
	Usage               Usage    `json:"usage"`
	InputSensitive      bool     `json:"input_sensitive"`
	OutputSensitive     bool     `json:"output_sensitive"`
	InputSensitiveType  int      `json:"input_sensitive_type"`
	OutputSensitiveType int      `json:"output_sensitive_type"`
	OutputSensitiveInt  int      `json:"output_sensitive_int"`
	BaseResp            BaseResp `json:"base_resp"`
}

type Choice struct {
	FinishReason string  `json:"finish_reason,omitempty"`
	Index        int     `json:"index"`
	Message      Message `json:"message,omitempty"`
	Delta        Delta   `json:"delta,omitempty"`
}

type Message struct {
	Content      string `json:"content"`
	Role         string `json:"role"`
	Name         string `json:"name"`
	AudioContent string `json:"audio_content"`
}

type Usage struct {
	TotalTokens      int `json:"total_tokens"`
	TotalCharacters  int `json:"total_characters"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type BaseResp struct {
	StatusCode int    `json:"status_code"`
	StatusMsg  string `json:"status_msg"`
}
