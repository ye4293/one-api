package controller

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/ali"
	"github.com/songquanpeng/one-api/relay/channel/doubao"
	"github.com/songquanpeng/one-api/relay/channel/keling"
	"github.com/songquanpeng/one-api/relay/channel/luma"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/channel/pixverse"
	"github.com/songquanpeng/one-api/relay/channel/runway"
	"github.com/songquanpeng/one-api/relay/channel/vertexai"
	"github.com/songquanpeng/one-api/relay/channel/viggle"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

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

	// 创建S3客户端
	client := s3.NewFromConfig(cfg)

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

	// 生成文件URL
	fileUrl := "https://file.ezlinkai.com"
	return fmt.Sprintf("%s/%s", fileUrl, filename), nil
}

func DoVideoRequest(c *gin.Context, modelName string) *model.ErrorWithStatusCode {
	ctx := c.Request.Context()
	var videoRequest model.VideoRequest
	err := common.UnmarshalBodyReusable(c, &videoRequest)
	meta := util.GetRelayMeta(c)
	if err != nil {
		return openai.ErrorWrapper(err, "invalid_text_request", http.StatusBadRequest)
	}

	if modelName == "video-01" ||
		modelName == "video-01-live2d" ||
		modelName == "S2V-01" ||
		modelName == "T2V-01" ||
		modelName == "I2V-01" ||
		modelName == "T2V-01-Director" ||
		modelName == "I2V-01-Director" ||
		modelName == "I2V-01-live" ||
		modelName == "MiniMax-Hailuo-02" {
		return handleMinimaxVideoRequest(c, ctx, videoRequest, meta)
	} else if modelName == "cogvideox" {
		return handleZhipuVideoRequest(c, ctx, videoRequest, meta)
	} else if strings.HasPrefix(modelName, "kling") {
		return handleKelingVideoRequest(c, ctx, meta)
	} else if modelName == "gen3a_turbo" {
		return handleRunwayVideoRequest(c, ctx, videoRequest, meta)
	} else if strings.HasPrefix(modelName, "luma") {
		return handleLumaVideoRequest(c, ctx, videoRequest, meta)
	} else if modelName == "viggle" {
		return handleViggleVideoRequest(c, ctx, videoRequest, meta)
	} else if modelName == "v3.5" {
		return handlePixverseVideoRequest(c, ctx, videoRequest, meta)
	} else if strings.HasPrefix(modelName, "doubao") {
		return handleDoubaoVideoRequest(c, ctx, videoRequest, meta)
	} else if strings.HasPrefix(modelName, "veo") {
		return handleVeoVideoRequest(c, ctx, videoRequest, meta)
	} else if strings.HasPrefix(modelName, "wan") {
		return handleAliVideoRequest(c, ctx, videoRequest, meta)
	} else if strings.Contains(modelName, "remix") || modelName == "sora-2-remix" || modelName == "sora-2-pro-remix" {
		// Sora Remix 请求
		return handleSoraRemixRequest(c, ctx, meta)
	} else if strings.HasPrefix(modelName, "sora") {
		return handleSoraVideoRequest(c, ctx, videoRequest, meta)
	} else {
		return openai.ErrorWrapper(fmt.Errorf("unsupported model"), "unsupported_model", http.StatusBadRequest)
	}
}

func handleSoraVideoRequest(c *gin.Context, ctx context.Context, videoRequest model.VideoRequest, meta *util.RelayMeta) *model.ErrorWithStatusCode {
	contentType := c.GetHeader("Content-Type")

	// 判断是 form-data 还是 JSON
	if strings.Contains(contentType, "multipart/form-data") {
		// Form-data 格式，直接透传
		return handleSoraVideoRequestFormData(c, ctx, meta)
	} else {
		// JSON 格式，需要转换为 form-data
		return handleSoraVideoRequestJSON(c, ctx, meta)
	}
}

// handleSoraVideoRequestFormData 处理原生 form-data 格式的请求（透传）
func handleSoraVideoRequestFormData(c *gin.Context, ctx context.Context, meta *util.RelayMeta) *model.ErrorWithStatusCode {
	// 解析 multipart form
	err := c.Request.ParseMultipartForm(32 << 20) // 32 MB
	if err != nil {
		return openai.ErrorWrapper(err, "parse_multipart_form_failed", http.StatusBadRequest)
	}

	// 提取参数用于计费
	modelName := c.Request.FormValue("model")
	if modelName == "" {
		modelName = meta.ActualModelName
	}

	secondsStr := c.Request.FormValue("seconds")
	if secondsStr == "" {
		secondsStr = "4" // 默认值 - Sora 官方默认 4 秒
	}

	size := c.Request.FormValue("size")
	if size == "" {
		size = "720x1280" // 默认分辨率
	}

	log.Printf("sora-video-request (form-data): model=%s, seconds=%s, size=%s", modelName, secondsStr, size)

	// 直接透传 form-data
	return sendRequestAndHandleSoraVideoResponseFormData(c, ctx, meta, modelName, secondsStr, size)
}

// handleSoraVideoRequestJSON 处理 JSON 格式的请求（转换为 form-data）
func handleSoraVideoRequestJSON(c *gin.Context, ctx context.Context, meta *util.RelayMeta) *model.ErrorWithStatusCode {
	// 使用 UnmarshalBodyReusable 以支持 body 多次读取（Distribute 中间件已读取过）
	var soraReq openai.SoraVideoRequest
	if err := common.UnmarshalBodyReusable(c, &soraReq); err != nil {
		return openai.ErrorWrapper(err, "parse_json_request_failed", http.StatusBadRequest)
	}

	// 设置默认值
	if soraReq.Model == "" {
		soraReq.Model = meta.ActualModelName
	}
	if soraReq.Seconds == "" {
		soraReq.Seconds = "4" // 默认值 - Sora 官方默认 4 秒
	}
	if soraReq.Size == "" {
		soraReq.Size = "720x1280"
	}

	log.Printf("sora-video-request (JSON): model=%s, seconds=%s, size=%s, has_input_reference=%v",
		soraReq.Model, soraReq.Seconds, soraReq.Size, soraReq.InputReference != "")

	// 转换为 form-data 并发送
	return sendRequestAndHandleSoraVideoResponseJSON(c, ctx, meta, &soraReq)
}

// handleSoraRemixRequest 处理 Sora Remix 请求
func handleSoraRemixRequest(c *gin.Context, ctx context.Context, meta *util.RelayMeta) *model.ErrorWithStatusCode {
	// 使用 UnmarshalBodyReusable 以支持 body 多次读取（Distribute 中间件已读取过）
	var remixReq openai.SoraRemixRequest
	if err := common.UnmarshalBodyReusable(c, &remixReq); err != nil {
		return openai.ErrorWrapper(err, "parse_remix_request_failed", http.StatusBadRequest)
	}

	log.Printf("sora-remix-request: model=%s, video_id=%s, prompt=%s", remixReq.Model, remixReq.VideoID, remixReq.Prompt)

	// 根据 video_id 查找原视频任务，获取原渠道信息
	videoTask, err := dbmodel.GetVideoTaskByVideoId(remixReq.VideoID)
	if err != nil {
		return openai.ErrorWrapper(
			fmt.Errorf("video_id not found: %s", remixReq.VideoID),
			"video_not_found",
			http.StatusNotFound,
		)
	}

	// 获取原渠道信息
	originalChannel, err := dbmodel.GetChannelById(videoTask.ChannelId, true)
	if err != nil {
		return openai.ErrorWrapper(err, "get_original_channel_error", http.StatusInternalServerError)
	}

	log.Printf("sora-remix: using original channel_id=%d, channel_name=%s", videoTask.ChannelId, originalChannel.Name)

	// 构建请求 URL
	baseUrl := *originalChannel.BaseURL
	if baseUrl == "" {
		baseUrl = "https://api.openai.com"
	}
	fullRequestUrl := fmt.Sprintf("%s/v1/videos/%s/remix", baseUrl, remixReq.VideoID)

	// 构建请求体（只需要 prompt，去掉 model 和 video_id 参数）
	requestBody := map[string]string{
		"prompt": remixReq.Prompt,
	}
	jsonData, err := json.Marshal(requestBody)

	log.Printf("sora-remix: sending to OpenAI - URL: %s, body: %s (model param removed)", fullRequestUrl, string(jsonData))
	if err != nil {
		return openai.ErrorWrapper(err, "marshal_request_failed", http.StatusInternalServerError)
	}

	// 创建请求
	req, err := http.NewRequest("POST", fullRequestUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}

	// 使用原渠道的 key
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+originalChannel.Key)

	// 发送请求
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	// 读取响应
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	if config.DebugEnabled {
		log.Printf("[DEBUG] Sora remix response: status=%d, body=%s", resp.StatusCode, string(respBody))
	}

	// 解析响应
	var soraResponse openai.SoraVideoResponse
	if err := json.Unmarshal(respBody, &soraResponse); err != nil {
		return openai.ErrorWrapper(err, "parse_remix_response_failed", http.StatusInternalServerError)
	}
	soraResponse.StatusCode = resp.StatusCode

	// 从响应中提取参数进行计费
	modelName := soraResponse.Model
	if modelName == "" {
		modelName = "sora-2" // 默认模型
	}

	secondsStr := soraResponse.Seconds
	if secondsStr == "" {
		secondsStr = "4" // 默认时长 - Sora 官方默认 4 秒
	}

	size := soraResponse.Size
	if size == "" {
		size = "720x1280" // 默认分辨率
	}

	// 计算费用
	quota := calculateSoraQuota(modelName, secondsStr, size)

	// 检查用户余额
	userQuota, err := dbmodel.CacheGetUserQuota(ctx, meta.UserId)
	if err != nil {
		return openai.ErrorWrapper(err, "get_user_quota_error", http.StatusInternalServerError)
	}
	if userQuota-quota < 0 {
		return openai.ErrorWrapper(fmt.Errorf("用户余额不足"), "User balance is not enough", http.StatusBadRequest)
	}

	// 处理响应
	return handleSoraRemixResponse(c, ctx, soraResponse, respBody, meta, modelName, quota, secondsStr, size, remixReq.VideoID)
}

// handleSoraRemixResponse 处理 Sora Remix 响应
func handleSoraRemixResponse(c *gin.Context, ctx context.Context, soraResponse openai.SoraVideoResponse, body []byte, meta *util.RelayMeta, modelName string, quota int64, secondsStr string, size string, originalVideoID string) *model.ErrorWithStatusCode {
	var taskId string
	var taskStatus string
	var message string

	// 检查是否有错误
	if soraResponse.Error != nil {
		// 有错误，不扣费，设置失败状态
		taskStatus = "failed"
		message = fmt.Sprintf("Error: %s (type: %s, code: %s)", soraResponse.Error.Message, soraResponse.Error.Type, soraResponse.Error.Code)
		taskId = soraResponse.ID
		logger.SysError(fmt.Sprintf("Sora remix request failed: %s", message))
	} else if soraResponse.StatusCode == 200 {
		// 成功响应，进行扣费
		err := dbmodel.PostConsumeTokenQuota(meta.TokenId, quota)
		if err != nil {
			logger.SysError("error consuming token quota: " + err.Error())
		}
		err = dbmodel.CacheUpdateUserQuota(ctx, meta.UserId)
		if err != nil {
			logger.SysError("error update user quota cache: " + err.Error())
		}

		// 获取任务ID
		taskId = soraResponse.ID

		// 创建视频日志记录
		videoType := "remix" // Remix 类型
		err = CreateVideoLog("sora", taskId, meta, size, secondsStr, videoType, originalVideoID, quota)
		if err != nil {
			logger.SysError("error creating sora remix video log: " + err.Error())
			return openai.ErrorWrapper(
				fmt.Errorf("error creating video log: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 记录消费日志到logs表
		consumeErr := handleSuccessfulResponseWithQuota(c, ctx, meta, meta.OriginModelName, size, secondsStr, quota, taskId)
		if consumeErr != nil {
			logger.SysError("error recording sora remix video consume log")
			return consumeErr
		}

		// 设置成功状态
		taskStatus = "succeed"
		message = fmt.Sprintf("Video remix request submitted successfully, task_id: %s, remixed_from: %s", taskId, originalVideoID)
	} else {
		// 其他错误状态码
		taskStatus = "failed"
		taskId = soraResponse.ID
		if soraResponse.Error != nil {
			message = fmt.Sprintf("Request failed: %s", soraResponse.Error.Message)
		} else {
			message = fmt.Sprintf("Request failed with status code: %d", soraResponse.StatusCode)
		}
		logger.SysError(fmt.Sprintf("Sora remix request failed: status=%d, body=%s", soraResponse.StatusCode, string(body)))
	}

	// 创建 GeneralVideoResponse 结构体
	generalResponse := model.GeneralVideoResponse{
		TaskId:     taskId,
		Message:    message,
		TaskStatus: taskStatus,
	}

	// 将 GeneralVideoResponse 结构体转换为 JSON
	jsonResponse, err := json.Marshal(generalResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "marshal_response_failed", http.StatusInternalServerError)
	}

	// 发送 JSON 响应给客户端
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(http.StatusOK)
	_, err = c.Writer.Write(jsonResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "write_response_failed", http.StatusInternalServerError)
	}

	return nil
}

func handleAliVideoRequest(c *gin.Context, ctx context.Context, videoRequest model.VideoRequest, meta *util.RelayMeta) *model.ErrorWithStatusCode {
	// 读取原始请求体
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_request_body_failed", http.StatusBadRequest)
	}

	// 恢复请求体
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	log.Printf("ali-video-request-body: %s", string(bodyBytes))

	// 解析请求体获取duration字段
	var requestData map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &requestData); err != nil {
		return openai.ErrorWrapper(err, "parse_request_body_failed", http.StatusBadRequest)
	}

	// 提取duration字段，默认为5
	var duration string = "5"       // 默认值
	var resolution string = "1080P" // 默认值

	if parameters, ok := requestData["parameters"].(map[string]interface{}); ok {
		// 提取duration
		if durationValue, exists := parameters["duration"]; exists {
			switch v := durationValue.(type) {
			case float64:
				duration = strconv.Itoa(int(v))
			case int:
				duration = strconv.Itoa(v)
			case string:
				duration = v
			default:
				duration = "5" // 如果类型不匹配，使用默认值
			}
		}

		// 提取resolution
		if resolutionValue, exists := parameters["resolution"]; exists {
			if res, ok := resolutionValue.(string); ok {
				// 验证resolution是否为有效值
				if res == "480P" || res == "720P" || res == "1080P" {
					resolution = res
				}
			}
		}
	}

	log.Printf("ali-video-duration: %s, resolution: %s", duration, resolution)

	// 直接透传请求体，发送请求并处理响应
	return sendRequestAndHandleAliVideoResponse(c, ctx, bodyBytes, meta, meta.ActualModelName, duration, resolution)
}

func sendRequestAndHandleAliVideoResponse(c *gin.Context, ctx context.Context, bodyBytes []byte, meta *util.RelayMeta, modelName string, duration string, resolution string) *model.ErrorWithStatusCode {
	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	// 根据分辨率和时长进行精确计费
	// 480P: 0.3元/秒, 720P: 0.6元/秒, 1080P: 1元/秒
	var pricePerSecond float64
	switch resolution {
	case "480P":
		pricePerSecond = 0.3
	case "720P":
		pricePerSecond = 0.6
	case "1080P":
		pricePerSecond = 1.0
	default:
		pricePerSecond = 1.0 // 默认按最高价格计费
	}

	// 转换duration为数值
	durationInt, err := strconv.Atoi(duration)
	if err != nil {
		log.Printf("Failed to parse duration %s, using default 5", duration)
		durationInt = 5
	}

	// 计算总费用（人民币）
	prePayCNY := float64(durationInt) * pricePerSecond
	prePayUSD, exchangeErr := convertCNYToUSD(prePayCNY)
	if exchangeErr != nil {
		// 如果汇率转换失败，使用固定汇率7.2作为备选方案
		log.Printf("Failed to get exchange rate for Ali video pre-payment: %v, using fallback rate 7.2", exchangeErr)
		prePayUSD = prePayCNY / 7.2
	}
	quota := int64(prePayUSD * config.QuotaPerUnit)
	log.Printf("Ali video pre-payment: resolution=%s, duration=%s, pricePerSecond=%.1f, totalCNY=%.2f, usd=%.6f, quota=%d",
		resolution, duration, pricePerSecond, prePayCNY, prePayUSD, quota)

	userQuota, err := dbmodel.CacheGetUserQuota(ctx, meta.UserId)
	if err != nil {
		return openai.ErrorWrapper(err, "get_user_quota_error", http.StatusInternalServerError)
	}
	if userQuota-quota < 0 {
		return openai.ErrorWrapper(fmt.Errorf("用户余额不足"), "User balance is not enough", http.StatusBadRequest)
	}

	// 构建请求URL - 根据不同地域选择端点
	baseUrl := meta.BaseURL

	fullRequestUrl := fmt.Sprintf("%s/api/v1/services/aigc/video-generation/video-synthesis", baseUrl)

	// 创建请求
	req, err := http.NewRequest("POST", fullRequestUrl, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+channel.Key)
	req.Header.Set("X-DashScope-Async", "enable") // 启用异步模式

	// 发送请求
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	// 仅在开发环境打印详细响应日志
	if config.DebugEnabled {
		log.Printf("[DEBUG] Ali video response: status=%d, body=%s", resp.StatusCode, string(body))
	}

	// 解析响应
	var aliResponse ali.AliVideoResponse
	if err := json.Unmarshal(body, &aliResponse); err != nil {
		return openai.ErrorWrapper(err, "parse_ali_video_response_failed", http.StatusInternalServerError)
	}

	// 处理响应并统一格式
	return handleAliVideoResponse(c, ctx, aliResponse, body, meta, modelName, quota, duration, resolution)
}

// calculateSoraQuota 计算 Sora 视频的费用
func calculateSoraQuota(modelName string, secondsStr string, size string) int64 {
	var pricePerSecond float64
	isHighRes := size == "1024x1792" || size == "1792x1024"

	if modelName == "sora-2" {
		pricePerSecond = 0.10
	} else if modelName == "sora-2-pro" {
		if isHighRes {
			pricePerSecond = 0.50
		} else {
			pricePerSecond = 0.30
		}
	} else {
		pricePerSecond = 0.10
	}

	// 将 string 转换为 int
	seconds, err := strconv.Atoi(secondsStr)
	if err != nil || seconds == 0 {
		seconds = 4 // 默认值 - Sora 官方默认 4 秒
		log.Printf("Invalid seconds value '%s', using default 4", secondsStr)
	}

	totalPriceUSD := float64(seconds) * pricePerSecond
	quota := int64(totalPriceUSD * config.QuotaPerUnit)

	log.Printf("Sora video pricing: model=%s, seconds=%s (%d), size=%s, pricePerSecond=%.2f, totalUSD=%.6f, quota=%d",
		modelName, secondsStr, seconds, size, pricePerSecond, totalPriceUSD, quota)

	return quota
}

// sendRequestAndHandleSoraVideoResponseFormData 透传 form-data 格式请求
func sendRequestAndHandleSoraVideoResponseFormData(c *gin.Context, ctx context.Context, meta *util.RelayMeta, modelName string, secondsStr string, size string) *model.ErrorWithStatusCode {
	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	// 计算费用
	quota := calculateSoraQuota(modelName, secondsStr, size)

	// 检查用户余额
	userQuota, err := dbmodel.CacheGetUserQuota(ctx, meta.UserId)
	if err != nil {
		return openai.ErrorWrapper(err, "get_user_quota_error", http.StatusInternalServerError)
	}
	if userQuota-quota < 0 {
		return openai.ErrorWrapper(fmt.Errorf("用户余额不足"), "User balance is not enough", http.StatusBadRequest)
	}

	// 构建请求URL
	baseUrl := meta.BaseURL
	fullRequestUrl := fmt.Sprintf("%s/v1/videos", baseUrl) // Sora 官方地址

	// 重新构建 multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// 复制所有表单字段
	for key, values := range c.Request.PostForm {
		for _, value := range values {
			writer.WriteField(key, value)
		}
	}

	// 复制所有文件
	if c.Request.MultipartForm != nil && c.Request.MultipartForm.File != nil {
		for fieldName, files := range c.Request.MultipartForm.File {
			for _, fileHeader := range files {
				file, err := fileHeader.Open()
				if err != nil {
					return openai.ErrorWrapper(err, "open_uploaded_file_failed", http.StatusBadRequest)
				}
				defer file.Close()

				part, err := writer.CreateFormFile(fieldName, fileHeader.Filename)
				if err != nil {
					return openai.ErrorWrapper(err, "create_form_file_failed", http.StatusInternalServerError)
				}
				io.Copy(part, file)
			}
		}
	}
	writer.Close()

	// 创建请求
	req, err := http.NewRequest("POST", fullRequestUrl, body)
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}

	// 设置请求头
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+channel.Key)

	// 发送请求
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	// 读取响应
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	if config.DebugEnabled {
		log.Printf("[DEBUG] Sora video response (form-data): status=%d, body=%s", resp.StatusCode, string(respBody))
	}

	// 解析响应
	var soraResponse openai.SoraVideoResponse
	if err := json.Unmarshal(respBody, &soraResponse); err != nil {
		return openai.ErrorWrapper(err, "parse_sora_video_response_failed", http.StatusInternalServerError)
	}
	soraResponse.StatusCode = resp.StatusCode

	// 处理响应
	return handleSoraVideoResponse(c, ctx, soraResponse, respBody, meta, modelName, quota, secondsStr, size)
}

