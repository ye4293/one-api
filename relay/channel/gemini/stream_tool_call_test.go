package gemini

import (
	"encoding/json"
	"testing"
)

func TestStreamResponseGeminiChat2OpenAIToolCallIncludesIndex(t *testing.T) {
	geminiResponse := &ChatResponse{
		ResponseId: "resp-test",
		Candidates: []ChatCandidate{
			{
				Content: ChatContent{
					Parts: []Part{
						{
							FunctionCall: &FunctionCall{
								Name: "fps_input",
								Args: map[string]any{
									"duration_ms": 50,
								},
							},
						},
					},
				},
			},
		},
	}

	openAIResponse := streamResponseGeminiChat2OpenAI(geminiResponse, "gemini-4-argon")
	if len(openAIResponse.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(openAIResponse.Choices))
	}
	toolCalls := openAIResponse.Choices[0].Delta.ToolCalls
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].Index == nil || *toolCalls[0].Index != 0 {
		t.Fatalf("expected tool call index 0, got %#v", toolCalls[0].Index)
	}

	body, err := json.Marshal(openAIResponse)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	choices := decoded["choices"].([]any)
	delta := choices[0].(map[string]any)["delta"].(map[string]any)
	encodedToolCalls := delta["tool_calls"].([]any)
	encodedToolCall := encodedToolCalls[0].(map[string]any)
	if got, ok := encodedToolCall["index"].(float64); !ok || got != 0 {
		t.Fatalf("encoded tool call index = %#v, want 0", encodedToolCall["index"])
	}
}
