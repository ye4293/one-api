package cloudflare

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	commonConfig "github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
)

// getExtensionFromMimeType 根据 MIME 类型获取文件扩展名
func getExtensionFromMimeType(mimeType string) string {
	mimeType = strings.ToLower(mimeType)
	switch {
	case strings.Contains(mimeType, "jpeg"), strings.Contains(mimeType, "jpg"):
		return ".jpg"
	case strings.Contains(mimeType, "png"):
		return ".png"
	case strings.Contains(mimeType, "gif"):
		return ".gif"
	case strings.Contains(mimeType, "webp"):
		return ".webp"
	case strings.Contains(mimeType, "bmp"):
		return ".bmp"
	case strings.Contains(mimeType, "svg"):
		return ".svg"
	default:
		return ".jpg" // 默认使用 .jpg
	}
}

// generateFileUUID 生成文件 UUID
func generateFileUUID() string {
	return fmt.Sprintf("%d%d", time.Now().UnixNano(), time.Now().Unix()%10000)
}

// UploadImageToR2 上传图片到 R2（用于 Gemini 响应图片）
// 返回：公开访问 URL, 错误
func UploadImageToR2(ctx context.Context, base64Data string, mimeType string) (string, error) {
	// 获取配置（使用 common/config 中的配置）
	accessKey := commonConfig.CfFileAccessKey
	secretKey := commonConfig.CfFileSecretKey
	bucketName := commonConfig.CfBucketFileName
	endpoint := commonConfig.CfFileEndpoint

	// 验证配置
	if accessKey == "" || secretKey == "" || bucketName == "" || endpoint == "" {
		return "", fmt.Errorf("R2 configuration is incomplete")
	}

	// 解码 base64
	imageData, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64: %v", err)
	}

	// 生成文件名：timestamp-uuid.ext
	timestamp := time.Now().Format("20060102-150405")
	uuid := generateFileUUID()
	ext := getExtensionFromMimeType(mimeType)
	filename := fmt.Sprintf("%s-%s%s", timestamp, uuid, ext)
	objectKey := path.Join("gemini-images", filename)

	// 加载 AWS 配置
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("auto"),
		config.WithCredentialsProvider(aws.NewCredentialsCache(aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
			return aws.Credentials{
				AccessKeyID:     accessKey,
				SecretAccessKey: secretKey,
			}, nil
		}))),
		config.WithEndpointResolverWithOptions(aws.EndpointResolverWithOptionsFunc(
			func(service, region string, options ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{URL: endpoint}, nil
			}),
		),
	)
	if err != nil {
		return "", fmt.Errorf("failed to create AWS config: %v", err)
	}

	// 创建 S3 客户端（使用 Path-Style 避免虚拟主机风格的子域名 TLS 问题）
	s3Client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
	})

	// 上传对象
	_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(objectKey),
		Body:        bytes.NewReader(imageData),
		ContentType: aws.String(mimeType),
		//ACL:         types.ObjectCannedACL(types.ObjectCannedACLPublicRead),
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload to R2: %v", err)
	}

	// 生成文件 URL
	// 优先使用公共访问 URL（如自定义域），否则使用 S3 Endpoint（Path-Style 格式）
	var resultUrl string
	if commonConfig.CfFilePublicUrl != "" {
		resultUrl = fmt.Sprintf("%s/%s", commonConfig.CfFilePublicUrl, objectKey)
	} else {
		resultUrl = fmt.Sprintf("%s/%s/%s", commonConfig.CfFileEndpoint, bucketName, objectKey)
	}
	logger.SysLog(fmt.Sprintf("Image uploaded to R2: %s (size: %d bytes)", resultUrl, len(imageData)))

	return resultUrl, nil
}