// sendRequestAndHandleSoraVideoResponseJSON 将 JSON 请求转换为 form-data 并发送
func sendRequestAndHandleSoraVideoResponseJSON(c *gin.Context, ctx context.Context, meta *util.RelayMeta, soraReq *openai.SoraVideoRequest) *model.ErrorWithStatusCode {
	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	// 计算费用
	quota := calculateSoraQuota(soraReq.Model, soraReq.Seconds, soraReq.Size)

	// 检查用户余额
	userQuota, err := dbmodel.CacheGetUserQuota(ctx, meta.UserId)
	if err != nil {
		return openai.ErrorWrapper(err, "get_user_quota_error", http.StatusInternalServerError)
	}
	if userQuota-quota < 0 {
		return openai.ErrorWrapper(fmt.Errorf("用户余额不足"), "User balance is not enough", http.StatusBadRequest)
	}

	// 构建请求URL
	baseUrl := meta.BaseURL
	fullRequestUrl := fmt.Sprintf("%s/v1/videos", baseUrl) // Sora 官方地址

	// 创建 multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// 添加基础字段
	writer.WriteField("model", soraReq.Model)
	writer.WriteField("prompt", soraReq.Prompt)
	if soraReq.Size != "" {
		writer.WriteField("size", soraReq.Size)
	}
	if soraReq.Seconds != "" {
		writer.WriteField("seconds", soraReq.Seconds)
	}
	if soraReq.AspectRatio != "" {
		writer.WriteField("aspect_ratio", soraReq.AspectRatio)
	}
	if soraReq.Loop {
		writer.WriteField("loop", "true")
	}

	// 处理 input_reference
	if soraReq.InputReference != "" {
		err := handleInputReference(writer, soraReq.InputReference)
		if err != nil {
			return openai.ErrorWrapper(err, "handle_input_reference_failed", http.StatusBadRequest)
		}
	}

	writer.Close()

	// 创建请求
	req, err := http.NewRequest("POST", fullRequestUrl, body)
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}

	// 设置请求头
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+channel.Key)

	// 发送请求
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	// 读取响应
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	if config.DebugEnabled {
		log.Printf("[DEBUG] Sora video response (JSON->form): status=%d, body=%s", resp.StatusCode, string(respBody))
	}

	// 解析响应
	var soraResponse openai.SoraVideoResponse
	if err := json.Unmarshal(respBody, &soraResponse); err != nil {
		return openai.ErrorWrapper(err, "parse_sora_video_response_failed", http.StatusInternalServerError)
	}
	soraResponse.StatusCode = resp.StatusCode

	// 处理响应
	return handleSoraVideoResponse(c, ctx, soraResponse, respBody, meta, soraReq.Model, quota, soraReq.Seconds, soraReq.Size)
}

// handleInputReference 处理 input_reference 的不同格式（URL/base64/dataURL）
func handleInputReference(writer *multipart.Writer, inputReference string) error {
	// 检测格式
	if strings.HasPrefix(inputReference, "http://") || strings.HasPrefix(inputReference, "https://") {
		// URL 格式 - 下载并上传
		return handleInputReferenceURL(writer, inputReference)
	} else if strings.HasPrefix(inputReference, "data:") {
		// Data URL 格式 - 解析并上传
		return handleInputReferenceDataURL(writer, inputReference)
	} else {
		// 纯 base64 格式
		return handleInputReferenceBase64(writer, inputReference)
	}
}

// handleInputReferenceURL 处理 URL 格式的 input_reference
func handleInputReferenceURL(writer *multipart.Writer, url string) error {
	// 下载文件
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download input_reference from URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("failed to download input_reference: HTTP %d", resp.StatusCode)
	}

	// 从 URL 中提取文件名
	filename := "input_reference"
	if urlParts := strings.Split(url, "/"); len(urlParts) > 0 {
		filename = urlParts[len(urlParts)-1]
	}

	// 创建表单文件字段
	part, err := writer.CreateFormFile("input_reference", filename)
	if err != nil {
		return fmt.Errorf("failed to create form file: %w", err)
	}

	// 复制文件内容
	_, err = io.Copy(part, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to copy file content: %w", err)
	}

	log.Printf("Input reference URL downloaded: %s", url)
	return nil
}

// handleInputReferenceDataURL 处理 data URL 格式的 input_reference
func handleInputReferenceDataURL(writer *multipart.Writer, dataURL string) error {
	// 解析 data URL: data:image/png;base64,iVBORw0KG...
	parts := strings.SplitN(dataURL, ",", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid data URL format")
	}

	// 提取 MIME type 和编码
	header := parts[0] // data:image/png;base64
	data := parts[1]   // base64 数据

	// 解码 base64
	fileData, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return fmt.Errorf("failed to decode base64 from data URL: %w", err)
	}

	// 提取文件扩展名
	filename := "input_reference"
	if strings.Contains(header, "image/png") {
		filename = "input_reference.png"
	} else if strings.Contains(header, "image/jpeg") || strings.Contains(header, "image/jpg") {
		filename = "input_reference.jpg"
	} else if strings.Contains(header, "image/gif") {
		filename = "input_reference.gif"
	} else if strings.Contains(header, "image/webp") {
		filename = "input_reference.webp"
	}

	// 创建表单文件字段
	part, err := writer.CreateFormFile("input_reference", filename)
	if err != nil {
		return fmt.Errorf("failed to create form file: %w", err)
	}

	// 写入文件数据
	_, err = part.Write(fileData)
	if err != nil {
		return fmt.Errorf("failed to write file data: %w", err)
	}

	log.Printf("Input reference data URL processed: %s", filename)
	return nil
}

// handleInputReferenceBase64 处理纯 base64 格式的 input_reference
func handleInputReferenceBase64(writer *multipart.Writer, base64Data string) error {
	// 解码 base64
	fileData, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return fmt.Errorf("failed to decode base64: %w", err)
	}

	// 创建表单文件字段
	part, err := writer.CreateFormFile("input_reference", "input_reference")
	if err != nil {
		return fmt.Errorf("failed to create form file: %w", err)
	}

	// 写入文件数据
	_, err = part.Write(fileData)
	if err != nil {
		return fmt.Errorf("failed to write file data: %w", err)
	}

	log.Printf("Input reference base64 processed: %d bytes", len(fileData))
	return nil
}

