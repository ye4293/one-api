package ali

import (
	"encoding/json"
	"testing"

	"github.com/songquanpeng/one-api/relay/model"
)

// TestCompatibleChatRequestMarshal 锁定 wrapper 结构体的序列化形状：
//   - 嵌入的 *GeneralOpenAIRequest 字段应扁平化到顶层
//   - 顶层 enable_search 字段应与之并列
//   - enable_search=false 时应因 omitempty 被省略
//
// 若未来某天 GeneralOpenAIRequest 内部新增同名 EnableSearch 字段，Go 的 JSON 编码器
// 会让外层字段静默覆盖内嵌字段，此测试会因为序列化结果变化而失败，提醒维护者处理冲突。
func TestCompatibleChatRequestMarshal(t *testing.T) {
	t.Run("enable_search_true_is_emitted", func(t *testing.T) {
		req := &model.GeneralOpenAIRequest{
			Model:  "qwen-plus",
			Stream: true,
		}
		wrapped := compatibleChatRequest{GeneralOpenAIRequest: req, EnableSearch: true}
		data, err := json.Marshal(wrapped)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if raw["model"] != "qwen-plus" {
			t.Errorf("expected flattened model=qwen-plus, got %v", raw["model"])
		}
		if raw["stream"] != true {
			t.Errorf("expected flattened stream=true, got %v", raw["stream"])
		}
		if raw["enable_search"] != true {
			t.Errorf("expected enable_search=true, got %v (full: %s)", raw["enable_search"], string(data))
		}
	})

	t.Run("enable_search_false_is_omitted", func(t *testing.T) {
		req := &model.GeneralOpenAIRequest{Model: "qwen-plus"}
		wrapped := compatibleChatRequest{GeneralOpenAIRequest: req, EnableSearch: false}
		data, err := json.Marshal(wrapped)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if _, present := raw["enable_search"]; present {
			t.Errorf("enable_search should be omitted when false, got %s", string(data))
		}
	})

	t.Run("thinking_fields_pass_through", func(t *testing.T) {
		enable := true
		req := &model.GeneralOpenAIRequest{
			Model:          "qwen3-max",
			EnableThinking: &enable,
			ThinkingBudget: 8192,
		}
		wrapped := compatibleChatRequest{GeneralOpenAIRequest: req}
		data, err := json.Marshal(wrapped)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if raw["enable_thinking"] != true {
			t.Errorf("expected enable_thinking=true, got %v", raw["enable_thinking"])
		}
		if raw["thinking_budget"] != float64(8192) {
			t.Errorf("expected thinking_budget=8192, got %v", raw["thinking_budget"])
		}
	})

	t.Run("thinking_disabled_is_emitted", func(t *testing.T) {
		disable := false
		req := &model.GeneralOpenAIRequest{Model: "qwen3-max", EnableThinking: &disable}
		data, err := json.Marshal(compatibleChatRequest{GeneralOpenAIRequest: req})
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		var raw map[string]any
		_ = json.Unmarshal(data, &raw)
		if raw["enable_thinking"] != false {
			t.Errorf("expected enable_thinking=false to be preserved, got %v", raw["enable_thinking"])
		}
	})
}
