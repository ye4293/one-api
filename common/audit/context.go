package audit

import (
	"bytes"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

const ctxKey = "audit_context"

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
	ac.truncatedFields = removeField(ac.truncatedFields, "converted_req_body")
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

type cappedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		b.buf.Write(p)
		return len(p), nil
	}
	if remain := b.limit - b.buf.Len(); remain > 0 {
		if len(p) > remain {
			b.buf.Write(p[:remain])
			b.truncated = true
		} else {
			b.buf.Write(p)
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil // 永远"全部写入"，不打断 TeeReader
}

func WrapUpstreamBody(c *gin.Context, resp *http.Response) {
	if !Enabled() || resp == nil || resp.Body == nil {
		return
	}
	ac := getAuditContext(c)
	if ac == nil {
		return
	}
	cb := &cappedBuffer{limit: pkgConfig.MaxRespKB * 1024}
	c.Set("audit_upstream_buf", cb)
	resp.Body = struct {
		io.Reader
		io.Closer
	}{
		Reader: io.TeeReader(resp.Body, cb),
		Closer: resp.Body,
	}
}

// SetUpstreamResponse 直接设置上游响应内容（用于非流式响应，如图片生成）。
// 若 body 超过 MaxRespKB 限制则截断并标记。
func SetUpstreamResponse(c *gin.Context, body []byte) {
	if !Enabled() {
		return
	}
	ac := getAuditContext(c)
	if ac == nil {
		return
	}
	s, truncated := truncate(string(body), pkgConfig.MaxRespKB)
	ac.UpstreamResponse = s
	if truncated {
		ac.truncatedFields = append(ac.truncatedFields, "upstream_response")
	}
}

func FinalizeUpstream(c *gin.Context) {
	if !Enabled() {
		return
	}
	ac := getAuditContext(c)
	if ac == nil {
		return
	}
	if v, ok := c.Get("audit_upstream_buf"); ok {
		if cb, ok := v.(*cappedBuffer); ok {
			ac.UpstreamResponse = cb.buf.String()
			if cb.truncated {
				ac.truncatedFields = append(ac.truncatedFields, "upstream_response")
			}
		}
	}
}