func handleSoraVideoResponse(c *gin.Context, ctx context.Context, soraResponse openai.SoraVideoResponse, body []byte, meta *util.RelayMeta, modelName string, quota int64, secondsStr string, size string) *model.ErrorWithStatusCode {
	var taskId string
	var taskStatus string
	var message string

	// 检查是否有错误
	if soraResponse.Error != nil {
		// 有错误，不扣费，设置失败状态
		taskStatus = "failed"
		message = fmt.Sprintf("Error: %s (type: %s, code: %s)", soraResponse.Error.Message, soraResponse.Error.Type, soraResponse.Error.Code)
		taskId = soraResponse.ID
		logger.SysError(fmt.Sprintf("Sora video request failed: %s", message))
	} else if soraResponse.StatusCode == 200 {
		// 成功响应，进行扣费
		err := dbmodel.PostConsumeTokenQuota(meta.TokenId, quota)
		if err != nil {
			logger.SysError("error consuming token quota: " + err.Error())
		}
		err = dbmodel.CacheUpdateUserQuota(ctx, meta.UserId)
		if err != nil {
			logger.SysError("error update user quota cache: " + err.Error())
		}

		// 获取任务ID
		taskId = soraResponse.ID

		// 创建视频日志记录
		// Sora 是文本生成视频
		videoType := "text-to-video"
		err = CreateVideoLog("sora", taskId, meta, size, secondsStr, videoType, "", quota)
		if err != nil {
			logger.SysError("error creating sora video log: " + err.Error())
			return openai.ErrorWrapper(
				fmt.Errorf("error creating video log: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 记录消费日志到logs表
		consumeErr := handleSuccessfulResponseWithQuota(c, ctx, meta, meta.OriginModelName, size, secondsStr, quota, taskId)
		if consumeErr != nil {
			logger.SysError("error recording sora video consume log")
			return consumeErr
		}

		// 设置成功状态
		taskStatus = "succeed"
		message = fmt.Sprintf("Video generation request submitted successfully, task_id: %s", taskId)
	} else {
		// 其他错误状态码
		taskStatus = "failed"
		taskId = soraResponse.ID
		if soraResponse.Error != nil {
			message = fmt.Sprintf("Request failed: %s", soraResponse.Error.Message)
		} else {
			message = fmt.Sprintf("Request failed with status code: %d", soraResponse.StatusCode)
		}
		logger.SysError(fmt.Sprintf("Sora video request failed: status=%d, body=%s", soraResponse.StatusCode, string(body)))
	}

	// 创建 GeneralVideoResponse 结构体 - 与其他视频处理保持一致
	generalResponse := model.GeneralVideoResponse{
		TaskId:     taskId,
		Message:    message,
		TaskStatus: taskStatus,
	}

	// 将 GeneralVideoResponse 结构体转换为 JSON
	jsonResponse, err := json.Marshal(generalResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "marshal_response_failed", http.StatusInternalServerError)
	}

	// 发送 JSON 响应给客户端
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(http.StatusOK)
	_, err = c.Writer.Write(jsonResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "write_response_failed", http.StatusInternalServerError)
	}

	return nil
}

func handleAliVideoResponse(c *gin.Context, ctx context.Context, aliResponse ali.AliVideoResponse, body []byte, meta *util.RelayMeta, modelName string, quota int64, duration string, resolution string) *model.ErrorWithStatusCode {
	var taskId string
	var taskStatus string
	var message string

	// 检查是否有错误
	if aliResponse.Code != "" {
		// 有错误，不扣费，设置失败状态
		taskStatus = "failed"
		message = fmt.Sprintf("Error: %s, request_id: %s", aliResponse.Message, aliResponse.RequestID)
		// 如果有任务ID，也包含进来
		if aliResponse.Output != nil && aliResponse.Output.TaskID != "" {
			taskId = aliResponse.Output.TaskID
		}
		logger.SysError(fmt.Sprintf("Ali video request failed: %s, request_id: %s", aliResponse.Message, aliResponse.RequestID))
	} else {
		// 没有错误，进行扣费
		err := dbmodel.PostConsumeTokenQuota(meta.TokenId, quota)
		if err != nil {
			logger.SysError("error consuming token quota: " + err.Error())
		}
		err = dbmodel.CacheUpdateUserQuota(ctx, meta.UserId)
		if err != nil {
			logger.SysError("error update user quota cache: " + err.Error())
		}

		// 处理阿里云响应
		if aliResponse.Output != nil {
			taskId = aliResponse.Output.TaskID
		}

		// 创建视频日志记录
		// 根据模型名称确定视频类型
		videoType := "image-to-video"
		if strings.Contains(strings.ToLower(modelName), "t2v") {
			videoType = "text-to-video"
		}
		err = CreateVideoLog("ali", taskId, meta, resolution, duration, videoType, "", quota)
		if err != nil {
			logger.SysError("error creating ali video log: " + err.Error())
			return openai.ErrorWrapper(
				fmt.Errorf("error creating video log: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 记录消费日志到logs表
		consumeErr := handleSuccessfulResponseWithQuota(c, ctx, meta, meta.OriginModelName, resolution, duration, quota, taskId)
		if consumeErr != nil {
			logger.SysError("error recording ali video consume log")
			return consumeErr
		}

		// 设置成功状态
		taskStatus = "succeed"
		message = fmt.Sprintf("Request submitted successfully, request_id: %s", aliResponse.RequestID)
	}

	// 创建 GeneralVideoResponse 结构体 - 与其他视频处理保持一致
	generalResponse := model.GeneralVideoResponse{
		TaskId:     taskId,
		Message:    message,
		TaskStatus: taskStatus,
	}

	// 将 GeneralVideoResponse 结构体转换为 JSON
	jsonResponse, err := json.Marshal(generalResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "marshal_response_failed", http.StatusInternalServerError)
	}

	// 发送 JSON 响应给客户端
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(http.StatusOK)
	_, err = c.Writer.Write(jsonResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "write_response_failed", http.StatusInternalServerError)
	}

	return nil
}

func handleDoubaoVideoRequest(c *gin.Context, ctx context.Context, videoRequest model.VideoRequest, meta *util.RelayMeta) *model.ErrorWithStatusCode {

	// 读取原始请求体
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_request_body_failed", http.StatusBadRequest)
	}

	// 恢复请求体
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	// 解析为豆包请求格式
	var doubaoRequest doubao.DoubaoVideoRequest
	if err := json.Unmarshal(bodyBytes, &doubaoRequest); err != nil {
		return openai.ErrorWrapper(err, "parse_doubao_request_failed", http.StatusBadRequest)
	}
	log.Printf("doubao-request-data: %+v", doubaoRequest)
	log.Printf("doubao-model-name: %s", doubaoRequest.Model)

	// 验证必填参数
	if doubaoRequest.Model == "" {
		return openai.ErrorWrapper(
			fmt.Errorf("model is required"),
			"invalid_request_error",
			http.StatusBadRequest,
		)
	}

	if len(doubaoRequest.Content) == 0 {
		return openai.ErrorWrapper(
			fmt.Errorf("content is required"),
			"invalid_request_error",
			http.StatusBadRequest,
		)
	}
	//     doubaoRequest.CallbackURL = config.ServerAddress + "/api/v3/contents/generations/tasks/" + doubaoRequest.ID
	// 构建请求URL - 匹配豆包实际API端点
	baseUrl := meta.BaseURL
	fullRequestUrl := baseUrl + "/api/v3/contents/generations/tasks"
	log.Printf("fullRequestUrl: %s", fullRequestUrl)
	// 序列化请求
	jsonData, err := json.Marshal(doubaoRequest)

	if err != nil {
		return openai.ErrorWrapper(err, "marshal_request_failed", http.StatusInternalServerError)
	}

	// 发送请求并处理响应
	return sendRequestDoubaoAndHandleResponse(c, ctx, fullRequestUrl, jsonData, meta, doubaoRequest.Model)
}
func sendRequestDoubaoAndHandleResponse(c *gin.Context, ctx context.Context, fullRequestUrl string, jsonData []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {
	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}
	//预扣费（人民币转美元后再计算quota）
	// 预扣人民币1.4元（大概相当于0.2美元）
	prePayCNY := 1.4
	prePayUSD, exchangeErr := convertCNYToUSD(prePayCNY)
	if exchangeErr != nil {
		// 如果汇率转换失败，使用固定汇率7.2作为备选方案
		log.Printf("Failed to get exchange rate for Doubao pre-payment: %v, using fallback rate 7.2", exchangeErr)
		prePayUSD = prePayCNY / 7.2
	}
	quota := int64(prePayUSD * config.QuotaPerUnit)
	log.Printf("Doubao pre-payment: cny=%.2f, usd=%.6f, quota=%d", prePayCNY, prePayUSD, quota)
	userQuota, err := dbmodel.CacheGetUserQuota(ctx, meta.UserId)
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}
	if userQuota-quota < 0 {
		return openai.ErrorWrapper(fmt.Errorf("用户余额不足"), "User balance is not enough", http.StatusBadRequest)
	}
	// 创建请求
	req, err := http.NewRequest("POST", fullRequestUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+channel.Key)
	// 发送请求
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	// 打印豆包完整的响应日志
	log.Printf("doubao-full-response-body: %s", string(body))
	log.Printf("doubao-response-status-code: %d", resp.StatusCode)
	log.Printf("doubao-response-headers: %v", resp.Header)

	// 解析响应
	var doubaoResponse doubao.DoubaoVideoResponse
	if err := json.Unmarshal(body, &doubaoResponse); err != nil {
		return openai.ErrorWrapper(err, "parse_response_error", http.StatusInternalServerError)
	}
	log.Printf("doubao-response-json-data: %v", doubaoResponse)
	doubaoResponse.StatusCode = resp.StatusCode
	return handleDoubaoVideoResponse(c, ctx, doubaoResponse, body, meta, modelName, quota)
}
func handleDoubaoVideoResponse(c *gin.Context, ctx context.Context, doubaoResponse doubao.DoubaoVideoResponse, body []byte, meta *util.RelayMeta, modelName string, quota int64) *model.ErrorWithStatusCode {
	switch doubaoResponse.StatusCode {
	case 200:
		// 解析模型参数来确定视频参数
		// duration := "5"         // 默认5秒
		// mode := "text-to-video" // 默认模式

		//		// 先计算quota - 修复函数调用，改为基于视频规格的计费
		//quota := calculateQuotaForDoubaoVideo(meta, modelName, mode, duration, c)

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralVideoResponse{
			TaskId:     doubaoResponse.ID,
			Message:    "",
			TaskStatus: "succeed",
		}
		// 序列化响应
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("Error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}
		// 创建视频日志 - 使用计算出的quota而不是0
		err = CreateVideoLog("doubao", doubaoResponse.ID, meta, "", "", "", "", quota)
		if err != nil {
			logger.Warnf(ctx, "Failed to create video log: %v", err)
			return openai.ErrorWrapper(
				fmt.Errorf("Error create video log: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}
		// 使用带videoTaskId的日志记录函数
		handleSuccessfulResponseWithQuota(c, ctx, meta, modelName, "", "", quota, doubaoResponse.ID)
		// 发送响应
		c.Data(http.StatusOK, "application/json", jsonResponse)
		return nil
	default:
		// 处理错误情况，直接使用豆包返回的错误信息
		errorMsg := "豆包API错误"
		if doubaoResponse.Error != nil && doubaoResponse.Error.Message != "" {
			errorMsg = doubaoResponse.Error.Message
		}
		return openai.ErrorWrapper(
			fmt.Errorf("%s", errorMsg),
			"api_error",
			doubaoResponse.StatusCode,
		)
	}
}

func handleVeoVideoRequest(c *gin.Context, ctx context.Context, videoRequest model.VideoRequest, meta *util.RelayMeta) *model.ErrorWithStatusCode {

	var fullRequestUrl string
	region := meta.Config.Region

	// 获取正确的项目ID - 支持多密钥模式
	// 创建VertexAI适配器实例获取项目ID
	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	credentials, err := vertexai.GetCredentialsFromConfig(meta.Config, channel)
	if err != nil {
		return openai.ErrorWrapper(err, "invalid_credentials", http.StatusInternalServerError)
	}

	projectID := credentials.ProjectID
	if projectID == "" {
		return openai.ErrorWrapper(fmt.Errorf("无法获取Vertex AI项目ID，请检查Key字段中的JSON凭证"), "invalid_project_id", http.StatusBadRequest)
	}

	if region == "global" {
		fullRequestUrl = fmt.Sprintf("https://aiplatform.googleapis.com/v1/projects/%s/locations/global/publishers/google/models/%s:predictLongRunning", projectID, meta.OriginModelName)
	} else {
		fullRequestUrl = fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s:predictLongRunning", region, projectID, region, meta.OriginModelName)
	}

	log.Printf("veo-full-request-url: %s", fullRequestUrl)

	// 读取原始请求体
	var reqBody map[string]interface{}
	if err := common.UnmarshalBodyReusable(c, &reqBody); err != nil {
		// 提供更详细的JSON格式错误信息
		log.Printf("[VEO] JSON解析失败: %v", err)
		if strings.Contains(err.Error(), "invalid character") {
			return openai.ErrorWrapper(
				fmt.Errorf("JSON格式错误: %v。请检查请求体中是否有多余的逗号或其他语法错误", err),
				"invalid_json_format",
				http.StatusBadRequest,
			)
		}
		return openai.ErrorWrapper(err, "invalid_request_body", http.StatusBadRequest)
	}

	// 验证instances数组存在（符合Google Vertex AI VEO格式）
	instances, ok := reqBody["instances"]
	if !ok {
		return openai.ErrorWrapper(
			fmt.Errorf("缺少instances数组。VEO模型需要使用Google Vertex AI标准格式：{\"instances\": [...], \"parameters\": {...}}"),
			"missing_instances",
			http.StatusBadRequest,
		)
	}

	// 验证instances是数组
	if _, ok := instances.([]interface{}); !ok {
		return openai.ErrorWrapper(
			fmt.Errorf("instances必须是数组格式"),
			"invalid_instances_format",
			http.StatusBadRequest,
		)
	}

	// 删除model参数（如果存在）
	delete(reqBody, "model")

	// 检查parameters字段
	params, ok := reqBody["parameters"].(map[string]interface{})
	if !ok {
		params = make(map[string]interface{})
	}

	// 处理generateAudio，默认为true
	generateAudio := true
	if val, ok := params["generateAudio"]; ok {
		if boolVal, ok := val.(bool); ok {
			generateAudio = boolVal
		}
	}
	c.Set("generateAudio", generateAudio)

	// sampleCount 参数已支持，不再删除
	// if _, ok := params["sampleCount"]; ok { //暂时处理只支持一个视频结果
	//	delete(params, "sampleCount")
	// }

	// 处理durationSeconds
	duration := 8
	if v, ok := params["durationSeconds"]; ok {
		// 允许int/float64/string三种类型
		switch val := v.(type) {
		case float64:
			duration = int(val)
		case int:
			duration = val
		case string:
			if d, err := strconv.Atoi(val); err == nil {
				duration = d
			}
		}
	} else {
		params["durationSeconds"] = duration
	}
	c.Set("durationSeconds", duration)

	// 更新parameters
	reqBody["parameters"] = params

	// 重新序列化
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return openai.ErrorWrapper(err, "json_marshal_error", http.StatusInternalServerError)
	}

	// 添加请求详细日志
	log.Printf("[VEO] Request URL: %s", fullRequestUrl)

	// 处理请求体日志，避免过长的base64内容
	requestBodyStr := string(jsonData)
	if len(requestBodyStr) > 2000 {
		// 如果请求体过长，截取前后部分
		log.Printf("[VEO] Request Body (truncated - too long): %s...%s",
			requestBodyStr[:1000],
			requestBodyStr[len(requestBodyStr)-1000:])
		log.Printf("[VEO] Request Body Length: %d characters", len(requestBodyStr))
	} else {
		log.Printf("[VEO] Request Body: %s", requestBodyStr)
	}

	// 发送请求并处理响应
	return sendRequestAndHandleVeoResponse(c, ctx, fullRequestUrl, jsonData, meta, meta.OriginModelName)
}

func sendRequestAndHandleVeoResponse(c *gin.Context, ctx context.Context, fullRequestUrl string, jsonData []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {
	// 预扣费检查 - 预扣6.0，后续处理完多退少补
	quota := int64(6.0 * config.QuotaPerUnit)
	userQuota, err := dbmodel.CacheGetUserQuota(ctx, meta.UserId)
	if err != nil {
		return openai.ErrorWrapper(err, "get_user_quota_error", http.StatusInternalServerError)
	}
	if userQuota-quota < 0 {
		return openai.ErrorWrapper(fmt.Errorf("余额不足：Veo3模型价格较高，需要预扣费约$6.0，请充值后重试"), "Insufficient balance: Veo3 model requires approximately $6.0 pre-payment, please recharge and try again", http.StatusBadRequest)
	}

	// 创建VertexAI适配器实例 - 支持新的Key字段存储方式
	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	credentials, err := vertexai.GetCredentialsFromConfig(meta.Config, channel)
	if err != nil {
		return openai.ErrorWrapper(err, "invalid_credentials", http.StatusInternalServerError)
	}

	adaptor := &vertexai.Adaptor{
		AccountCredentials: *credentials,
	}

	// 获取访问令牌
	accessToken, err := vertexai.GetAccessToken(adaptor, meta)
	if err != nil {
		return openai.ErrorWrapper(err, "get_access_token_error", http.StatusInternalServerError)
	}

	// 创建HTTP请求
	req, err := http.NewRequest("POST", fullRequestUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	// 发送请求
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	// 读取响应体
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	// 添加详细的响应日志
	log.Printf("[VEO] Response Status: %d", resp.StatusCode)
	log.Printf("[VEO] Response Headers: %+v", resp.Header)

	// 处理响应体日志，避免过长的base64内容
	responseBodyStr := string(body)
	if len(responseBodyStr) > 1000 {
		// 如果响应体过长，截取前后部分
		log.Printf("[VEO] Response Body (truncated - too long): %s...%s",
			responseBodyStr[:500],
			responseBodyStr[len(responseBodyStr)-500:])
		log.Printf("[VEO] Response Body Length: %d characters", len(responseBodyStr))
	} else {
		log.Printf("[VEO] Response Body: %s", responseBodyStr)
	}

	// 解析响应
	var veoResponse map[string]interface{}
	err = json.Unmarshal(body, &veoResponse)
	if err != nil {
		log.Printf("[VEO] Failed to parse response JSON: %v", err)
		return openai.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}

	// 处理响应
	return handleVeoVideoResponse(c, ctx, veoResponse, body, meta, modelName, resp.StatusCode)
}

func handleVeoVideoResponse(c *gin.Context, ctx context.Context, veoResponse map[string]interface{}, body []byte, meta *util.RelayMeta, modelName string, statusCode int) *model.ErrorWithStatusCode {
	if statusCode == 200 {
		// 从响应中提取任务ID或操作名称
		var taskId string
		if name, ok := veoResponse["name"].(string); ok {
			// 只取操作ID部分，不暴露项目信息
			parts := strings.Split(name, "/")
			if len(parts) > 0 {
				taskId = parts[len(parts)-1] // 取最后一部分作为taskId
			} else {
				taskId = name
			}
		}

		generateAudio, _ := c.Get("generateAudio")
		durationSeconds := c.GetInt("durationSeconds")

		// 根据generateAudio设置videoMode
		var videoMode string
		if audioEnabled, ok := generateAudio.(bool); ok && audioEnabled {
			videoMode = "AudioVideo"
		} else {
			videoMode = "NoAudioVideo"
		}

		// 计算配额 - 这里需要根据generateAudio和durationSeconds来计算
		quota := calculateVeoQuota(meta, modelName, generateAudio, durationSeconds)

		// 创建视频日志
		err := CreateVideoLog("vertexai", taskId, meta, videoMode, strconv.Itoa(durationSeconds), "", "", quota)
		if err != nil {
			logger.Warnf(ctx, "Failed to create video log: %v", err)
			return openai.ErrorWrapper(
				fmt.Errorf("Error create video log: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 创建通用响应
		generalResponse := model.GeneralVideoResponse{
			TaskId:     taskId,
			Message:    "Request submitted successfully",
			TaskStatus: "succeed",
		}

		// 序列化响应
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("Error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 使用带videoTaskId的日志记录函数 - 确保使用正确的模型名称
		handleSuccessfulResponseWithQuota(c, ctx, meta, meta.OriginModelName, "", strconv.Itoa(durationSeconds), quota, taskId)

		// 发送响应
		c.Data(http.StatusOK, "application/json", jsonResponse)
		return nil
	} else {
		// 处理错误响应 - 添加详细的错误信息解析
		errorMsg := "Unknown error"
		errorCode := "api_error"
		var errorDetails map[string]interface{}

		// 解析错误信息 - 改进的错误提取逻辑
		if msg, ok := veoResponse["error"].(map[string]interface{}); ok {
			errorDetails = msg

			// 提取错误消息
			if message, ok := msg["message"].(string); ok {
				errorMsg = message
			}

			// 提取错误代码 - 支持数字和字符串类型
			if code, ok := msg["code"].(float64); ok {
				errorCode = fmt.Sprintf("%.0f", code) // 转换为字符串
			} else if code, ok := msg["code"].(string); ok {
				errorCode = code
			} else if code, ok := msg["code"].(int); ok {
				errorCode = strconv.Itoa(code)
			}

			// 提取错误状态
			var errorStatus string
			if status, ok := msg["status"].(string); ok {
				errorStatus = status
			}

			// 提取详细错误信息
			var errorReason, errorDomain string
			if details, ok := msg["details"].([]interface{}); ok && len(details) > 0 {
				if detail, ok := details[0].(map[string]interface{}); ok {
					if reason, ok := detail["reason"].(string); ok {
						errorReason = reason
					}
					if domain, ok := detail["domain"].(string); ok {
						errorDomain = domain
					}
				}
			}

			// 记录详细的错误信息
			log.Printf("[VEO] Error Status: %s", errorStatus)
			log.Printf("[VEO] Error Reason: %s", errorReason)
			log.Printf("[VEO] Error Domain: %s", errorDomain)
		}

		// 打印详细的错误信息
		log.Printf("[VEO] ===== 非200错误响应详情 =====")
		log.Printf("[VEO] HTTP Status Code: %d", statusCode)
		log.Printf("[VEO] Error Details: %+v", errorDetails)
		log.Printf("[VEO] Raw Error Message: %s", errorMsg)
		log.Printf("[VEO] Raw Error Code: %s", errorCode)

		// 处理响应体日志，避免过长的base64内容
		responseBodyStr := string(body)
		if len(responseBodyStr) > 1000 {
			// 如果响应体过长，截取前后部分
			log.Printf("[VEO] Full Response Body (truncated - too long): %s...%s",
				responseBodyStr[:500],
				responseBodyStr[len(responseBodyStr)-500:])
			log.Printf("[VEO] Response Body Length: %d characters", len(responseBodyStr))
		} else {
			log.Printf("[VEO] Full Response Body: %s", responseBodyStr)
		}
		log.Printf("[VEO] ===== 错误响应详情结束 =====")

		// 简化错误消息处理
		detailedErrorMsg := fmt.Sprintf("VEO API错误: %s", errorMsg)

		return openai.ErrorWrapper(
			fmt.Errorf(detailedErrorMsg),
			errorCode,
			statusCode,
		)
	}
}

// 计算Veo配额的函数
func calculateVeoQuota(meta *util.RelayMeta, modelName string, generateAudio interface{}, durationSeconds int) int64 {
	// 基础价格：根据模型类型设置
	var basePrice float64
	switch modelName {
	case "veo-3.0-generate-preview":
		basePrice = 0.75 // Veo 3版本带音频的价格
	case "veo-3.0-fast-generate-preview":
		basePrice = 0.40 // Veo 3 Fast版本带音频的价格
	case "veo-2.0-generate-001":
		basePrice = 0.50 // Veo 2版本价格
	default:
		basePrice = 0.50
	}

	// 如果有音频生成，价格可能不同
	if generateAudio != nil {
		if hasAudio, ok := generateAudio.(bool); ok && hasAudio {
			// 如果是veo-3.0-generate-preview模型且生成音频，使用带音频价格
			if modelName == "veo-3.0-generate-preview" {
				basePrice = 0.75
			} else if modelName == "veo-3.0-fast-generate-preview" {
				basePrice = 0.40
			}
		} else {
			// 如果是veo-3.0但不生成音频，使用不带音频价格
			if modelName == "veo-3.0-generate-preview" {
				basePrice = 0.50
			} else if modelName == "veo-3.0-fast-generate-preview" {
				basePrice = 0.25
			}
		}
	}

	// 按秒计费，直接按实际秒数计算
	finalPrice := basePrice * float64(durationSeconds)
	// quota := int64(finalPrice * config.QuotaPerUnit * meta.ChannelRatio * meta.UserChannelTypeRatio)
	quota := int64(finalPrice * config.QuotaPerUnit)

	return quota
}

func handlePixverseVideoRequest(c *gin.Context, ctx context.Context, videoRequest model.VideoRequest, meta *util.RelayMeta) *model.ErrorWithStatusCode {
	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	var fullRequestUrl string
	var jsonData []byte

	// 1. 读取原始请求体
	jsonData, err = io.ReadAll(c.Request.Body)
	if err != nil {
		log.Printf("Error reading request body: %v", err)
		return openai.ErrorWrapper(err, "read_request_error", http.StatusBadRequest)
	}
	// 重新设置请求体
	c.Request.Body = io.NopCloser(bytes.NewBuffer(jsonData))

	var imageCheck struct {
		Image      string      `json:"image"`
		Duration   interface{} `json:"duration"`
		Quality    string      `json:"quality"`
		MotionMode string      `json:"motion_mode"`
	}

	if err := common.UnmarshalBodyReusable(c, &imageCheck); err != nil {
		return openai.ErrorWrapper(err, "invalid_request_body", http.StatusBadRequest)
	}

	// Convert duration to int
	var duration int
	switch v := imageCheck.Duration.(type) {
	case float64:
		duration = int(v)
	case string:
		var err error
		duration, err = strconv.Atoi(v)
		if err != nil {
			return openai.ErrorWrapper(err, "invalid_duration_format", http.StatusBadRequest)
		}
	case int:
		duration = v
	default:
		return openai.ErrorWrapper(fmt.Errorf("unsupported duration type"), "invalid_duration_type", http.StatusBadRequest)
	}

	c.Set("Duration", duration)
	c.Set("Quality", imageCheck.Quality)
	c.Set("MotionMode", imageCheck.MotionMode)

	if imageCheck.Image != "" {
		// 1. 先上传图片
		uploadUrl := meta.BaseURL + "/openapi/v2/image/upload"

		// 创建multipart表单
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)

		// 创建文件表单字段
		part, err := writer.CreateFormFile("image", "image.png")
		if err != nil {
			return openai.ErrorWrapper(err, "failed_to_create_form", http.StatusInternalServerError)
		}

		// 检查是否为base64格式
		isBase64 := strings.HasPrefix(imageCheck.Image, "data:")

		if isBase64 {
			// 处理base64格式
			// 移除 "data:image/jpeg;base64," 这样的前缀
			base64Data := imageCheck.Image
			if i := strings.Index(base64Data, ","); i != -1 {
				base64Data = base64Data[i+1:]
			}

			// 解码base64数据
			imgData, err := base64.StdEncoding.DecodeString(base64Data)
			if err != nil {
				return openai.ErrorWrapper(err, "invalid_base64_image", http.StatusBadRequest)
			}

			// 写入图片数据
			if _, err = part.Write(imgData); err != nil {
				return openai.ErrorWrapper(err, "failed_to_write_image", http.StatusInternalServerError)
			}
		} else {
			// 处理URL格式
			// 检查是否是有效的URL
			if !strings.HasPrefix(imageCheck.Image, "http://") && !strings.HasPrefix(imageCheck.Image, "https://") {
				return openai.ErrorWrapper(fmt.Errorf("invalid URL format"), "invalid_url", http.StatusBadRequest)
			}

			resp, err := http.Get(imageCheck.Image)
			if err != nil {
				return openai.ErrorWrapper(err, "failed_to_download_image", http.StatusBadRequest)
			}
			defer resp.Body.Close()

			// 复制图片数据到表单
			if _, err = io.Copy(part, resp.Body); err != nil {
				return openai.ErrorWrapper(err, "failed_to_copy_image", http.StatusInternalServerError)
			}
		}

		writer.Close()

		// 创建上传请求
		uploadReq, err := http.NewRequest("POST", uploadUrl, body)
		if err != nil {
			return openai.ErrorWrapper(err, "failed_to_create_request", http.StatusInternalServerError)
		}

		log.Printf("key:%s", channel.Key)
		// 设置请求头
		uploadReq.Header.Set("Content-Type", writer.FormDataContentType())
		uploadReq.Header.Set("API-KEY", channel.Key)
		uploadReq.Header.Set("AI-trace-id", helper.GetUUID())

		// 发送请求
		client := &http.Client{}
		uploadResp, err := client.Do(uploadReq)
		if err != nil {
			return openai.ErrorWrapper(err, "failed_to_upload_image", http.StatusInternalServerError)
		}
		defer uploadResp.Body.Close()

		// 解析响应
		var uploadResponse pixverse.UploadImageResponse
		if err := json.NewDecoder(uploadResp.Body).Decode(&uploadResponse); err != nil {
			return openai.ErrorWrapper(err, "failed_to_parse_upload_response", http.StatusInternalServerError)
		}

		// 检查上传是否成功
		if uploadResponse.ErrCode != 0 {
			return openai.ErrorWrapper(
				fmt.Errorf("image upload failed: %s", uploadResponse.ErrMsg),
				"image_upload_failed",
				http.StatusBadRequest,
			)
		}

		// 2. 使用返回的图片ID构建视频生成请求
		fullRequestUrl = meta.BaseURL + "/openapi/v2/video/img/generate"

		// 将原始请求体中的img_url替换为img_id
		var originalBody pixverse.PixverseRequest2
		if err := common.UnmarshalBodyReusable(c, &originalBody); err != nil {
			return openai.ErrorWrapper(err, "invalid_request_body", http.StatusBadRequest)
		}

		// Convert duration to int in originalBody
		switch v := originalBody.Duration.(type) {
		case float64:
			originalBody.Duration = int(v)
		case string:
			duration, err := strconv.Atoi(v)
			if err != nil {
				return openai.ErrorWrapper(err, "invalid_duration_format", http.StatusBadRequest)
			}
			originalBody.Duration = duration
		case int:
			// already in correct format
		default:
			return openai.ErrorWrapper(fmt.Errorf("unsupported duration type"), "invalid_duration_type", http.StatusBadRequest)
		}

		originalBody.ImgId = uploadResponse.Resp.ImgId
		originalBody.Image = ""

		// 将修改后的请求体重新设置到context中，同时更新jsonData
		jsonData, err = json.Marshal(originalBody)
		if err != nil {
			return openai.ErrorWrapper(err, "failed_to_marshal_request", http.StatusInternalServerError)
		}
		c.Request.Body = io.NopCloser(bytes.NewBuffer(jsonData))
	} else {
		// 处理 PixverseRequest1 的情况
		var textRequest pixverse.PixverseRequest1
		if err := common.UnmarshalBodyReusable(c, &textRequest); err != nil {
			return openai.ErrorWrapper(err, "invalid_request_body", http.StatusBadRequest)
		}
		// Convert duration to int in textRequest
		switch v := textRequest.Duration.(type) {
		case float64:
			textRequest.Duration = int(v)
		case string:
			duration, err := strconv.Atoi(v)
			if err != nil {
				return openai.ErrorWrapper(err, "invalid_duration_format", http.StatusBadRequest)
			}
			textRequest.Duration = duration
		case int:
			// already in correct format
		default:
			return openai.ErrorWrapper(fmt.Errorf("unsupported duration type"), "invalid_duration_type", http.StatusBadRequest)
		}

		jsonData, err = json.Marshal(textRequest)
		if err != nil {
			return openai.ErrorWrapper(err, "failed_to_marshal_request", http.StatusInternalServerError)
		}
		c.Request.Body = io.NopCloser(bytes.NewBuffer(jsonData))

		fullRequestUrl = meta.BaseURL + "/openapi/v2/video/text/generate"
	}
	return sendRequestAndHandlePixverseResponse(c, ctx, fullRequestUrl, jsonData, meta, "pixverse")
}

func sendRequestAndHandlePixverseResponse(c *gin.Context, ctx context.Context, fullRequestUrl string, jsonData []byte, meta *util.RelayMeta, s string) *model.ErrorWithStatusCode {
	// 预扣费检查 - 预扣0.2，后续处理完多退少补
	quota := int64(0.2 * config.QuotaPerUnit)
	userQuota, err := dbmodel.CacheGetUserQuota(ctx, meta.UserId)
	if err != nil {
		return openai.ErrorWrapper(err, "get_user_quota_error", http.StatusInternalServerError)
	}
	if userQuota-quota < 0 {
		return openai.ErrorWrapper(fmt.Errorf("用户余额不足"), "User balance is not enough", http.StatusBadRequest)
	}

	// // 添加请求体日志
	// log.Printf("Request URL: %s", fullRequestUrl)
	// log.Printf("Request Body: %s", string(jsonData))

	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		log.Printf("Get channel error: %v", err)
		return openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	req, err := http.NewRequest("POST", fullRequestUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Create request error: %v", err)
		return openai.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}

	// 添加请求头日志
	req.Header.Set("Ai-trace-id", helper.GetUUID())
	req.Header.Set("API-KEY", channel.Key)
	req.Header.Set("Content-Type", "application/json") // 添加 Content-Type 头
	// log.Printf("Request Headers: %v", req.Header)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Request error: %v", err)
		return openai.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Read response error: %v", err)
		return openai.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	// // 添加响应日志
	// log.Printf("Response Status: %d", resp.StatusCode)
	// log.Printf("Response Body: %s", string(body))

	var PixverseFinalResp pixverse.PixverseVideoResponse
	err = json.Unmarshal(body, &PixverseFinalResp)
	if err != nil {
		log.Printf("Response parse error: %v", err)
		return openai.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}

	PixverseFinalResp.StatusCode = resp.StatusCode
	return handlePixverseVideoResponse(c, ctx, PixverseFinalResp, body, meta, "")
}

func handlePixverseVideoResponse(c *gin.Context, ctx context.Context, videoResponse pixverse.PixverseVideoResponse, body []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {
	duration := c.GetInt("Duration")
	quality := c.GetString("Quality")
	motionMode := c.GetString("MotionMode")
	if videoResponse.ErrCode == 0 && videoResponse.StatusCode == 200 {
		// 先计算quota
		quota := calculateQuota(meta, "v3.5", "", strconv.Itoa(duration), c)

		err := CreateVideoLog("pixverse", strconv.Itoa(videoResponse.Resp.VideoId), meta,
			quality,
			strconv.Itoa(duration),
			motionMode,
			"",
			quota,
		)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("API error: %s", err),
				"api_error",
				http.StatusBadRequest,
			)
		}

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralVideoResponse{
			TaskId:     strconv.Itoa(videoResponse.Resp.VideoId),
			Message:    videoResponse.ErrMsg,
			TaskStatus: "succeed",
		}

		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("Error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 发送 JSON 响应给客户端
		c.Data(http.StatusOK, "application/json", jsonResponse)

		return handleSuccessfulResponseWithQuota(c, ctx, meta, modelName, "", "", quota)

	} else {
		return openai.ErrorWrapper(
			fmt.Errorf("error: %s", videoResponse.ErrMsg),
			"internal_error",
			http.StatusInternalServerError,
		)
	}
}

func handleViggleVideoRequest(c *gin.Context, ctx context.Context, videoRequest model.VideoRequest, meta *util.RelayMeta) *model.ErrorWithStatusCode {
	// 使用map定义URL映射关系
	urlMap := map[string]string{
		"mix":   "/api/video/gen",
		"multi": "/api/video/gen/multi",
		"move":  "/api/video/gen/move",
	}

	// 获取type参数，默认为"mix"
	typeValue := c.DefaultPostForm("type", "mix")

	// 获取对应的URL路径
	path, exists := urlMap[typeValue]
	if !exists {
		return openai.ErrorWrapper(errors.New("invalid type"), "invalid_type", http.StatusBadRequest)
	}

	fullRequestUrl := meta.BaseURL + path

	// 直接转发原始请求
	return sendRequestAndHandleViggleResponse(c, ctx, fullRequestUrl, meta, "viggle")
}

func sendRequestAndHandleViggleResponse(c *gin.Context, ctx context.Context, fullRequestUrl string, meta *util.RelayMeta, s string) *model.ErrorWithStatusCode {
	// 预扣费检查 - 预扣0.2，后续处理完多退少补
	quota := int64(0.2 * config.QuotaPerUnit)
	userQuota, err := dbmodel.CacheGetUserQuota(ctx, meta.UserId)
	if err != nil {
		return openai.ErrorWrapper(err, "get_user_quota_error", http.StatusInternalServerError)
	}
	if userQuota-quota < 0 {
		return openai.ErrorWrapper(fmt.Errorf("用户余额不足"), "User balance is not enough", http.StatusBadRequest)
	}

	// 先读取请求体
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_request_error", http.StatusInternalServerError)
	}

	// 打印完整请求体
	// log.Printf("Original request body: %s", string(bodyBytes))

	// 重新设置请求体，因为读取后需要重置
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	// 创建新请求时使用保存的请求体
	req, err := http.NewRequest(c.Request.Method, fullRequestUrl, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}

	// // 打印请求的详细信息
	// log.Printf("Request Method: %s", req.Method)
	// log.Printf("Request URL: %s", fullRequestUrl)
	// log.Printf("Request Headers: %+v", req.Header)

	// 复制原始请求头
	req.Header.Set("Access-Token", meta.APIKey)
	// 确保设置正确的 Content-Type
	req.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))

	// 发送请求
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	// 读取响应体
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	log.Printf("Raw response body: %s", string(respBody))

	// 解析响应
	var viggleResponse viggle.ViggleResponse
	if err := json.Unmarshal(respBody, &viggleResponse); err != nil {
		return openai.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}

	viggleResponse.StatusCode = resp.StatusCode
	return handleViggleVideoResponse(c, ctx, viggleResponse, respBody, meta, "")
}

func handleViggleVideoResponse(c *gin.Context, ctx context.Context, viggleResponse viggle.ViggleResponse, body []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {
	if viggleResponse.Code == 0 && viggleResponse.Message == "成功" {
		// 先计算quota
		quota := calculateQuota(meta, "viggle", "", strconv.Itoa(viggleResponse.Data.SubtractScore), c)

		err := CreateVideoLog("viggle", viggleResponse.Data.TaskID, meta, "", strconv.Itoa(viggleResponse.Data.SubtractScore), "", "", quota)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("API error: %s", err),
				"api_error",
				http.StatusBadRequest,
			)
		}

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralVideoResponse{
			TaskId:     viggleResponse.Data.TaskID,
			Message:    viggleResponse.Message,
			TaskStatus: "succeed",
		}

		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("Error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 发送 JSON 响应给客户端
		c.Data(http.StatusOK, "application/json", jsonResponse)

		if viggleResponse.Data.SubtractScore == 2 {
			return handleSuccessfulResponseWithQuota(c, ctx, meta, "viggle", "", "2", quota)
		} else {
			return handleSuccessfulResponseWithQuota(c, ctx, meta, "viggle", "", "1", quota)
		}

	} else {
		return openai.ErrorWrapper(
			fmt.Errorf("error: %s", viggleResponse.Message),
			"internal_error",
			http.StatusInternalServerError,
		)
	}
}

func handleLumaVideoRequest(c *gin.Context, ctx context.Context, videoRequest model.VideoRequest, meta *util.RelayMeta) *model.ErrorWithStatusCode {
	baseUrl := meta.BaseURL
	var fullRequestUrl string
	if meta.ChannelType == 44 {
		fullRequestUrl = baseUrl + "/dream-machine/v1/generations"
	} else {
		fullRequestUrl = baseUrl + "/luma/dream-machine/v1/generations"
	}

	var lumaVideoRequest luma.LumaGenerationRequest
	if err := common.UnmarshalBodyReusable(c, &lumaVideoRequest); err != nil {
		return openai.ErrorWrapper(err, "invalid_video_generation_request", http.StatusBadRequest)
	}

	jsonData, err := json.Marshal(lumaVideoRequest)
	if err != nil {
		return openai.ErrorWrapper(err, "json_marshal_error", http.StatusInternalServerError)
	}

	return sendRequestAndHandleLumaResponse(c, ctx, fullRequestUrl, jsonData, meta, "luma")
}

func sendRequestAndHandleLumaResponse(c *gin.Context, ctx context.Context, fullRequestUrl string, jsonData []byte, meta *util.RelayMeta, s string) *model.ErrorWithStatusCode {
	// 预扣费检查 - 预扣0.2，后续处理完多退少补
	quota := int64(0.2 * config.QuotaPerUnit)
	userQuota, err := dbmodel.CacheGetUserQuota(ctx, meta.UserId)
	if err != nil {
		return openai.ErrorWrapper(err, "get_user_quota_error", http.StatusInternalServerError)
	}
	if userQuota-quota < 0 {
		return openai.ErrorWrapper(fmt.Errorf("用户余额不足"), "User balance is not enough", http.StatusBadRequest)
	}

	// 1. 获取频道信息
	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	// 2. 创建请求
	req, err := http.NewRequest("POST", fullRequestUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}

	// 3. 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+channel.Key)

	// 4. 发送请求
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	// 5. 读取响应体
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	// 6. 解析响应
	var lumaResponse luma.LumaGenerationResponse
	err = json.Unmarshal(body, &lumaResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}

	// 7. 设置状态码
	lumaResponse.StatusCode = resp.StatusCode

	// 8. 处理响
	result := handleLumaVideoResponse(c, ctx, lumaResponse, body, meta, "")

	return result
}

func handleMinimaxVideoRequest(c *gin.Context, ctx context.Context, videoRequest model.VideoRequest, meta *util.RelayMeta) *model.ErrorWithStatusCode {
	// 验证必填参数
	if videoRequest.Prompt == "" {
		return openai.ErrorWrapper(
			fmt.Errorf("prompt is required"),
			"invalid_request_error",
			http.StatusBadRequest,
		)
	}

	baseUrl := meta.BaseURL
	fullRequestUrl := baseUrl + "/v1/video_generation"

	// 直接绑定请求体到 VideoRequestMinimax 结构体
	var videoRequestMinimax model.VideoRequestMinimax
	if err := c.ShouldBindJSON(&videoRequestMinimax); err != nil {
		return openai.ErrorWrapper(err, "invalid_request_error", http.StatusBadRequest)
	}

	// 如果存在 image 参数，将其值赋给 FirstFrameImage 并清空 image
	if videoRequestMinimax.Image != "" {
		videoRequestMinimax.FirstFrameImage = videoRequestMinimax.Image
		videoRequestMinimax.Image = ""
	}

	jsonData, err := json.Marshal(videoRequestMinimax)
	if err != nil {
		return openai.ErrorWrapper(err, "json_marshal_error", http.StatusInternalServerError)
	}

	return sendRequestMinimaxAndHandleResponse(c, ctx, fullRequestUrl, jsonData, meta, videoRequest.Model)
}

func handleZhipuVideoRequest(c *gin.Context, ctx context.Context, videoRequest model.VideoRequest, meta *util.RelayMeta) *model.ErrorWithStatusCode {
	baseUrl := meta.BaseURL
	fullRequestUrl := baseUrl + "/api/paas/v4/videos/generations"

	videoRequestZhipu := model.VideoRequestZhipu{
		Model:    videoRequest.Model,
		Prompt:   videoRequest.Prompt,
		ImageURL: videoRequest.ImageURL,
	}

	jsonData, err := json.Marshal(videoRequestZhipu)
	if err != nil {
		return openai.ErrorWrapper(err, "json_marshal_error", http.StatusInternalServerError)
	}

	return sendRequestZhipuAndHandleResponse(c, ctx, fullRequestUrl, jsonData, meta, "cogvideox")
}

func handleKelingVideoRequest(c *gin.Context, ctx context.Context, meta *util.RelayMeta) *model.ErrorWithStatusCode {
	// 构建基础URL和路由映射
	baseUrl := meta.BaseURL
	routeMap := map[string]map[int]string{
		"kling-lip": {
			41: "/v1/videos/lip-sync",
			0:  "/kling/v1/videos/lip2video",
		},
		"text-to-video": {
			41: "/v1/videos/text2video",
			0:  "/kling/v1/videos/text2video",
		},
		"image-to-video": {
			41: "/v1/videos/image2video",
			0:  "/kling/v1/videos/image2video",
		},
		"multi-image-to-video": {
			41: "/v1/videos/multi-image2video",
			0:  "/kling/v1/videos/multi-image2video",
		},
	}

	// 确定请求类型和URL
	var requestType string
	var requestBody interface{}
	var videoType string
	var videoId string
	var mode string
	var duration string

	if meta.OriginModelName == "kling-lip" {
		requestType = "kling-lip"
		videoType = "kling-lip"
		var lipRequest keling.KlingLipRequest
		if err := common.UnmarshalBodyReusable(c, &lipRequest); err != nil {
			return openai.ErrorWrapper(err, "invalid_request", http.StatusBadRequest)
		}
		requestBody = lipRequest
		videoId = lipRequest.Input.VideoId
	} else {
		// 检查是否为多图生视频请求或单图生视频请求
		var imageCheck struct {
			Image     string      `json:"image,omitempty"`
			Mode      string      `json:"mode,omitempty"`
			Duration  interface{} `json:"duration,omitempty"`
			ImageTail string      `json:"image_tail,omitempty"`
			ImageList []struct {
				Image string `json:"image"`
			} `json:"image_list,omitempty"`
		}
		if err := common.UnmarshalBodyReusable(c, &imageCheck); err != nil {
			return openai.ErrorWrapper(err, "invalid_request_body", http.StatusBadRequest)
		}

		// 只有当请求体中包含这些字段时才设置它们
		if imageCheck.Mode != "" {
			mode = imageCheck.Mode
		}

		if imageCheck.Duration != nil {
			switch v := imageCheck.Duration.(type) {
			case float64:
				duration = strconv.Itoa(int(v))
			case string:
				duration = v
			}
		}

		// 检查是否为多图生视频请求
		if len(imageCheck.ImageList) > 0 {
			requestType = "multi-image-to-video"
			videoType = "multi-image-to-video"
			var multiImageToVideoReq keling.MultiImageToVideoRequest
			if err := common.UnmarshalBodyReusable(c, &multiImageToVideoReq); err != nil {
				return openai.ErrorWrapper(err, "invalid_request", http.StatusBadRequest)
			}

			// 只有当有值时才设置这些字段
			if mode != "" {
				multiImageToVideoReq.Mode = mode
			}
			if duration != "" {
				multiImageToVideoReq.Duration = duration
			}

			// 如果 Model 有值，将其赋给 ModelName
			if multiImageToVideoReq.Model != "" {
				multiImageToVideoReq.ModelName = multiImageToVideoReq.Model
				multiImageToVideoReq.Model = "" // 清除 Model 字段
			}

			requestBody = multiImageToVideoReq
		} else if imageCheck.Image != "" || imageCheck.ImageTail != "" {
			requestType = "image-to-video"
			videoType = "image-to-video"
			var imageToVideoReq keling.ImageToVideoRequest
			if err := common.UnmarshalBodyReusable(c, &imageToVideoReq); err != nil {
				return openai.ErrorWrapper(err, "invalid_request", http.StatusBadRequest)
			}

			// 只有当有值时才设置这些字段
			if mode != "" {
				imageToVideoReq.Mode = mode
			}
			if duration != "" {
				imageToVideoReq.Duration = duration
			}

			// 如果 Model 有值，将其赋给 ModelNames
			if imageToVideoReq.Model != "" {
				imageToVideoReq.ModelName = imageToVideoReq.Model
				imageToVideoReq.Model = "" // 清除 Model 字段
			}

			requestBody = imageToVideoReq
		} else {
			requestType = "text-to-video"
			videoType = "text-to-video"
			var textToVideoReq keling.TextToVideoRequest
			if err := common.UnmarshalBodyReusable(c, &textToVideoReq); err != nil {
				return openai.ErrorWrapper(err, "invalid_request", http.StatusBadRequest)
			}

			// 只有当有值时才设置这些字段
			if mode != "" {
				textToVideoReq.Mode = mode
			}
			if duration != "" {
				textToVideoReq.Duration = duration
			}

			// 如果 Model 有值，将其赋给 ModelName
			if textToVideoReq.Model != "" {
				textToVideoReq.ModelName = textToVideoReq.Model
				textToVideoReq.Model = "" // 清除 Model 字段
			}

			requestBody = textToVideoReq
		}
	}

	// 构建完整URL
	channelType := meta.ChannelType
	if channelType != 41 {
		channelType = 0
	}
	fullRequestUrl := baseUrl + routeMap[requestType][channelType]

	// 序列化请求体
	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return openai.ErrorWrapper(err, "json_marshal_error", http.StatusInternalServerError)
	}
	// log.Printf("Request body JSON: %s", string(jsonData))

	return sendRequestKelingAndHandleResponse(c, ctx, fullRequestUrl, jsonData, meta, meta.OriginModelName, mode, duration, videoType, videoId)
}

func handleRunwayVideoRequest(c *gin.Context, ctx context.Context, videoRequest model.VideoRequest, meta *util.RelayMeta) *model.ErrorWithStatusCode {
	baseUrl := meta.BaseURL
	var fullRequestUrl string
	if meta.ChannelType == 42 {
		fullRequestUrl = baseUrl + "/v1/image_to_video"
	} else {
		fullRequestUrl = baseUrl + "/runwayml/v1/image_to_video"
	}

	// 解析请求体
	var runwayRequest runway.VideoGenerationRequest
	if err := common.UnmarshalBodyReusable(c, &runwayRequest); err != nil {
		return openai.ErrorWrapper(err, "invalid_video_generation_request", http.StatusBadRequest)
	}

	// 设置默认时长
	if runwayRequest.Duration == 0 {
		runwayRequest.Duration = 10
	}

	// 设置 duration 到上下文
	c.Set("duration", strconv.Itoa(runwayRequest.Duration))

	// 序列化请求
	jsonData, err := json.Marshal(runwayRequest)
	if err != nil {
		return openai.ErrorWrapper(err, "failed to marshal request body", http.StatusInternalServerError)
	}

	return sendRequestRunwayAndHandleResponse(c, ctx, fullRequestUrl, jsonData, meta, "gen3a_turbo")
}

func sendRequestMinimaxAndHandleResponse(c *gin.Context, ctx context.Context, fullRequestUrl string, jsonData []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {
	// 预扣费检查 - 预扣0.2，后续处理完多退少补
	quota := int64(0.2 * config.QuotaPerUnit)
	userQuota, err := dbmodel.CacheGetUserQuota(ctx, meta.UserId)
	if err != nil {
		return openai.ErrorWrapper(err, "get_user_quota_error", http.StatusInternalServerError)
	}
	if userQuota-quota < 0 {
		return openai.ErrorWrapper(fmt.Errorf("用户余额不足"), "User balance is not enough", http.StatusBadRequest)
	}

	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	req, err := http.NewRequest("POST", fullRequestUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("authorization", "Bearer "+channel.Key)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	var videoResponse model.VideoResponse
	err = json.Unmarshal(body, &videoResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}

	videoResponse.StatusCode = resp.StatusCode
	return handleMinimaxVideoResponse(c, ctx, videoResponse, body, meta, modelName)

}

func sendRequestZhipuAndHandleResponse(c *gin.Context, ctx context.Context, fullRequestUrl string, jsonData []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {
	// 预扣费检查 - 预扣0.2，后续处理完多退少补
	quota := int64(0.2 * config.QuotaPerUnit)
	userQuota, err := dbmodel.CacheGetUserQuota(ctx, meta.UserId)
	if err != nil {
		return openai.ErrorWrapper(err, "get_user_quota_error", http.StatusInternalServerError)
	}
	if userQuota-quota < 0 {
		return openai.ErrorWrapper(fmt.Errorf("用户余额不足"), "User balance is not enough", http.StatusBadRequest)
	}

	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	req, err := http.NewRequest("POST", fullRequestUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("authorization", "Bearer "+channel.Key)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	var videoResponse model.VideoResponse
	err = json.Unmarshal(body, &videoResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}
	videoResponse.StatusCode = resp.StatusCode
	return handleMZhipuVideoResponse(c, ctx, videoResponse, body, meta, modelName)

}

func sendRequestKelingAndHandleResponse(c *gin.Context, ctx context.Context, fullRequestUrl string, jsonData []byte, meta *util.RelayMeta, modelName string, mode string, duration string, videoType string, videoId string) *model.ErrorWithStatusCode {
	// 预扣费检查 - 预扣0.2，后续处理完多退少补
	quota := int64(0.2 * config.QuotaPerUnit)
	userQuota, err := dbmodel.CacheGetUserQuota(ctx, meta.UserId)
	if err != nil {
		return openai.ErrorWrapper(err, "get_user_quota_error", http.StatusInternalServerError)
	}
	if userQuota-quota < 0 {
		return openai.ErrorWrapper(fmt.Errorf("用户余额不足"), "User balance is not enough", http.StatusBadRequest)
	}

	// log.Printf("Request body JSON: %s", string(jsonData))
	req, err := http.NewRequest("POST", fullRequestUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}

	var token string

	if meta.OriginModelName == "kling-lip" {
		video, err := dbmodel.GetVideoTaskByVideoId(videoId)
		if err != nil {
			return openai.ErrorWrapper(err, "get_video_task_error", http.StatusInternalServerError)
		}
		meta.ChannelId = video.ChannelId
		channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
		if err != nil {
			return openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
		}
		meta.ChannelType = channel.Type
	}

	if meta.ChannelType == 41 {
		// 获取渠道信息以支持Key字段解析
		channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
		if err != nil {
			return openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
		}

		// 智能获取可灵凭证 - 支持Key字段和Config
		credentials, err := keling.GetKelingCredentialsFromConfig(meta.Config, channel, 0)
		if err != nil {
			return openai.ErrorWrapper(err, "get_keling_credentials_error", http.StatusInternalServerError)
		}

		// Generate JWT token
		token = EncodeJWTToken(credentials.AK, credentials.SK)
	} else {
		token = meta.APIKey
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	// 添加原始响应日志
	log.Printf("Raw response body: %s", string(body))

	var KelingvideoResponse keling.KelingVideoResponse
	err = json.Unmarshal(body, &KelingvideoResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}
	KelingvideoResponse.StatusCode = resp.StatusCode
	return handleKelingVideoResponse(c, ctx, KelingvideoResponse, body, meta, modelName, mode, duration, videoType)
}

func sendRequestRunwayAndHandleResponse(c *gin.Context, ctx context.Context, fullRequestUrl string, jsonData []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {
	// 预扣费检查 - 预扣0.2，后续处理完多退少补
	quota := int64(0.2 * config.QuotaPerUnit)
	userQuota, err := dbmodel.CacheGetUserQuota(ctx, meta.UserId)
	if err != nil {
		return openai.ErrorWrapper(err, "get_user_quota_error", http.StatusInternalServerError)
	}
	if userQuota-quota < 0 {
		return openai.ErrorWrapper(fmt.Errorf("用户余额不足"), "User balance is not enough", http.StatusBadRequest)
	}

	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	req, err := http.NewRequest("POST", fullRequestUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Runway-Version", "2024-11-06")
	req.Header.Set("authorization", "Bearer "+channel.Key)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	var videoResponse runway.VideoResponse
	err = json.Unmarshal(body, &videoResponse)
	if err != nil {
		log.Printf("Unmarshal error: %v", err)
		return openai.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}

	videoResponse.StatusCode = resp.StatusCode
	return handleRunwayVideoResponse(c, ctx, videoResponse, body, meta, modelName)
}

func EncodeJWTToken(ak, sk string) string {
	claims := jwt.MapClaims{
		"iss": ak,
		"exp": time.Now().Add(30 * time.Minute).Unix(),
		"nbf": time.Now().Add(-5 * time.Second).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(sk))
	if err != nil {
		// Handle error (you might want to return an error instead of panicking in production)
		panic(err)
	}

	return tokenString
}

func getStatusMessage(statusCode int) string {
	switch statusCode {
	case 0:
		return "请求成功"
	case 1002:
		return "触发限流，请稍后再试"
	case 1004:
		return "账号鉴权失败，请检查 API-Key 是否填写正确"
	case 1008:
		return "账号余额不足"
	case 1013:
		return "传入参数异常，请检查入参是否按要求填写"
	case 1026:
		return "视频描述涉及敏感内容"
	default:
		return fmt.Sprintf("未知错误码: %d", statusCode)
	}
}

func handleMinimaxVideoResponse(c *gin.Context, ctx context.Context, videoResponse model.VideoResponse, body []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {
	switch videoResponse.BaseResp.StatusCode {
	case 0:
		// 先计算quota
		quota := calculateQuota(meta, modelName, "", "", c)

		err := CreateVideoLog("minimax", videoResponse.TaskID, meta, "", "", "", "", quota)
		if err != nil {

		}
		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralVideoResponse{
			TaskId:  videoResponse.TaskID,
			Message: videoResponse.BaseResp.StatusMsg,
		}

		switch videoResponse.BaseResp.StatusCode {
		case 0:
			generalResponse.TaskStatus = "succeed"
		default:
			generalResponse.TaskStatus = "failed"
		}
		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("Error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 发送 JSON 响应给客户端
		c.Data(http.StatusOK, "application/json", jsonResponse)
		return handleSuccessfulResponseWithQuota(c, ctx, meta, modelName, "", "", quota)
	case 1002, 1008:
		return openai.ErrorWrapper(
			fmt.Errorf("API error: %s", videoResponse.BaseResp.StatusMsg),
			"api_error",
			http.StatusTooManyRequests,
		)
	case 1004:
		return openai.ErrorWrapper(
			fmt.Errorf("API error: %s", videoResponse.BaseResp.StatusMsg),
			"api_error",
			http.StatusForbidden,
		)
	case 1013, 1026:
		return openai.ErrorWrapper(
			fmt.Errorf("API error: %s", videoResponse.BaseResp.StatusMsg),
			"api_error",
			http.StatusBadRequest,
		)
	default:
		return openai.ErrorWrapper(
			fmt.Errorf("Unknown API error: %s", videoResponse.BaseResp.StatusMsg),
			"api_error",
			http.StatusInternalServerError,
		)
	}
}

func handleMZhipuVideoResponse(c *gin.Context, ctx context.Context, videoResponse model.VideoResponse, body []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {
	switch videoResponse.StatusCode {
	case 200:
		// 先计算quota
		quota := calculateQuota(meta, modelName, "", "", c)

		err := CreateVideoLog("zhipu", videoResponse.ID, meta, "", "", "", "", quota)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("API error: %s", videoResponse.ZhipuError.Message),
				"api_error",
				http.StatusBadRequest,
			)
		}

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralVideoResponse{
			TaskId:  videoResponse.ID,
			Message: "",
		}

		// 修改 TaskStatus 处理逻辑
		switch videoResponse.TaskStatus {
		case "FAIL":
			generalResponse.TaskStatus = "failed"
		default:
			generalResponse.TaskStatus = "succeed"
		}

		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 发送 JSON 响应给客户端
		c.Data(http.StatusOK, "application/json", jsonResponse)

		return handleSuccessfulResponseWithQuota(c, ctx, meta, modelName, "", "", quota)
	case 400:
		return openai.ErrorWrapper(
			fmt.Errorf("API error: %s", videoResponse.ZhipuError.Message),
			"api_error",
			http.StatusBadRequest,
		)
	case 429:
		return openai.ErrorWrapper(
			fmt.Errorf("API error: %s", videoResponse.ZhipuError.Message),
			"api_error",
			http.StatusTooManyRequests,
		)
	default:
		return openai.ErrorWrapper(
			fmt.Errorf("Unknown API error: %s", videoResponse.ZhipuError.Message),
			"api_error",
			http.StatusInternalServerError,
		)
	}
}

func handleKelingVideoResponse(c *gin.Context, ctx context.Context, videoResponse keling.KelingVideoResponse, body []byte, meta *util.RelayMeta, modelName string, mode string, duration string, videoType string) *model.ErrorWithStatusCode {
	modelName2 := c.GetString("original_model")
	switch videoResponse.StatusCode {
	case 200:
		// 首先打印完整的响应内容以便调试
		log.Printf("Video Response: %+v", videoResponse)

		// 先计算quota
		quota := calculateQuota(meta, modelName2, mode, duration, c)

		// 现在可以安全地访问这些字段
		err := CreateVideoLog(
			"kling",
			videoResponse.Data.TaskID,
			meta,
			mode,
			duration,
			videoType,
			"",
			quota,
		)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("API error: %s", err),
				"api_error",
				http.StatusBadRequest,
			)
		}

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralVideoResponse{
			TaskId:  videoResponse.Data.TaskID,
			Message: videoResponse.Message,
		}

		switch videoResponse.Data.TaskStatus {
		case "failed":
			generalResponse.TaskStatus = "failed"
		default:
			generalResponse.TaskStatus = "succeed"
		}

		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("Error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 发送 JSON 响应给客户端
		c.Data(http.StatusOK, "application/json", jsonResponse)

		return handleSuccessfulResponseWithQuota(c, ctx, meta, modelName2, mode, duration, quota)
	case 400:
		return openai.ErrorWrapper(
			fmt.Errorf("API error (400): %s\nFull response: %s", videoResponse.Message, string(body)),
			"api_error",
			http.StatusBadRequest,
		)
	case 429:
		return openai.ErrorWrapper(
			fmt.Errorf("API error (429): %s\nFull response: %s", videoResponse.Message, string(body)),
			"api_error",
			http.StatusTooManyRequests,
		)
	default:
		// 对于未知错误，我们需要更详细的信息
		errorMessage := fmt.Sprintf("Unknown API error (Status Code: %d): %s\nFull response: %s",
			videoResponse.StatusCode,
			videoResponse.Message,
			string(body))

		log.Printf("Error occurred: %s", errorMessage)

		return openai.ErrorWrapper(
			fmt.Errorf(errorMessage),
			"api_error",
			http.StatusInternalServerError,
		)
	}
}

func handleRunwayVideoResponse(c *gin.Context, ctx context.Context, videoResponse runway.VideoResponse, body []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {
	switch videoResponse.StatusCode {
	case 200:
		// 先计算quota
		quota := calculateQuota(meta, modelName, "", "", c)

		err := CreateVideoLog("runway", videoResponse.Id, meta, "", "", "", "", quota)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("API error: %s", err.Error()),
				"api_error",
				http.StatusBadRequest,
			)
		}

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralVideoResponse{
			TaskId:     videoResponse.Id,
			Message:    "",
			TaskStatus: "succeed",
		}

		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 发送 JSON 响应给客户端
		c.Data(http.StatusOK, "application/json", jsonResponse)

		return handleSuccessfulResponseWithQuota(c, ctx, meta, modelName, "", "", quota)
	case 400:
		return openai.ErrorWrapper(
			fmt.Errorf("API error: %s", videoResponse.Error),
			"api_error",
			http.StatusBadRequest,
		)
	case 429:
		return openai.ErrorWrapper(
			fmt.Errorf("API error: %s", videoResponse.Error),
			"api_error",
			http.StatusTooManyRequests,
		)
	default:
		return openai.ErrorWrapper(
			fmt.Errorf("Unknown API error: %s", videoResponse.Error),
			"api_error",
			http.StatusInternalServerError,
		)
	}
}

// Add this function definition to resolve the error
func handleLumaVideoResponse(c *gin.Context, ctx context.Context, lumaResponse luma.LumaGenerationResponse, body []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {
	switch lumaResponse.StatusCode {
	case 201:
		// 先计算quota
		quota := calculateQuota(meta, "luma", "", "", c)

		err := CreateVideoLog("luma", lumaResponse.ID, meta, "", "", "", "", quota)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("API error: %s", err),
				"api_error",
				http.StatusBadRequest,
			)
		}

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralVideoResponse{
			TaskId:  lumaResponse.ID,
			Message: "",
		}

		switch lumaResponse.State {
		case "failed":
			generalResponse.TaskStatus = "failed"
		default:
			generalResponse.TaskStatus = "succeed"
		}

		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("Error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 发送 JSON 响应给客户端
		c.Data(http.StatusOK, "application/json", jsonResponse)
		return handleSuccessfulResponseWithQuota(c, ctx, meta, "luma", "", "", quota)
	case 400:
		return openai.ErrorWrapper(
			fmt.Errorf("API error (400): %s\nFull response: %s", *lumaResponse.FailureReason, string(body)),
			"api_error",
			http.StatusBadRequest,
		)
	case 429:
		return openai.ErrorWrapper(
			fmt.Errorf("API error (429): %s\nFull response: %s", *lumaResponse.FailureReason, string(body)),
			"api_error",
			http.StatusTooManyRequests,
		)
	default:
		// 对于未知错误，我们需要更详细的信息
		errorMessage := fmt.Sprintf("Unknown API error (Status Code: %d): %s\nFull response: %s",
			lumaResponse.StatusCode,
			*lumaResponse.FailureReason,
			string(body))

		log.Printf("Error occurred: %s", errorMessage)

		return openai.ErrorWrapper(
			fmt.Errorf(errorMessage),
			"api_error",
			http.StatusInternalServerError,
		)
	}
}

// 新增计算quota的函数
func calculateQuota(meta *util.RelayMeta, modelName string, mode string, duration string, c *gin.Context) int64 {
	var modelPrice float64
	defaultPrice, ok := common.DefaultModelPrice[modelName]
	if !ok {
		modelPrice = 0.1
	} else {
		modelPrice = defaultPrice
	}
	quota := int64(modelPrice * config.QuotaPerUnit)

	// 特殊处理 kling-v1 模型
	if modelName == "kling-v1" {
		var multiplier float64
		switch {
		case mode == "std" && duration == "5":
			multiplier = 1
		case mode == "std" && duration == "10":
			multiplier = 2
		case mode == "pro" && duration == "5":
			multiplier = 3.5
		case mode == "pro" && duration == "10":
			multiplier = 7
		default:
			multiplier = 1
		}
		quota = int64(float64(quota) * multiplier)
	}
	if modelName == "kling-v1-5" || modelName == "kling-v1-6" {
		var multiplier float64
		switch {
		case mode == "std" && duration == "5":
			multiplier = 1
		case mode == "std" && duration == "10":
			multiplier = 2
		case mode == "pro" && duration == "5":
			multiplier = 1.75
		case mode == "pro" && duration == "10":
			multiplier = 3.5
		default:
			multiplier = 1
		}
		quota = int64(float64(quota) * multiplier)
	}

	if modelName == "viggle" && duration == "2" {
		quota = quota * 2
	}

	value, exists := c.Get("duration")
	if exists {
		runwayDuration := value.(string)
		if runwayDuration == "10" {
			quota = quota * 2
		}
	}

	if modelName == "v3.5" {
		durationInt := c.GetInt("Duration")
		modeStr := c.GetString("Mode")
		motionMode := c.GetString("MotionMode")
		var multiplier float64
		switch {
		case modeStr == "Turbo" && durationInt == 5 && motionMode == "Normal":
			multiplier = 1
		case modeStr == "Turbo" && durationInt == 5 && motionMode == "Performance":
			multiplier = 2
		case modeStr == "Turbo" && durationInt == 8 && motionMode == "Normal":
			multiplier = 2
		case modeStr == "540P" && durationInt == 5 && motionMode == "Normal":
			multiplier = 1
		case modeStr == "540P" && durationInt == 5 && motionMode == "Performance":
			multiplier = 2
		case modeStr == "540P" && durationInt == 8 && motionMode == "Normal":
			multiplier = 2
		case modeStr == "720P" && durationInt == 5 && motionMode == "Normal":
			multiplier = 1.33
		case modeStr == "720P" && durationInt == 5 && motionMode == "Performance":
			multiplier = 2.67
		case modeStr == "720P" && durationInt == 8 && motionMode == "Normal":
			multiplier = 2.67
		case modeStr == "1080P" && durationInt == 5 && motionMode == "Normal":
			multiplier = 2.67
		default:
			multiplier = 1
		}
		quota = int64(float64(45) * multiplier)
	}

	return quota
}

// 新增带quota参数的成功响应处理函数，支持可选的videoTaskId参数
func handleSuccessfulResponseWithQuota(c *gin.Context, ctx context.Context, meta *util.RelayMeta, modelName string, mode string, duration string, quota int64, videoTaskId ...string) *model.ErrorWithStatusCode {
	referer := c.Request.Header.Get("HTTP-Referer")
	title := c.Request.Header.Get("X-Title")

	err := dbmodel.PostConsumeTokenQuota(meta.TokenId, quota)
	if err != nil {
		logger.SysError("error consuming token remain quota: " + err.Error())
	}

	err = dbmodel.CacheUpdateUserQuota(ctx, meta.UserId)
	if err != nil {
		logger.SysError("error update user quota cache: " + err.Error())
	}

	if quota != 0 {
		var modelPrice float64
		defaultPrice, ok := common.DefaultModelPrice[modelName]
		if !ok {
			modelPrice = 0.1
		} else {
			modelPrice = defaultPrice
		}

		tokenName := c.GetString("token_name")
		logContent := fmt.Sprintf("模型固定价格 %.2f$", modelPrice)

		// 如果提供了videoTaskId，使用RecordVideoConsumeLog，否则使用普通的RecordConsumeLog
		if len(videoTaskId) > 0 && videoTaskId[0] != "" {
			dbmodel.RecordVideoConsumeLog(ctx, meta.UserId, meta.ChannelId, 0, 0, modelName, tokenName, quota, logContent, 0, title, referer, videoTaskId[0])
		} else {
			dbmodel.RecordConsumeLog(ctx, meta.UserId, meta.ChannelId, 0, 0, modelName, tokenName, quota, logContent, 0, title, referer, false, 0.0)
		}

		dbmodel.UpdateUserUsedQuotaAndRequestCount(meta.UserId, quota)
		channelId := c.GetInt("channel_id")
		dbmodel.UpdateChannelUsedQuota(channelId, quota)
	}

	return nil
}

func CreateVideoLog(provider string, taskId string, meta *util.RelayMeta, mode string, duration string, videoType string, videoId string, quota int64) error {
	// 对于VertexAI，保存完整的JSON凭证
	var credentialsJSON string
	if provider == "vertexai" {
		// 获取当前使用的凭证
		channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
		if err != nil {
			log.Printf("[VEO任务创建] 获取渠道失败 - 任务:%s, 渠道ID:%d, 错误:%v", taskId, meta.ChannelId, err)
		} else {
			credentials, err := vertexai.GetCredentialsFromConfig(meta.Config, channel)
			if err != nil {
				log.Printf("[VEO任务创建] 获取凭证失败 - 任务:%s, 错误:%v", taskId, err)
			} else {
				if credentialsBytes, err := json.Marshal(credentials); err == nil {
					credentialsJSON = string(credentialsBytes)
					log.Printf("[VEO任务创建] ✅ 成功保存凭证 - 任务:%s, 项目ID:%s, 服务账号:%s",
						taskId, credentials.ProjectID, credentials.ClientEmail)
				} else {
					log.Printf("[VEO任务创建] JSON序列化失败 - 任务:%s, 错误:%v", taskId, err)
				}
			}
		}

		// 如果没有获取到凭证，记录警告
		if credentialsJSON == "" {
			log.Printf("[VEO任务创建] ⚠️  未能保存凭证，查询时将使用当前渠道配置 - 任务:%s", taskId)
		}
	}

	// 根据模型名称确定最终的视频类型
	finalVideoType := videoType
	if videoType == "image-to-video" && strings.Contains(strings.ToLower(meta.OriginModelName), "t2v") {
		finalVideoType = "text-to-video"
	}

	// 创建新的 Video 实例
	video := &dbmodel.Video{
		Prompt:      "prompt",
		CreatedAt:   time.Now().Unix(), // 使用当前时间戳
		TaskId:      taskId,
		Provider:    provider,
		Username:    dbmodel.GetUsernameById(meta.UserId),
		ChannelId:   meta.ChannelId,
		UserId:      meta.UserId,
		Mode:        mode, //keling
		Type:        finalVideoType,
		Model:       meta.OriginModelName,
		Duration:    duration,
		VideoId:     videoId,
		Quota:       quota,
		Credentials: credentialsJSON, // 保存完整的JSON凭证
	}

	// 调用 Insert 方法插入记录
	err := video.Insert()
	if err != nil {
		return fmt.Errorf("failed to insert video log: %v", err)
	}

	return nil
}

func mapTaskStatus(status string) string {
	switch status {
	case "PROCESSING":
		return "processing"
	case "SUCCESS":
		return "succeed"
	case "FAIL":
		return "failed"
	default:
		return "unknown"
	}
}

func mapTaskStatusMinimax(status string) string {
	switch status {
	case "Processing":
		return "processing"
	case "Success":
		return "succeed"
	case "Fail":
		return "failed"
	default:
		return "unknown"
	}
}

func mapTaskStatusLuma(status string) string {
	switch status {
	case "completed":
		return "scucceed"
	case "dreaming":
		return "processing"
	case "failed":
		return "failed"
	default:
		return "unknown"
	}
}

func mapTaskStatusRunway(status string) string {
	switch status {
	case "PENDING":
		return "processing"
	case "SUCCEEDED":
		return "succeed"
	default:
		return "unknown"
	}
}

func GetVideoResult(c *gin.Context, taskId string) *model.ErrorWithStatusCode {
	videoTask, err := dbmodel.GetVideoTaskById(taskId)
	if err != nil {
		return openai.ErrorWrapper(
			fmt.Errorf("failed to get video: %v", err),
			"database_error",
			http.StatusInternalServerError,
		)
	}

	channel, err := dbmodel.GetChannelById(videoTask.ChannelId, true)
	if err != nil {
		return openai.ErrorWrapper(
			fmt.Errorf("failed to get channel: %v", err),
			"database_error",
			http.StatusInternalServerError,
		)
	}
	logger.SysLog(fmt.Sprintf("channelId2:%d", channel.Id))
	cfg, err := channel.LoadConfig()
	if err != nil {
		return openai.ErrorWrapper(
			fmt.Errorf("failed to load channel config: %v", err),
			"config_error",
			http.StatusInternalServerError,
		)
	}

	var fullRequestUrl string
	switch videoTask.Provider {
	case "zhipu":
		fullRequestUrl = fmt.Sprintf("https://open.bigmodel.cn/api/paas/v4/async-result/%s", taskId)
	case "minimax":
		fullRequestUrl = fmt.Sprintf("%s/v1/query/video_generation?task_id=%s", *channel.BaseURL, taskId)
	case "kling":
		if videoTask.Type == "text-to-video" {
			if channel.Type == 41 {
				fullRequestUrl = fmt.Sprintf("%s/v1/videos/text2video/%s", *channel.BaseURL, taskId)
			} else {
				fullRequestUrl = fmt.Sprintf("%s/kling/v1/videos/text2video/%s", *channel.BaseURL, taskId)
			}
		} else if videoTask.Type == "image-to-video" {
			if channel.Type == 41 {
				fullRequestUrl = fmt.Sprintf("%s/v1/videos/image2video/%s", *channel.BaseURL, taskId)
			} else {
				fullRequestUrl = fmt.Sprintf("%s/kling/v1/videos/image2video/%s", *channel.BaseURL, taskId)
			}
		} else if videoTask.Type == "kling-lip" {
			if channel.Type == 41 {
				fullRequestUrl = fmt.Sprintf("%s/v1/videos/lip-sync/%s", *channel.BaseURL, taskId)
			} else {
				fullRequestUrl = fmt.Sprintf("%s/kling/v1/videos/lip2video/%s", *channel.BaseURL, taskId)
			}
		} else if videoTask.Type == "multi-image-to-video" {
			if channel.Type == 41 {
				fullRequestUrl = fmt.Sprintf("%s/v1/videos/multi-image2video/%s", *channel.BaseURL, taskId)
			} else {
				fullRequestUrl = fmt.Sprintf("%s/kling/v1/videos/multi-image2video/%s", *channel.BaseURL, taskId)
			}
		}
	case "runway":
		if channel.Type != 42 {
			fullRequestUrl = fmt.Sprintf("%s/runwayml/v1/tasks/%s", *channel.BaseURL, taskId)
		} else {
			fullRequestUrl = fmt.Sprintf("%s/v1/tasks/%s", *channel.BaseURL, taskId)
		}

	case "luma":
		if channel.Type != 44 {
			fullRequestUrl = fmt.Sprintf("%s/dream-machine/v1/generations/%s", *channel.BaseURL, taskId)
		} else {
			fullRequestUrl = fmt.Sprintf("%s/luma/dream-machine/v1/generations/%s", *channel.BaseURL, taskId)
		}

	case "viggle":
		fullRequestUrl = fmt.Sprintf("%s/api/video/task?task_id=%s", *channel.BaseURL, taskId)
	case "pixverse":
		fullRequestUrl = fmt.Sprintf("%s/openapi/v2/video/result/%s", *channel.BaseURL, taskId)
	case "doubao":
		fullRequestUrl = fmt.Sprintf("%s/api/v3/contents/generations/tasks/%s", *channel.BaseURL, taskId)
	case "vertexai":
		// 对于 VertexAI Veo，taskId 现在只是操作ID部分
		// 需要从渠道配置重新构建完整的操作名称，并使用与发送任务时相同的密钥

		// 使用保存的JSON凭证
		var credentials *vertexai.Credentials
		if videoTask.Credentials != "" {
			// 使用保存的凭证
			credentials = &vertexai.Credentials{}
			if err := json.Unmarshal([]byte(videoTask.Credentials), credentials); err != nil {
				log.Printf("[VEO查询] ❌ 解析保存的凭证失败 - 任务:%s, 凭证长度:%d, 错误:%v",
					taskId, len(videoTask.Credentials), err)
				return openai.ErrorWrapper(
					fmt.Errorf("解析保存的Vertex AI凭证失败: %v", err),
					"invalid_saved_credentials",
					http.StatusInternalServerError,
				)
			}
			log.Printf("[VEO查询] ✅ 使用保存的凭证 - 任务:%s, 项目ID:%s, 服务账号:%s",
				taskId, credentials.ProjectID, credentials.ClientEmail)
		} else {
			// 回退到当前渠道配置（向后兼容）
			log.Printf("[VEO查询] ⚠️  任务未保存凭证，回退到当前渠道配置 - 任务:%s", taskId)
			var err error
			credentials, err = vertexai.GetCredentialsFromConfig(cfg, channel)
			if err != nil {
				log.Printf("[VEO查询] ❌ 获取当前渠道凭证失败 - 任务:%s, 渠道ID:%d, 错误:%v",
					taskId, videoTask.ChannelId, err)
				return openai.ErrorWrapper(
					fmt.Errorf("获取Vertex AI凭证失败: %v", err),
					"invalid_credentials",
					http.StatusInternalServerError,
				)
			}
			log.Printf("[VEO查询] ✅ 使用当前渠道凭证 - 任务:%s, 项目ID:%s, 服务账号:%s",
				taskId, credentials.ProjectID, credentials.ClientEmail)
		}

		projectId := credentials.ProjectID
		if projectId == "" {
			return openai.ErrorWrapper(
				fmt.Errorf("无法获取Vertex AI项目ID，请检查凭证配置"),
				"invalid_project_id",
				http.StatusInternalServerError,
			)
		}

		region := cfg.Region
		if region == "" {
			region = "global"
		}
		modelId := videoTask.Model // 从数据库中的视频任务记录获取模型名

		log.Printf("[VEO查询] 使用项目ID:%s, 区域:%s, 模型:%s", projectId, region, modelId)

		// 构建 fetchPredictOperation URL
		var baseURL string
		if region == "global" {
			baseURL = "https://aiplatform.googleapis.com"
		} else {
			baseURL = fmt.Sprintf("https://%s-aiplatform.googleapis.com", region)
		}

		fullRequestUrl = fmt.Sprintf("%s/v1/projects/%s/locations/%s/publishers/google/models/%s:fetchPredictOperation",
			baseURL, projectId, region, modelId)

		// 保存凭证到context中，供后续请求使用
		c.Set("query_credentials", credentials)
	case "ali":
		// 根据不同地域选择查询端点
		baseUrl := *channel.BaseURL
		fullRequestUrl = fmt.Sprintf("%s/api/v1/tasks/%s", baseUrl, taskId)
	case "sora":
		// Sora 视频状态查询
		baseUrl := *channel.BaseURL
		if baseUrl == "" {
			baseUrl = "https://api.openai.com"
		}
		fullRequestUrl = fmt.Sprintf("%s/v1/videos/%s", baseUrl, taskId)
	default:
		return openai.ErrorWrapper(
			fmt.Errorf("unsupported model type:"),
			"invalid_request_error",
			http.StatusBadRequest,
		)
	}
	// 创建新的请求
	var req *http.Request
	var fullOperationName string // 声明变量

	if videoTask.Provider == "vertexai" {
		// 从context中获取保存的凭证来构建操作名称
		var projectId string
		if queryCredentials, exists := c.Get("query_credentials"); exists {
			credentials := queryCredentials.(*vertexai.Credentials)
			projectId = credentials.ProjectID
		} else {
			// 回退到配置中的项目ID（向后兼容）
			projectId = cfg.VertexAIProjectID
		}

		region := cfg.Region
		if region == "" {
			region = "global"
		}
		modelId := videoTask.Model

		// 重新构建完整的操作名称用于API请求
		fullOperationName = fmt.Sprintf("projects/%s/locations/%s/publishers/google/models/%s/operations/%s",
			projectId, region, modelId, taskId)

		// VertexAI 需要 POST 请求，并在请求体中包含完整的操作名称
		requestBody := map[string]string{
			"operationName": fullOperationName,
		}
		jsonBody, err := json.Marshal(requestBody)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to marshal request body: %v", err),
				"marshal_error",
				http.StatusInternalServerError,
			)
		}
		req, err = http.NewRequest("POST", fullRequestUrl, bytes.NewReader(jsonBody))
	} else {
		req, err = http.NewRequest("GET", fullRequestUrl, nil)
	}
	if err != nil {
		return openai.ErrorWrapper(
			fmt.Errorf("failed to create request: %v", err),
			"api_error",
			http.StatusInternalServerError,
		)
	}

	if videoTask.Provider == "kling" && channel.Type == 41 {
		// 智能获取可灵凭证 - 支持Key字段和Config
		credentials, err := keling.GetKelingCredentialsFromConfig(cfg, channel, 0)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to get Keling credentials: %v", err),
				"credential_error",
				http.StatusInternalServerError,
			)
		}

		token := EncodeJWTToken(credentials.AK, credentials.SK)

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

	} else if videoTask.Provider == "runway" && channel.Type == 42 {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Runway-Version", "2024-11-06")
		req.Header.Set("Authorization", "Bearer "+channel.Key)
	} else if videoTask.Provider == "viggle" {
		req.Header.Set("Access-Token", channel.Key)
	} else if videoTask.Provider == "pixverse" {
		req.Header.Set("API-KEY", channel.Key)
		req.Header.Set("Ai-trace-id", "aaaaa")
	} else if videoTask.Provider == "vertexai" {
		// VertexAI 需要使用 OAuth2 token 进行认证 - 使用保存的凭证
		var credentials *vertexai.Credentials

		// 从context中获取保存的凭证
		if queryCredentials, exists := c.Get("query_credentials"); exists {
			credentials = queryCredentials.(*vertexai.Credentials)
			log.Printf("[VEO查询认证] ✅ 使用保存的凭证进行认证 - 项目ID:%s, 服务账号:%s",
				credentials.ProjectID, credentials.ClientEmail)
		} else {
			// 回退逻辑（向后兼容）
			log.Printf("[VEO查询认证] ⚠️  未找到保存的凭证，使用当前渠道配置")
			var err error
			credentials, err = vertexai.GetCredentialsFromConfig(cfg, channel)
			if err != nil {
				log.Printf("[VEO查询认证] ❌ 获取渠道凭证失败 - 错误:%v", err)
				return openai.ErrorWrapper(
					fmt.Errorf("failed to get VertexAI credentials: %v", err),
					"credential_error",
					http.StatusInternalServerError,
				)
			}
			log.Printf("[VEO查询认证] ✅ 使用当前渠道凭证进行认证 - 项目ID:%s, 服务账号:%s",
				credentials.ProjectID, credentials.ClientEmail)
		}

		adaptor := &vertexai.Adaptor{
			AccountCredentials: *credentials,
		}

		// 创建临时的 RelayMeta 来获取访问令牌 - 使用保存的凭证
		tempMeta := &util.RelayMeta{
			ChannelId: channel.Id,
			Config: dbmodel.ChannelConfig{
				Region:            cfg.Region,
				VertexAIProjectID: credentials.ProjectID, // 使用保存的凭证中的项目ID
			},
			ActualAPIKey: func() string {
				if credBytes, err := json.Marshal(credentials); err == nil {
					return string(credBytes)
				}
				return ""
			}(), // 使用保存的凭证
			IsMultiKey: false, // 单个凭证，不是多密钥模式
		}

		accessToken, err := vertexai.GetAccessToken(adaptor, tempMeta)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to get VertexAI access token: %v", err),
				"auth_error",
				http.StatusInternalServerError,
			)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+accessToken)
	} else {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+channel.Key)
	}

	// 发送 HTTP 请求获取结果
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openai.ErrorWrapper(
			fmt.Errorf("failed to fetch video result: %v", err),
			"api_error",
			http.StatusInternalServerError,
		)
	}
	defer resp.Body.Close()
	log.Printf("video response body: %+v", resp)
	// 检查响应状态码
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return openai.ErrorWrapper(
			fmt.Errorf("API error: %s", string(body)),
			"api_error",
			resp.StatusCode,
		)
	}

	if videoTask.Provider == "zhipu" {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to read response body: %v", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}
		defer resp.Body.Close()

		// 解析JSON响应
		var zhipuResp model.FinalVideoResponse
		if err := json.Unmarshal(body, &zhipuResp); err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to parse response JSON: %v", err),
				"json_parse_error",
				http.StatusInternalServerError,
			)
		}

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralFinalVideoResponse{
			TaskId:      taskId,
			TaskStatus:  mapTaskStatus(zhipuResp.TaskStatus), // 使用 mapTaskStatus 函数
			Message:     "",
			VideoResult: "",
		}

		// 如果任务成功且有视频结果，添加到响应中
		if zhipuResp.TaskStatus == "SUCCESS" && len(zhipuResp.VideoResults) > 0 {
			generalResponse.VideoResult = zhipuResp.VideoResults[0].URL
			// 同时设置 VideoResults
			generalResponse.VideoResults = []model.VideoResultItem{
				{Url: zhipuResp.VideoResults[0].URL},
			}
		}

		// 更新任务状态并检查是否需要退款
		needRefund := UpdateVideoTaskStatus(taskId, generalResponse.TaskStatus, "")
		if needRefund {
			log.Printf("Task %s failed, compensating user", taskId)
			CompensateVideoTask(taskId)
		}

		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("Error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 直接使用上游返回的状态码
		c.Data(resp.StatusCode, "application/json", jsonResponse)
		return nil
	} else if videoTask.Provider == "minimax" {
		err := handleMinimaxResponse(c, channel, taskId)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("Error handling minimax response: %v", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}
	} else if videoTask.Provider == "kling" {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to read response body: %v", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}
		defer resp.Body.Close()

		// 打印完整响应体
		log.Printf("Kling response body: %s", string(body))

		// 解析JSON响应
		var klingResp keling.KelingVideoResponse
		if err := json.Unmarshal(body, &klingResp); err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to parse response JSON: %v", err),
				"json_parse_error",
				http.StatusInternalServerError,
			)
		}

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralFinalVideoResponse{
			TaskId:      klingResp.Data.TaskID,
			Message:     klingResp.Data.TaskStatusMsg,
			VideoResult: "",
			Duration:    "",
		}

		// 检查是否有视频结果
		if len(klingResp.Data.TaskResult.Videos) > 0 {
			generalResponse.VideoId = klingResp.Data.TaskResult.Videos[0].ID
			generalResponse.Duration = klingResp.Data.TaskResult.Videos[0].Duration
		}

		// 处理任务状态
		switch klingResp.Data.TaskStatus {
		case "submitted":
			generalResponse.TaskStatus = "processing"
		default:
			generalResponse.TaskStatus = klingResp.Data.TaskStatus
		}

		// 如果任务成功且有视频结果，添加到响应中
		if klingResp.Data.TaskStatus == "succeed" && len(klingResp.Data.TaskResult.Videos) > 0 {
			generalResponse.VideoResult = klingResp.Data.TaskResult.Videos[0].URL
			generalResponse.Duration = klingResp.Data.TaskResult.Videos[0].Duration
			// 同时设置 VideoResults
			generalResponse.VideoResults = []model.VideoResultItem{
				{Url: klingResp.Data.TaskResult.Videos[0].URL},
			}
		}

		// 更新任务状态并检查是否需要退款
		failReason := ""
		if klingResp.Data.TaskStatus == "failed" {
			failReason = klingResp.Data.TaskStatusMsg
		}
		needRefund := UpdateVideoTaskStatus(taskId, generalResponse.TaskStatus, failReason)
		if needRefund {
			log.Printf("Task %s failed, compensating user", taskId)
			CompensateVideoTask(taskId)
		}

		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("Error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 直接使用上游返回的状态码
		c.Data(resp.StatusCode, "application/json", jsonResponse)

		return nil
	} else if videoTask.Provider == "runway" {
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to read response body: %v", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 解析JSON响应
		var runwayResp runway.VideoFinalResponse
		if err := json.Unmarshal(body, &runwayResp); err != nil {
			log.Printf("Failed to parse response: %v, body: %s", err, string(body))
			return openai.ErrorWrapper(
				fmt.Errorf("failed to parse response JSON: %v", err),
				"json_parse_error",
				http.StatusInternalServerError,
			)
		}

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralFinalVideoResponse{
			TaskId:      taskId,
			TaskStatus:  mapTaskStatusRunway(runwayResp.Status),
			Message:     "", // 添加错误信息
			VideoResult: "",
		}

		// 如果任务成功且有视频结果，添加到响应中
		if runwayResp.Status == "SUCCEEDED" && len(runwayResp.Output) > 0 {
			generalResponse.VideoResult = runwayResp.Output[0]
			// 同时设置 VideoResults
			generalResponse.VideoResults = []model.VideoResultItem{
				{Url: runwayResp.Output[0]},
			}
		} else {
			log.Printf("Task not succeeded or no output. Status: %s, Output length: %d",
				runwayResp.Status, len(runwayResp.Output))
		}

		// 更新任务状态并检查是否需要退款
		failReason := ""
		if runwayResp.Status == "FAILED" {
			failReason = "Task failed"
		}
		needRefund := UpdateVideoTaskStatus(taskId, generalResponse.TaskStatus, failReason)
		if needRefund {
			log.Printf("Task %s failed, compensating user", taskId)
			CompensateVideoTask(taskId)
		}

		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 直接使用上游返回的状态码
		c.Data(resp.StatusCode, "application/json", jsonResponse)
		return nil
	} else if videoTask.Provider == "luma" {
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to read response body: %v", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 解析JSON响应
		var lumaResp luma.LumaGenerationResponse
		if err := json.Unmarshal(body, &lumaResp); err != nil {
			log.Printf("Failed to parse response: %v, body: %s", err, string(body))
			return openai.ErrorWrapper(
				fmt.Errorf("failed to parse response JSON: %v", err),
				"json_parse_error",
				http.StatusInternalServerError,
			)
		}

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralFinalVideoResponse{
			TaskId:      taskId,
			TaskStatus:  mapTaskStatusLuma(lumaResp.State),
			Message:     "", // 添加错误信息
			VideoResult: "",
		}

		// 如果任务成功且有视频结果，添加到响应中
		if lumaResp.State == "completed" && lumaResp.Assets != nil {
			// 将 interface{} 转换为 map[string]interface{}
			if assets, ok := lumaResp.Assets.(map[string]interface{}); ok {
				// 获取 video URL
				if videoURL, ok := assets["video"].(string); ok {
					generalResponse.VideoResult = videoURL
					// 同时设置 VideoResults
					generalResponse.VideoResults = []model.VideoResultItem{
						{Url: videoURL},
					}
				} else {
					log.Printf("Video URL not found or invalid type in assets")
				}
			} else {
				log.Printf("Failed to convert assets to map")
			}
		} else {
			log.Printf("Task not completed or no assets. State: %s, Assets: %v",
				lumaResp.State, lumaResp.Assets)
		}

		// 更新任务状态并检查是否需要退款
		failReason := ""
		if lumaResp.State == "failed" {
			if lumaResp.FailureReason != nil {
				failReason = *lumaResp.FailureReason
			} else {
				failReason = "Task failed"
			}
		}
		needRefund := UpdateVideoTaskStatus(taskId, generalResponse.TaskStatus, failReason)
		if needRefund {
			log.Printf("Task %s failed, compensating user", taskId)
			CompensateVideoTask(taskId)
		}

		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 直接使用上游返回的状态码
		c.Data(resp.StatusCode, "application/json", jsonResponse)
		return nil
	} else if videoTask.Provider == "viggle" {
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to read response body: %v", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 解析JSON响应
		var viggleResp viggle.ViggleFinalResponse
		if err := json.Unmarshal(body, &viggleResp); err != nil {
			log.Printf("Failed to parse response: %v, body: %s", err, string(body))
			return openai.ErrorWrapper(
				fmt.Errorf("failed to parse response JSON: %v", err),
				"json_parse_error",
				http.StatusInternalServerError,
			)
		}

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralFinalVideoResponse{
			TaskId:      taskId,
			TaskStatus:  "",
			Message:     viggleResp.Message, // 添加错误信息
			VideoResult: "",
		}

		// 首先检查 Data 切片是否为空
		if len(viggleResp.Data.Data) == 0 {
			generalResponse.TaskStatus = "failed"
		} else {
			// 处理不同状态的情况
			if viggleResp.Data.Code == 0 {
				if viggleResp.Data.Data[0].Result == "" {
					generalResponse.TaskStatus = "processing"
				} else {
					generalResponse.TaskStatus = "succeed"
					generalResponse.VideoResult = viggleResp.Data.Data[0].Result
					// 同时设置 VideoResults
					generalResponse.VideoResults = []model.VideoResultItem{
						{Url: viggleResp.Data.Data[0].Result},
					}
				}
			} else {
				// code 不为 0 的情况都视为失败
				generalResponse.TaskStatus = "failed"
				// 如果有错误信息，可以更新 Message
				if viggleResp.Data.Message != "" {
					generalResponse.Message = viggleResp.Data.Message
				}
			}
		}

		// 更新任务状态并检查是否需要退款
		failReason := ""
		if generalResponse.TaskStatus == "failed" {
			failReason = generalResponse.Message
			if failReason == "" {
				failReason = "Task failed"
			}
		}
		needRefund := UpdateVideoTaskStatus(taskId, generalResponse.TaskStatus, failReason)
		if needRefund {
			log.Printf("Task %s failed, compensating user", taskId)
			CompensateVideoTask(taskId)
		}

		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 直接使用上游返回的状态码
		c.Data(resp.StatusCode, "application/json", jsonResponse)
		return nil
	} else if videoTask.Provider == "pixverse" {
		// 读取响应体
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to read response body: %v", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}
		defer resp.Body.Close()

		// 打印响应体用于调试
		log.Printf("Pixverse response body: %s", string(body))

		// 解析JSON响应
		var pixverseResp pixverse.PixverseFinalResponse
		if err := json.Unmarshal(body, &pixverseResp); err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to parse response JSON: %v", err),
				"json_parse_error",
				http.StatusInternalServerError,
			)
		}

		// 创建通用响应结构体
		generalResponse := model.GeneralFinalVideoResponse{
			TaskId:      strconv.Itoa(pixverseResp.Resp.Id),
			VideoResult: "",
			VideoId:     strconv.Itoa(pixverseResp.Resp.Id),
			TaskStatus:  "succeed",
			Message:     pixverseResp.ErrMsg,
		}

		if pixverseResp.Resp.Url != "" {
			generalResponse.VideoResult = pixverseResp.Resp.Url
			// 同时设置 VideoResults
			generalResponse.VideoResults = []model.VideoResultItem{
				{Url: pixverseResp.Resp.Url},
			}
		}

		// 检查任务状态，如果ErrCode不为0则为失败
		if pixverseResp.ErrCode != 0 {
			generalResponse.TaskStatus = "failed"
		}

		// 更新任务状态并检查是否需要退款
		failReason := ""
		if generalResponse.TaskStatus == "failed" {
			failReason = pixverseResp.ErrMsg
			if failReason == "" {
				failReason = "Task failed"
			}
		}
		needRefund := UpdateVideoTaskStatus(taskId, generalResponse.TaskStatus, failReason)
		if needRefund {
			log.Printf("Task %s failed, compensating user", taskId)
			CompensateVideoTask(taskId)
		}

		// 将响应转换为JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("Error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 直接使用上游返回的状态码
		c.Data(resp.StatusCode, "application/json", jsonResponse)
		return nil
	} else if videoTask.Provider == "doubao" {

		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to read response body: %v", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 解析JSON响应
		var doubaoResp doubao.DoubaoVideoResult
		if err := json.Unmarshal(body, &doubaoResp); err != nil {
			log.Printf("Failed to parse doubao response: %v, body: %s", err, string(body))
			return openai.ErrorWrapper(
				fmt.Errorf("failed to parse response JSON: %v", err),
				"json_parse_error",
				http.StatusInternalServerError,
			)
		}

		// 创建通用响应结构体
		generalResponse := model.GeneralFinalVideoResponse{
			TaskId:      doubaoResp.ID,
			VideoResult: "",
			VideoId:     doubaoResp.ID,
			Message:     "",
		}

		// 处理任务状态映射
		switch doubaoResp.Status {
		case "queued":
			generalResponse.TaskStatus = "processing"
		case "running":
			generalResponse.TaskStatus = "processing"
		case "succeeded":
			generalResponse.TaskStatus = "succeeded"
			if doubaoResp.Content.VideoURL != "" {
				generalResponse.VideoResult = doubaoResp.Content.VideoURL
				// 同时设置 VideoResults
				generalResponse.VideoResults = []model.VideoResultItem{
					{Url: doubaoResp.Content.VideoURL},
				}
			}
		case "failed":
			generalResponse.TaskStatus = "failed"
			generalResponse.Message = doubaoResp.Error.Message
		default:
			generalResponse.TaskStatus = "unknown"
		}

		// 序列化响应
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}
		// 更新任务状态并检查是否需要退款
		failReason := ""
		if generalResponse.TaskStatus == "failed" {
			failReason = generalResponse.Message
			if failReason == "" {
				failReason = "Task failed"
			}
		}
		needRefund := UpdateVideoTaskStatus(taskId, generalResponse.TaskStatus, failReason)
		if needRefund {
			log.Printf("Task %s failed, compensating user", taskId)
			CompensateVideoTask(taskId)
		}

		// 豆包特殊处理：如果任务成功，需要基于实际token使用量进行补差价
		if generalResponse.TaskStatus == "succeeded" {
			actualQuota := calculateQuotaForDoubao(doubaoResp.Model, int64(doubaoResp.Usage.TotalTokens), c)
			preQuota := videoTask.Quota
			quotaDiff := int64(actualQuota - preQuota) // 计算差价

			// 更新用户配额和统计信息（只处理差价部分）
			if quotaDiff != 0 {
				quotaErr := dbmodel.PostConsumeTokenQuota(c.GetInt("token_id"), quotaDiff)
				if quotaErr != nil {
					log.Printf("Error consuming token quota diff: %v", quotaErr)
				}

				ctx := c.Request.Context()
				cacheErr := dbmodel.CacheUpdateUserQuota(ctx, videoTask.UserId)
				if cacheErr != nil {
					log.Printf("Error update user quota cache: %v", cacheErr)
				}

				dbmodel.UpdateUserUsedQuotaAndRequestCount(videoTask.UserId, quotaDiff)
				dbmodel.UpdateChannelUsedQuota(videoTask.ChannelId, quotaDiff)
			}

			// 更新原有日志记录的Quota和CompletionTokens字段（显示完整的实际费用）
			updateErr := dbmodel.UpdateLogQuotaAndTokens(doubaoResp.ID, int64(actualQuota), doubaoResp.Usage.TotalTokens)
			if updateErr != nil {
				log.Printf("Failed to update log quota and tokens for task %s: %v", doubaoResp.ID, updateErr)
			} else {
				log.Printf("Successfully updated log for task %s: quota=%d, completion_tokens=%d", doubaoResp.ID, actualQuota, doubaoResp.Usage.TotalTokens)
			}
		}

		// 直接使用上游返回的状态码
		c.Data(resp.StatusCode, "application/json", jsonResponse)
		return nil
	} else if videoTask.Provider == "vertexai" {
		defer resp.Body.Close()

		// 首先检查数据库中是否已有存储的URL
		if videoTask.StoreUrl != "" {
			log.Printf("Found existing store URL for task %s: %s", taskId, videoTask.StoreUrl)
			generalResponse := model.GeneralFinalVideoResponse{
				TaskId:       taskId,
				VideoResult:  videoTask.StoreUrl,
				VideoId:      taskId,
				TaskStatus:   "succeed",
				Message:      "Video retrieved from cache",
				VideoResults: []model.VideoResultItem{{Url: videoTask.StoreUrl}},
			}
			jsonResponse, err := json.Marshal(generalResponse)
			if err != nil {
				return openai.ErrorWrapper(fmt.Errorf("error marshaling response: %s", err), "internal_error", http.StatusInternalServerError)
			}
			c.Data(http.StatusOK, "application/json", jsonResponse)
			return nil
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return openai.ErrorWrapper(fmt.Errorf("failed to read response body: %v", err), "internal_error", http.StatusInternalServerError)
		}

		var veoResp map[string]interface{}
		if err := json.Unmarshal(body, &veoResp); err != nil {
			log.Printf("Failed to parse Vertex AI response as JSON. Body: %s", string(body))
			return openai.ErrorWrapper(fmt.Errorf("failed to parse response JSON: %v", err), "json_parse_error", http.StatusInternalServerError)
		}

		// 打印完整的原始响应体（用于调试）
		log.Printf("=== [VEO查询] 完整响应体 for task %s ===", taskId)

		// 处理响应体日志，避免过长的base64内容
		responseBodyStr := string(body)
		if len(responseBodyStr) > 2000 {
			// 如果响应体过长，截取前后部分
			log.Printf("原始响应体 (truncated - too long): %s...%s",
				responseBodyStr[:1000],
				responseBodyStr[len(responseBodyStr)-1000:])
			log.Printf("响应体长度: %d characters", len(responseBodyStr))
		} else {
			log.Printf("原始响应体: %s", responseBodyStr)
		}

		log.Printf("=== [VEO查询] 响应体结构分析 ===")
		printJSONStructure(veoResp, "", 4)
		log.Printf("=== [VEO查询] 响应体分析结束 ===")

		generalResponse := model.GeneralFinalVideoResponse{
			TaskId:     taskId,
			VideoId:    taskId,
			TaskStatus: "processing", // 默认状态
			Message:    "Operation in progress",
		}

		if done, ok := veoResp["done"].(bool); ok && done {
			// 操作已完成，检查结果或错误
			if errorInfo, ok := veoResp["error"].(map[string]interface{}); ok {
				// 操作失败
				generalResponse.TaskStatus = "failed"
				if message, ok := errorInfo["message"].(string); ok {
					generalResponse.Message = message
				} else {
					generalResponse.Message = "Operation failed with an unknown error."
				}
			} else if response, ok := veoResp["response"].(map[string]interface{}); ok {
				// 检查是否被AI安全过滤器拦截
				if raiFilteredCount, hasFiltered := response["raiMediaFilteredCount"]; hasFiltered {
					if filteredCount, ok := raiFilteredCount.(float64); ok && filteredCount > 0 {
						// 内容被过滤了
						generalResponse.TaskStatus = "failed"

						// 获取过滤原因
						var filterReasons []string
						if reasons, hasReasons := response["raiMediaFilteredReasons"].([]interface{}); hasReasons {
							for _, reason := range reasons {
								if reasonStr, ok := reason.(string); ok {
									filterReasons = append(filterReasons, reasonStr)
								}
							}
						}

						if len(filterReasons) > 0 {
							generalResponse.Message = strings.Join(filterReasons, "; ")
							log.Printf("[VEO查询] ❌ 内容被过滤 - 任务:%s, 过滤数量:%v, 原因:%v", taskId, filteredCount, filterReasons)
						} else {
							generalResponse.Message = fmt.Sprintf("Content filtered (count: %.0f)", filteredCount)
							log.Printf("[VEO查询] ❌ 内容被过滤 - 任务:%s, 过滤数量:%v", taskId, filteredCount)
						}
					} else {
						// 没有被过滤，尝试提取视频URI
						videoURIs := extractVeoVideoURIs(response)
						if len(videoURIs) > 0 {
							var processedVideoURIs []string
							responseFormat := c.GetString("response_format")

							// 处理每个视频URI - 并发上传优化
							if responseFormat == "url" {
								// 使用并发上传
								processedVideoURIs = processVideosConcurrently(videoURIs, videoTask.UserId, taskId)
							} else {
								// 如果不需要上传，直接使用原始URI
								processedVideoURIs = videoURIs
							}

							// 构建响应结果
							generalResponse.TaskStatus = "succeed"
							generalResponse.Message = "Video generated successfully."
							generalResponse.VideoResult = processedVideoURIs[0] // 保持兼容性，设置第一个视频

							// 设置所有视频结果
							generalResponse.VideoResults = make([]model.VideoResultItem, len(processedVideoURIs))
							for i, uri := range processedVideoURIs {
								generalResponse.VideoResults[i] = model.VideoResultItem{Url: uri}
							}
						} else {
							// 没有被过滤但也没有找到视频
							log.Printf("[VEO查询] ❌ 操作完成但未找到视频结果 - 任务:%s", taskId)
							log.Printf("[VEO查询] Response字段内容: %+v", response)

							generalResponse.TaskStatus = "failed"
							generalResponse.Message = "Operation completed, but no video result was found."
						}
					}
				} else {
					// 没有过滤信息，直接尝试提取视频URI
					videoURIs := extractVeoVideoURIs(response)
					if len(videoURIs) > 0 {
						var processedVideoURIs []string
						responseFormat := c.GetString("response_format")

						// 处理每个视频URI - 并发上传优化
						if responseFormat == "url" {
							// 使用并发上传
							processedVideoURIs = processVideosConcurrently(videoURIs, videoTask.UserId, taskId)
						} else {
							// 如果不需要上传，直接使用原始URI
							processedVideoURIs = videoURIs
						}

						// 构建响应结果
						generalResponse.TaskStatus = "succeed"
						generalResponse.Message = "Video generated successfully."
						generalResponse.VideoResult = processedVideoURIs[0] // 保持兼容性，设置第一个视频

						// 设置所有视频结果
						generalResponse.VideoResults = make([]model.VideoResultItem, len(processedVideoURIs))
						for i, uri := range processedVideoURIs {
							generalResponse.VideoResults[i] = model.VideoResultItem{Url: uri}
						}
					} else {
						// 完成了，但未找到视频URI也未找到错误
						log.Printf("[VEO查询] ❌ 操作完成但未找到视频结果 - 任务:%s", taskId)
						log.Printf("[VEO查询] Response字段内容: %+v", response)

						// 检查response中的具体字段
						if videos, hasVideos := response["videos"]; hasVideos {
							log.Printf("[VEO查询] Response.videos字段: %+v", videos)
						} else {
							log.Printf("[VEO查询] Response中缺少videos字段")
						}

						if generatedSamples, hasSamples := response["generatedSamples"]; hasSamples {
							log.Printf("[VEO查询] Response.generatedSamples字段: %+v", generatedSamples)
						} else {
							log.Printf("[VEO查询] Response中缺少generatedSamples字段")
						}

						generalResponse.TaskStatus = "failed"
						generalResponse.Message = "Operation completed, but no video result was found."
					}
				}
			} else {
				// 完成了，但没有response和error字段
				generalResponse.TaskStatus = "failed"
				generalResponse.Message = "Operation completed with an invalid response format."
			}
		}

		// 更新数据库任务状态并在必要时处理退款
		failReason := ""
		if generalResponse.TaskStatus == "failed" {
			failReason = generalResponse.Message
		}
		needRefund := UpdateVideoTaskStatus(taskId, generalResponse.TaskStatus, failReason)
		if needRefund {
			log.Printf("Task %s failed, compensating user", taskId)
			CompensateVideoTask(taskId)
		}

		// 序列化并返回响应给客户端
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(fmt.Errorf("error marshaling response: %s", err), "internal_error", http.StatusInternalServerError)
		}

		c.Data(http.StatusOK, "application/json", jsonResponse)
		return nil
	} else if videoTask.Provider == "ali" {
		defer resp.Body.Close()

		// 首先检查数据库中是否已有存储的URL
		if videoTask.StoreUrl != "" {
			log.Printf("Found existing store URL for Ali task %s: %s", taskId, videoTask.StoreUrl)
			generalResponse := model.GeneralFinalVideoResponse{
				TaskId:       taskId,
				VideoResult:  videoTask.StoreUrl,
				VideoId:      taskId,
				TaskStatus:   "succeed",
				Message:      "Video retrieved from cache",
				VideoResults: []model.VideoResultItem{{Url: videoTask.StoreUrl}},
			}
			jsonResponse, err := json.Marshal(generalResponse)
			if err != nil {
				return openai.ErrorWrapper(fmt.Errorf("error marshaling response: %s", err), "internal_error", http.StatusInternalServerError)
			}
			c.Data(http.StatusOK, "application/json", jsonResponse)
			return nil
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to read response body: %v", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 打印完整的阿里云响应体
		log.Printf("Ali video query response body: %s", string(body))

		// 解析JSON响应
		var aliResp ali.AliVideoQueryResponse
		if err := json.Unmarshal(body, &aliResp); err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to parse response JSON: %v", err),
				"json_parse_error",
				http.StatusInternalServerError,
			)
		}

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralFinalVideoResponse{
			TaskId:      taskId,
			VideoId:     taskId,
			TaskStatus:  "processing", // 默认状态
			Message:     "",
			VideoResult: "",
		}

		// 处理响应
		if aliResp.Code != "" {
			// 查询API本身出错
			generalResponse.TaskStatus = "failed"
			generalResponse.Message = fmt.Sprintf("Query error: %s, request_id: %s", aliResp.Message, aliResp.RequestID)
		} else if aliResp.Output != nil {
			// 根据任务状态处理
			switch aliResp.Output.TaskStatus {
			case "SUCCEEDED":
				generalResponse.TaskStatus = "succeed"
				generalResponse.Message = fmt.Sprintf("Video generation completed, request_id: %s", aliResp.RequestID)
				if aliResp.Output.VideoURL != "" {
					// 保存URL到数据库
					if updateErr := dbmodel.UpdateVideoStoreUrl(taskId, aliResp.Output.VideoURL); updateErr != nil {
						log.Printf("Failed to save Ali video URL for task %s: %v", taskId, updateErr)
					} else {
						log.Printf("Successfully saved Ali video URL for task %s: %s", taskId, aliResp.Output.VideoURL)
					}

					generalResponse.VideoResult = aliResp.Output.VideoURL
					generalResponse.VideoResults = []model.VideoResultItem{
						{Url: aliResp.Output.VideoURL},
					}
				}
			case "FAILED":
				generalResponse.TaskStatus = "failed"
				generalResponse.Message = fmt.Sprintf("Video generation failed, request_id: %s", aliResp.RequestID)
			case "UNKNOWN":
				generalResponse.TaskStatus = "failed"
				generalResponse.Message = fmt.Sprintf("Task expired or unknown, request_id: %s", aliResp.RequestID)
			case "PROCESSING", "RUNNING":
				generalResponse.TaskStatus = "processing"
				generalResponse.Message = fmt.Sprintf("Video generation in progress, request_id: %s", aliResp.RequestID)
			default:
				generalResponse.TaskStatus = "processing"
				generalResponse.Message = fmt.Sprintf("Video generation in progress (status: %s), request_id: %s", aliResp.Output.TaskStatus, aliResp.RequestID)
			}
		} else {
			// 无输出，可能是API错误
			generalResponse.TaskStatus = "failed"
			generalResponse.Message = fmt.Sprintf("No output received, request_id: %s", aliResp.RequestID)
		}

		// 更新数据库任务状态并在必要时处理退款
		failReason := ""
		if generalResponse.TaskStatus == "failed" {
			failReason = generalResponse.Message // 包含request_id的完整错误信息
		}
		needRefund := UpdateVideoTaskStatus(taskId, generalResponse.TaskStatus, failReason)
		if needRefund {
			log.Printf("Ali task %s failed, compensating user", taskId)
			CompensateVideoTask(taskId)
		}

		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 返回响应
		c.Data(http.StatusOK, "application/json", jsonResponse)
		return nil
	} else if videoTask.Provider == "sora" {
		defer resp.Body.Close()

		// 首先检查数据库中是否已有存储的URL
		if videoTask.StoreUrl != "" {
			log.Printf("Found existing store URL for Sora task %s: %s", taskId, videoTask.StoreUrl)
			generalResponse := model.GeneralFinalVideoResponse{
				TaskId:       taskId,
				VideoResult:  videoTask.StoreUrl,
				VideoId:      taskId,
				TaskStatus:   "succeed",
				Message:      "Video retrieved from cache",
				VideoResults: []model.VideoResultItem{{Url: videoTask.StoreUrl}},
				Duration:     videoTask.Duration,
			}
			jsonResponse, err := json.Marshal(generalResponse)
			if err != nil {
				return openai.ErrorWrapper(fmt.Errorf("error marshaling response: %s", err), "internal_error", http.StatusInternalServerError)
			}
			c.Data(http.StatusOK, "application/json", jsonResponse)
			return nil
		}

		// 读取状态查询响应
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to read response body: %v", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		log.Printf("Sora video query response body: %s", string(body))

		// 解析 Sora 状态响应
		var soraResp openai.SoraVideoResponse
		if err := json.Unmarshal(body, &soraResp); err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to parse Sora response JSON: %v", err),
				"json_parse_error",
				http.StatusInternalServerError,
			)
		}

		// 创建初始响应
		generalResponse := model.GeneralFinalVideoResponse{
			TaskId:     taskId,
			VideoId:    taskId,
			TaskStatus: "processing",
			Message:    "Video is still processing",
			Duration:   soraResp.Seconds, // 已经是 string 类型
		}

		// 根据状态处理
		switch soraResp.Status {
		case "completed":
			// 视频已完成，下载并上传到 R2
			log.Printf("Sora video completed, downloading: task_id=%s", taskId)

			videoUrl, downloadErr := downloadAndUploadSoraVideo(channel, taskId, videoTask.UserId)
			if downloadErr != nil {
				// 下载失败，但状态是完成的，可能是暂时性错误
				generalResponse.TaskStatus = "processing"
				generalResponse.Message = fmt.Sprintf("Video completed but download failed, please retry: %v", downloadErr)
				log.Printf("Failed to download Sora video for task %s: %v", taskId, downloadErr)
			} else {
				// 下载成功，保存URL到数据库
				if updateErr := dbmodel.UpdateVideoStoreUrl(taskId, videoUrl); updateErr != nil {
					log.Printf("Failed to save Sora video URL for task %s: %v", taskId, updateErr)
				} else {
					log.Printf("Successfully saved Sora video URL for task %s: %s", taskId, videoUrl)
				}

				generalResponse.TaskStatus = "succeed"
				generalResponse.Message = "Video generation completed and uploaded to R2"
				generalResponse.VideoResult = videoUrl
				generalResponse.VideoResults = []model.VideoResultItem{{Url: videoUrl}}
			}

		case "failed":
			generalResponse.TaskStatus = "failed"
			if soraResp.Error != nil {
				generalResponse.Message = fmt.Sprintf("Video generation failed: %s", soraResp.Error.Message)
			} else {
				generalResponse.Message = "Video generation failed"
			}

		case "queued", "processing":
			generalResponse.TaskStatus = "processing"
			if soraResp.Progress > 0 {
				generalResponse.Message = fmt.Sprintf("Video generation in progress (%d%%)", soraResp.Progress)
			} else {
				generalResponse.Message = "Video generation in progress"
			}

		default:
			generalResponse.TaskStatus = "processing"
			generalResponse.Message = fmt.Sprintf("Video status: %s", soraResp.Status)
		}

		// 更新数据库任务状态并在必要时处理退款
		failReason := ""
		if generalResponse.TaskStatus == "failed" {
			failReason = generalResponse.Message
		}
		needRefund := UpdateVideoTaskStatus(taskId, generalResponse.TaskStatus, failReason)
		if needRefund {
			log.Printf("Sora task %s failed, compensating user", taskId)
			CompensateVideoTask(taskId)
		}

		// 返回响应
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		c.Data(http.StatusOK, "application/json", jsonResponse)
		return nil
	}
	return nil
}

