package vertexai

import (
	"encoding/json"
	"testing"
)

func TestIsClaudeModel(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"claude-opus-4-7", true},
		{"claude-3-5-sonnet-20241022", true},
		{"Claude-Opus", true}, // 大小写不敏感
		{"gemini-2.5-pro", false},
		{"veo-3.0-generate-001", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isClaudeModel(tt.in); got != tt.want {
			t.Errorf("isClaudeModel(%q)=%v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestMapClaudeModelForURL(t *testing.T) {
	// 在 map 里的：转为 @ 格式
	if got := mapClaudeModelForURL("claude-opus-4-1-20250805"); got != "claude-opus-4-1@20250805" {
		t.Errorf("mapped wrong: %q", got)
	}
	// 不在 map 里的：原样返回
	if got := mapClaudeModelForURL("claude-future-model"); got != "claude-future-model" {
		t.Errorf("fallback wrong: %q", got)
	}
}

func TestClaudeSuffix(t *testing.T) {
	if got := claudeSuffix(false); got != "rawPredict" {
		t.Errorf("non-stream suffix wrong: %q", got)
	}
	if got := claudeSuffix(true); got != "streamRawPredict?alt=sse" {
		t.Errorf("stream suffix wrong: %q", got)
	}
}

func TestRewriteBodyForVertexClaude_InjectsAnthropicVersion(t *testing.T) {
	in := []byte(`{"model":"claude-opus-4-1-20250805","messages":[{"role":"user","content":"hi"}],"max_tokens":100,"stream":false}`)
	out, err := rewriteBodyForVertexClaude(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output not valid json: %v", err)
	}
	if v, _ := m["anthropic_version"].(string); v != anthropicVersion {
		t.Errorf("anthropic_version not injected, got %v", m["anthropic_version"])
	}
	if _, exists := m["model"]; exists {
		t.Errorf("model field should be stripped from body (Vertex Anthropic forbids), got %v", m["model"])
	}
	if _, exists := m["messages"]; !exists {
		t.Errorf("messages field must be preserved")
	}
	if v, ok := m["max_tokens"]; !ok || v == nil {
		t.Errorf("max_tokens must be preserved")
	}
}

func TestRewriteBodyForVertexClaude_PreservesThinkingAndTools(t *testing.T) {
	in := []byte(`{"model":"claude-opus-4-7","messages":[],"thinking":{"type":"enabled","budget_tokens":1024},"tools":[{"name":"calc"}]}`)
	out, err := rewriteBodyForVertexClaude(in)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var m map[string]interface{}
	_ = json.Unmarshal(out, &m)
	if _, ok := m["thinking"]; !ok {
		t.Errorf("thinking must survive")
	}
	if _, ok := m["tools"]; !ok {
		t.Errorf("tools must survive")
	}
}

func TestRewriteBodyForVertexClaude_InvalidJSON(t *testing.T) {
	_, err := rewriteBodyForVertexClaude([]byte(`{not json`))
	if err == nil {
		t.Fatal("expected error on invalid json")
	}
}
