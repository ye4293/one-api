package aws

import (
	"encoding/json"
	"testing"
)

// TestStripNoSamplingParams 覆盖 copier 复活 temperature 的核心修复：
// no-sampling 模型（4.7+）即使 Temperature 是指向 0 的非 nil 指针，也必须被清 nil，
// 序列化后 body 不得含 temperature/top_p/top_k；<4.7 模型保持原样。
func TestStripNoSamplingParams(t *testing.T) {
	t.Parallel()
	zero := 0.0

	t.Run("no-sampling model strips revived temperature", func(t *testing.T) {
		req := &Request{Temperature: &zero, TopP: 0.9, TopK: 5, MaxTokens: 16}
		stripNoSamplingParams(req, "claude-opus-4-8")
		if req.Temperature != nil {
			t.Fatalf("temperature should be nil, got %v", *req.Temperature)
		}
		body, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		for _, k := range []string{"temperature", "top_p", "top_k"} {
			if _, ok := payload[k]; ok {
				t.Fatalf("%s should be omitted for no-sampling model, body=%s", k, body)
			}
		}
	})

	t.Run("sub-4.7 model preserves temperature", func(t *testing.T) {
		req := &Request{Temperature: &zero, TopP: 0.9, TopK: 5}
		stripNoSamplingParams(req, "claude-opus-4-6")
		if req.Temperature == nil {
			t.Fatalf("temperature should be preserved for claude-opus-4-6")
		}
		if req.TopP != 0.9 || req.TopK != 5 {
			t.Fatalf("top_p/top_k should be untouched, got top_p=%v top_k=%v", req.TopP, req.TopK)
		}
	})
}