// downloadAndUploadSoraVideo 下载 Sora 视频并上传到 R2
func downloadAndUploadSoraVideo(channel *dbmodel.Channel, videoId string, userId int) (string, error) {
	// 构建下载 URL
	baseUrl := *channel.BaseURL
	if baseUrl == "" {
		baseUrl = "https://api.openai.com"
	}
	downloadUrl := fmt.Sprintf("%s/v1/videos/%s/content", baseUrl, videoId)

	log.Printf("Downloading Sora video: %s", downloadUrl)

	// 创建下载请求
	req, err := http.NewRequest("GET", downloadUrl, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create download request: %w", err)
	}

	// 设置授权头
	req.Header.Set("Authorization", "Bearer "+channel.Key)

	// 发送请求
	client := &http.Client{
		Timeout: 5 * time.Minute, // 5分钟超时，视频下载可能需要时间
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download video: %w", err)
	}
	defer resp.Body.Close()

	// 检查状态码
	if resp.StatusCode == 404 {
		return "", fmt.Errorf("video not ready yet (404)")
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("download failed with status %d: %s", resp.StatusCode, string(body))
	}

	// 读取视频数据
	videoData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read video data: %w", err)
	}

	log.Printf("Downloaded Sora video: %d bytes", len(videoData))

	// 转换为 base64
	base64Data := base64.StdEncoding.EncodeToString(videoData)

	// 上传到 R2
	videoUrl, err := UploadVideoBase64ToR2(base64Data, userId, "mp4")
	if err != nil {
		return "", fmt.Errorf("failed to upload to R2: %w", err)
	}

	log.Printf("Successfully uploaded Sora video to R2: %s", videoUrl)
	return videoUrl, nil
}

