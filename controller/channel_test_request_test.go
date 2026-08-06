package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/relay/channel/anthropic"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/constant"
)

// 测试/探测请求必须是「最小兼容请求」：除 model / messages / max_tokens 外
// 不带任何可选采样参数。
//
// 为什么要把这条钉死成测试：新模型对采样参数的限制越来越严 ——
//   - OpenAI o 系列 / gpt-5：temperature 只接受默认值 1，传 0 会报
//     "Unsupported value: 'temperature' does not support 0 with this model"
//   - Claude Opus 4.7+：完全不支持 temperature/top_p/top_k，传了报
//     "temperature is deprecated for this model"
//
// GeneralOpenAIRequest 的 Temperature/TopP 都带 omitempty，只要
// buildTestRequest 不显式赋值，零值就不会被序列化。这是个**很容易被无意打破**
// 的隐式契约：任何人给 buildTestRequest 加一行 `Temperature: 0.7`，
// 或在 adaptor 里做 `math.Max(0.01, temperature)` 之类的钳制，
// 都会让测活与探针在这些模型上全线 400。
var forbiddenTestRequestParams = []string{"temperature", "top_p", "top_k", "presence_penalty", "frequency_penalty"}

func assertNoForbiddenParams(t *testing.T, label string, payload []byte) {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("%s: payload 不是合法 JSON: %v", label, err)
	}
	for _, p := range forbiddenTestRequestParams {
		if _, found := m[p]; found {
			t.Errorf("%s: 测试请求不应包含采样参数 %q —— 新模型会拒绝。\n  payload: %s",
				label, p, string(payload))
		}
	}
}

// TestOpenAITestRequestCarriesNoSamplingParams 覆盖 GPT 链路（含推理模型）。
func TestOpenAITestRequestCarriesNoSamplingParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	models := []string{
		"gpt-3.5-turbo", "gpt-4", "gpt-4o", // 旧模型
		"o1", "o1-mini", "o3", "o3-mini", "o4-mini", "gpt-5", "gpt-5-mini", // 推理模型
	}
	for _, m := range models {
		t.Run(m, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = &http.Request{
				Method: "POST",
				URL:    &url.URL{Path: "/v1/chat/completions"},
				Header: make(http.Header),
			}

			req := buildTestRequest(m)
			req.Model = m
			a := &openai.Adaptor{}
			conv, err := a.ConvertRequest(c, constant.RelayModeChatCompletions, req)
			if err != nil {
				t.Fatalf("ConvertRequest 失败: %v", err)
			}
			payload, err := json.Marshal(conv)
			if err != nil {
				t.Fatal(err)
			}
			assertNoForbiddenParams(t, m, payload)

			// max_tokens 必须存在且 > 0，且已被 adaptor 转成 max_completion_tokens
			var parsed map[string]any
			_ = json.Unmarshal(payload, &parsed)
			v, ok := parsed["max_completion_tokens"]
			if !ok {
				t.Errorf("%s: 缺少 max_completion_tokens（openai adaptor 应已转换）\n  payload: %s", m, payload)
			} else if n, _ := v.(float64); n < 1 {
				t.Errorf("%s: max_completion_tokens = %v，必须 >= 1", m, v)
			}
		})
	}
}

// TestClaudeTestRequestSamplingParams 覆盖 Claude 链路。
//
// 与 GPT 不同，Claude thinking 模型**必须**带 temperature=1（Anthropic 的硬性
// 要求，见 anthropic/main.go:143-146），所以 thinking 单独断言。
func TestClaudeTestRequestSamplingParams(t *testing.T) {
	t.Run("普通模型不带任何采样参数", func(t *testing.T) {
		for _, m := range []string{"claude-3-5-sonnet-20241022", "claude-3-opus-20240229"} {
			req := buildTestRequest(m)
			req.Model = m
			payload, err := json.Marshal(anthropic.ConvertRequest(*req))
			if err != nil {
				t.Fatal(err)
			}
			assertNoForbiddenParams(t, m, payload)
		}
	})

	t.Run("thinking 模型带 temperature=1 且 max_tokens 大于 budget", func(t *testing.T) {
		m := "claude-opus-4-thinking"
		req := buildTestRequest(m)
		req.Model = m
		conv := anthropic.ConvertRequest(*req)
		payload, err := json.Marshal(conv)
		if err != nil {
			t.Fatal(err)
		}
		var parsed struct {
			MaxTokens   int      `json:"max_tokens"`
			Temperature *float64 `json:"temperature"`
			TopP        *float64 `json:"top_p"`
			Thinking    *struct {
				BudgetTokens int `json:"budget_tokens"`
			} `json:"thinking"`
		}
		if err := json.Unmarshal(payload, &parsed); err != nil {
			t.Fatal(err)
		}
		// Anthropic 要求 thinking 模式下 temperature 必须为 1
		if parsed.Temperature == nil || *parsed.Temperature != 1 {
			t.Errorf("thinking 模型必须带 temperature=1，实际 %v\n  payload: %s", parsed.Temperature, payload)
		}
		// 且不能同时设 top_p
		if parsed.TopP != nil {
			t.Errorf("thinking 模式不能设 top_p\n  payload: %s", payload)
		}
		// 核心：max_tokens 必须大于 thinking budget，否则 Anthropic 直接 400
		if parsed.Thinking == nil {
			t.Fatalf("thinking 配置缺失\n  payload: %s", payload)
		}
		if parsed.MaxTokens <= parsed.Thinking.BudgetTokens {
			t.Errorf("max_tokens(%d) 必须大于 thinking budget(%d)，否则 Anthropic 400",
				parsed.MaxTokens, parsed.Thinking.BudgetTokens)
		}
	})

	t.Run("max_tokens 永不为 0", func(t *testing.T) {
		// Claude 的 max_tokens 无 omitempty，0 会被发出去并被 Anthropic 拒绝
		for _, m := range []string{"claude-3-5-sonnet-20241022", "claude-opus-4-thinking", "未知模型"} {
			req := buildTestRequest(m)
			req.Model = m
			payload, _ := json.Marshal(anthropic.ConvertRequest(*req))
			if strings.Contains(string(payload), `"max_tokens":0`) {
				t.Errorf("%s: 发出了 max_tokens:0，Anthropic 会直接拒绝\n  payload: %s", m, payload)
			}
		}
	})
}
