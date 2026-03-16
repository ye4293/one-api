package controller

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
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
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	relaychannel "github.com/songquanpeng/one-api/relay/channel"
	"github.com/songquanpeng/one-api/relay/channel/ali"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/channel/runway"
	"github.com/songquanpeng/one-api/relay/channel/vertexai"
	relayhelper "github.com/songquanpeng/one-api/relay/helper"
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

func DoVideoRequest(c *gin.Context, modelName string) *model.ErrorWithStatusCode {
	ctx := c.Request.Context()
	var videoRequest model.VideoRequest
	err := common.UnmarshalBodyReusable(c, &videoRequest)
	meta := util.GetRelayMeta(c)
	if err != nil {
		return openai.ErrorWrapper(err, "invalid_text_request", http.StatusBadRequest)
	}

	// 适配器路由：已迁移供应商由对应 VideoAdaptor 处理
	if adaptor := relayhelper.GetVideoAdaptor(modelName); adaptor != nil {
		return invokeVideoAdaptorRequest(c, ctx, adaptor, &videoRequest, meta)
	}

	if strings.HasPrefix(modelName, "video-01") ||
		strings.HasPrefix(modelName, "S2V-01") ||
		strings.HasPrefix(modelName, "T2V-01") ||
		strings.HasPrefix(modelName, "I2V-01") ||
		strings.HasPrefix(strings.ToLower(modelName), "minimax") {
		return handleMinimaxVideoRequest(c, ctx, videoRequest, meta)
	} else if modelName == "cogvideox" {
		return handleZhipuVideoRequest(c, ctx, videoRequest, meta)
	} else if modelName == "kling-identify-face" {
		DoIdentifyFace(c)
		return nil
	} else if modelName == "kling-advanced-lip-sync" {
		DoAdvancedLipSync(c)
		return nil
	} else if modelName == "gen3a_turbo" {
		return handleRunwayVideoRequest(c, ctx, videoRequest, meta)
	} else if strings.HasPrefix(modelName, "veo") {
		return handleVeoVideoRequest(c, ctx, videoRequest, meta)
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

	// 构建请求 URL，Azure 渠道需要添加 /openai 前缀
	baseUrl := *originalChannel.BaseURL
	if baseUrl == "" {
		baseUrl = "https://api.openai.com"
	}
	var fullRequestUrl string
	if originalChannel.Type == common.ChannelTypeAzure {
		fullRequestUrl = fmt.Sprintf("%s/openai/v1/videos/%s/remix", baseUrl, remixReq.VideoID)
	} else {
		fullRequestUrl = fmt.Sprintf("%s/v1/videos/%s/remix", baseUrl, remixReq.VideoID)
	}

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

	// 使用原渠道的 key，Azure 渠道使用 Api-key header
	req.Header.Set("Content-Type", "application/json")
	if originalChannel.Type == common.ChannelTypeAzure {
		req.Header.Set("Api-key", originalChannel.Key)
	} else {
		req.Header.Set("Authorization", "Bearer "+originalChannel.Key)
	}

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
		// 有错误，不扣费，返回错误以触发自动禁用和重试逻辑
		logger.SysError(fmt.Sprintf("Sora remix request failed: %s (type: %s, code: %s)",
			soraResponse.Error.Message, soraResponse.Error.Type, soraResponse.Error.Code))

		// 返回错误对象，以便触发自动禁用和重试逻辑
		return &model.ErrorWithStatusCode{
			Error: model.Error{
				Message: soraResponse.Error.Message,
				Type:    soraResponse.Error.Type,
				Code:    soraResponse.Error.Code,
			},
			StatusCode: soraResponse.StatusCode,
		}
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
		// 其他错误状态码，返回错误以触发自动禁用和重试逻辑
		var errMsg string
		var errType string
		var errCode string

		if soraResponse.Error != nil {
			errMsg = soraResponse.Error.Message
			errType = soraResponse.Error.Type
			errCode = soraResponse.Error.Code
		} else {
			errMsg = fmt.Sprintf("Request failed with status code: %d", soraResponse.StatusCode)
			errType = "api_error"
			errCode = ""
		}

		logger.SysError(fmt.Sprintf("Sora remix request failed: status=%d, body=%s", soraResponse.StatusCode, string(body)))

		// 返回错误对象，以便触发自动禁用和重试逻辑
		return &model.ErrorWithStatusCode{
			Error: model.Error{
				Message: errMsg,
				Type:    errType,
				Code:    errCode,
			},
			StatusCode: soraResponse.StatusCode,
		}
	}

	// 成功情况：创建 GeneralVideoResponse 结构体
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

	// 构建请求URL，Azure 渠道需要添加 /openai 前缀
	baseUrl := meta.BaseURL
	var fullRequestUrl string
	if meta.ChannelType == common.ChannelTypeAzure {
		fullRequestUrl = fmt.Sprintf("%s/openai/v1/videos", baseUrl)
	} else {
		fullRequestUrl = fmt.Sprintf("%s/v1/videos", baseUrl)
	}

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

	// 设置请求头，Azure 渠道使用 Api-key header
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if meta.ChannelType == common.ChannelTypeAzure {
		req.Header.Set("Api-key", channel.Key)
	} else {
		req.Header.Set("Authorization", "Bearer "+channel.Key)
	}

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

	// 构建请求URL，Azure 渠道需要添加 /openai 前缀
	baseUrl := meta.BaseURL
	var fullRequestUrl string
	if meta.ChannelType == common.ChannelTypeAzure {
		fullRequestUrl = fmt.Sprintf("%s/openai/v1/videos", baseUrl)
	} else {
		fullRequestUrl = fmt.Sprintf("%s/v1/videos", baseUrl)
	}

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

	// 设置请求头，Azure 渠道使用 Api-key header
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if meta.ChannelType == common.ChannelTypeAzure {
		req.Header.Set("Api-key", channel.Key)
	} else {
		req.Header.Set("Authorization", "Bearer "+channel.Key)
	}

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

	// 读取文件内容
	fileData, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read file data: %w", err)
	}

	// 获取 Content-Type，用于确定文件扩展名
	contentType := resp.Header.Get("Content-Type")

	// 根据 Content-Type 确定文件扩展名
	filename := ""
	if strings.Contains(contentType, "image/jpeg") || strings.Contains(contentType, "image/jpg") {
		filename = "input_reference.jpg"
	} else if strings.Contains(contentType, "image/png") {
		filename = "input_reference.png"
	} else if strings.Contains(contentType, "image/webp") {
		filename = "input_reference.webp"
	} else if strings.Contains(contentType, "image/gif") {
		filename = "input_reference.gif"
	}

	// 如果 Content-Type 未识别，从 URL 提取扩展名
	if filename == "" {
		// 从 URL 中尝试提取扩展名
		urlLower := strings.ToLower(url)
		if strings.HasSuffix(urlLower, ".jpg") || strings.HasSuffix(urlLower, ".jpeg") {
			filename = "input_reference.jpg"
		} else if strings.HasSuffix(urlLower, ".png") {
			filename = "input_reference.png"
		} else if strings.HasSuffix(urlLower, ".webp") {
			filename = "input_reference.webp"
		} else if strings.HasSuffix(urlLower, ".gif") {
			filename = "input_reference.gif"
		} else if strings.Contains(urlLower, ".jpg?") || strings.Contains(urlLower, ".jpeg?") {
			filename = "input_reference.jpg"
		} else if strings.Contains(urlLower, ".png?") {
			filename = "input_reference.png"
		} else if strings.Contains(urlLower, ".webp?") {
			filename = "input_reference.webp"
		} else {
			// 尝试通过文件头检测
			filename = detectImageFilename(fileData)
		}
	}

	log.Printf("Input reference URL: %s, Content-Type: %s, detected filename: %s", url, contentType, filename)

	// 确定正确的 MIME type
	mimeType := "image/jpeg" // 默认
	if strings.HasSuffix(filename, ".png") {
		mimeType = "image/png"
	} else if strings.HasSuffix(filename, ".webp") {
		mimeType = "image/webp"
	} else if strings.HasSuffix(filename, ".gif") {
		mimeType = "image/gif"
	}

	// 手动创建 part header，设置正确的 Content-Type
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="input_reference"; filename="%s"`, filename)}
	h["Content-Type"] = []string{mimeType}

	part, err := writer.CreatePart(h)
	if err != nil {
		return fmt.Errorf("failed to create form part: %w", err)
	}

	// 写入文件数据
	_, err = part.Write(fileData)
	if err != nil {
		return fmt.Errorf("failed to write file data: %w", err)
	}

	log.Printf("Input reference URL uploaded: %s, MIME: %s, filename: %s, size: %d bytes",
		url, mimeType, filename, len(fileData))
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

	// 提取文件扩展名和 MIME type
	filename := "input_reference.jpg"
	mimeType := "image/jpeg"

	if strings.Contains(header, "image/png") {
		filename = "input_reference.png"
		mimeType = "image/png"
	} else if strings.Contains(header, "image/jpeg") || strings.Contains(header, "image/jpg") {
		filename = "input_reference.jpg"
		mimeType = "image/jpeg"
	} else if strings.Contains(header, "image/gif") {
		filename = "input_reference.gif"
		mimeType = "image/gif"
	} else if strings.Contains(header, "image/webp") {
		filename = "input_reference.webp"
		mimeType = "image/webp"
	}

	// 手动创建 part header，设置正确的 Content-Type
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="input_reference"; filename="%s"`, filename)}
	h["Content-Type"] = []string{mimeType}

	part, err := writer.CreatePart(h)
	if err != nil {
		return fmt.Errorf("failed to create form part: %w", err)
	}

	// 写入文件数据
	_, err = part.Write(fileData)
	if err != nil {
		return fmt.Errorf("failed to write file data: %w", err)
	}

	log.Printf("Input reference data URL processed: filename=%s, MIME=%s, size=%d bytes", filename, mimeType, len(fileData))
	return nil
}

