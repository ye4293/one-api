package audit

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const ctxKey = "audit_context"

// pkgConfig 占位声明，Task 5 将由 manager 统一管理/复用。
var pkgConfig *config

// AuditContext 暂存单次请求在 relay 流程中埋点写入的数据。
type AuditContext struct {
	ConvertedReqHeaders http.Header
	ConvertedReqBody    string
	UpstreamResponse    string
	truncatedFields     []string
}

func InitAuditContext(c *gin.Context) {
	if !Enabled() {
		return
	}
	c.Set(ctxKey, &AuditContext{})
}

func getAuditContext(c *gin.Context) *AuditContext {
	v, ok := c.Get(ctxKey)
	if !ok {
		return nil
	}
	ac, _ := v.(*AuditContext)
	return ac
}

func SetConvertedBody(c *gin.Context, body string) {
	if !Enabled() {
		return
	}
	ac := getAuditContext(c)
	if ac == nil {
		return
	}
	s, truncated := truncate(body, pkgConfig.MaxBodyKB)
	ac.ConvertedReqBody = s
	if truncated {
		ac.truncatedFields = append(ac.truncatedFields, "converted_req_body")
	}
}

func SetConvertedHeader(c *gin.Context, h http.Header) {
	if !Enabled() {
		return
	}
	ac := getAuditContext(c)
	if ac == nil {
		return
	}
	ac.ConvertedReqHeaders = h.Clone()
}

// SetMeta 暂存 relay 流程才知道的元信息，供中间件 defer 阶段组装记录。
func SetMeta(c *gin.Context, isStream bool, actualModel string) {
	if !Enabled() {
		return
	}
	c.Set("audit_is_stream", isStream)
	c.Set("audit_actual_model", actualModel)
}

// Enabled 占位实现，Task 5 改为从 manager 读取。
func Enabled() bool { return pkgConfig != nil && pkgConfig.Enabled }