// 豆包专用的quota计算函数（基于token，用于查询结果时的实际计费）
func calculateQuotaForDoubao(modelName string, tokens int64, c *gin.Context) int64 {
	var basePriceCNY float64

	// 根据不同模型设置基础价格（人民币，单位：元/百万token）
	switch {
	case strings.Contains(modelName, "doubao-seedance-1-0-lite"):
		basePriceCNY = 10 / 1000000.0 // 轻量版价格
	case strings.Contains(modelName, "doubao-seedance-1-0-pro"):
		basePriceCNY = 15 / 1000000.0 // 专业版价格更高
	case strings.Contains(modelName, "doubao-seaweed"):
		basePriceCNY = 30 / 1000000.0 // 海草版价格适中
	case strings.Contains(modelName, "wan2.1-14b"):
		basePriceCNY = 50 / 1000000.0 // 标准价格
	default:
		basePriceCNY = 50 / 1000000.0 // 默认价格
	}

	// 计算人民币费用
	cnyAmount := basePriceCNY * float64(tokens)

	// 转换为美元
	usdAmount, exchangeErr := convertCNYToUSD(cnyAmount)
	if exchangeErr != nil {
		// 如果汇率转换失败，使用固定汇率7.2作为备选方案
		log.Printf("Failed to get exchange rate for Doubao pricing: %v, using fallback rate 7.2", exchangeErr)
		usdAmount = cnyAmount / 7.2
	}

	quota := int64(usdAmount * config.QuotaPerUnit)
	log.Printf("Doubao pricing calculation: model=%s, tokens=%d, cny=%.6f, usd=%.6f, quota=%d",
		modelName, tokens, cnyAmount, usdAmount, quota)

	return quota
}

