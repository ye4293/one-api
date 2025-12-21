package model

// GoogleExtraContent contains Google-specific extra content for tool calls
type GoogleExtraContent struct {
	ThoughtSignature string `json:"thought_signature,omitempty"`
}

// ExtraContent contains provider-specific extra content
type ExtraContent struct {
	Google *GoogleExtraContent `json:"google,omitempty"`
}

type Tool struct {
	Id           string        `json:"id,omitempty"`
	Type         string        `json:"type,omitempty"` // when splicing claude tools stream messages, it is empty
	Function     Function      `json:"function"`
	ExtraContent *ExtraContent `json:"extra_content,omitempty"` // Gemini thought signature support
}

type Function struct {
	Description string `json:"description,omitempty"`
	Name        string `json:"name,omitempty"`       // when splicing claude tools stream messages, it is empty
	Parameters  any    `json:"parameters,omitempty"` // request
	Arguments   any    `json:"arguments,omitempty"`  // response
}
