package audit

import (
	"net/http"
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
	pkgConfig = &config{Enabled: false}
	c := newTestCtx()
	SetConvertedBody(c, "{}") // 关闭时不应 panic、不应写入
	if _, ok := c.Get(ctxKey); ok {
		t.Errorf("关闭时不应在 context 写入审计数据")
	}
}

func TestSetConvertedBodyEnabled(t *testing.T) {
	pkgConfig = &config{Enabled: true, MaxBodyKB: 10240}
	c := newTestCtx()
	InitAuditContext(c)
	SetConvertedBody(c, `{"model":"gpt-4"}`)
	ac := getAuditContext(c)
	if ac == nil || ac.ConvertedReqBody != `{"model":"gpt-4"}` {
		t.Errorf("开启时应暂存转换后请求体")
	}
}