func handleMinimaxResponse(c *gin.Context, channel *dbmodel.Channel, taskId string) *model.ErrorWithStatusCode {
	// 第一次请求，获取初始状态
	url := fmt.Sprintf("%s/v1/query/video_generation?task_id=%s", *channel.BaseURL, taskId)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return openai.ErrorWrapper(fmt.Errorf("failed to create request: %v", err), "api_error", http.StatusInternalServerError)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+channel.Key)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openai.ErrorWrapper(fmt.Errorf("failed to send request: %v", err), "api_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(fmt.Errorf("failed to read response body: %v", err), "internal_error", http.StatusInternalServerError)
	}

	var minimaxResp model.FinalVideoResponse
	if err := json.Unmarshal(body, &minimaxResp); err != nil {
		return openai.ErrorWrapper(fmt.Errorf("failed to parse response JSON: %v", err), "json_parse_error", http.StatusInternalServerError)
	}

	generalResponse := model.GeneralFinalVideoResponse{
		TaskId:      taskId,
		TaskStatus:  mapTaskStatusMinimax(minimaxResp.Status),
		Message:     minimaxResp.BaseResp.StatusMsg,
		VideoResult: "",
	}

	// 如果 FileID 为空，直接返回当前状态
	if minimaxResp.FileID == "" {
		// 更新任务状态并检查是否需要退款
		failReason := ""
		if generalResponse.TaskStatus == "failed" {
			failReason = generalResponse.Message
			if failReason == "" {
				failReason = "Task failed"
			}
		}
		needRefund := UpdateVideoTaskStatus(taskId, generalResponse.TaskStatus, failReason)
		if needRefund {
			log.Printf("Task %s failed, compensating user", taskId)
			CompensateVideoTask(taskId)
		}

		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(fmt.Errorf("Error marshaling response: %s", err), "internal_error", http.StatusInternalServerError)
		}
		c.Data(resp.StatusCode, "application/json", jsonResponse)
		return nil
	}

	// 如果 FileID 不为空，获取文件信息
	fileUrl := fmt.Sprintf("%s/v1/files/retrieve?file_id=%s", *channel.BaseURL, minimaxResp.FileID)
	fileReq, err := http.NewRequest("GET", fileUrl, nil)
	if err != nil {
		return openai.ErrorWrapper(fmt.Errorf("failed to create file request: %v", err), "api_error", http.StatusInternalServerError)
	}
	fileReq.Header.Set("Content-Type", "application/json")
	fileReq.Header.Set("Authorization", "Bearer "+channel.Key)

	fileResp, err := client.Do(fileReq)
	if err != nil {
		return openai.ErrorWrapper(fmt.Errorf("failed to send file request: %v", err), "api_error", http.StatusInternalServerError)
	}
	defer fileResp.Body.Close()

	fileBody, err := io.ReadAll(fileResp.Body)
	if err != nil {
		return openai.ErrorWrapper(fmt.Errorf("failed to read file response body: %v", err), "internal_error", http.StatusInternalServerError)
	}

	var fileResponse model.MinimaxFinalResponse
	if err := json.Unmarshal(fileBody, &fileResponse); err != nil {
		return openai.ErrorWrapper(fmt.Errorf("failed to parse file response JSON: %v", err), "json_parse_error", http.StatusInternalServerError)
	}

	generalResponse.VideoResult = fileResponse.File.DownloadURL
	// 同时设置 VideoResults
	generalResponse.VideoResults = []model.VideoResultItem{
		{Url: fileResponse.File.DownloadURL},
	}
	generalResponse.TaskStatus = "succeed" // 假设有 FileID 且能获取到下载 URL 就意味着成功

	// 更新任务状态并检查是否需要退款
	failReason := ""
	if generalResponse.TaskStatus == "failed" {
		failReason = generalResponse.Message
		if failReason == "" {
			failReason = "Task failed"
		}
	}
	needRefund := UpdateVideoTaskStatus(taskId, generalResponse.TaskStatus, failReason)
	if needRefund {
		log.Printf("Task %s failed, compensating user", taskId)
		CompensateVideoTask(taskId)
	}

	jsonResponse, err := json.Marshal(generalResponse)
	if err != nil {
		return openai.ErrorWrapper(fmt.Errorf("Error marshaling response: %s", err), "internal_error", http.StatusInternalServerError)
	}

	c.Data(fileResp.StatusCode, "application/json", jsonResponse)
	return nil
}

