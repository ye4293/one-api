package vertexai

import "testing"

func TestAnthropicVersion(t *testing.T) {
	if anthropicVersion != "vertex-2023-10-16" {
		t.Fatalf("anthropicVersion mismatch, got %q", anthropicVersion)
	}
}

func TestClaudeModelMapCoverage(t *testing.T) {
	wants := map[string]string{
		"claude-opus-4-1-20250805":   "claude-opus-4-1@20250805",
		"claude-sonnet-4-5-20250929": "claude-sonnet-4-5@20250929",
		"claude-haiku-4-5-20251001":  "claude-haiku-4-5@20251001",
		"claude-opus-4-5-20251101":   "claude-opus-4-5@20251101",
		"claude-opus-4-6":            "claude-opus-4-6",
		"claude-opus-4-7":            "claude-opus-4-7",
	}
	for k, v := range wants {
		got, ok := claudeModelMap[k]
		if !ok {
			t.Errorf("claudeModelMap missing key %q", k)
			continue
		}
		if got != v {
			t.Errorf("claudeModelMap[%q] = %q, want %q", k, got, v)
		}
	}
}

func TestModelListContainsClaude(t *testing.T) {
	want := "claude-opus-4-1-20250805"
	found := false
	for _, m := range ModelList {
		if m == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ModelList does not contain %q", want)
	}
}
