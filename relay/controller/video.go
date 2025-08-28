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
	commonConfig "github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
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
				AccessKeyID:     commonConfig.CfFileAccessKey,
				SecretAccessKey: commonConfig.CfFileSecretKey,
			}, nil
		}))),
		awsConfig.WithEndpointResolverWithOptions(aws.EndpointResolverWithOptionsFunc(
			func(service, region string, options ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{URL: commonConfig.CfFileEndpoint}, nil
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
		Bucket:      aws.String(commonConfig.CfBucketFileName),
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
	} else {
		return openai.ErrorWrapper(fmt.Errorf("Unsupported model"), "unsupported_model", http.StatusBadRequest)
	}
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
	case 400:
		errorMsg := "豆包API错误"
		if doubaoResponse.Error != nil {
			errorMsg = fmt.Sprintf("豆包API错误: %s", doubaoResponse.Error.Message)
		}
		return openai.ErrorWrapper(
			fmt.Errorf(errorMsg),
			"api_error",
			http.StatusBadRequest,
		)
	case 401:
		return openai.ErrorWrapper(
			fmt.Errorf("豆包API认证失败"),
			"api_error",
			http.StatusUnauthorized,
		)
	case 429:
		return openai.ErrorWrapper(
			fmt.Errorf("豆包API请求过于频繁"),
			"api_error",
			http.StatusTooManyRequests,
		)
	default:
		return openai.ErrorWrapper(
			fmt.Errorf("豆包API未知错误 (状态码: %d)", doubaoResponse.StatusCode),
			"api_error",
			http.StatusInternalServerError,
		)
	}
}

// handleVeoVideoRequest 处理 Veo 视频生成请求，支持两种 API 方式：
// 1. Vertex AI API: 使用 OAuth2 认证，需要项目ID和区域配置
// 2. Gemini API: 使用 API Key 认证，端点为 generativelanguage.googleapis.com
// 两种 API 的请求体格式完全相同，只是端点和认证方式不同
func handleVeoVideoRequest(c *gin.Context, ctx context.Context, videoRequest model.VideoRequest, meta *util.RelayMeta) *model.ErrorWithStatusCode {

	var fullRequestUrl string

	// 根据渠道类型选择不同的 API 端点
	if meta.ChannelType == common.ChannelTypeGemini {
		// Gemini API 端点
		fullRequestUrl = fmt.Sprintf("%s/v1beta/models/%s:predictLongRunning", meta.BaseURL, meta.OriginModelName)
	} else {
		// Vertex AI 端点（默认）
		region := meta.Config.Region
		if region == "global" {
			fullRequestUrl = fmt.Sprintf("https://aiplatform.googleapis.com/v1/projects/%s/locations/global/publishers/google/models/%s:predictLongRunning", meta.Config.VertexAIProjectID, meta.OriginModelName)
		} else {
			fullRequestUrl = fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s:predictLongRunning", region, meta.Config.VertexAIProjectID, region, meta.OriginModelName)
		}
	}

	log.Printf("veo-full-request-url: %s", fullRequestUrl)

	// 读取原始请求体
	var reqBody map[string]interface{}
	if err := common.UnmarshalBodyReusable(c, &reqBody); err != nil {
		return openai.ErrorWrapper(err, "invalid_request_body", http.StatusBadRequest)
	}

	// 删除model参数（如果存在）
	if _, exists := reqBody["model"]; exists {
		delete(reqBody, "model")
	}

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

	if _, ok := params["sampleCount"]; ok { //暂时处理只支持一个视频结果
		delete(params, "sampleCount")
	}

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

	// 创建HTTP请求
	req, err := http.NewRequest("POST", fullRequestUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}

	// 根据渠道类型设置不同的认证方式
	req.Header.Set("Content-Type", "application/json")

	if meta.ChannelType == common.ChannelTypeGemini {
		// Gemini API 使用 API Key 认证
		req.Header.Set("x-goog-api-key", meta.APIKey)
		log.Printf("Using Gemini API authentication for Veo video generation")
	} else {
		// Vertex AI 使用 OAuth2 token 认证
		var credentials vertexai.Credentials
		if err := json.Unmarshal([]byte(meta.Config.VertexAIADC), &credentials); err != nil {
			return openai.ErrorWrapper(err, "invalid_credentials", http.StatusInternalServerError)
		}

		adaptor := &vertexai.Adaptor{
			AccountCredentials: credentials,
		}

		// 获取访问令牌
		accessToken, err := vertexai.GetAccessToken(adaptor, meta)
		if err != nil {
			return openai.ErrorWrapper(err, "get_access_token_error", http.StatusInternalServerError)
		}

		req.Header.Set("Authorization", "Bearer "+accessToken)
		log.Printf("Using Vertex AI authentication for Veo video generation")
	}

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

		// 创建视频日志 - 根据渠道类型选择provider名称
		provider := "vertexai"
		if meta.ChannelType == common.ChannelTypeGemini {
			provider = "gemini"
		}
		err := CreateVideoLog(provider, taskId, meta, videoMode, strconv.Itoa(durationSeconds), "", "", quota)
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
		// 处理错误响应
		errorMsg := "Unknown error"
		if msg, ok := veoResponse["error"].(map[string]interface{}); ok {
			if message, ok := msg["message"].(string); ok {
				errorMsg = message
			}
		}

		return openai.ErrorWrapper(
			fmt.Errorf("API error: %s", errorMsg),
			"api_error",
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
		ak := meta.Config.AK
		sk := meta.Config.SK

		// Generate JWT token
		token = EncodeJWTToken(ak, sk)
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
			dbmodel.RecordConsumeLog(ctx, meta.UserId, meta.ChannelId, 0, 0, modelName, tokenName, quota, logContent, 0, title, referer)
		}

		dbmodel.UpdateUserUsedQuotaAndRequestCount(meta.UserId, quota)
		channelId := c.GetInt("channel_id")
		dbmodel.UpdateChannelUsedQuota(channelId, quota)
	}

	return nil
}