func UpdateVideoTaskStatus(taskid string, status string, failreason string) bool {
	videoTask, err := dbmodel.GetVideoTaskById(taskid)
	if err != nil {
		log.Printf("Failed to get video task for update: %v", err)
		return false
	}

	// 记录原始状态
	oldStatus := videoTask.Status

	// 检查状态是否真的发生了变化
	if oldStatus == status {
		log.Printf("Task %s status unchanged: %s", taskid, status)
		return false
	}

	// 更新字段
	videoTask.Status = status
	if failreason != "" {
		videoTask.FailReason = failreason
	}

	// 尝试更新数据库
	err = videoTask.Update()
	if err != nil {
		log.Printf("Failed to update video task %s using model method: %v", taskid, err)

		// 如果Update失败，尝试直接使用SQL更新作为回退方案
		log.Printf("Attempting direct SQL update for task %s", taskid)
		updateFields := map[string]interface{}{
			"status": status,
		}
		if failreason != "" {
			updateFields["fail_reason"] = failreason
		}

		result := dbmodel.DB.Model(&dbmodel.Video{}).
			Where("task_id = ?", taskid).
			Updates(updateFields)

		if result.Error != nil {
			log.Printf("Direct SQL update also failed for task %s: %v", taskid, result.Error)
			return false
		}

		if result.RowsAffected == 0 {
			log.Printf("No rows affected for task %s update - record may not exist", taskid)
			return false
		}

		log.Printf("Direct SQL update successful for task %s, affected rows: %d", taskid, result.RowsAffected)
	} else {
		log.Printf("Model update successful for task %s", taskid)
	}

	log.Printf("Task %s status updated from '%s' to '%s'", taskid, oldStatus, status)

	// 返回是否需要退款：只有当状态变为失败且之前不是失败状态时才退款
	// 空字符串被视为非失败状态，这是正确的，因为任务刚创建时就是这个状态
	needRefund := (oldStatus != "failed" && status == "failed")
	log.Printf("Task %s refund decision: oldStatus='%s', newStatus='%s', needRefund=%v", taskid, oldStatus, status, needRefund)

	return needRefund
}

