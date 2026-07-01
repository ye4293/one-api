package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/songquanpeng/one-api/common/logger"
)

// bodyDoc 是存到 S3 的 JSON 结构，只含四个大字段。
type bodyDoc struct {
	OriginalReqBody  string `json:"original_req_body"`
	ConvertedReqBody string `json:"converted_req_body"`
	UpstreamResponse string `json:"upstream_response"`
	ClientResponse   string `json:"client_response"`
}

// bodyS3Key 按 prefix/YYYY-MM-DD/requestID.json 生成 key。
func bodyS3Key(cfg *auditConfig, eventTime time.Time, xRequestID string) string {
	prefix := cfg.BodyS3Prefix
	if prefix == "" {
		prefix = "audit-bodies"
	}
	return fmt.Sprintf("%s/%s/%s.json", prefix, eventTime.UTC().Format("2006-01-02"), xRequestID)
}

// uploadBodyAsync 异步上传 body 到 S3，失败只记日志不影响主流程。
func uploadBodyAsync(cfg *auditConfig, s3c *s3.Client, key string, doc bodyDoc) {
	go func() {
		data, err := json.Marshal(doc)
		if err != nil {
			logger.SysError("audit: marshal body for S3: " + err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err = s3c.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(cfg.BodyS3Bucket),
			Key:         aws.String(key),
			Body:        bytes.NewReader(data),
			ContentType: aws.String("application/json"),
		})
		if err != nil {
			logger.SysError("audit: upload body to S3 key=" + key + ": " + err.Error())
		}
	}()
}

// fetchBodyFromS3 同步从 S3 拉取 body，供 QueryDetail 使用。
func (c *awsAuditClient) fetchBodyFromS3(ctx context.Context, key string) (*bodyDoc, error) {
	out, err := c.s3c.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.cfg.BodyS3Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("S3 GetObject key=%s: %w", key, err)
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("S3 read body key=%s: %w", key, err)
	}
	var doc bodyDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("S3 unmarshal key=%s: %w", key, err)
	}
	return &doc, nil
}
