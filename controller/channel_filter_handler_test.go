package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
)

// callTestChannels 用 gin test recorder 构造 GET /api/channel/test?<query> 请求，
// 直接调 TestChannels handler，返回 status code + JSON body。
func callTestChannels(t *testing.T, query string) (int, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	u, _ := url.Parse("/api/channel/test?" + query)
	c.Request = &http.Request{
		Method: "GET",
		URL:    u,
		Header: make(http.Header),
	}
	TestChannels(c)
	body := make(map[string]any)
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

// TestChannelsHandler_Filter_MissingBothKeywordAndType
// scope=filter 未提供 keyword 也未提供 type → 400
func TestChannelsHandler_Filter_MissingBothKeywordAndType(t *testing.T) {
	code, body := callTestChannels(t, "scope=filter")
	if code != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d", code)
	}
	if success, _ := body["success"].(bool); success {
		t.Fatalf("期望 success=false")
	}
	if msg, _ := body["message"].(string); msg == "" {
		t.Fatalf("应返回错误消息")
	}
}

// TestChannelsHandler_Filter_InvalidType type 非数字 → 400
func TestChannelsHandler_Filter_InvalidType(t *testing.T) {
	code, body := callTestChannels(t, "scope=filter&type=abc&keyword=x")
	if code != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d", code)
	}
	if msg, _ := body["message"].(string); msg == "" {
		t.Fatalf("应返回错误消息")
	}
}

// TestChannelsHandler_Filter_InvalidStatus_OutOfRange status=5 越界 → 400
func TestChannelsHandler_Filter_InvalidStatus_OutOfRange(t *testing.T) {
	code, _ := callTestChannels(t, "scope=filter&keyword=x&status=5")
	if code != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d", code)
	}
}

// TestChannelsHandler_Filter_InvalidStatus_NonNumeric status 含非数字 → 400
func TestChannelsHandler_Filter_InvalidStatus_NonNumeric(t *testing.T) {
	code, _ := callTestChannels(t, "scope=filter&keyword=x&status=1,abc")
	if code != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d", code)
	}
}

// 注意：正例（合法参数走完 handler）需要 setup DB —— 那会进入 testChannels 真实调用
// GetAllChannelsForTest 与后台 goroutine。本测试文件只覆盖参数校验层的 400 路径，
// SQL filter 语义与 model/channel_filter_test.go 覆盖。