func CompensateVideoTask(taskid string) {
	videoTask, err := dbmodel.GetVideoTaskById(taskid)
	if err != nil {
		log.Printf("Failed to get video task for compensation: %v", err)
		return
	}
	quota := videoTask.Quota
	log.Printf("Compensating user %d for failed task %s with quota %d", videoTask.UserId, taskid, quota)

	// 1. 补偿用户配额（增加余额、减少已使用配额和请求次数）
	err = dbmodel.CompensateVideoTaskQuota(videoTask.UserId, quota)
	if err != nil {
		log.Printf("Failed to compensate user quota for task %s: %v", taskid, err)
		return
	}
	log.Printf("Successfully compensated user %d quota for task %s", videoTask.UserId, taskid)

	// 2. 补偿渠道配额（减少渠道已使用配额）
	err = dbmodel.CompensateChannelQuota(videoTask.ChannelId, quota)
	if err != nil {
		log.Printf("Failed to compensate channel quota for task %s: %v", taskid, err)
	} else {
		log.Printf("Successfully compensated channel %d quota for task %s", videoTask.ChannelId, taskid)
	}

	log.Printf("Successfully completed compensation for task %s: user %d and channel %d restored quota %d", taskid, videoTask.UserId, videoTask.ChannelId, quota)
}

// 汇率API响应结构体
type ExchangeRateResponse struct {
	Result             string             `json:"result"`
	BaseCode           string             `json:"base_code"`
	ConversionRates    map[string]float64 `json:"conversion_rates"`
	TimeLastUpdateUnix int64              `json:"time_last_update_unix"`
}

// 中国银行汇率API响应结构体
type BOCRateResponse struct {
	Success bool `json:"success"`
	Result  struct {
		USD float64 `json:"USD"`
	} `json:"result"`
}

// 汇率管理器
type ExchangeRateManager struct {
	cnyToUSDRate  float64
	lastUpdate    time.Time
	cacheDuration time.Duration
}

var exchangeManager = &ExchangeRateManager{
	cacheDuration: 10 * time.Minute, // 缓存10分钟
}

// 从ExchangeRate-API获取汇率
func fetchRateFromExchangeRateAPI() (float64, error) {
	url := "https://api.exchangerate-api.com/v4/latest/CNY"

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch from ExchangeRate-API: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("ExchangeRate-API returned status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response body: %v", err)
	}

	var exchangeRate ExchangeRateResponse
	if err := json.Unmarshal(body, &exchangeRate); err != nil {
		return 0, fmt.Errorf("failed to parse JSON response: %v", err)
	}

	usdRate, exists := exchangeRate.ConversionRates["USD"]
	if !exists {
		return 0, fmt.Errorf("USD rate not found in response")
	}

	return usdRate, nil
}

// 从Fixer.io获取汇率（备选方案）
func fetchRateFromFixer() (float64, error) {
	// 注意：免费版需要注册获取API key
	url := "http://data.fixer.io/api/latest?access_key=YOUR_API_KEY&base=CNY&symbols=USD"

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch from Fixer: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("Fixer API returned status code: %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("failed to parse Fixer response: %v", err)
	}

	if rates, ok := result["rates"].(map[string]interface{}); ok {
		if usdRate, ok := rates["USD"].(float64); ok {
			return usdRate, nil
		}
	}

	return 0, fmt.Errorf("USD rate not found in Fixer response")
}

// 获取人民币对美元汇率（带缓存）
func (e *ExchangeRateManager) getCNYToUSDRate() (float64, error) {
	// 检查缓存是否有效
	if time.Since(e.lastUpdate) < e.cacheDuration && e.cnyToUSDRate > 0 {
		log.Printf("Using cached exchange rate: %.6f", e.cnyToUSDRate)
		return e.cnyToUSDRate, nil
	}

	log.Printf("Fetching new exchange rate...")

	// 尝试多个API源
	var rate float64
	var err error

	// 首先尝试ExchangeRate-API
	rate, err = fetchRateFromExchangeRateAPI()
	if err != nil {
		log.Printf("ExchangeRate-API failed: %v", err)

		// 如果第一个API失败，可以尝试其他API
		// rate, err = fetchRateFromFixer()
		// if err != nil {
		//     log.Printf("Fixer API also failed: %v", err)
		//     // 使用默认汇率作为最后的备选方案
		//     rate = 0.14 // 大概的CNY to USD汇率
		//     log.Printf("Using fallback exchange rate: %.6f", rate)
		// }

		// 如果API失败，使用默认汇率
		rate = 0.14 // 大概的CNY to USD汇率
		log.Printf("Using fallback exchange rate: %.6f", rate)
	}

	// 更新缓存
	e.cnyToUSDRate = rate
	e.lastUpdate = time.Now()

	log.Printf("Updated exchange rate: %.6f CNY to USD", rate)
	return rate, nil
}

// 将人民币转换为美元
func convertCNYToUSD(cnyAmount float64) (float64, error) {
	rate, err := exchangeManager.getCNYToUSDRate()
	if err != nil {
		return 0, err
	}

	usdAmount := cnyAmount * rate
	log.Printf("Converted %.6f CNY to %.6f USD (rate: %.6f)", cnyAmount, usdAmount, rate)
	return usdAmount, nil
}

// 手动更新汇率（可以通过API调用）
func refreshExchangeRate() error {
	exchangeManager.lastUpdate = time.Time{} // 重置缓存时间
	_, err := exchangeManager.getCNYToUSDRate()
	return err
}

// printJSONStructure 打印JSON结构，但不显示具体内容（避免base64数据过长）
func printJSONStructure(data interface{}, prefix string, maxDepth int) {
	if maxDepth <= 0 {
		return
	}

	switch v := data.(type) {
	case map[string]interface{}:
		log.Printf("%s{", prefix)
		for key, value := range v {
			switch v := value.(type) {
			case string:
				if len(v) > 100 {
					log.Printf("%s  \"%s\": \"<string length: %d>\"", prefix, key, len(v))
				} else {
					log.Printf("%s  \"%s\": \"%s\"", prefix, key, v)
				}
			case bool:
				log.Printf("%s  \"%s\": %v", prefix, key, value)
			case float64:
				log.Printf("%s  \"%s\": %v", prefix, key, value)
			case []interface{}:
				log.Printf("%s  \"%s\": [", prefix, key)
				if len(value.([]interface{})) > 0 {
					printJSONStructure(value.([]interface{})[0], prefix+"    ", maxDepth-1)
					if len(value.([]interface{})) > 1 {
						log.Printf("%s    ... (%d more items)", prefix, len(value.([]interface{}))-1)
					}
				}
				log.Printf("%s  ]", prefix)
			case map[string]interface{}:
				log.Printf("%s  \"%s\":", prefix, key)
				printJSONStructure(value, prefix+"    ", maxDepth-1)
			case nil:
				log.Printf("%s  \"%s\": null", prefix, key)
			default:
				log.Printf("%s  \"%s\": <%T>", prefix, key, value)
			}
		}
		log.Printf("%s}", prefix)
	case []interface{}:
		log.Printf("%s[", prefix)
		if len(v) > 0 {
			printJSONStructure(v[0], prefix+"  ", maxDepth-1)
			if len(v) > 1 {
				log.Printf("%s  ... (%d more items)", prefix, len(v)-1)
			}
		}
		log.Printf("%s]", prefix)
	default:
		log.Printf("%s<%T>", prefix, v)
	}
}

// extractVeoVideoURI 从 Vertex AI Veo 操作响应中提取视频URI或base64数据（保持兼容性，仅返回第一个视频）
func extractVeoVideoURI(response map[string]interface{}) string {
	videoURIs := extractVeoVideoURIs(response)
	if len(videoURIs) > 0 {
		return videoURIs[0]
	}
	return ""
}

// extractVeoVideoURIs 从 Vertex AI Veo 操作响应中提取所有视频URI或base64数据
func extractVeoVideoURIs(response map[string]interface{}) []string {
	var videoURIs []string

	log.Printf("[VEO视频提取] 开始解析响应中的视频URI")
	log.Printf("[VEO视频提取] 响应中的顶级字段: %+v", func() []string {
		keys := make([]string, 0, len(response))
		for k := range response {
			keys = append(keys, k)
		}
		return keys
	}())

	// 检查 fetchPredictOperation 格式 (`videos` 字段)
	if videos, ok := response["videos"].([]interface{}); ok && len(videos) > 0 {
		log.Printf("[VEO视频提取] 找到videos字段，包含 %d 个视频", len(videos))
		for i, videoInterface := range videos {
			if video, ok := videoInterface.(map[string]interface{}); ok {
				log.Printf("[VEO视频提取] 视频 %d 的字段: %+v", i, func() []string {
					keys := make([]string, 0, len(video))
					for k := range video {
						keys = append(keys, k)
					}
					return keys
				}())

				// 优先检查是否有 GCS URI
				if gcsUri, ok := video["gcsUri"].(string); ok && gcsUri != "" {
					log.Printf("[VEO视频提取] ✅ 找到GCS URI: %s", gcsUri)
					videoURIs = append(videoURIs, gcsUri)
					continue
				}
				// 检查是否有 base64 编码的视频数据
				if bytesBase64, ok := video["bytesBase64Encoded"].(string); ok && bytesBase64 != "" {
					log.Printf("[VEO视频提取] ✅ 找到base64数据，长度: %d", len(bytesBase64))
					videoURIs = append(videoURIs, "data:video/mp4;base64,"+bytesBase64)
				}
			} else {
				log.Printf("[VEO视频提取] ⚠️  视频 %d 不是map格式: %T", i, videoInterface)
			}
		}
	} else {
		log.Printf("[VEO视频提取] ❌ 未找到videos字段或为空")
	}

	// 检查标准长轮询操作格式 (`generatedSamples` 字段)
	if generatedSamples, ok := response["generatedSamples"].([]interface{}); ok && len(generatedSamples) > 0 {
		log.Printf("[VEO视频提取] 找到generatedSamples字段，包含 %d 个样本", len(generatedSamples))
		for i, sampleInterface := range generatedSamples {
			if sample, ok := sampleInterface.(map[string]interface{}); ok {
				if video, ok := sample["video"].(map[string]interface{}); ok {
					log.Printf("[VEO视频提取] 样本 %d 的video字段: %+v", i, func() []string {
						keys := make([]string, 0, len(video))
						for k := range video {
							keys = append(keys, k)
						}
						return keys
					}())

					// 优先检查是否有 URI
					if uri, ok := video["uri"].(string); ok && uri != "" {
						log.Printf("[VEO视频提取] ✅ 找到URI: %s", uri)
						videoURIs = append(videoURIs, uri)
						continue
					}
					// 检查是否有 base64 编码的视频数据
					if bytesBase64, ok := video["bytesBase64Encoded"].(string); ok && bytesBase64 != "" {
						log.Printf("[VEO视频提取] ✅ 找到base64数据，长度: %d", len(bytesBase64))
						videoURIs = append(videoURIs, "data:video/mp4;base64,"+bytesBase64)
					}
				} else {
					log.Printf("[VEO视频提取] ⚠️  样本 %d 中未找到video字段", i)
				}
			} else {
				log.Printf("[VEO视频提取] ⚠️  样本 %d 不是map格式: %T", i, sampleInterface)
			}
		}
	} else {
		log.Printf("[VEO视频提取] ❌ 未找到generatedSamples字段或为空")
	}

	log.Printf("[VEO视频提取] 最终提取到 %d 个视频URI", len(videoURIs))
	return videoURIs
}

// processVideosConcurrently 并发处理多个视频上传
func processVideosConcurrently(videoURIs []string, userId int, taskId string) []string {
	type uploadResult struct {
		index int
		url   string
		err   error
	}

	results := make(chan uploadResult, len(videoURIs))
	processedVideoURIs := make([]string, len(videoURIs))

	// 启动并发上传协程
	for i, videoURI := range videoURIs {
		go func(index int, uri string) {
			var finalURL string
			var uploadErr error

			// 如果是 base64 数据，则上传到 R2
			if strings.HasPrefix(uri, "data:video/mp4;base64,") {
				base64Data := strings.TrimPrefix(uri, "data:video/mp4;base64,")
				finalURL, uploadErr = UploadVideoBase64ToR2(base64Data, userId, "mp4")
				if uploadErr != nil {
					log.Printf("Failed to upload video %d to R2 for task %s: %v", index, taskId, uploadErr)
					// 上传失败时使用原始base64数据
					finalURL = uri
				} else {
					log.Printf("Successfully uploaded video %d to R2 for task %s: %s", index, taskId, finalURL)
				}
			} else {
				// 不是base64数据，直接使用原URI
				finalURL = uri
			}

			results <- uploadResult{
				index: index,
				url:   finalURL,
				err:   uploadErr,
			}
		}(i, videoURI)
	}

	// 收集所有结果
	for i := 0; i < len(videoURIs); i++ {
		result := <-results
		processedVideoURIs[result.index] = result.url
	}

	// 保存所有URL到数据库（JSON化存储）
	if len(processedVideoURIs) > 0 {
		// 将URL数组JSON化为字符串
		if urlsJson, err := json.Marshal(processedVideoURIs); err == nil {
			if updateErr := dbmodel.UpdateVideoStoreUrl(taskId, string(urlsJson)); updateErr != nil {
				log.Printf("Failed to save store URLs for task %s: %v", taskId, updateErr)
			} else {
				log.Printf("Successfully saved all store URLs for task %s: %v", taskId, processedVideoURIs)
			}
		} else {
			log.Printf("Failed to marshal URLs for task %s: %v", taskId, err)
			// 如果JSON化失败，至少保存第一个URL
			if updateErr := dbmodel.UpdateVideoStoreUrl(taskId, processedVideoURIs[0]); updateErr != nil {
				log.Printf("Failed to save fallback store URL for task %s: %v", taskId, updateErr)
			}
		}
	}

	return processedVideoURIs
}
