package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/keling"
	"github.com/songquanpeng/one-api/relay/channel/openai"
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
	log.Printf("fullRequestUrl: %s", fullRequestUrl)
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
func handleMinimaxVideoResponse(c *gin.Context, ctx context.Context, videoResponse model.VideoResponse, body []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {
	switch videoResponse.BaseResp.StatusCode {
	case 0:
		err := CreateVideoLog("minimax", videoResponse.TaskID, meta, "", "", "")
		if err != nil {

		}
		c.Data(http.StatusOK, "application/json", body)
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
		c.Data(http.StatusOK, "application/json", body)
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
	// 首先，记录完整的响应体
	log.Printf("Full response body: %s", string(body))

	switch videoResponse.StatusCode {
	case 200:
		err := CreateVideoLog("keling", videoResponse.Data.TaskID, meta, mode, duration, videoType)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("API error: %s", err),
				"api_error",
				http.StatusBadRequest,
			)
		}
		c.Data(http.StatusOK, "application/json", body)
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

func GetVideoResult(c *gin.Context, provider string, taskId string) *model.ErrorWithStatusCode {
	channelId, err := dbmodel.GetChannelIdByTaskIdAndProvider(taskId, provider)
	logger.SysLog(fmt.Sprintf("channelId:%d", channelId))
	if err != nil {
		return openai.ErrorWrapper(
			fmt.Errorf("failed to get channel ID: %v", err),
			"database_error",
			http.StatusInternalServerError,
		)
	}

	channel, err := dbmodel.GetChannelById(channelId, true)
	logger.SysLog(fmt.Sprintf("channelId2:%d", channel.Id))
	cfg, _ := channel.LoadConfig()
	if err != nil {
		return openai.ErrorWrapper(
			fmt.Errorf("failed to get channel: %v", err),
			"database_error",
			http.StatusInternalServerError,
		)
	}
	videoTask, err := dbmodel.GetVideoTaskByIdAndProvider(taskId, provider)
	if err != nil {
		return openai.ErrorWrapper(
			fmt.Errorf("failed to get videoTask: %v", err),
			"database_error",
			http.StatusBadRequest,
		)
	}

	var fullRequestUrl string
	switch provider {
	case "zhipu":
		fullRequestUrl = fmt.Sprintf("https://open.bigmodel.cn/api/paas/v4/async-result/%s", taskId)
	case "minimax":
		fullRequestUrl = fmt.Sprintf("https://api.minimax.chat/v1/query/video_generation?task_id=%s", taskId)
	case "keling":
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

	default:
		return openai.ErrorWrapper(
			fmt.Errorf("unsupported model type: %s", provider),
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
	if provider == "keling" && channel.Type == 41 {
		token := encodeJWTToken(cfg.AK, cfg.SK)

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

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

	if provider == "zhipu" {
		// 直接将 zhipu 的响应体传递给客户端
		c.DataFromReader(http.StatusOK, resp.ContentLength, resp.Header.Get("Content-Type"), resp.Body, nil)
	} else if provider == "minimax" {
		// 解析 Minimax 的响应
		minimaxResponse, err := pollForCompletion(channel, taskId, 10, 1*time.Second)
		if err != nil {
			// 检查是否是最大重试错误
			if strings.Contains(err.Error(), "max retries reached") {
				// 直接将最后的响应返回给客户端
				c.JSON(http.StatusOK, minimaxResponse)
				return nil
			}
			// 处理其他类型的错误
			return openai.ErrorWrapper(err, "api_error", http.StatusInternalServerError)
		}

		fullRequestUrl2 := fmt.Sprintf("https://api.minimax.chat/v1/files/retrieve?file_id=%s", minimaxResponse.FileID)
		logger.SysLog(fmt.Sprintf("minimaxResponse.FileID:%s", minimaxResponse.FileID))
		// 创建新的请求
		req2, err := http.NewRequest("GET", fullRequestUrl2, nil)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to create second request: %v", err),
				"api_error",
				http.StatusInternalServerError,
			)
		}

		// 添加头部
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("Authorization", "Bearer "+channel.Key)

		// 发送第二个 HTTP 请求
		client := &http.Client{}
		resp2, err := client.Do(req2)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to fetch file: %v", err),
				"api_error",
				http.StatusInternalServerError,
			)
		}
		defer resp2.Body.Close()

		// 检查响应状态码
		if resp2.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp2.Body)
			return openai.ErrorWrapper(
				fmt.Errorf("API error in second request: %s", string(body)),
				"api_error",
				resp2.StatusCode,
			)
		}

		// 直接将第二个请求的响应体传递给客户端
		c.DataFromReader(http.StatusOK, resp2.ContentLength, resp2.Header.Get("Content-Type"), resp2.Body, nil)
	} else if provider == "keling" {
		c.DataFromReader(http.StatusOK, resp.ContentLength, resp.Header.Get("Content-Type"), resp.Body, nil)
	}

	return nil
}

func pollForCompletion(channel *dbmodel.Channel, taskId string, maxRetries int, retryInterval time.Duration) (*model.FinalVideoResponse, error) {
	var lastResponse *model.FinalVideoResponse
	var lastRawResponse string

	for i := 0; i < maxRetries; i++ {
		fullRequestUrl := fmt.Sprintf("https://api.minimax.chat/v1/query/video_generation?task_id=%s", taskId)

		// 创建请求并设置头部
		req, err := http.NewRequest("GET", fullRequestUrl, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+channel.Key)

		// 发送请求
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to send request: %v", err)
		}
		defer resp.Body.Close()

		// 读取并解析响应
		body, _ := io.ReadAll(resp.Body)
		lastRawResponse = string(body)
		logger.SysLog(fmt.Sprintf("Raw response body: %s", lastRawResponse))

		var minimaxResponse model.FinalVideoResponse
		if err := json.Unmarshal(body, &minimaxResponse); err != nil {
			return nil, fmt.Errorf("failed to unmarshal response: %v", err)
		}
		lastResponse = &minimaxResponse

		// 检查任务状态
		if minimaxResponse.Status == "Success" || minimaxResponse.FileID != "" {
			return &minimaxResponse, nil
		} else if minimaxResponse.Status == "Failed" {
			return &minimaxResponse, fmt.Errorf("task failed: %s", minimaxResponse.BaseResp.StatusMsg)
		}

		// 如果还在处理中，等待一段时间后重试
		logger.SysLog(fmt.Sprintf("Task still processing. Retry %d/%d", i+1, maxRetries))
		time.Sleep(retryInterval)
	}

	// 达到最大重试次数，返回最后一次的响应
	return lastResponse, fmt.Errorf("max retries reached, last raw response: %s", lastRawResponse)
}
