package anthropic

import (
	"encoding/json"
	"testing"
)

func TestRequestUnmarshalToolChoiceCompatibility(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		body         string
		wantNil      bool
		wantType     string
		wantToolName string
	}{
		{
			name:     "string auto",
			body:     `{"model":"claude-opus-4-6","max_tokens":16,"messages":[],"tool_choice":"auto"}`,
			wantType: "auto",
		},
		{
			name:     "string any",
			body:     `{"model":"claude-opus-4-6","max_tokens":16,"messages":[],"tool_choice":"any"}`,
			wantType: "any",
		},
		{
			name:    "string none is normalized away",
			body:    `{"model":"claude-opus-4-6","max_tokens":16,"messages":[],"tool_choice":"none"}`,
			wantNil: true,
		},
		{
			name:         "object tool",
			body:         `{"model":"claude-opus-4-6","max_tokens":16,"messages":[],"tool_choice":{"type":"tool","name":"bash"}}`,
			wantType:     "tool",
			wantToolName: "bash",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var req Request
			if err := json.Unmarshal([]byte(tc.body), &req); err != nil {
				t.Fatalf("json.Unmarshal returned error: %v", err)
			}

			if tc.wantNil {
				if req.ToolChoice != nil {
					t.Fatalf("ToolChoice should be nil, got %#v", req.ToolChoice)
				}
				return
			}

			if req.ToolChoice == nil {
				t.Fatalf("ToolChoice should not be nil")
			}
			if req.ToolChoice.Type != tc.wantType {
				t.Fatalf("unexpected ToolChoice.Type: got %q want %q", req.ToolChoice.Type, tc.wantType)
			}
			if req.ToolChoice.Name != tc.wantToolName {
				t.Fatalf("unexpected ToolChoice.Name: got %q want %q", req.ToolChoice.Name, tc.wantToolName)
			}
		})
	}
}
