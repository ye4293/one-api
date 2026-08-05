package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/metrics"
)

func newTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

func TestResolveModelPrefersOriginalModel(t *testing.T) {
	c := newTestContext()
	// original_model 是用户请求的原始模型名，与 DB logs.model_name 列口径一致
	c.Set("original_model", "gpt-4o")
	c.Set("model", "gpt-4o-mapped")
	c.Set(metrics.CtxModelKey, "from-503-fallback")

	if got := resolveModel(c); got != "gpt-4o" {
		t.Errorf("resolveModel = %q, want %q（应优先 original_model 以与 DB 口径一致）", got, "gpt-4o")
	}
}

func TestResolveModelFallbackChain(t *testing.T) {
	cases := []struct {
		name string
		keys map[string]string
		want string
	}{
		{"只有 model", map[string]string{"model": "claude-sonnet-4-5"}, "claude-sonnet-4-5"},
		// 503 无可用渠道时 distributor 在 c.Set("model") 之前就 abort，只剩这个兜底键
		{"只有 503 兜底键", map[string]string{metrics.CtxModelKey: "ghost-model"}, "ghost-model"},
		{"什么都没有", map[string]string{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestContext()
			for k, v := range tc.keys {
				c.Set(k, v)
			}
			if got := resolveModel(c); got != tc.want {
				t.Errorf("resolveModel = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResolveOutcomeStreamingTrap 是本文件存在的理由。
//
// SSE 流式请求一旦响应头写出，中途失败时 c.Writer.Status() 仍然是 200
// （controller/relay.go 里的 c.JSON 只会打一条 "headers already written" 警告）。
// 如果只看状态码，流式失败会被记成成功 —— 而流式恰好是 LLM 网关的主要流量形态，
// 这会系统性低估错误率。relay 层写入的 CtxRelayFailedKey 必须优先于状态码。
func TestResolveOutcomeStreamingTrap(t *testing.T) {
	c := newTestContext()
	c.Status(http.StatusOK) // 流式：头已写出，状态码是 200
	c.Set(metrics.CtxRelayFailedKey, metrics.ReasonUpstream5xx)

	_, relayFailed := c.Get(metrics.CtxRelayFailedKey)
	if got := resolveOutcome(c, relayFailed); got != metrics.OutcomeError {
		t.Errorf("流式中途失败被判为 %q, want %q —— relay 层标记必须优先于状态码", got, metrics.OutcomeError)
	}
}

func TestResolveOutcome(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		relayFailed bool
		want        string
	}{
		{"正常成功", http.StatusOK, false, metrics.OutcomeSuccess},
		{"非流式 500", http.StatusInternalServerError, false, metrics.OutcomeError},
		{"鉴权失败 401", http.StatusUnauthorized, false, metrics.OutcomeError},
		{"无可用渠道 503", http.StatusServiceUnavailable, false, metrics.OutcomeError},
		{"限流 429", http.StatusTooManyRequests, false, metrics.OutcomeError},
		{"流式 200 但 relay 标记失败", http.StatusOK, true, metrics.OutcomeError},
		{"状态码 4xx 且 relay 也标记失败（不应重复判定出别的结果）", http.StatusBadRequest, true, metrics.OutcomeError},
		{"3xx 不算失败", http.StatusFound, false, metrics.OutcomeSuccess},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestContext()
			c.Status(tc.status)
			if got := resolveOutcome(c, tc.relayFailed); got != tc.want {
				t.Errorf("resolveOutcome(status=%d, relayFailed=%v) = %q, want %q",
					tc.status, tc.relayFailed, got, tc.want)
			}
		})
	}
}

// TestRelayMetricsSkipsNonRelayRequests 确认中间件不会把 web / /api 这类
// 非 relay 请求计入 LLM 指标。判据是 context 里没有任何模型名来源，
// 而不是路径白名单 —— relay 路由有 19 个前缀，白名单会随新增路由失效。
func TestRelayMetricsSkipsNonRelayRequests(t *testing.T) {
	c := newTestContext()
	c.Status(http.StatusOK)

	model := resolveModel(c)
	_, relayFailed := c.Get(metrics.CtxRelayFailedKey)
	if model != "" || relayFailed {
		t.Fatalf("空 context 被判成了 relay 请求：model=%q relayFailed=%v", model, relayFailed)
	}
}
