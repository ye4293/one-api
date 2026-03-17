package channel

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/songquanpeng/one-api/common/config"
)

// SendJSONVideoRequest 发送 JSON POST 请求并返回原始响应体
func SendJSONVideoRequest(url string, body any, headers map[string]string) (*http.Response, []byte, error) {
	jsonData, err := json.Marshal(body)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal request body: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(jsonData))
	if err != nil {
		return nil, nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, nil, fmt.Errorf("read response body: %w", err)
	}
	return resp, respBody, nil
}

// SendVideoResultQuery 发送 GET 请求用于结果轮询，返回原始响应体
func SendVideoResultQuery(url string, headers map[string]string) (*http.Response, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, nil, fmt.Errorf("read response body: %w", err)
	}
	return resp, respBody, nil
}

// BearerAuthHeaders 返回携带 Bearer Token 的请求头 map
func BearerAuthHeaders(apiKey string) map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + apiKey,
	}
}

// UploadVideoBase64ToR2 将base64编码的视频数据上传到Cloudflare R2并返回URL
func UploadVideoBase64ToR2(base64Data string, userId int, videoFormat string) (string, error) {
	// 参数检查
	if base64Data == "" {
		return "", fmt.Errorf("base64 data is required")
	}
	if videoFormat == "" {
		videoFormat = "mp4" // 默认格式
	}

	// 解码base64数据
	videoData, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64 data: %v", err)
	}

	// 生成唯一的文件名
	randomBytes := make([]byte, 8)
	rand.Read(randomBytes)
	timestamp := time.Now().Unix()
	filename := fmt.Sprintf("%d_%d_%x.%s", userId, timestamp, randomBytes, videoFormat)

	// 确定内容类型
	var contentType string
	switch strings.ToLower(videoFormat) {
	case "mp4":
		contentType = "video/mp4"
	case "avi":
		contentType = "video/x-msvideo"
	case "mov":
		contentType = "video/quicktime"
	case "wmv":
		contentType = "video/x-ms-wmv"
	case "flv":
		contentType = "video/x-flv"
	case "webm":
		contentType = "video/webm"
	default:
		contentType = "video/mp4"
	}

	// 创建上下文
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// 加载AWS配置
	cfg, err := awsConfig.LoadDefaultConfig(ctx,
		awsConfig.WithRegion("us-east-1"),
		awsConfig.WithCredentialsProvider(aws.NewCredentialsCache(aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
			return aws.Credentials{
				AccessKeyID:     config.CfFileAccessKey,
				SecretAccessKey: config.CfFileSecretKey,
			}, nil
		}))),
		awsConfig.WithEndpointResolverWithOptions(aws.EndpointResolverWithOptionsFunc(
			func(service, region string, options ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{URL: config.CfFileEndpoint}, nil
			}),
		),
	)
	if err != nil {
		return "", fmt.Errorf("unable to load SDK config: %w", err)
	}

	// 创建S3客户端（使用 Path-Style 避免虚拟主机风格的子域名 TLS 问题）
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
	})

	// 上传视频到R2
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(config.CfBucketFileName),
		Key:         aws.String(filename),
		Body:        bytes.NewReader(videoData),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload video to R2: %w", err)
	}

	// 生成文件 URL
	// 优先使用公共访问 URL（如自定义域），否则使用 S3 Endpoint（Path-Style 格式）
	if config.CfFilePublicUrl != "" {
		return fmt.Sprintf("%s/%s", config.CfFilePublicUrl, filename), nil
	}
	return fmt.Sprintf("%s/%s/%s", config.CfFileEndpoint, config.CfBucketFileName, filename), nil
}
