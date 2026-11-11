package controller

import "github.com/songquanpeng/one-api/relay/channel/gemini"

// geminiImageResponse is shared by JSON/form image responses and their billing path.
type geminiImageResponse struct {
	ResponseId string `json:"responseId,omitempty"`
	Candidates []struct {
		Content struct {
			Parts []struct {
				InlineData *gemini.InlineData `json:"inlineData,omitempty"`
				Text       string             `json:"text,omitempty"`
			} `json:"parts,omitempty"`
			Role string `json:"role,omitempty"`
		} `json:"content,omitempty"`
		FinishReason  string `json:"finishReason,omitempty"`
		FinishMessage string `json:"finishMessage,omitempty"`
		Index         int    `json:"index,omitempty"`
	} `json:"candidates,omitempty"`
	PromptFeedback *struct {
		BlockReason        string `json:"blockReason,omitempty"`
		BlockReasonMessage string `json:"blockReasonMessage,omitempty"`
	} `json:"promptFeedback,omitempty"`
	ModelVersion  string `json:"modelVersion,omitempty"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount,omitempty"`
		CandidatesTokenCount int `json:"candidatesTokenCount,omitempty"`
		TotalTokenCount      int `json:"totalTokenCount,omitempty"`
		ThoughtsTokenCount   int `json:"thoughtsTokenCount,omitempty"`
		PromptTokensDetails  []struct {
			Modality   string `json:"modality"`
			TokenCount int    `json:"tokenCount"`
		} `json:"promptTokensDetails,omitempty"`
		CandidatesTokensDetails []struct {
			Modality   string `json:"modality"`
			TokenCount int    `json:"tokenCount"`
		} `json:"candidatesTokensDetails,omitempty"`
	} `json:"usageMetadata,omitempty"`
}
