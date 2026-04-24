package vertexai

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/relay/util"
)

func TestSetupRequestHeader_ClaudeDoesNotLeakAnthropicHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	r := httptest.NewRequest("POST", "/v1/messages", nil)
	r.Header.Set("x-api-key", "sk-ant-xxx")
	r.Header.Set("anthropic-version", "2023-06-01")
	r.Header.Set("anthropic-beta", "thinking-2025-05-14")
	c.Request = r

	req, _ := http.NewRequest("POST", "https://example/x", nil)
	// 把这些头先手动放进 req，模拟上游中间件透传的情况：
	req.Header.Set("x-api-key", "sk-ant-xxx")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", "thinking-2025-05-14")

	a := &Adaptor{IsAPIKeyMode: false}
	meta := &util.RelayMeta{
		ActualModelName: "claude-opus-4-7",
	}
	_ = a.SetupRequestHeader(c, req, meta) // 允许返回错误（access token 获取失败），只看 header 副作用

	if got := req.Header.Get("x-api-key"); got != "" {
		t.Errorf("x-api-key should be stripped before reaching Vertex, got %q", got)
	}
	if got := req.Header.Get("anthropic-version"); got != "" {
		t.Errorf("anthropic-version should be stripped (version goes in body), got %q", got)
	}
	if got := req.Header.Get("anthropic-beta"); got != "" {
		t.Errorf("anthropic-beta should be stripped, got %q", got)
	}
}

// 回归：Gemini 分支不受此清理影响
func TestSetupRequestHeader_GeminiPreservesContentType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	req, _ := http.NewRequest("POST", "https://example/x", nil)

	a := &Adaptor{IsAPIKeyMode: true, APIKey: "k"} // API Key 模式避免 GetAccessToken
	meta := &util.RelayMeta{
		ActualModelName: "gemini-2.5-pro",
	}
	_ = a.SetupRequestHeader(c, req, meta)

	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type missing: %q", got)
	}
}
