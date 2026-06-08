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
	out, err := rewriteBodyForVertexClaude(in, "claude-opus-4-1-20250805", "")
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
	// Use a non-4.7 model so thinking/tools fields pass through unmodified.
	in := []byte(`{"model":"claude-sonnet-4-5-20250929","messages":[],"thinking":{"type":"enabled","budget_tokens":1024},"tools":[{"name":"calc"}]}`)
	out, err := rewriteBodyForVertexClaude(in, "claude-sonnet-4-5-20250929", "")
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
	_, err := rewriteBodyForVertexClaude([]byte(`{not json`), "claude-opus-4-7", "")
	if err == nil {
		t.Fatal("expected error on invalid json")
	}
}

func TestRewriteBodyForVertexClaude_Claude47_StripsSamplingAndAdaptsThinking(t *testing.T) {
	in := []byte(`{"model":"claude-opus-4-7-thinking","messages":[],"thinking":{"type":"enabled","budget_tokens":2048},"temperature":0.7,"top_p":0.95,"top_k":40}`)
	out, err := rewriteBodyForVertexClaude(in, "claude-opus-4-7-thinking", "")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	_ = json.Unmarshal(out, &m)
	// temperature/top_p/top_k removed
	if _, ok := m["temperature"]; ok {
		t.Error("temperature must be stripped for 4.7")
	}
	if _, ok := m["top_p"]; ok {
		t.Error("top_p must be stripped for 4.7")
	}
	if _, ok := m["top_k"]; ok {
		t.Error("top_k must be stripped for 4.7")
	}
	// thinking adapted
	thinking, _ := m["thinking"].(map[string]interface{})
	if thinking["type"] != "adaptive" {
		t.Errorf("thinking.type must be adaptive, got %v", thinking["type"])
	}
	if _, ok := thinking["budget_tokens"]; ok {
		t.Error("thinking.budget_tokens must be stripped for 4.7")
	}
}

func TestRewriteBodyForVertexClaude_Claude45_ForcesTempOneWhenThinking(t *testing.T) {
	in := []byte(`{"model":"claude-sonnet-4-5-20250929","messages":[],"thinking":{"type":"enabled","budget_tokens":1024},"temperature":0.5}`)
	out, err := rewriteBodyForVertexClaude(in, "claude-sonnet-4-5-20250929", "")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	_ = json.Unmarshal(out, &m)
	if m["temperature"] != 1.0 {
		t.Errorf("thinking on non-4.7 should force temperature=1.0, got %v", m["temperature"])
	}
	// thinking left alone on 4.5
	thinking, _ := m["thinking"].(map[string]interface{})
	if thinking["type"] != "enabled" {
		t.Errorf("thinking.type on 4.5 must remain enabled")
	}
	if thinking["budget_tokens"] != 1024.0 {
		t.Errorf("budget_tokens must be preserved on 4.5, got %v", thinking["budget_tokens"])
	}
}

func TestRewriteBodyForVertexClaude_NoThinking_NoTempForcing(t *testing.T) {
	in := []byte(`{"model":"claude-opus-4-5-20251101","messages":[],"temperature":0.3}`)
	out, err := rewriteBodyForVertexClaude(in, "claude-opus-4-5-20251101", "")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	_ = json.Unmarshal(out, &m)
	// temperature unchanged because no thinking field
	if m["temperature"] != 0.3 {
		t.Errorf("temperature should be preserved when no thinking, got %v", m["temperature"])
	}
}

func TestRewriteBodyForVertexClaude_InjectsBetaFromHeader(t *testing.T) {
	in := []byte(`{"model":"claude-opus-4-6","messages":[],"max_tokens":100}`)
	out, err := rewriteBodyForVertexClaude(in, "claude-opus-4-6", "context-management-2025-06-27,output-128k-2025-02-19")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	_ = json.Unmarshal(out, &m)
	beta, ok := m["anthropic_beta"].([]interface{})
	if !ok {
		t.Fatalf("anthropic_beta not injected or wrong type, got %v", m["anthropic_beta"])
	}
	if len(beta) != 2 {
		t.Errorf("expected 2 beta flags, got %d: %v", len(beta), beta)
	}
}

func TestRewriteBodyForVertexClaude_FiltersBetaNotInWhitelist(t *testing.T) {
	in := []byte(`{"model":"claude-opus-4-6","messages":[],"max_tokens":100}`)
	out, err := rewriteBodyForVertexClaude(in, "claude-opus-4-6", "unknown-flag-2025-01-01,output-128k-2025-02-19")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	_ = json.Unmarshal(out, &m)
	beta, ok := m["anthropic_beta"].([]interface{})
	if !ok {
		t.Fatalf("anthropic_beta not injected, got %v", m["anthropic_beta"])
	}
	if len(beta) != 1 {
		t.Errorf("expected 1 beta flag (unknown filtered), got %d: %v", len(beta), beta)
	}
	if beta[0] != "output-128k-2025-02-19" {
		t.Errorf("wrong beta flag: %v", beta[0])
	}
}

func TestRewriteBodyForVertexClaude_InfersBetaFromContextManagement(t *testing.T) {
	in := []byte(`{"model":"claude-opus-4-6","messages":[],"max_tokens":100,"context_management":{"edits":[{"type":"clear_thinking_20251015"}]}}`)
	out, err := rewriteBodyForVertexClaude(in, "claude-opus-4-6", "")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	_ = json.Unmarshal(out, &m)
	beta, ok := m["anthropic_beta"].([]interface{})
	if !ok {
		t.Fatalf("anthropic_beta should be auto-inferred from context_management, got %v", m["anthropic_beta"])
	}
	found := false
	for _, b := range beta {
		if b == "context-management-2025-06-27" {
			found = true
		}
	}
	if !found {
		t.Errorf("context-management-2025-06-27 should be inferred, got %v", beta)
	}
	// context_management field must be preserved
	if _, ok := m["context_management"]; !ok {
		t.Error("context_management field must NOT be deleted from body")
	}
}

func TestRewriteBodyForVertexClaude_NoBetaWhenHeaderEmptyAndNoInference(t *testing.T) {
	in := []byte(`{"model":"claude-opus-4-6","messages":[],"max_tokens":100}`)
	out, err := rewriteBodyForVertexClaude(in, "claude-opus-4-6", "")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	_ = json.Unmarshal(out, &m)
	if _, ok := m["anthropic_beta"]; ok {
		t.Errorf("anthropic_beta should not be present when no flags, got %v", m["anthropic_beta"])
	}
}
