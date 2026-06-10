package audit

import (
	"net/http"
	"testing"
)

func TestRedactHeaders(t *testing.T) {
	cfg := loadConfig()
	h := http.Header{}
	h.Set("Authorization", "Bearer sk-secret")
	h.Set("Content-Type", "application/json")
	h.Set("X-Api-Key", "abc123")
	out := redactHeaders(h, cfg.redactSet)
	if out["Authorization"][0] != redactedValue {
		t.Errorf("Authorization 应被脱敏, got %v", out["Authorization"])
	}
	if out["X-Api-Key"][0] != redactedValue {
		t.Errorf("X-Api-Key 应被脱敏（大小写不敏感）")
	}
	if out["Content-Type"][0] != "application/json" {
		t.Errorf("Content-Type 不应被脱敏")
	}
}

func TestHeadersToJSON(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	s := headersToJSON(h)
	if s == "" || s[0] != '{' {
		t.Errorf("应返回 JSON 对象字符串, got %q", s)
	}
}
