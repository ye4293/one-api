package audit

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func newTestCtx() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	c.Request, _ = http.NewRequest("POST", "/v1/chat/completions", nil)
	return c
}

func TestSetConvertedBodyDisabledIsNoop(t *testing.T) {
	pkgConfig = &auditConfig{Enabled: false}
	c := newTestCtx()
	SetConvertedBody(c, "{}") // 关闭时不应 panic、不应写入
	if _, ok := c.Get(ctxKey); ok {
		t.Errorf("关闭时不应在 context 写入审计数据")
	}
}

func TestSetConvertedBodyEnabled(t *testing.T) {
	pkgConfig = &auditConfig{Enabled: true, MaxBodyKB: 10240}
	c := newTestCtx()
	InitAuditContext(c)
	SetConvertedBody(c, `{"model":"gpt-4"}`)
	ac := getAuditContext(c)
	if ac == nil || ac.ConvertedReqBody != `{"model":"gpt-4"}` {
		t.Errorf("开启时应暂存转换后请求体")
	}
}

func TestWrapUpstreamBody(t *testing.T) {
	pkgConfig = &auditConfig{Enabled: true, MaxRespKB: 4096}
	c := newTestCtx()
	InitAuditContext(c)
	resp := &http.Response{
		Body: io.NopCloser(strings.NewReader("upstream-data")),
	}
	WrapUpstreamBody(c, resp)
	// 模拟 DoResponse 照常消费 body
	consumed, _ := io.ReadAll(resp.Body)
	if string(consumed) != "upstream-data" {
		t.Errorf("包装后 body 仍应可被完整消费, got %q", consumed)
	}
	// tee 旁路应抓到同样内容
	FinalizeUpstream(c)
	ac := getAuditContext(c)
	if ac.UpstreamResponse != "upstream-data" {
		t.Errorf("tee 应抓到上游响应, got %q", ac.UpstreamResponse)
	}
}