func CreateVideoLog(provider string, taskId string, meta *util.RelayMeta, mode string, duration string, videoType string, videoId string, quota int64) error {
	// 创建新的 Video 实例
	video := &dbmodel.Video{
		Prompt:    "prompt",
		CreatedAt: time.Now().Unix(), // 使用当前时间戳
		TaskId:    taskId,
		Provider:  provider,
		Username:  dbmodel.GetUsernameById(meta.UserId),
		ChannelId: meta.ChannelId,
		UserId:    meta.UserId,
		Mode:      mode, //keling
		Type:      videoType,
		Model:     meta.OriginModelName,
		Duration:  duration,
		VideoId:   videoId,
		Quota:     quota,
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
		// 需要从渠道配置重新构建完整的操作名称
		// 配置已在函数开始时读取，直接使用

		projectId := cfg.VertexAIProjectID
		region := cfg.Region
		modelId := videoTask.Model // 从数据库中的视频任务记录获取模型名

		// 构建 fetchPredictOperation URL
		var baseURL string
		if region == "global" {
			baseURL = "https://aiplatform.googleapis.com"
		} else {
			baseURL = fmt.Sprintf("https://%s-aiplatform.googleapis.com", region)
		}

		fullRequestUrl = fmt.Sprintf("%s/v1/projects/%s/locations/%s/publishers/google/models/%s:fetchPredictOperation",
			baseURL, projectId, region, modelId)
	case "gemini":
		// 对于 Gemini API，使用相同的操作查询端点
		fullRequestUrl = fmt.Sprintf("%s/v1beta/operations/%s", *channel.BaseURL, taskId)
	default:
		return openai.ErrorWrapper(
			fmt.Errorf("unsupported model type:"),
			"invalid_request_error",
			http.StatusBadRequest,
		)
	}
	// 创建新的请求
	var req *http.Request

	if videoTask.Provider == "vertexai" || videoTask.Provider == "gemini" {
		var operationName string
		if videoTask.Provider == "vertexai" {
			// 配置已在函数开始时读取，直接使用
			projectId := cfg.VertexAIProjectID
			region := cfg.Region
			modelId := videoTask.Model
			// 重新构建完整的操作名称用于API请求
			operationName = fmt.Sprintf("projects/%s/locations/%s/publishers/google/models/%s/operations/%s",
				projectId, region, modelId, taskId)
		} else {
			// Gemini API 中 taskId 就是操作名称
			operationName = taskId
		}

		// 两者都需要 POST 请求，并在请求体中包含操作名称
		requestBody := map[string]string{
			"operationName": operationName,
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
		token := EncodeJWTToken(cfg.AK, cfg.SK)

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
		// VertexAI 需要使用 OAuth2 token 进行认证
		var credentials vertexai.Credentials
		if err := json.Unmarshal([]byte(cfg.VertexAIADC), &credentials); err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to parse VertexAI credentials: %v", err),
				"credential_error",
				http.StatusInternalServerError,
			)
		}

		adaptor := &vertexai.Adaptor{
			AccountCredentials: credentials,
		}

		// 创建临时的 RelayMeta 来获取访问令牌
		tempMeta := &util.RelayMeta{
			Config: dbmodel.ChannelConfig{
				Region:            cfg.Region,
				VertexAIProjectID: cfg.VertexAIProjectID,
				VertexAIADC:       cfg.VertexAIADC,
			},
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
		log.Printf("Using Vertex AI authentication for video task query: %s", taskId)
	} else if videoTask.Provider == "gemini" {
		// Gemini API 使用 API Key 认证
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-goog-api-key", channel.Key)
		log.Printf("Using Gemini API authentication for video task query: %s", taskId)
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
				fmt.Errorf("Error marshaling response: %s", err),
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
	} else if videoTask.Provider == "vertexai" || videoTask.Provider == "gemini" {
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

		// 确定API类型用于日志
		apiType := "Vertex AI"
		if videoTask.Provider == "gemini" {
			apiType = "Gemini API"
		}

		if err := json.Unmarshal(body, &veoResp); err != nil {
			log.Printf("Failed to parse %s response as JSON. Body: %s", apiType, string(body))
			return openai.ErrorWrapper(fmt.Errorf("failed to parse response JSON: %v", err), "json_parse_error", http.StatusInternalServerError)
		}

		// 打印原始响应体的JSON结构（不显示具体内容以避免base64数据过长）
		log.Printf("=== %s Response Structure for task %s ===", apiType, taskId)
		printJSONStructure(veoResp, "", 4)
		log.Printf("=== End of Response Structure ===")

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
				// 操作成功，提取视频URI
				videoURI := extractVeoVideoURI(response)
				if videoURI != "" {
					// 如果是 base64 数据且用户要求 URL 格式，则上传到 R2
					if strings.HasPrefix(videoURI, "data:video/mp4;base64,") {
						responseFormat := c.GetString("response_format")
						if responseFormat == "url" {
							base64Data := strings.TrimPrefix(videoURI, "data:video/mp4;base64,")
							if url, err := UploadVideoBase64ToR2(base64Data, videoTask.UserId, "mp4"); err == nil {
								videoURI = url
								// 保存URL到数据库
								if updateErr := dbmodel.UpdateVideoStoreUrl(taskId, url); updateErr != nil {
									log.Printf("Failed to save store URL for task %s: %v", taskId, updateErr)
								} else {
									log.Printf("Successfully saved store URL for task %s: %s", taskId, url)
								}
							} else {
								log.Printf("Failed to upload video to R2 for task %s: %v", taskId, err)
								// 如果上传失败，继续使用原始base64数据
							}
						}
					}

					generalResponse.TaskStatus = "succeed"
					generalResponse.Message = "Video generated successfully."
					generalResponse.VideoResult = videoURI
					generalResponse.VideoResults = []model.VideoResultItem{{Url: videoURI}}
				} else {
					// 完成了，但未找到视频URI也未找到错误
					generalResponse.TaskStatus = "failed"
					generalResponse.Message = "Operation completed, but no video result was found."
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
	}
	return nil
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
			switch value.(type) {
			case string:
				if len(value.(string)) > 100 {
					log.Printf("%s  \"%s\": \"<string length: %d>\"", prefix, key, len(value.(string)))
				} else {
					log.Printf("%s  \"%s\": \"%s\"", prefix, key, value.(string))
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

// extractVeoVideoURI 从 Vertex AI Veo 操作响应中提取视频URI或base64数据
func extractVeoVideoURI(response map[string]interface{}) string {
	// 检查 fetchPredictOperation 格式 (`videos` 字段)
	if videos, ok := response["videos"].([]interface{}); ok && len(videos) > 0 {
		if video, ok := videos[0].(map[string]interface{}); ok {
			// 优先检查是否有 GCS URI
			if gcsUri, ok := video["gcsUri"].(string); ok && gcsUri != "" {
				return gcsUri
			}
			// 检查是否有 base64 编码的视频数据
			if bytesBase64, ok := video["bytesBase64Encoded"].(string); ok && bytesBase64 != "" {
				return "data:video/mp4;base64," + bytesBase64
			}
		}
	}

	// 检查标准长轮询操作格式 (`generatedSamples` 字段)
	if generatedSamples, ok := response["generatedSamples"].([]interface{}); ok && len(generatedSamples) > 0 {
		if sample, ok := generatedSamples[0].(map[string]interface{}); ok {
			if video, ok := sample["video"].(map[string]interface{}); ok {
				// 优先检查是否有 URI
				if uri, ok := video["uri"].(string); ok && uri != "" {
					return uri
				}
				// 检查是否有 base64 编码的视频数据
				if bytesBase64, ok := video["bytesBase64Encoded"].(string); ok && bytesBase64 != "" {
					return "data:video/mp4;base64," + bytesBase64
				}
			}
		}
	}

	return "" // 未找到URI或base64数据则返回空字符串
}