// handleInputReferenceBase64 处理纯 base64 格式的 input_reference
func handleInputReferenceBase64(writer *multipart.Writer, base64Data string) error {
	// 解码 base64
	fileData, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return fmt.Errorf("failed to decode base64: %w", err)
	}

	// 通过文件头检测文件类型
	filename := detectImageFilename(fileData)

	// 根据文件名确定 MIME type
	mimeType := "image/jpeg" // 默认
	if strings.HasSuffix(filename, ".png") {
		mimeType = "image/png"
	} else if strings.HasSuffix(filename, ".webp") {
		mimeType = "image/webp"
	} else if strings.HasSuffix(filename, ".gif") {
		mimeType = "image/gif"
	}

	// 手动创建 part header，设置正确的 Content-Type
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="input_reference"; filename="%s"`, filename)}
	h["Content-Type"] = []string{mimeType}

	part, err := writer.CreatePart(h)
	if err != nil {
		return fmt.Errorf("failed to create form part: %w", err)
	}

	// 写入文件数据
	_, err = part.Write(fileData)
	if err != nil {
		return fmt.Errorf("failed to write file data: %w", err)
	}

	log.Printf("Input reference base64 processed: filename=%s, MIME=%s, size=%d bytes", filename, mimeType, len(fileData))
	return nil
}

// detectImageFilename 通过文件头检测图片类型并返回合适的文件名
func detectImageFilename(data []byte) string {
	if len(data) < 12 {
		return "input_reference.jpg" // 默认
	}

	// 检测文件头
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xD8 {
		return "input_reference.jpg" // JPEG
	} else if len(data) >= 8 && data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
		return "input_reference.png" // PNG
	} else if len(data) >= 12 && string(data[8:12]) == "WEBP" {
		return "input_reference.webp" // WebP
	} else if len(data) >= 6 && string(data[0:3]) == "GIF" {
		return "input_reference.gif" // GIF
	}

	return "input_reference.jpg" // 默认使用 JPG
}

func handleSoraVideoResponse(c *gin.Context, ctx context.Context, soraResponse openai.SoraVideoResponse, body []byte, meta *util.RelayMeta, modelName string, quota int64, secondsStr string, size string) *model.ErrorWithStatusCode {
	var taskId string
	var taskStatus string
	var message string

	// 检查是否有错误
	if soraResponse.Error != nil {
		// 有错误，不扣费，返回错误以触发自动禁用和重试逻辑
		logger.SysError(fmt.Sprintf("Sora video request failed: %s (type: %s, code: %s)",
			soraResponse.Error.Message, soraResponse.Error.Type, soraResponse.Error.Code))

		// 返回错误对象，以便触发自动禁用和重试逻辑
		return &model.ErrorWithStatusCode{
			Error: model.Error{
				Message: soraResponse.Error.Message,
				Type:    soraResponse.Error.Type,
				Code:    soraResponse.Error.Code,
			},
			StatusCode: soraResponse.StatusCode,
		}
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
		// 其他错误状态码，返回错误以触发自动禁用和重试逻辑
		var errMsg string
		var errType string
		var errCode string

		if soraResponse.Error != nil {
			errMsg = soraResponse.Error.Message
			errType = soraResponse.Error.Type
			errCode = soraResponse.Error.Code
		} else {
			errMsg = fmt.Sprintf("Request failed with status code: %d", soraResponse.StatusCode)
			errType = "api_error"
			errCode = ""
		}

		logger.SysError(fmt.Sprintf("Sora video request failed: status=%d, body=%s", soraResponse.StatusCode, string(body)))

		// 返回错误对象，以便触发自动禁用和重试逻辑
		return &model.ErrorWithStatusCode{
			Error: model.Error{
				Message: errMsg,
				Type:    errType,
				Code:    errCode,
			},
			StatusCode: soraResponse.StatusCode,
		}
	}

	// 成功情况：创建 GeneralVideoResponse 结构体 - 与其他视频处理保持一致
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
		return openai.ErrorWrapper(err, "invalid_request_body", http.StatusBadRequest)
	}

	// 删除model参数（如果存在）
	delete(reqBody, "model")

	// 读取parameters字段
	params, _ := reqBody["parameters"].(map[string]interface{})
	if params == nil {
		params = make(map[string]interface{})
	}

	// 读取generateAudio（默认true）
	generateAudio := true
	if val, ok := params["generateAudio"].(bool); ok {
		generateAudio = val
	}
	c.Set("generateAudio", generateAudio)

	// 读取durationSeconds（默认8）
	duration := 8
	if val, ok := params["durationSeconds"].(float64); ok {
		duration = int(val)
	}
	c.Set("durationSeconds", duration)

	// 添加storageUri参数（从渠道配置中读取）
	if meta.Config.GoogleStorage != "" {
		params["storageUri"] = meta.Config.GoogleStorage
	}
	reqBody["parameters"] = params

	// 序列化请求体
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return openai.ErrorWrapper(err, "json_marshal_error", http.StatusInternalServerError)
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

		// 计算配额 - 使用通用视频计费函数
		quota := common.CalculateVideoQuota(modelName, "", videoMode, strconv.Itoa(durationSeconds), "")

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

func handleMinimaxVideoRequest(c *gin.Context, ctx context.Context, videoRequest model.VideoRequest, meta *util.RelayMeta) *model.ErrorWithStatusCode {

	baseUrl := meta.BaseURL
	fullRequestUrl := baseUrl + "/v1/video_generation"

	// 读取原始请求体
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_request_body_failed", http.StatusBadRequest)
	}

	// 先解析为 map 以便处理 duration 的多种类型
	var requestMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &requestMap); err != nil {
		return openai.ErrorWrapper(err, "invalid_request_error", http.StatusBadRequest)
	}

	// 处理 duration 字段，兼容多种类型（int、float64、string）
	var durationInt int
	if durationValue, exists := requestMap["duration"]; exists && durationValue != nil {
		switch v := durationValue.(type) {
		case float64:
			durationInt = int(v)
		case string:
			parsed, parseErr := strconv.Atoi(v)
			if parseErr == nil {
				durationInt = parsed
			} else {
				durationInt = 6 // 解析失败使用默认值
			}
		case int:
			durationInt = v
		default:
			durationInt = 6 // 未知类型使用默认值
		}
	}

	// 如果没有传递或值为 0，设置默认值
	if durationInt == 0 {
		durationInt = 6
		requestMap["duration"] = 6
	} else {
		requestMap["duration"] = durationInt
	}

	// 重新序列化为 JSON
	modifiedBodyBytes, err := json.Marshal(requestMap)
	if err != nil {
		return openai.ErrorWrapper(err, "json_marshal_error", http.StatusInternalServerError)
	}

	// 解析请求体以获取 duration 和 resolution 参数
	var videoRequestMinimax model.VideoRequestMinimax
	if err := json.Unmarshal(modifiedBodyBytes, &videoRequestMinimax); err != nil {
		return openai.ErrorWrapper(err, "invalid_request_error", http.StatusBadRequest)
	}

	// 设置默认 resolution（如果未提供则使用 768P）
	if videoRequestMinimax.Resolution == "" {
		videoRequestMinimax.Resolution = "768P"
	}

	// 将 duration 和 resolution 存储到 context 中供后续计费使用
	c.Set("minimax_duration", videoRequestMinimax.Duration)
	c.Set("minimax_resolution", videoRequestMinimax.Resolution)

	// 请求参数已通过c.Set存储，无需额外日志

	// 重新序列化请求体
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

func handleMinimaxVideoResponse(c *gin.Context, ctx context.Context, videoResponse model.VideoResponse, body []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {
	switch videoResponse.BaseResp.StatusCode {
	case 0:
		// 先计算quota
		quota := calculateQuota(meta, modelName, "", "", c)

		// 从 context 中获取 duration 和 resolution
		var durationStr string
		var resolutionStr string
		if minimaxDuration, exists := c.Get("minimax_duration"); exists {
			if durationInt, ok := minimaxDuration.(int); ok {
				durationStr = fmt.Sprintf("%d", durationInt)
			}
		}
		if minimaxResolution, exists := c.Get("minimax_resolution"); exists {
			if resolution, ok := minimaxResolution.(string); ok {
				resolutionStr = resolution
			}
		}

		// 将 resolution 存储到 mode 参数中
		err := CreateVideoLog("minimax", videoResponse.TaskID, meta, resolutionStr, durationStr, "", "", quota, resolutionStr)
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

	// 特殊处理 MiniMax-Hailuo 视频模型（基于 duration 和 resolution 计费）
	if modelName == "MiniMax-Hailuo-2.3-Fast" || modelName == "MiniMax-Hailuo-2.3" || modelName == "MiniMax-Hailuo-02" {
		// 从 context 中获取 duration 和 resolution
		minimaxDuration, hasDuration := c.Get("minimax_duration")
		minimaxResolution, hasResolution := c.Get("minimax_resolution")

		if hasDuration && hasResolution {
			// 安全的类型断言
			durationInt, ok1 := minimaxDuration.(int)
			resolutionStr, ok2 := minimaxResolution.(string)

			if !ok1 || !ok2 {
				// 类型断言失败，使用默认值
				log.Printf("[计费警告] duration 或 resolution 类型不匹配，使用默认计费")
				return quota
			}

			// 定义价格（人民币）
			var priceCNY float64

			// 根据模型、分辨率和时长设置价格（单位：人民币元）
			switch modelName {
			case "MiniMax-Hailuo-2.3-Fast":
				switch {
				case resolutionStr == "768P" && durationInt == 6:
					priceCNY = 1.35
				case resolutionStr == "768P" && durationInt == 10:
					priceCNY = 2.25
				case resolutionStr == "1080P" && durationInt == 6:
					priceCNY = 2.31
				default:
					// 未匹配到价格表，使用 768P 6秒作为默认
					log.Printf("[计费警告] MiniMax-Hailuo-2.3-Fast 未找到匹配价格: resolution=%s, duration=%d, 使用默认价格1.35元", resolutionStr, durationInt)
					priceCNY = 1.35
				}
			case "MiniMax-Hailuo-2.3":
				switch {
				case resolutionStr == "768P" && durationInt == 6:
					priceCNY = 2.0
				case resolutionStr == "768P" && durationInt == 10:
					priceCNY = 4.0
				case resolutionStr == "1080P" && durationInt == 6:
					priceCNY = 3.5
				default:
					// 未匹配到价格表，使用 768P 6秒作为默认
					log.Printf("[计费警告] MiniMax-Hailuo-2.3 未找到匹配价格: resolution=%s, duration=%d, 使用默认价格2.0元", resolutionStr, durationInt)
					priceCNY = 2.0
				}
			case "MiniMax-Hailuo-02":
				// MiniMax-Hailuo-02 支持多种分辨率
				switch {
				case resolutionStr == "512P" && durationInt == 6:
					priceCNY = 1.5 // 根据官方文档补充
				case resolutionStr == "512P" && durationInt == 10:
					priceCNY = 3.0 // 根据官方文档补充
				case resolutionStr == "768P" && durationInt == 6:
					priceCNY = 2.0
				case resolutionStr == "768P" && durationInt == 10:
					priceCNY = 4.0
				case resolutionStr == "1080P" && durationInt == 6:
					priceCNY = 3.5
				case resolutionStr == "1088P" && durationInt == 6:
					priceCNY = 3.5 // 根据官方文档补充
				default:
					// 未匹配到价格表，使用 768P 6秒作为默认
					log.Printf("[计费警告] MiniMax-Hailuo-02 未找到匹配价格: resolution=%s, duration=%d, 使用默认价格2.0元", resolutionStr, durationInt)
					priceCNY = 2.0
				}
			}

			// 将人民币转换为美元（使用固定汇率 7.2）
			priceUSD := priceCNY / 7.2
			quota = int64(priceUSD * config.QuotaPerUnit)

			// 计费信息已记录到数据库
		}
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
		xRequestID := c.GetString("X-Request-ID")
		logContent := fmt.Sprintf("模型固定价格 %.2f$", modelPrice)

		// 如果提供了videoTaskId，使用RecordVideoConsumeLog，否则使用普通的RecordConsumeLogWithRequestID
		if len(videoTaskId) > 0 && videoTaskId[0] != "" {
			dbmodel.RecordVideoConsumeLog(ctx, meta.UserId, meta.ChannelId, 0, 0, modelName, tokenName, quota, logContent, 0, title, referer, videoTaskId[0])
		} else {
			dbmodel.RecordConsumeLogWithRequestID(ctx, meta.UserId, meta.ChannelId, 0, 0, modelName, tokenName, quota, logContent, 0, title, referer, false, 0.0, xRequestID)
		}

		dbmodel.UpdateUserUsedQuotaAndRequestCount(meta.UserId, quota)
		channelId := c.GetInt("channel_id")
		dbmodel.UpdateChannelUsedQuota(channelId, quota)
	}

	return nil
}

// invokeVideoAdaptorRequest 通过 VideoAdaptor 接口处理视频生成请求
func invokeVideoAdaptorRequest(c *gin.Context, ctx context.Context, adaptor relaychannel.VideoAdaptor, videoRequest *model.VideoRequest, meta *util.RelayMeta) *model.ErrorWithStatusCode {
	// 预扣费余额检查
	prePayment := adaptor.GetPrePaymentQuota()
	userQuota, err := dbmodel.CacheGetUserQuota(ctx, meta.UserId)
	if err != nil {
		return openai.ErrorWrapper(err, "get_user_quota_error", http.StatusInternalServerError)
	}
	if userQuota-prePayment < 0 {
		return openai.ErrorWrapper(fmt.Errorf("用户余额不足"), "User balance is not enough", http.StatusBadRequest)
	}

	adaptor.Init(meta)
	taskResult, apiErr := adaptor.HandleVideoRequest(c, videoRequest, meta)
	if apiErr != nil {
		return apiErr
	}

	// 创建视频任务日志
	_ = CreateVideoLog(adaptor.GetProviderName(), taskResult.TaskId, meta,
		taskResult.Mode, taskResult.Duration, taskResult.VideoType,
		taskResult.VideoId, taskResult.Quota, taskResult.Resolution)

	// 响应客户端
	c.JSON(http.StatusOK, model.GeneralVideoResponse{
		TaskId:     taskResult.TaskId,
		TaskStatus: taskResult.TaskStatus,
		Message:    taskResult.Message,
	})

	return handleSuccessfulResponseWithQuota(c, ctx, meta,
		meta.ActualModelName, taskResult.Mode, taskResult.Duration,
		taskResult.Quota, taskResult.TaskId)
}

// invokeVideoAdaptorResult 通过 VideoAdaptor 接口查询视频任务结果
func invokeVideoAdaptorResult(c *gin.Context, adaptor relaychannel.VideoAdaptor, videoTask *dbmodel.Video, channel *dbmodel.Channel, cfg *dbmodel.ChannelConfig) *model.ErrorWithStatusCode {
	adaptor.Init(nil)
	result, apiErr := adaptor.HandleVideoResult(c, videoTask, channel, cfg)
	if apiErr != nil {
		return apiErr
	}

	taskId := videoTask.TaskId

	// 更新任务状态，检查是否需要退款
	needRefund := UpdateVideoTaskStatus(taskId, result.TaskStatus, result.Message)
	if needRefund {
		log.Printf("Task %s failed, compensating user", taskId)
		CompensateVideoTask(taskId)
	}

	// 保存视频 URL 到数据库
	if result.VideoResult != "" {
		if err := dbmodel.UpdateVideoStoreUrl(taskId, result.VideoResult); err != nil {
			log.Printf("Failed to update store_url for task %s: %v", taskId, err)
		}
	}

	c.JSON(http.StatusOK, result)
	return nil
}

func CreateVideoLog(provider string, taskId string, meta *util.RelayMeta, mode string, duration string, videoType string, videoId string, quota int64, resolution ...string) error {
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

	// 处理 resolution 参数
	var resolutionStr string
	if len(resolution) > 0 {
		resolutionStr = resolution[0]
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
		Resolution:  resolutionStr, // 保存分辨率
		VideoId:     videoId,
		Quota:       quota,
		Credentials: credentialsJSON, // 保存完整的JSON凭证
		Status:      "processing",    // 初始状态设置为处理中
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
	case "Preparing":
		return "processing"
	case "Processing":
		return "processing"
	case "Success":
		return "succeed"
	case "Fail":
		return "failed"
	default:
		log.Printf("[MiniMax状态映射] 未知状态: %s", status)
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
	cfg, err := channel.LoadConfig()
	if err != nil {
		return openai.ErrorWrapper(
			fmt.Errorf("failed to load channel config: %v", err),
			"config_error",
			http.StatusInternalServerError,
		)
	}

	// 适配器路由：已迁移供应商由对应 VideoAdaptor 处理
	if adaptor := relayhelper.GetVideoAdaptorByProvider(videoTask.Provider); adaptor != nil {
		return invokeVideoAdaptorResult(c, adaptor, videoTask, channel, &cfg)
	}

	var fullRequestUrl string
	switch videoTask.Provider {
	case "zhipu":
		fullRequestUrl = fmt.Sprintf("https://open.bigmodel.cn/api/paas/v4/async-result/%s", taskId)
	case "minimax":
		fullRequestUrl = fmt.Sprintf("%s/v1/query/video_generation?task_id=%s", *channel.BaseURL, taskId)
	case "runway":
		if channel.Type != 42 {
			fullRequestUrl = fmt.Sprintf("%s/runwayml/v1/tasks/%s", *channel.BaseURL, taskId)
		} else {
			fullRequestUrl = fmt.Sprintf("%s/v1/tasks/%s", *channel.BaseURL, taskId)
		}

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
	case "sora":
		// Sora 视频状态查询，Azure 渠道需要添加 /openai 前缀
		baseUrl := *channel.BaseURL
		if baseUrl == "" {
			baseUrl = "https://api.openai.com"
		}
		if channel.Type == common.ChannelTypeAzure {
			fullRequestUrl = fmt.Sprintf("%s/openai/v1/videos/%s", baseUrl, taskId)
		} else {
			fullRequestUrl = fmt.Sprintf("%s/v1/videos/%s", baseUrl, taskId)
		}
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
	if videoTask.Provider == "runway" && channel.Type == 42 {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Runway-Version", "2024-11-06")
		req.Header.Set("Authorization", "Bearer "+channel.Key)
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
	} else if videoTask.Provider == "sora" && channel.Type == common.ChannelTypeAzure {
		// Sora + Azure 渠道使用 Api-key header
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Api-key", channel.Key)
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
	// log.Printf("video response body: %+v", resp)
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
		// ✅ 修复：defer 必须在 ReadAll 之前
		defer func() {
			if resp.Body != nil {
				_ = resp.Body.Close()
			}
		}()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to read response body: %v", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

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
			Duration:    videoTask.Duration,
		}

		// 如果任务成功且有视频结果，添加到响应中
		if zhipuResp.TaskStatus == "SUCCESS" && len(zhipuResp.VideoResults) > 0 {
			generalResponse.VideoResult = zhipuResp.VideoResults[0].URL
			// 同时设置 VideoResults
			generalResponse.VideoResults = []model.VideoResultItem{
				{Url: zhipuResp.VideoResults[0].URL},
			}

			// 将视频URL存储到数据库
			if generalResponse.VideoResult != "" {
				err := dbmodel.UpdateVideoStoreUrl(taskId, generalResponse.VideoResult)
				if err != nil {
					log.Printf("Failed to update store_url for task %s: %v", taskId, err)
				}
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
	} else if videoTask.Provider == "runway" {
		// ✅ defer 位置正确（在 ReadAll 之前）
		defer func() {
			if resp.Body != nil {
				_ = resp.Body.Close()
			}
		}()

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
			Duration:    videoTask.Duration,
		}

		// 如果任务成功且有视频结果，添加到响应中
		if runwayResp.Status == "SUCCEEDED" && len(runwayResp.Output) > 0 {
			generalResponse.VideoResult = runwayResp.Output[0]
			// 同时设置 VideoResults
			generalResponse.VideoResults = []model.VideoResultItem{
				{Url: runwayResp.Output[0]},
			}

			// 将视频URL存储到数据库
			if generalResponse.VideoResult != "" {
				err := dbmodel.UpdateVideoStoreUrl(taskId, generalResponse.VideoResult)
				if err != nil {
					log.Printf("Failed to update store_url for task %s: %v", taskId, err)
				}
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
	} else if videoTask.Provider == "vertexai" {
		defer resp.Body.Close()

		// 首先检查数据库中是否已有存储的URL
		if videoTask.StoreUrl != "" {
			log.Printf("Found existing store URL for task %s: %s", taskId, videoTask.StoreUrl)

			// 解析StoreUrl，可能是JSON数组格式或单个URL
			var videoUrls []string
			if err := json.Unmarshal([]byte(videoTask.StoreUrl), &videoUrls); err != nil {
				// 如果不是JSON数组，就当作单个URL处理
				videoUrls = []string{videoTask.StoreUrl}
			}

			// 构建VideoResults
			videoResults := make([]model.VideoResultItem, len(videoUrls))
			for i, url := range videoUrls {
				videoResults[i] = model.VideoResultItem{Url: url}
			}

			generalResponse := model.GeneralFinalVideoResponse{
				TaskId:       taskId,
				VideoResult:  videoUrls[0], // 第一个URL作为主URL
				VideoId:      taskId,
				TaskStatus:   "succeed",
				Message:      "Video retrieved from cache",
				VideoResults: videoResults,
				Duration:     videoTask.Duration,
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
			Duration:   videoTask.Duration,
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

							// 保存视频URL到数据库
							var storeUrl string
							if len(processedVideoURIs) == 1 {
								storeUrl = processedVideoURIs[0]
							} else {
								urlsJSON, _ := json.Marshal(processedVideoURIs)
								storeUrl = string(urlsJSON)
							}
							if updateErr := dbmodel.UpdateVideoStoreUrl(taskId, storeUrl); updateErr != nil {
								log.Printf("[VEO] 保存视频URL失败 - 任务:%s, 错误:%v", taskId, updateErr)
							}
						} else {
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
							processedVideoURIs = processVideosConcurrently(videoURIs, videoTask.UserId, taskId)
						} else {
							processedVideoURIs = videoURIs
						}

						// 构建响应结果
						generalResponse.TaskStatus = "succeed"
						generalResponse.Message = "Video generated successfully."
						generalResponse.VideoResult = processedVideoURIs[0]

						// 设置所有视频结果
						generalResponse.VideoResults = make([]model.VideoResultItem, len(processedVideoURIs))
						for i, uri := range processedVideoURIs {
							generalResponse.VideoResults[i] = model.VideoResultItem{Url: uri}
						}

						// 保存视频URL到数据库
						var storeUrl string
						if len(processedVideoURIs) == 1 {
							storeUrl = processedVideoURIs[0]
						} else {
							urlsJSON, _ := json.Marshal(processedVideoURIs)
							storeUrl = string(urlsJSON)
						}
						if updateErr := dbmodel.UpdateVideoStoreUrl(taskId, storeUrl); updateErr != nil {
							log.Printf("[VEO] 保存视频URL失败 - 任务:%s, 错误:%v", taskId, updateErr)
						}
					} else {
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

			// 解析StoreUrl，可能是JSON数组格式或单个URL
			var videoUrls []string
			if err := json.Unmarshal([]byte(videoTask.StoreUrl), &videoUrls); err != nil {
				// 如果不是JSON数组，就当作单个URL处理
				videoUrls = []string{videoTask.StoreUrl}
			}

			// 构建VideoResults
			videoResults := make([]model.VideoResultItem, len(videoUrls))
			for i, url := range videoUrls {
				videoResults[i] = model.VideoResultItem{Url: url}
			}

			generalResponse := model.GeneralFinalVideoResponse{
				TaskId:       taskId,
				VideoResult:  videoUrls[0], // 第一个URL作为主URL
				VideoId:      taskId,
				TaskStatus:   "succeed",
				Message:      "Video retrieved from cache",
				VideoResults: videoResults,
				Duration:     videoTask.Duration,
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
			Duration:    videoTask.Duration,
		}

		// 处理响应
		if aliResp.Code != "" {
			// 查询API本身出错 - 直接返回阿里云的错误信息
			generalResponse.TaskStatus = "failed"
			if aliResp.Message != "" {
				generalResponse.Message = fmt.Sprintf("阿里云API错误: [%s] %s (request_id: %s)", aliResp.Code, aliResp.Message, aliResp.RequestID)
			} else {
				generalResponse.Message = fmt.Sprintf("阿里云API错误: [%s] (request_id: %s)", aliResp.Code, aliResp.RequestID)
			}
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
				// 优先使用阿里云返回的详细错误信息（错误信息在output对象内部）
				if aliResp.Output.Code != "" && aliResp.Output.Message != "" {
					generalResponse.Message = fmt.Sprintf("视频生成失败: [%s] %s (request_id: %s)", aliResp.Output.Code, aliResp.Output.Message, aliResp.RequestID)
				} else if aliResp.Output.Message != "" {
					generalResponse.Message = fmt.Sprintf("视频生成失败: %s (request_id: %s)", aliResp.Output.Message, aliResp.RequestID)
				} else if aliResp.Code != "" && aliResp.Message != "" {
					// 兼容顶层错误信息
					generalResponse.Message = fmt.Sprintf("视频生成失败: [%s] %s (request_id: %s)", aliResp.Code, aliResp.Message, aliResp.RequestID)
				} else if aliResp.Message != "" {
					generalResponse.Message = fmt.Sprintf("视频生成失败: %s (request_id: %s)", aliResp.Message, aliResp.RequestID)
				} else {
					generalResponse.Message = fmt.Sprintf("视频生成失败 (request_id: %s)", aliResp.RequestID)
				}
			case "UNKNOWN":
				generalResponse.TaskStatus = "failed"
				// 优先使用output内的错误信息
				if aliResp.Output.Code != "" && aliResp.Output.Message != "" {
					generalResponse.Message = fmt.Sprintf("任务已过期或未知: [%s] %s (request_id: %s)", aliResp.Output.Code, aliResp.Output.Message, aliResp.RequestID)
				} else if aliResp.Output.Message != "" {
					generalResponse.Message = fmt.Sprintf("任务已过期或未知: %s (request_id: %s)", aliResp.Output.Message, aliResp.RequestID)
				} else if aliResp.Message != "" {
					generalResponse.Message = fmt.Sprintf("任务已过期或未知: %s (request_id: %s)", aliResp.Message, aliResp.RequestID)
				} else {
					generalResponse.Message = fmt.Sprintf("任务已过期或未知 (request_id: %s)", aliResp.RequestID)
				}
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
			if aliResp.Message != "" {
				generalResponse.Message = fmt.Sprintf("未收到响应数据: %s (request_id: %s)", aliResp.Message, aliResp.RequestID)
			} else {
				generalResponse.Message = fmt.Sprintf("未收到响应数据 (request_id: %s)", aliResp.RequestID)
			}
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

			// 解析StoreUrl，可能是JSON数组格式或单个URL
			var videoUrls []string
			if err := json.Unmarshal([]byte(videoTask.StoreUrl), &videoUrls); err != nil {
				// 如果不是JSON数组，就当作单个URL处理
				videoUrls = []string{videoTask.StoreUrl}
			}

			// 构建VideoResults
			videoResults := make([]model.VideoResultItem, len(videoUrls))
			for i, url := range videoUrls {
				videoResults[i] = model.VideoResultItem{Url: url}
			}

			generalResponse := model.GeneralFinalVideoResponse{
				TaskId:       taskId,
				VideoResult:  videoUrls[0], // 第一个URL作为主URL
				VideoId:      taskId,
				TaskStatus:   "succeed",
				Message:      "Video retrieved from cache",
				VideoResults: videoResults,
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
			Duration:   videoTask.Duration, // 从数据库获取
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
	// 构建下载 URL，Azure 渠道需要添加 /openai 前缀
	baseUrl := *channel.BaseURL
	if baseUrl == "" {
		baseUrl = "https://api.openai.com"
	}
	var downloadUrl string
	if channel.Type == common.ChannelTypeAzure {
		downloadUrl = fmt.Sprintf("%s/openai/v1/videos/%s/content", baseUrl, videoId)
	} else {
		downloadUrl = fmt.Sprintf("%s/v1/videos/%s/content", baseUrl, videoId)
	}

	log.Printf("Downloading Sora video: %s", downloadUrl)

	client := &http.Client{
		Timeout: 5 * time.Minute, // 5分钟超时，视频下载可能需要时间
	}

	// 重试逻辑：视频状态为completed后，内容端点可能还需要短暂时间才能可用
	maxRetries := 5
	var resp *http.Response
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			waitSeconds := time.Duration(attempt*3) * time.Second // 3s, 6s, 9s, 12s, 15s
			log.Printf("Sora video content not ready yet, retrying in %v (attempt %d/%d): %s", waitSeconds, attempt, maxRetries, videoId)
			time.Sleep(waitSeconds)
		}

		// 创建下载请求（每次重试都需要新建request）
		req, err := http.NewRequest("GET", downloadUrl, nil)
		if err != nil {
			return "", fmt.Errorf("failed to create download request: %w", err)
		}

		// 设置授权头，Azure 渠道使用 api-key，其他渠道使用 Bearer token
		if channel.Type == common.ChannelTypeAzure {
			req.Header.Set("api-key", channel.Key)
		} else {
			req.Header.Set("Authorization", "Bearer "+channel.Key)
		}

		resp, lastErr = client.Do(req)
		if lastErr != nil {
			lastErr = fmt.Errorf("failed to download video: %w", lastErr)
			continue
		}

		// 如果是404，说明内容还未就绪，关闭响应体后重试
		if resp.StatusCode == 404 {
			resp.Body.Close()
			lastErr = fmt.Errorf("video not ready yet (404)")
			continue
		}

		// 非404错误，直接返回
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return "", fmt.Errorf("download failed with status %d: %s", resp.StatusCode, string(body))
		}

		// 成功，跳出重试循环
		lastErr = nil
		break
	}

	if lastErr != nil {
		return "", fmt.Errorf("download failed after %d retries: %w", maxRetries, lastErr)
	}
	defer resp.Body.Close()

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

func handleMinimaxResponse(c *gin.Context, channel *dbmodel.Channel, taskId string) *model.ErrorWithStatusCode {
	// 查询数据库中的任务信息以获取Duration等字段
	videoTask, err := dbmodel.GetVideoTaskById(taskId)
	if err != nil {
		log.Printf("Failed to get video task for minimax: %v", err)
		// 继续处理，但duration将为空
	}

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

	// 打印海螺原始响应体
	log.Printf("[MiniMax原始响应] TaskId:%s, StatusCode:%d, Body:%s", taskId, resp.StatusCode, string(body))

	var minimaxResp model.FinalVideoResponse
	if err := json.Unmarshal(body, &minimaxResp); err != nil {
		return openai.ErrorWrapper(fmt.Errorf("failed to parse response JSON: %v", err), "json_parse_error", http.StatusInternalServerError)
	}

	duration := ""
	if videoTask != nil {
		duration = videoTask.Duration
	}

	generalResponse := model.GeneralFinalVideoResponse{
		TaskId:      taskId,
		TaskStatus:  mapTaskStatusMinimax(minimaxResp.Status),
		Message:     minimaxResp.BaseResp.StatusMsg,
		VideoResult: "",
		Duration:    duration,
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

	// 打印海螺文件信息原始响应体
	log.Printf("[MiniMax文件响应] TaskId:%s, FileID:%s, StatusCode:%d, Body:%s", taskId, minimaxResp.FileID, fileResp.StatusCode, string(fileBody))

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

	// 将视频URL存储到数据库的StoreUrl字段
	if fileResponse.File.DownloadURL != "" {
		err := dbmodel.UpdateVideoStoreUrl(taskId, fileResponse.File.DownloadURL)
		if err != nil {
			log.Printf("Failed to update store_url for task %s: %v", taskId, err)
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

	// 计算总耗时（秒）
	videoTask.TotalDuration = time.Now().Unix() - videoTask.CreatedAt

	// 尝试更新数据库
	err = videoTask.Update()
	if err != nil {
		log.Printf("Failed to update video task %s using model method: %v", taskid, err)

		// 如果Update失败，尝试直接使用SQL更新作为回退方案
		log.Printf("Attempting direct SQL update for task %s", taskid)
		updateFields := map[string]interface{}{
			"status":         status,
			"total_duration": time.Now().Unix() - videoTask.CreatedAt,
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

// convertGCStoHTTPS 将 gs:// 格式的 URI 转换为 https://storage.googleapis.com/ 格式
func convertGCStoHTTPS(gcsUri string) string {
	if strings.HasPrefix(gcsUri, "gs://") {
		// 将 gs:// 替换为 https://storage.googleapis.com/
		httpsUrl := strings.Replace(gcsUri, "gs://", "https://storage.googleapis.com/", 1)
		log.Printf("[VEO URL转换] GCS URI: %s -> HTTPS URL: %s", gcsUri, httpsUrl)
		return httpsUrl
	}
	// 如果不是 gs:// 格式，直接返回原始 URI
	return gcsUri
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
					// 转换 gs:// 为 https://storage.googleapis.com/
					httpsUrl := convertGCStoHTTPS(gcsUri)
					videoURIs = append(videoURIs, httpsUrl)
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
						// 转换 gs:// 为 https://storage.googleapis.com/
						httpsUrl := convertGCStoHTTPS(uri)
						videoURIs = append(videoURIs, httpsUrl)
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
