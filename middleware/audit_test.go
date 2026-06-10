package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/audit"
)

func TestAuditMiddlewareDisabledPassthrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Audit())
	r.POST("/x", func(c *gin.Context) { c.String(200, "ok") })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/x", strings.NewReader("body"))
	r.ServeHTTP(w, req)
	if w.Body.String() != "ok" {
		t.Errorf("关闭时中间件应完全透传, got %q", w.Body.String())
	}
	_ = audit.Enabled()
}
