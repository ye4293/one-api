package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
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
func handleMinimaxVideoResponse(c *gin.Context, ctx context.Context, videoResponse model.VideoResponse, body []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {
	switch videoResponse.BaseResp.StatusCode {
	case 0:
		err := CreateVideoLog("minimax", videoResponse.TaskID, meta)
		if err != nil {

		}
		c.Data(http.StatusOK, "application/json", body)
		return handleSuccessfulResponse(c, ctx, meta, modelName)
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
		err := CreateVideoLog("zhipu", videoResponse.ID, meta)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("API error: %s", videoResponse.ZhipuError.Message),
				"api_error",
				http.StatusBadRequest,
			)
		}
		c.Data(http.StatusOK, "application/json", body)
		return handleSuccessfulResponse(c, ctx, meta, modelName)
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

func handleSuccessfulResponse(c *gin.Context, ctx context.Context, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {
	quota := int64(36000)
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
		logContent := fmt.Sprintf("模型固定价格 %.2f，分组倍率 %.2f，操作 %s", 2.00, 3.00, "midjRequest.Action")
		dbmodel.RecordConsumeLog(ctx, meta.UserId, meta.ChannelId, 0, 0, modelName, tokenName, quota, logContent, 0, title, referer)
		dbmodel.UpdateUserUsedQuotaAndRequestCount(meta.UserId, quota)
		channelId := c.GetInt("channel_id")
		dbmodel.UpdateChannelUsedQuota(channelId, quota)
	}

	return nil
}

func CreateVideoLog(modelType string, taskId string, meta *util.RelayMeta) error {
	// 创建新的 Video 实例
	video := &dbmodel.Video{
		CreatedAt: time.Now().Unix(), // 使用当前时间戳
		TaskId:    taskId,
		Type:      modelType,
		Username:  dbmodel.GetUsernameById(meta.UserId),
		ChannelId: meta.ChannelId,
		UseId:     meta.UserId,
	}

	// 调用 Insert 方法插入记录
	err := video.Insert()
	if err != nil {
		return fmt.Errorf("failed to insert video log: %v", err)
	}

	return nil
}

func GetVideoResult(c *gin.Context, modelType string, taskId string) *model.ErrorWithStatusCode {
	channelId, err := dbmodel.GetChannelIdByTaskIdAndType(taskId, modelType)
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
	if err != nil {
		return openai.ErrorWrapper(
			fmt.Errorf("failed to get channel: %v", err),
			"database_error",
			http.StatusInternalServerError,
		)
	}

	var fullRequestUrl string
	switch modelType {
	case "zhipu":
		fullRequestUrl = fmt.Sprintf("https://open.bigmodel.cn/api/paas/v4/async-result/%s", taskId)
		logger.SysLog(fmt.Sprintf("fullRequestUrl:%s", fullRequestUrl))
	case "minimax":
		fullRequestUrl = fmt.Sprintf("https://api.minimax.chat/v1/query/video_generation?task_id=%s", taskId)
	default:
		return openai.ErrorWrapper(
			fmt.Errorf("unsupported model type: %s", modelType),
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

	// 添加头部
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+channel.Key)

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

	if modelType == "zhipu" {
		// 直接将 zhipu 的响应体传递给客户端
		c.DataFromReader(http.StatusOK, resp.ContentLength, resp.Header.Get("Content-Type"), resp.Body, nil)
	} else if modelType == "minimax" {
		// 解析 Minimax 的响应
		minimaxResponse, err := pollForCompletion(channel, taskId, 10, 5*time.Second)
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
