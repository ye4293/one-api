package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/keling"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/channel/runway"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

func DoVideoRequest(c *gin.Context, modelName string) *model.ErrorWithStatusCode {
	ctx := c.Request.Context()
	var videoRequest model.VideoRequest
	err := common.UnmarshalBodyReusable(c, &videoRequest)
	meta := util.GetRelayMeta(c)
	if err != nil {
		return openai.ErrorWrapper(err, "invalid_text_request", http.StatusBadRequest)
	}

	if modelName == "video-01" { // minimax
		return handleMinimaxVideoRequest(c, ctx, videoRequest, meta)
	} else if modelName == "cogvideox" {
		// 处理其他模型的逻辑
		return handleZhipuVideoRequest(c, ctx, videoRequest, meta)
	} else if modelName == "kling-v1" {
		return handleKelingVideoRequest(c, ctx, meta)
	} else if modelName == "gen3a_turbo" {
		return handleRunwayVideoRequest(c, ctx, videoRequest, meta)
	} else {
		// 处理其他模型的逻辑
		return openai.ErrorWrapper(fmt.Errorf("Unsupported model"), "unsupported_model", http.StatusBadRequest)
	}
}

func handleMinimaxVideoRequest(c *gin.Context, ctx context.Context, videoRequest model.VideoRequest, meta *util.RelayMeta) *model.ErrorWithStatusCode {
	baseUrl := meta.BaseURL
	fullRequestUrl := baseUrl + "/v1/video_generation"

	videoRequestMinimax := model.VideoRequestMinimax{
		Model:  videoRequest.Model,
		Prompt: videoRequest.Prompt,
	}

	jsonData, err := json.Marshal(videoRequestMinimax)
	if err != nil {
		return openai.ErrorWrapper(err, "json_marshal_error", http.StatusInternalServerError)
	}

	return sendRequestMinimaxAndHandleResponse(c, ctx, fullRequestUrl, jsonData, meta, "video-01")
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
	baseUrl := meta.BaseURL

	// 只解析 image 参数
	var imageCheck struct {
		Image string `json:"image"`
		// Model string `json:"model"`
		Mode     string `json:"mode"`
		Duration string `json:"duration"`
	}

	if err := common.UnmarshalBodyReusable(c, &imageCheck); err != nil {
		return openai.ErrorWrapper(err, "invalid_request_body", http.StatusBadRequest)
	}

	var requestBody interface{}
	var fullRequestUrl string
	var videoType string

	if imageCheck.Image != "" {
		// 图生视频请求
		if meta.ChannelType == 41 {
			fullRequestUrl = baseUrl + "/v1/videos/image2video"
		} else {
			fullRequestUrl = baseUrl + "/kling/v1/videos/image2video"
		}
		videoType = "image-to-video"
		var imageToVideoReq keling.ImageToVideoRequest
		if err := common.UnmarshalBodyReusable(c, &imageToVideoReq); err != nil {
			return openai.ErrorWrapper(err, "invalid_image_to_video_request", http.StatusBadRequest)
		}
		requestBody = imageToVideoReq
	} else {
		// 文生视频请求
		if meta.ChannelType == 41 {
			fullRequestUrl = baseUrl + "/v1/videos/text2video"
		} else {
			fullRequestUrl = baseUrl + "/kling/v1/videos/text2video"
		}
		videoType = "text-to-video"
		var videoGenerationReq keling.TextToVideoRequest
		if err := common.UnmarshalBodyReusable(c, &videoGenerationReq); err != nil {
			return openai.ErrorWrapper(err, "invalid_video_generation_request", http.StatusBadRequest)
		}
		requestBody = videoGenerationReq
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return openai.ErrorWrapper(err, "json_marshal_error", http.StatusInternalServerError)
	}
	return sendRequestKelingAndHandleResponse(c, ctx, fullRequestUrl, jsonData, meta, "kling-v1", imageCheck.Mode, imageCheck.Duration, videoType)
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

func sendRequestKelingAndHandleResponse(c *gin.Context, ctx context.Context, fullRequestUrl string, jsonData []byte, meta *util.RelayMeta, modelName string, mode string, duration string, videoType string) *model.ErrorWithStatusCode {
	req, err := http.NewRequest("POST", fullRequestUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}
	var token string
	if meta.ChannelType == 41 {
		ak := meta.Config.AK
		sk := meta.Config.SK

		// Generate JWT token
		token = encodeJWTToken(ak, sk)
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

	var KelingvideoResponse keling.KelingVideoResponse
	err = json.Unmarshal(body, &KelingvideoResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}
	KelingvideoResponse.StatusCode = resp.StatusCode
	return handleKelingVideoResponse(c, ctx, KelingvideoResponse, body, meta, modelName, mode, duration, videoType)
}

func sendRequestRunwayAndHandleResponse(c *gin.Context, ctx context.Context, fullRequestUrl string, jsonData []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {

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

func encodeJWTToken(ak, sk string) string {
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
		err := CreateVideoLog("minimax", videoResponse.TaskID, meta, "", "", "")
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
		return handleSuccessfulResponse(c, ctx, meta, modelName, "", "")
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
		err := CreateVideoLog("zhipu", videoResponse.ID, meta, "", "", "")
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

		return handleSuccessfulResponse(c, ctx, meta, modelName, "", "")
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
	switch videoResponse.StatusCode {
	case 200:
		err := CreateVideoLog("kling", videoResponse.Data.TaskID, meta, mode, duration, videoType)
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
		return handleSuccessfulResponse(c, ctx, meta, modelName, mode, duration)
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
		err := CreateVideoLog("runway", videoResponse.Id, meta, "", "", "")
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

		return handleSuccessfulResponse(c, ctx, meta, modelName, "", "")
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

func handleSuccessfulResponse(c *gin.Context, ctx context.Context, meta *util.RelayMeta, modelName string, mode string, duration string) *model.ErrorWithStatusCode {
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
			// 如果不匹配任何条件，使用默认倍率 1
			multiplier = 1
		}
		quota = int64(float64(quota) * multiplier)
	}

	value, exists := c.Get("duration")
	if exists {
		runwayDuration := value.(string)
		if runwayDuration == "10" {
			quota = quota * 2
		}
	}

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
		tokenName := c.GetString("token_name")
		logContent := fmt.Sprintf("模型固定价格 %.2f$", modelPrice)
		dbmodel.RecordConsumeLog(ctx, meta.UserId, meta.ChannelId, 0, 0, modelName, tokenName, quota, logContent, 0, title, referer)
		dbmodel.UpdateUserUsedQuotaAndRequestCount(meta.UserId, quota)
		channelId := c.GetInt("channel_id")
		dbmodel.UpdateChannelUsedQuota(channelId, quota)
	}

	return nil
}

func CreateVideoLog(provider string, taskId string, meta *util.RelayMeta, mode string, duration string, videoType string) error {
	// 创建新的 Video 实例
	video := &dbmodel.Video{
		Prompt:    "prompt",
		CreatedAt: time.Now().Unix(), // 使用当前时间戳
		TaskId:    taskId,
		Provider:  provider,
		Username:  dbmodel.GetUsernameById(meta.UserId),
		ChannelId: meta.ChannelId,
		UseId:     meta.UserId,
		Mode:      mode, //keling
		Type:      videoType,
		Duration:  duration,
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
	logger.SysLog(fmt.Sprintf("channelId2:%d", channel.Id))
	cfg, _ := channel.LoadConfig()
	if err != nil {
		return openai.ErrorWrapper(
			fmt.Errorf("failed to get channel: %v", err),
			"database_error",
			http.StatusInternalServerError,
		)
	}
	if err != nil {
		return openai.ErrorWrapper(
			fmt.Errorf("failed to get videoTask: %v", err),
			"database_error",
			http.StatusBadRequest,
		)
	}

	var fullRequestUrl string
	switch videoTask.Provider {
	case "zhipu":
		fullRequestUrl = fmt.Sprintf("https://open.bigmodel.cn/api/paas/v4/async-result/%s", taskId)
	case "minimax":
		fullRequestUrl = fmt.Sprintf("https://api.minimax.chat/v1/query/video_generation?task_id=%s", taskId)
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
		}
	case "runway":
		if channel.Type != 42 {
			fullRequestUrl = fmt.Sprintf("%s/runwayml/v1/tasks/%s", *channel.BaseURL, taskId)
		} else {
			fullRequestUrl = fmt.Sprintf("%s/v1/tasks/%s", *channel.BaseURL, taskId)
		}

	default:
		return openai.ErrorWrapper(
			fmt.Errorf("unsupported model type:"),
			"invalid_request_error",
			http.StatusBadRequest,
		)
	}
	// 创建新的请求
	req, err := http.NewRequest("GET", fullRequestUrl, nil)
	if err != nil {
		return openai.ErrorWrapper(
			fmt.Errorf("failed to create request: %v", err),
			"api_error",
			http.StatusInternalServerError,
		)
	}
	if videoTask.Provider == "kling" && channel.Type == 41 {
		token := encodeJWTToken(cfg.AK, cfg.SK)

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

	} else if videoTask.Provider == "runway" && channel.Type == 42 {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Runway-Version", "2024-11-06")
		req.Header.Set("Authorization", "Bearer "+channel.Key)
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

		// 打印响应内容，方便调试
		log.Printf("Runway response body: %s", string(body))

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
		} else {
			log.Printf("Task not succeeded or no output. Status: %s, Output length: %d",
				runwayResp.Status, len(runwayResp.Output))
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
		return nil
	}

	return nil

}

func handleMinimaxResponse(c *gin.Context, channel *dbmodel.Channel, taskId string) *model.ErrorWithStatusCode {
	// 第一次请求，获取初始状态
	url := fmt.Sprintf("https://api.minimax.chat/v1/query/video_generation?task_id=%s", taskId)
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
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(fmt.Errorf("Error marshaling response: %s", err), "internal_error", http.StatusInternalServerError)
		}
		c.Data(http.StatusOK, "application/json", jsonResponse)
		return nil
	}

	// 如果 FileID 不为空，获取文件信息
	fileUrl := fmt.Sprintf("https://api.minimax.chat/v1/files/retrieve?file_id=%s", minimaxResp.FileID)
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
	generalResponse.TaskStatus = "success" // 假设有 FileID 且能获取到下载 URL 就意味着成功

	jsonResponse, err := json.Marshal(generalResponse)
	if err != nil {
		return openai.ErrorWrapper(fmt.Errorf("Error marshaling response: %s", err), "internal_error", http.StatusInternalServerError)
	}

	c.Data(http.StatusOK, "application/json", jsonResponse)
	return nil
}
