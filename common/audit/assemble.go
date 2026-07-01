package audit

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// FinalizeInput 汇集中间件 defer 阶段才掌握的原始请求/响应数据。
type FinalizeInput struct {
	Start          time.Time
	OrigHeaders    http.Header
	OrigBody       []byte
	ClientResponse string
	ClientTrunc    bool
	StatusCode     int
}

// MaxRespBytes 返回上游/客户端响应允许捕获的最大字节数。
func MaxRespBytes() int {
	if pkgConfig == nil {
		return 4096 * 1024
	}
	return pkgConfig.MaxRespKB * 1024
}

// BuildAndSubmit 合成 AuditRecord（脱敏、截断、转换体去重）并提交。
func BuildAndSubmit(c *gin.Context, in FinalizeInput) {
	if !Enabled() {
		return
	}
	ac := getAuditContext(c)
	if ac == nil {
		return
	}

	origBody, origTrunc := truncate(string(in.OrigBody), pkgConfig.MaxBodyKB)
	truncFields := append([]string{}, ac.truncatedFields...)
	if origTrunc {
		truncFields = append(truncFields, "original_req_body")
	}
	if in.ClientTrunc {
		truncFields = append(truncFields, "client_response")
	}

	convHeaders := ""
	if ac.ConvertedReqHeaders != nil {
		convHeaders = headersToJSON(redactHeaders(ac.ConvertedReqHeaders, pkgConfig.redactSet))
	}
	origHeaders := ""
	if in.OrigHeaders != nil {
		origHeaders = headersToJSON(redactHeaders(in.OrigHeaders, pkgConfig.redactSet))
	}

	sameAsOrig := ac.ConvertedReqBody != "" && ac.ConvertedReqBody == origBody

	// 检查四个 body 字段总大小是否超过 S3 阈值
	convBody := ac.ConvertedReqBody
	if sameAsOrig {
		convBody = ""
	}
	totalBodySize := len(origBody) + len(convBody) + len(ac.UpstreamResponse) + len(in.ClientResponse)
	threshold := pkgConfig.BodyS3ThresholdKB * 1024

	var s3Key string
	if pkgConfig.BodyS3Bucket != "" && threshold > 0 && totalBodySize > threshold {
		xrid := c.GetString("X-Request-ID")
		s3Key = bodyS3Key(pkgConfig, in.Start, xrid)
		uploadBodyAsync(pkgConfig, awsClient.s3c, s3Key, bodyDoc{
			OriginalReqBody:  origBody,
			ConvertedReqBody: convBody,
			UpstreamResponse: ac.UpstreamResponse,
			ClientResponse:   in.ClientResponse,
		})
		// body 字段留空，Iceberg 只存 key
		origBody = ""
		convBody = ""
		ac.UpstreamResponse = ""
		in.ClientResponse = ""
	}

	r := &AuditRecord{
		EventTime:               in.Start,
		XRequestID:              c.GetString("X-Request-ID"),
		UserID:                  c.GetInt("id"),
		Username:                c.GetString("username"),
		ChannelID:               c.GetInt("channel_id"),
		TokenName:               c.GetString("token_name"),
		OriginModel:             c.GetString("original_model"),
		ActualModel:             actualModelFromCtx(c),
		IsStream:                c.GetBool("audit_is_stream"),
		StatusCode:              in.StatusCode,
		DurationMS:              time.Since(in.Start).Milliseconds(),
		OriginalReqHeaders:      origHeaders,
		OriginalReqBody:         origBody,
		ConvertedReqHeaders:     convHeaders,
		ConvertedReqBody:        convBody,
		ConvertedSameAsOriginal: sameAsOrig,
		UpstreamResponse:        ac.UpstreamResponse,
		ClientResponse:          in.ClientResponse,
		TruncatedFields:         truncFields,
	}
	Submit(r)
}

func actualModelFromCtx(c *gin.Context) string {
	if v := c.GetString("audit_actual_model"); v != "" {
		return v
	}
	return c.GetString("original_model")
}
