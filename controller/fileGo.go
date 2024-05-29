package controller

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gin-gonic/gin"
	commonConfig "github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/model"
)

func DownloadImage(url string) ([]byte, error) {
	// 发送HTTP GET请求
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to download image: %v", err)
	}
	defer resp.Body.Close()

	// 检查HTTP响应状态码
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download image: received non-200 status code %d", resp.StatusCode)
	}

	// 读取响应体
	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read image data: %v", err)
	}

	return buf.Bytes(), nil
}

func UploadToR2WithURL(ctx context.Context, imageData []byte, bucketName, objectKey, accessKey, secretKey, endpoint string) (string, error) {
	// 参数检查
	if bucketName == "" {
		return "", fmt.Errorf("bucket name is required")
	}
	if objectKey == "" {
		return "", fmt.Errorf("object key is required")
	}
	if accessKey == "" {
		return "", fmt.Errorf("access key is required")
	}
	if secretKey == "" {
		return "", fmt.Errorf("secret key is required")
	}
	if endpoint == "" {
		return "", fmt.Errorf("endpoint is required")
	}

	// 加载配置
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
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
		return "", fmt.Errorf("failed to create config: %v", err)
	}

	// 创建 S3 客户端
	s3Client := s3.NewFromConfig(cfg)

	// 上传对象
	_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(objectKey),
		Body:        bytes.NewReader(imageData),
		ContentType: aws.String("image/png"),
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload image to R2: %v", err)
	}

	// 生成 URL
	Imager2Url := "https://pub-787c236addba492a978fd31529395f95.r2.dev"
	return fmt.Sprintf("%s/%s", Imager2Url, objectKey), nil
}
func DeleteFileR2(ctx context.Context, filename string) error {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(aws.NewCredentialsCache(aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
			return aws.Credentials{
				AccessKeyID:     commonConfig.CfFileAccessKey,
				SecretAccessKey: commonConfig.CfFileSecretKey,
			}, nil
		}))),
		config.WithEndpointResolverWithOptions(aws.EndpointResolverWithOptionsFunc(
			func(service, region string, options ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{URL: commonConfig.CfFileEndpoint}, nil
			}),
		),
	)
	if err != nil {
		return fmt.Errorf("unable to load SDK config: %w", err)
	}

	client := s3.NewFromConfig(cfg)

	_, err = client.DeleteObject(context.TODO(), &s3.DeleteObjectInput{
		Bucket: aws.String(commonConfig.CfBucketFileName),
		Key:    aws.String(filename),
	})
	if err != nil {
		return fmt.Errorf("failed to delete file from R2: %w", err)
	}

	return nil
}

func UploadFileR2WithUrl(ctx context.Context, file multipart.File, filename, contentType string) (string, error) {
	if contentType == "" {
		ext := filepath.Ext(filename)
		contentType = mime.TypeByExtension(ext)
		if contentType == "" {
			contentType = "application/octet-stream"
		}
	}

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(aws.NewCredentialsCache(aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
			return aws.Credentials{
				AccessKeyID:     commonConfig.CfFileAccessKey,
				SecretAccessKey: commonConfig.CfFileSecretKey,
			}, nil
		}))),
		config.WithEndpointResolverWithOptions(aws.EndpointResolverWithOptionsFunc(
			func(service, region string, options ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{URL: commonConfig.CfFileEndpoint}, nil
			}),
		),
	)
	if err != nil {
		return "", fmt.Errorf("unable to load SDK config: %w", err)
	}

	client := s3.NewFromConfig(cfg)

	_, err = client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:      aws.String(commonConfig.CfBucketFileName),
		Key:         aws.String(filename),
		Body:        file,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload file to R2: %w", err)
	}

	fileUrl := "https://pub-749922e955214210b0ae4eb664a62eca.r2.dev"

	return fmt.Sprintf("%s/%s", fileUrl, filename), nil
}

func CheckUserUsage(group string, UserUsedBytes int64) bool {
	levelSizeMap := map[string]int64{
		"Lv1": 100 * 1024 * 1024,
		"Lv2": 300 * 1024 * 1024,
		"Lv3": 500 * 1024 * 1024,
		"Lv4": 1 * 1024 * 1024 * 1024,
		"Lv5": 2 * 1024 * 1024 * 1024,
	}

	limit, exists := levelSizeMap[group]
	if !exists {
		// 如果传入的group值不存在于levelSizeMap中，返回false
		return false
	}

	if UserUsedBytes > limit {
		// 如果用户使用的字节数超过了对应的限制，返回false
		return false
	}

	// 如果用户使用的字节数没有超过限制，返回true
	return true
}

type UploadPurpose struct {
	Purpose string `json:"purpose"`
}

func UploadFile(c *gin.Context) {
	var uploadPurpose UploadPurpose
	if err := c.ShouldBind(&uploadPurpose); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": err.Error(),
			"success": false,
		})
		return
	}
	userId := c.GetInt("id")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()
	createTime := time.Now()
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"message": "Error retrieving the file",
			"success": false,
		})
		return
	}
	defer file.Close()

	group, err := model.GetUserGroup(userId)
	if err != nil {
		return
	}
	UserUsedBytes, err := model.SumBytesByUserId(userId)
	if err != nil {
		return
	}
	if !CheckUserUsage(group, UserUsedBytes+header.Size) {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": "The total size of uploaded files exceeds your level limit",
			"success": false,
		})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error retrieving the file"})
		return
	}

	fileStoreUrl, err := UploadFileR2WithUrl(ctx, file, strconv.Itoa(userId)+header.Filename, header.Header.Get("Content-Type"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"message": "Failed to upload file to R2",
			"success": false,
		})
		return
	}
	finishTime := time.Now()

	newFile := model.File{
		UserId:     userId,
		CreatTime:  createTime.Unix(),
		FinishTime: finishTime.Unix(),
		FileName:   header.Filename,
		StoreUrl:   fileStoreUrl,
		Bytes:      header.Size,
		Purpose:    uploadPurpose.Purpose,
	}
	err = newFile.Insert()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "File uploaded successfully",
		"success": true,
		"file":    newFile,
	})
	return
}

func DeletiFile(c *gin.Context) {
	type DeleteFile struct {
		Filename string `json:"filename"`
	}

	var filename DeleteFile
	if err := c.ShouldBind(&filename); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": err.Error(),
			"success": false,
		})
		return
	}
	userId := c.GetInt("id")
	filename2 := strconv.Itoa(userId) + filename.Filename
	ctx, cancel := context.WithTimeout(c.Request.Context(), 1*time.Minute)
	defer cancel()
	err := DeleteFileR2(ctx, filename2)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": err.Error(),
			"success": false,
		})
		return
	}
	err = model.DeleteFileByFilename(filename.Filename)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": err.Error(),
			"success": false,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "File deleted successfully",
		"success": true,
	})
	return
}
