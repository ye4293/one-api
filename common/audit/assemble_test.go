package audit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func loadConfigEnabledForTest() *auditConfig {
	c := loadConfig()
	c.Enabled = true
	if c.MaxBodyKB <= 0 {
		c.MaxBodyKB = 10240
	}
	if c.MaxRespKB <= 0 {
		c.MaxRespKB = 4096
	}
	if _, ok := c.redactSet["authorization"]; !ok {
		c.redactSet["authorization"] = struct{}{}
	}
	return c
}

func newTestCtxAssemble() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("id", 7)
	c.Set("channel_id", 3)
	c.Set("username", "alice")
	c.Set("token_name", "tok")
	c.Set("original_model", "gpt-4")
	c.Set("X-Request-ID", "req-1")
	return c
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

func TestBuildAndSubmitAssembles(t *testing.T) {
	resetForTest()
	pkgConfig = loadConfigEnabledForTest()
	recordChan = make(chan *AuditRecord, 10)
	c := newTestCtxAssemble()
	InitAuditContext(c)
	SetConvertedBody(c, `{"model":"gpt-4"}`)
	h := http.Header{}
	h.Set("Authorization", "Bearer up-key")
	SetConvertedHeader(c, h)

	in := FinalizeInput{
		Start:          time.Now().Add(-100 * time.Millisecond),
		OrigHeaders:    func() http.Header { hh := http.Header{}; hh.Set("Authorization", "Bearer client-key"); return hh }(),
		OrigBody:       []byte(`{"model":"gpt-4"}`),
		ClientResponse: "data: hi",
		StatusCode:     200,
	}
	BuildAndSubmit(c, in)

	r := <-recordChan
	if r.UserID != 7 || r.ChannelID != 3 {
		t.Errorf("业务字段应从 context 提取")
	}
	if !contains(r.OriginalReqHeaders, redactedValue) {
		t.Errorf("原始请求头中的 Authorization 应脱敏")
	}
	if !contains(r.ConvertedReqHeaders, redactedValue) {
		t.Errorf("转换后请求头中的 Authorization 应脱敏")
	}
	if !r.ConvertedSameAsOriginal {
		t.Errorf("转换体与原始体逐字节相同应置标记")
	}
	if r.DurationMS <= 0 {
		t.Errorf("应计算耗时")
	}
}
