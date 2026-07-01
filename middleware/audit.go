package middleware

import (
	"bytes"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/audit"
)

type auditRespWriter struct {
	gin.ResponseWriter
	buf   bytes.Buffer
	limit int
	trunc bool
}

func (w *auditRespWriter) Write(b []byte) (int, error) {
	if w.limit <= 0 {
		w.buf.Write(b)
	} else if remain := w.limit - w.buf.Len(); remain > 0 {
		if len(b) > remain {
			w.buf.Write(b[:remain])
			w.trunc = true
		} else {
			w.buf.Write(b)
		}
	}
	return w.ResponseWriter.Write(b)
}

func Audit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !audit.Enabled() {
			c.Next()
			return
		}
		start := time.Now()
		audit.InitAuditContext(c)
		origBody, _ := common.GetRequestBody(c)
		arw := &auditRespWriter{ResponseWriter: c.Writer, limit: audit.MaxRespBytes()}
		c.Writer = arw
		origHeaders := c.Request.Header.Clone()

		defer func() {
			r := recover()
			audit.FinalizeUpstream(c)
			audit.BuildAndSubmit(c, audit.FinalizeInput{
				Start:          start,
				OrigHeaders:    origHeaders,
				OrigBody:       origBody,
				ClientResponse: arw.buf.String(),
				ClientTrunc:    arw.trunc,
				StatusCode:     arw.Status(),
			})
			if r != nil {
				panic(r)
			}
		}()
		c.Next()
	}
}
