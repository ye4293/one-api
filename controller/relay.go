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
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/middleware"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/monitor"
	"github.com/songquanpeng/one-api/relay/channel/midjourney"
	"github.com/songquanpeng/one-api/relay/constant"
	relayconstant "github.com/songquanpeng/one-api/relay/constant"
	"github.com/songquanpeng/one-api/relay/controller"
	"github.com/songquanpeng/one-api/relay/model"

	"github.com/songquanpeng/one-api/relay/util"
)

// https://platform.openai.com/docs/api-reference/chat

func relayHelper(c *gin.Context, relayMode int) *model.ErrorWithStatusCode {
	var err *model.ErrorWithStatusCode
	switch relayMode {
	case constant.RelayModeImagesGenerations:
		err = controller.RelayImageHelper(c, relayMode)
	case constant.RelayModeAudioSpeech:
		fallthrough
	case constant.RelayModeAudioTranslation:
		fallthrough
	case constant.RelayModeAudioTranscription:
		err = controller.RelayAudioHelper(c, relayMode)
	default:
		err = controller.RelayTextHelper(c)
	}
	return err
}

func Relay(c *gin.Context) {
	ctx := c.Request.Context()
	relayMode := constant.Path2RelayMode(c.Request.URL.Path)
	requestID := c.GetHeader("X-Request-ID")
	c.Set("X-Request-ID", requestID)
	if config.DebugEnabled {
		requestBody, _ := common.GetRequestBody(c)
		logger.Debugf(ctx, "request body: %s", string(requestBody))
	}
	channelId := c.GetInt("channel_id")
	userId := c.GetInt("id")
	bizErr := relayHelper(c, relayMode)

	if bizErr == nil {
		monitor.Emit(channelId, true)
		return
	}
	lastFailedChannelId := channelId
	channelName := c.GetString("channel_name")
	group := c.GetString("group")
	originalModel := c.GetString("original_model")
	go processChannelRelayError(ctx, userId, channelId, channelName, bizErr)

	retryTimes := config.RetryTimes
	if !shouldRetry(c, bizErr.StatusCode, bizErr.Error.Message) {
		logger.Errorf(ctx, "Relay error happen, status code is %d, won't retry in this case", bizErr.StatusCode)
		retryTimes = 0
	}

	for i := retryTimes; i > 0; i-- {
		channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, originalModel, i != retryTimes)
		if err != nil {
			logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed: %w", err)
			break
		}
		if channel.Id == lastFailedChannelId {
			continue
		}
		logger.Infof(ctx, "Using channel #%d to retry (remain times %d)", channel.Id, i)

		middleware.SetupContextForSelectedChannel(c, channel, originalModel)
		requestBody, err := common.GetRequestBody(c)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		bizErr = relayHelper(c, relayMode)
		if bizErr == nil {
			return
		}

		channelId = c.GetInt("channel_id")
		lastFailedChannelId = channelId
		channelName = c.GetString("channel_name")
		go processChannelRelayError(ctx, userId, channelId, channelName, bizErr)
	}

	// 如果所有尝试都失败，不处理耗时记录
	if bizErr != nil {
		if bizErr.StatusCode == http.StatusTooManyRequests {
			bizErr.Error.Message = "The current group upstream load is saturated, please try again later."
		}
		c.JSON(bizErr.StatusCode, gin.H{
			"error": gin.H{
				"message": bizErr.Error.Message,
				"type":    "api_error",
				"param":   "",
				"code":    bizErr.Error.Code,
			},
		})
	}
}

func shouldRetry(c *gin.Context, statusCode int, message string) bool {
	if _, ok := c.Get("specific_channel_id"); ok {
		return false
	}
	if statusCode == http.StatusTooManyRequests {
		return true
	}
	if statusCode/100 == 5 {
		return true
	}
	if statusCode == http.StatusBadRequest {
		return false
	}
	if statusCode/100 == 2 {
		return false
	}
	if strings.Contains(message, "Operation not allowed") {
		return true
	}
	return true
}

func processChannelRelayError(ctx context.Context, userId int, channelId int, channelName string, err *model.ErrorWithStatusCode) {
	logger.Errorf(ctx, "relay error (userId #%d,channel #%d): %s", userId, channelId, err.Error.Message)
	if util.ShouldDisableChannel(&err.Error, err.StatusCode) {
		monitor.DisableChannel(channelId, channelName, err.Error.Message)
	} else {
		monitor.Emit(channelId, false)
	}
}

func relayMidjourney(c *gin.Context, relayMode int) *midjourney.MidjourneyResponseWithStatusCode {
	var err *midjourney.MidjourneyResponseWithStatusCode
	switch relayMode {
	case relayconstant.RelayModeMidjourneyNotify:
		err = controller.RelayMidjourneyNotify(c)
	case relayconstant.RelayModeMidjourneyTaskFetch, relayconstant.RelayModeMidjourneyTaskFetchByCondition:
		err = controller.RelayMidjourneyTask(c, relayMode)
	case relayconstant.RelayModeMidjourneyTaskImageSeed:
		err = controller.RelayMidjourneyTaskImageSeed(c)
	case relayconstant.RelayModeSwapFace:
		err = controller.RelaySwapFace(c)
	default:
		err = controller.RelayMidjourneySubmit(c, relayMode)
	}
	return err
}

func RelayMidjourney(c *gin.Context) {
	ctx := c.Request.Context()
	relayMode := c.GetInt("relay_mode")

	var MjErr *midjourney.MidjourneyResponseWithStatusCode
	MjErr = relayMidjourney(c, relayMode)
	retryTimes := config.RetryTimes
	if MjErr == nil {
		return
	}
	// channelId := c.GetInt("channel_id")
	// channelName := c.GetString("channel_name")
	group := c.GetString("group")
	originalModel := c.GetString("original_model")

	// if originalModel != "" {
	// 	ShouldDisabelMidjourneyChannel(channelId, channelName, MjErr)
	// }
	if !MidjourneyShouldRetryByCode(MjErr) { //返回false就不执行重试
		retryTimes = 0
		logger.SysLog("no retry!!!")
	}
	for i := retryTimes; i > 0; i-- {
		if originalModel != "" {
			channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, originalModel, i != retryTimes)
			if err != nil {
				logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed: %+v", err)
				break
			}
			logger.Infof(ctx, "Using channel #%d to retry (remain times %d)", channel.Id, i)
			middleware.SetupContextForSelectedChannel(c, channel, originalModel)
			requestBody, err := common.GetRequestBody(c)
			if err != nil {
				return
			}
			c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
			MjErr := relayMidjourney(c, relayMode)
			if MjErr == nil {
				return
			}
			// ShouldDisabelMidjourneyChannel(channelId, channelName, MjErr)
		} else {
			requestBody, err := common.GetRequestBody(c)
			if err == nil {
				return
			}
			c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
			MjErr = relayMidjourney(c, relayMode)
			if MjErr == nil {
				return
			}
			logger.SysLog(fmt.Sprintf("relayMode:%+v;retry:%d\n", relayMode, i))
		}
	}
	if MjErr != nil {
		statusCode := http.StatusBadRequest
		if MjErr.Response.Code == 30 {
			MjErr.Response.Result = "The current group load is saturated, please try again later, or upgrade your account to improve service quality."
			statusCode = http.StatusTooManyRequests
		}
		c.JSON(statusCode, gin.H{
			"description": util.ProcessString(fmt.Sprintf("%s %s", MjErr.Response.Description, MjErr.Response.Result)),
			"type":        "upstream_error",
			"code":        MjErr.Response.Code,
		})
		channelId := c.GetInt("channel_id")
		logger.SysError(fmt.Sprintf("relay error (channel #%d): %s", channelId, fmt.Sprintf("%s %s", MjErr.Response.Description, MjErr.Response.Result)))
	}
}

func MidjourneyShouldRetryByCode(MjErr *midjourney.MidjourneyResponseWithStatusCode) bool {
	// if MjErr.Response.Code == 23 { //当前渠道已满
	// 	return true
	// }
	// if MjErr.Response.Code == 24 {
	// 	return false
	// }
	// if MjErr.Response.Code != 1 && MjErr.Response.Code != 21 && MjErr.Response.Code != 22 && MjErr.Response.Code != 4 {
	// 	return true
	// }

	return false
}

// func ShouldDisabelMidjourneyChannel(channelId int, channelName string, MjErr *midjourney.MidjourneyResponseWithStatusCode) {
// 	if MjErr.Response.Code == 3 {
// 		monitor.DisableChannel(channelId, channelName, MjErr.Response.Description)
// 	}

// 	if MjErr.StatusCode == 403 || MjErr.StatusCode == 401 || MjErr.StatusCode == 404 {
// 		monitor.DisableChannel(channelId, channelName, MjErr.Response.Description)
// 	}

// }

func RelayNotImplemented(c *gin.Context) {
	err := model.Error{
		Message: "API not implemented",
		Type:    "api_error",
		Param:   "",
		Code:    "api_not_implemented",
	}
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": err,
	})
}

func relaySd(c *gin.Context, relayMode int) *model.ErrorWithStatusCode {
	var err *model.ErrorWithStatusCode
	if relayMode == relayconstant.RelayModeUpscaleCreativeResult || relayMode == relayconstant.RelayModeVideoResult {
		err = controller.GetUpscaleResults(c)
	} else {
		err = controller.RelaySdGenerate(c, relayMode)
	}
	return err
}

func RelaySd(c *gin.Context) {
	ctx := c.Request.Context()
	relayMode := c.GetInt("relay_mode")
	channelId := c.GetInt("channel_id")
	userId := c.GetInt("id")

	SdErr := relaySd(c, relayMode)
	if SdErr == nil {
		return
	}

	lastFailedChannelId := channelId
	group := c.GetString("group")
	originalModel := c.GetString("original_model")
	retryTimes := config.RetryTimes
	if !shouldRetry(c, SdErr.StatusCode, SdErr.Error.Message) {
		logger.Errorf(ctx, "Relay error happen, status code is %d, won't retry in this case", SdErr.StatusCode)
		retryTimes = 0
	}

	for i := retryTimes; i > 0; i-- {
		channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, originalModel, i != retryTimes)
		if err != nil {
			logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed: %w", err)
			break
		}
		if channel.Id == lastFailedChannelId {
			continue
		}
		logger.Infof(ctx, "Using channel #%d to retry (remain times %d)", channel.Id, i)

		middleware.SetupContextForSelectedChannel(c, channel, originalModel)
		requestBody, err := common.GetRequestBody(c)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		SdErr = relaySd(c, relayMode)
		if SdErr == nil {
			return
		}

		channelId = c.GetInt("channel_id")
		lastFailedChannelId = channelId
		channelName := c.GetString("channel_name")
		go processChannelRelayError(ctx, userId, channelId, channelName, SdErr)

	}
	if SdErr != nil {

		c.JSON(http.StatusBadRequest, gin.H{
			"error": SdErr.Code,
		})
	}

}

// func SdShouldRetry(c *gin.Context, err *model.ErrorWithStatusCode) bool {

// }

// func ShouldDisabelSdChannel(channelId int, channelName string, MjErr *midjourney.MidjourneyResponseWithStatusCode) {

// }

func RelayNotFound(c *gin.Context) {
	err := model.Error{
		Message: fmt.Sprintf("Invalid URL (%s %s)", c.Request.Method, c.Request.URL.Path),
		Type:    "invalid_request_error",
		Param:   "",
		Code:    "",
	}
	c.JSON(http.StatusNotFound, gin.H{
		"error": err,
	})
}

func RelayVideoGenerate(c *gin.Context) {
	ctx := c.Request.Context()
	requestID := c.GetHeader("X-Request-ID")
	c.Set("X-Request-ID", requestID)

	channelId := c.GetInt("channel_id")
	userId := c.GetInt("id")
	modelName := c.GetString("original_model")

	bizErr := controller.DoVideoRequest(c, modelName)

	if bizErr == nil {
		return
	}

	lastFailedChannelId := channelId
	channelName := c.GetString("channel_name")
	group := c.GetString("group")

	go processChannelRelayError(ctx, userId, channelId, channelName, bizErr)

	retryTimes := config.RetryTimes
	if !shouldRetry(c, bizErr.StatusCode, bizErr.Error.Message) {
		logger.Errorf(ctx, "Video generation error happen, status code is %d, won't retry in this case", bizErr.StatusCode)
		retryTimes = 0
	}

	for i := retryTimes; i > 0; i-- {
		// 获取新的可用通道
		channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, modelName, i != retryTimes)
		if err != nil {
			logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed: %v", err)
			break
		}
		if channel.Id == lastFailedChannelId {
			continue
		}
		logger.Infof(ctx, "Using channel #%d to retry video generation (remain times %d)", channel.Id, i)

		// 使用新通道的配置更新上下文
		middleware.SetupContextForSelectedChannel(c, channel, modelName)
		requestBody, err := common.GetRequestBody(c)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))

		bizErr = controller.DoVideoRequest(c, modelName)
		if bizErr == nil {
			return
		}

		channelId = c.GetInt("channel_id")
		lastFailedChannelId = channelId
		channelName = c.GetString("channel_name")
		go processChannelRelayError(ctx, userId, channelId, channelName, bizErr)
	}

	// 所有重试都失败后的处理
	if bizErr != nil {
		if bizErr.StatusCode == http.StatusTooManyRequests {
			bizErr.Error.Message = "The current group upstream load is saturated, please try again later."
		}
		c.JSON(bizErr.StatusCode, gin.H{
			"error": gin.H{
				"message": util.ProcessString(bizErr.Error.Message),
				"type":    bizErr.Error.Type,
				"param":   bizErr.Error.Param,
				"code":    bizErr.Error.Code,
			},
		})
	}
}

func RelayVideoResult(c *gin.Context) {
	taskId := c.Query("taskid")
	responseFormat := c.Query("response_format")
	c.Set("response_format", responseFormat)
	bizErr := controller.GetVideoResult(c, taskId)
	if bizErr != nil {
		if bizErr.StatusCode == http.StatusTooManyRequests {
			bizErr.Error.Message = "The current group upstream load is saturated, please try again later."
		}
		c.JSON(bizErr.StatusCode, gin.H{
			"error": util.ProcessString(bizErr.Error.Message),
		})
	}
}

// RelayVideoResultById 通过Query String参数获取视频生成结果
// 完全匹配豆包原始API格式 /api/v3/contents/generations/tasks?id=task_id
func RelayDouBaoVideoResultById(c *gin.Context) {
	taskId := c.Param("taskid")
	logger.SysLog(fmt.Sprintf("doubao-task-id: %s", taskId))
	if taskId == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": "Task ID is required in query parameter 'id'",
				"type":    "invalid_request_error",
				"code":    "missing_required_parameter",
			},
		})
		return
	}
	bizErr := controller.GetVideoResult(c, taskId)
	if bizErr != nil {
		if bizErr.StatusCode == http.StatusTooManyRequests {
			bizErr.Error.Message = "The current group upstream load is saturated, please try again later."
		}
		c.JSON(bizErr.StatusCode, gin.H{
			"error": util.ProcessString(bizErr.Error.Message),
		})
	}
}

func RelayDirectFlux(c *gin.Context) {
	ctx := c.Request.Context()
	requestID := c.GetHeader("X-Request-ID")

	// Get the full path
	fullPath := c.Request.URL.Path
	logger.Debugf(ctx, "[%s] RelayDirectFlux called with path: %s, method: %s", requestID, fullPath, c.Request.Method)

	// Extract the model name (last part of the path)
	// For a path like "/v1/flux-pro-1.1", this will extract "flux-pro-1.1"
	pathParts := strings.Split(fullPath, "/")
	modelName := pathParts[len(pathParts)-1]
	logger.Debugf(ctx, "[%s] Extracted model name: %s", requestID, modelName)

	// You can now use modelName ("flux-pro-1.1") for further processing
	// For example, you might want to set it in the context for use downstream
	c.Set("model_name", modelName)

	userId := c.GetInt("id")
	logger.Debugf(ctx, "[%s] User ID: %d", requestID, userId)

	userGroup, err := dbmodel.CacheGetUserGroup(userId)
	if err != nil {
		logger.Errorf(ctx, "[%s] Failed to get user group: %v", requestID, err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": helper.MessageWithRequestId("Failed to get user group", requestID),
				"type":    "api_error",
			},
		})
		return
	}
	logger.Debugf(ctx, "[%s] User group: %s", requestID, userGroup)
	c.Set("group", userGroup)

	var fullRequestUrl string

	logger.Debugf(ctx, "[%s] Looking for channel with model: %s, group: %s", requestID, modelName, userGroup)
	channel, err := dbmodel.CacheGetRandomSatisfiedChannel(userGroup, modelName, false)
	if err != nil {
		message := fmt.Sprintf("There are no channels available for model %s under the current group %s", modelName, userGroup)
		if channel != nil {
			logger.SysError(fmt.Sprintf("Channel does not exist：%d", channel.Id))
			message = "Database consistency has been violated, please contact the administrator"
		}
		logger.Errorf(ctx, "[%s] %s: %v", requestID, message, err)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{
				"message": helper.MessageWithRequestId(message, requestID),
				"type":    "api_error",
			},
		})
		c.Abort()
		return
	}
	logger.Debugf(ctx, "[%s] Found channel ID: %d, type: %d, base URL: %s", requestID, channel.Id, channel.Type, *channel.BaseURL)
	middleware.SetupContextForSelectedChannel(c, channel, modelName)

	if channel.Type == 46 {
		fullRequestUrl = *channel.BaseURL + fullPath
	} else {
		fullRequestUrl = *channel.BaseURL + "/flux" + fullPath
	}
	logger.Debugf(ctx, "[%s] Full request URL: %s", requestID, fullRequestUrl)

	// Read and log request body for debugging
	requestBodyBytes, err := common.GetRequestBody(c)
	if err != nil {
		logger.Errorf(ctx, "[%s] Failed to read request body: %v", requestID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read request body"})
		return
	}
	if len(requestBodyBytes) > 0 {
		// Only log a portion of the body if it's large
		bodyPreview := string(requestBodyBytes)
		if len(bodyPreview) > 200 {
			bodyPreview = bodyPreview[:200] + "... (truncated)"
		}
		logger.Debugf(ctx, "[%s] Request body: %s", requestID, bodyPreview)
	} else {
		logger.Debugf(ctx, "[%s] Request body is empty", requestID)
	}

	// Create the request to forward
	request, err := http.NewRequest(c.Request.Method, fullRequestUrl, bytes.NewBuffer(requestBodyBytes))
	if err != nil {
		logger.Errorf(ctx, "[%s] Failed to create request: %v", requestID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request"})
		return
	}

	// Get Content-Type from original request or use default
	contentType := c.Request.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	request.Header.Set("Content-Type", contentType)

	// Set authorization header
	request.Header.Set("Authorization", "Bearer "+channel.Key)
	logger.Debugf(ctx, "[%s] Request headers set, sending request", requestID)

	client := &http.Client{
		Timeout: 60 * time.Second,
	}
	response, err := client.Do(request)
	if err != nil {
		logger.Errorf(ctx, "[%s] Failed to send request: %v", requestID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to send request to provider"})
		return
	}
	defer response.Body.Close()
	logger.Debugf(ctx, "[%s] Received response with status code: %d", requestID, response.StatusCode)

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		logger.Errorf(ctx, "[%s] Failed to read provider response: %v", requestID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read provider response"})
		return
	}

	// Log response body preview
	responsePreview := string(responseBody)
	if len(responsePreview) > 200 {
		responsePreview = responsePreview[:200] + "... (truncated)"
	}
	logger.Debugf(ctx, "[%s] Response body: %s", requestID, responsePreview)

	// 处理不同状态码的响应
	if response.StatusCode != 200 {
		// 如果是422错误，直接返回原始错误信息
		var errorResponse map[string]interface{}
		if err := json.Unmarshal(responseBody, &errorResponse); err != nil {
			logger.Errorf(ctx, "[%s] Failed to parse error response: %v", requestID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse error response"})
			return
		}
		logger.Debugf(ctx, "[%s] Returning error response with status code: %d", requestID, response.StatusCode)
		c.JSON(response.StatusCode, errorResponse)
		return
	} else if response.StatusCode == 200 {
		// 如果是200成功，解析JSON并获取polling_url
		var successResponse map[string]interface{}
		if err := json.Unmarshal(responseBody, &successResponse); err != nil {
			logger.Errorf(ctx, "[%s] Failed to parse success response: %v", requestID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse success response"})
			return
		}

		// 获取polling_url并发起第二次请求
		pollingURL, ok := successResponse["polling_url"].(string)
		if !ok {
			logger.Errorf(ctx, "[%s] polling_url not found or not a string in response: %v", requestID, successResponse)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid response format, polling_url not found"})
			return
		}
		logger.Debugf(ctx, "[%s] Found polling URL: %s", requestID, pollingURL)

		// 创建并发送polling请求
		pollingRequest, err := http.NewRequest("GET", pollingURL, nil)
		if err != nil {
			logger.Errorf(ctx, "[%s] Failed to create polling request: %v", requestID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create polling request"})
			return
		}

		// 设置polling请求的头信息
		pollingRequest.Header.Set("x-key", channel.Key)
		logger.Debugf(ctx, "[%s] Sending polling request", requestID)

		// 执行polling请求
		pollingResponse, err := client.Do(pollingRequest)
		if err != nil {
			logger.Errorf(ctx, "[%s] Failed to execute polling request: %v", requestID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to execute polling request"})
			return
		}
		defer pollingResponse.Body.Close()
		logger.Debugf(ctx, "[%s] Received polling response with status code: %d", requestID, pollingResponse.StatusCode)

		// 读取并返回polling响应
		pollingBody, err := io.ReadAll(pollingResponse.Body)
		if err != nil {
			logger.Errorf(ctx, "[%s] Failed to read polling response: %v", requestID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read polling response"})
			return
		}

		// Log polling response preview
		pollingPreview := string(pollingBody)
		if len(pollingPreview) > 200 {
			pollingPreview = pollingPreview[:200] + "... (truncated)"
		}
		logger.Debugf(ctx, "[%s] Polling response body: %s", requestID, pollingPreview)

		logger.Debugf(ctx, "[%s] Returning polling response to client", requestID)
		c.Writer.WriteHeader(pollingResponse.StatusCode)
		c.Writer.Write(pollingBody)
	} else {
		// 处理其他状态码
		logger.Debugf(ctx, "[%s] Returning original response with status code: %d", requestID, response.StatusCode)
		c.Writer.WriteHeader(response.StatusCode)
		c.Writer.Write(responseBody)
	}
	logger.Debugf(ctx, "[%s] RelayDirectFlux completed", requestID)
}

func RelayOcr(c *gin.Context) {
	ctx := c.Request.Context()
	channelId := c.GetInt("channel_id")
	channel, err := dbmodel.GetChannelById(channelId, true)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("Failed to get channel: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get channel"})
		return
	}

	userId := c.GetInt("id")
	startTime := time.Now()

	fullRequestUrl := "https://api.mistral.ai/v1/ocr"
	requestBody, err := common.GetRequestBody(c)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("Failed to read request body: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read request body"})
		return
	}

	// Create the request to forward to Mistral API
	request, err := http.NewRequest(c.Request.Method, fullRequestUrl, bytes.NewBuffer(requestBody))
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("Failed to create request: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request"})
		return
	}

	// Set necessary headers
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+channel.Key)

	// Send the request
	client := &http.Client{}
	response, err := client.Do(request)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("Failed to send request: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to send request to provider"})
		return
	}
	defer response.Body.Close()

	// Read response body
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("Failed to read provider response: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read provider response"})
		return
	}

	// Calculate duration
	duration := float64(time.Since(startTime).Milliseconds()) / 1000.0

	// Process the response based on status code
	if response.StatusCode == http.StatusOK {
		// For successful responses, extract usage info
		var ocrResponse map[string]interface{}
		if err := json.Unmarshal(responseBody, &ocrResponse); err != nil {
			logger.Error(ctx, fmt.Sprintf("Failed to parse OCR response: %v", err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse OCR response"})
			return
		}

		// Extract usage info if available
		if usageInfo, ok := ocrResponse["usage_info"].(map[string]interface{}); ok {
			pagesProcessed := 0
			docSizeBytes := 0

			if pp, ok := usageInfo["pages_processed"].(float64); ok {
				pagesProcessed = int(pp)
			}
			if dsb, ok := usageInfo["doc_size_bytes"].(float64); ok {
				docSizeBytes = int(dsb)
			}

			// Log usage info
			logger.Infof(ctx, "OCR usage info - channel #%d: pages processed: %d, doc size: %d bytes",
				channelId, pagesProcessed, docSizeBytes)

			// Define model name and token name
			modelName := "mistral-ocr-latest"
			tokenName := c.GetString("token_name")

			// Calculate quota: pages_processed * 1/1000 * 500000
			quota := float64(pagesProcessed) * 0.001 * 500000

			// Record consumption log with all required parameters
			logContent := fmt.Sprintf("OCR pages processed: %d, doc size: %d bytes", pagesProcessed, docSizeBytes)
			title := ""
			httpReferer := ""

			dbmodel.RecordConsumeLog(ctx, userId, channelId, pagesProcessed, docSizeBytes, modelName, tokenName, int64(quota), logContent, duration, title, httpReferer)

			// Update user and channel quota
			err := dbmodel.PostConsumeTokenQuota(c.GetInt("token_id"), int64(quota))
			if err != nil {
				logger.SysError("error consuming token remain quota: " + err.Error())
			}

			err = dbmodel.CacheUpdateUserQuota(ctx, userId)
			if err != nil {
				logger.SysError("error update user quota cache: " + err.Error())
			}

			dbmodel.UpdateUserUsedQuotaAndRequestCount(userId, int64(quota))
			dbmodel.UpdateChannelUsedQuota(channelId, int64(quota))
		}

		// Return the original response to the client
		c.Data(http.StatusOK, "application/json", responseBody)
	} else {
		// For error responses, just forward the error
		c.Data(response.StatusCode, "application/json", responseBody)
	}
}

func RelayRecraft(c *gin.Context) {
	ctx := c.Request.Context()
	requestID := c.GetHeader("X-Request-ID")
	c.Set("X-Request-ID", requestID)

	channelId := c.GetInt("channel_id")
	userId := c.GetInt("id")
	startTime := time.Now()
	modelName := c.GetString("model")

	channel, err := dbmodel.GetChannelById(channelId, true)
	if err != nil {
		logger.Errorf(ctx, "[%s] Failed to get channel: %v", requestID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get channel"})
		return
	}

	fullPath := c.Request.URL.Path
	var fullRequestUrl string
	if channel.Type == 43 {
		fullRequestUrl = *channel.BaseURL + fullPath
	} else {
		fullRequestUrl = *channel.BaseURL + "/recraft" + fullPath
	}
	logger.Debugf(ctx, "[%s] Full request URL: %s", requestID, fullRequestUrl)

	// Read request body
	requestBodyBytes, err := common.GetRequestBody(c)
	if err != nil {
		logger.Errorf(ctx, "[%s] Failed to read request body: %v", requestID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read request body"})
		return
	}

	// Parse request to check for normalizeResponseFormat parameter
	var requestData map[string]interface{}
	normalizeFormat := false
	if len(requestBodyBytes) > 0 {
		if err := json.Unmarshal(requestBodyBytes, &requestData); err == nil {
			// Check if normalizeResponseFormat is present and true
			if format, ok := requestData["normalizeResponseFormat"].(bool); ok && format {
				normalizeFormat = true
				logger.Debugf(ctx, "[%s] Response format will be normalized to OpenAI format", requestID)

				// Remove the parameter before forwarding to Recraft
				delete(requestData, "normalizeResponseFormat")

				// Re-encode the modified request
				modifiedRequestBytes, err := json.Marshal(requestData)
				if err == nil {
					requestBodyBytes = modifiedRequestBytes
				} else {
					logger.Warnf(ctx, "[%s] Failed to re-encode modified request: %v", requestID, err)
				}
			} else {
				logger.Debugf(ctx, "[%s] Using native Recraft response format", requestID)
			}
		}
	}

	// Create the request to forward
	request, err := http.NewRequest(c.Request.Method, fullRequestUrl, bytes.NewBuffer(requestBodyBytes))
	if err != nil {
		logger.Errorf(ctx, "[%s] Failed to create request: %v", requestID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request"})
		return
	}

	// Get Content-Type from original request or use default
	contentType := c.Request.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	request.Header.Set("Content-Type", contentType)

	// Set authorization header
	request.Header.Set("Authorization", "Bearer "+channel.Key)

	// Send the request
	client := &http.Client{
		Timeout: 120 * time.Second,
	}
	response, err := client.Do(request)
	if err != nil {
		logger.Errorf(ctx, "[%s] Failed to send request: %v", requestID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to send request to provider"})
		return
	}
	defer response.Body.Close()

	// Read response body
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		logger.Errorf(ctx, "[%s] Failed to read provider response: %v", requestID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read provider response"})
		return
	}

	// Calculate duration
	duration := float64(time.Since(startTime).Milliseconds()) / 1000.0

	// Process the response based on status code
	if response.StatusCode == http.StatusOK {
		// For successful responses, deduct quota

		// Define default quota based on endpoint
		var modelPrice float64
		defaultPrice, ok := common.DefaultModelPrice[modelName]
		if !ok {
			modelPrice = 0.1
		} else {
			modelPrice = defaultPrice
		}
		quota := int64(modelPrice * 500000)

		// Record consumption log
		tokenName := c.GetString("token_name")
		logContent := fmt.Sprintf("Recraft API call: %s", modelName)
		title := ""
		httpReferer := ""

		// Use placeholder values for input/output tokens since we don't have actual token counts
		inputTokens := 0
		outputTokens := 0

		dbmodel.RecordConsumeLog(ctx, userId, channelId, inputTokens, outputTokens,
			modelName, tokenName, quota, logContent, duration, title, httpReferer)

		// Update user and channel quota
		err := dbmodel.PostConsumeTokenQuota(c.GetInt("token_id"), quota)
		if err != nil {
			logger.SysError("error consuming token remain quota: " + err.Error())
		}

		err = dbmodel.CacheUpdateUserQuota(ctx, userId)
		if err != nil {
			logger.SysError("error update user quota cache: " + err.Error())
		}

		dbmodel.UpdateUserUsedQuotaAndRequestCount(userId, quota)
		dbmodel.UpdateChannelUsedQuota(channelId, quota)

		logger.Infof(ctx, "[%s] Recraft API call completed - model: %s, channel #%d, quota: %d",
			requestID, modelName, channelId, quota)

		// Handle response format normalization if requested
		if normalizeFormat {
			var recraftResponse map[string]interface{}
			if err := json.Unmarshal(responseBody, &recraftResponse); err == nil {
				// Convert Recraft response to OpenAI DALL-E format
				openaiResponse := map[string]interface{}{
					"created": time.Now().Unix(),
					"data": []map[string]interface{}{
						{
							"url":            "",
							"revised_prompt": "",
						},
					},
				}

				// Extract image URL from Recraft response
				if image, ok := recraftResponse["image"].(map[string]interface{}); ok {
					if url, ok := image["url"].(string); ok {
						openaiResponse["data"].([]map[string]interface{})[0]["url"] = url
					}
				}

				// Convert to JSON
				normalizedResponse, err := json.Marshal(openaiResponse)
				if err == nil {
					responseBody = normalizedResponse
					logger.Debugf(ctx, "[%s] Response normalized to OpenAI format", requestID)
				} else {
					logger.Warnf(ctx, "[%s] Failed to normalize response: %v", requestID, err)
				}
			} else {
				logger.Warnf(ctx, "[%s] Failed to parse Recraft response for normalization: %v", requestID, err)
			}
		} else {
			// When normalizeResponseFormat is false or not present, use native Recraft format
			logger.Debugf(ctx, "[%s] Using native Recraft response format", requestID)
			// responseBody is already the native format, no processing needed
		}
	} else {
		// Log error responses
		logger.Errorf(ctx, "[%s] Recraft API error: %d - %s", requestID, response.StatusCode, string(responseBody))

		// Check if we should disable the channel
		if response.StatusCode == 401 || response.StatusCode == 403 {
			monitor.DisableChannel(channelId, channel.Name, "Authentication error with Recraft API")
		}

		// Handle error format normalization if requested
		if normalizeFormat {
			var recraftError map[string]interface{}
			if err := json.Unmarshal(responseBody, &recraftError); err == nil {
				// Convert Recraft error to OpenAI error format
				openaiError := map[string]interface{}{
					"error": map[string]interface{}{
						"message": "An error occurred",
						"type":    "api_error",
						"code":    "recraft_error",
					},
				}

				// Extract error message and code from Recraft response
				if message, ok := recraftError["message"].(string); ok {
					openaiError["error"].(map[string]interface{})["message"] = message
				}
				if code, ok := recraftError["code"].(string); ok {
					openaiError["error"].(map[string]interface{})["code"] = code
				}

				// Convert to JSON
				normalizedError, err := json.Marshal(openaiError)
				if err == nil {
					responseBody = normalizedError
					logger.Debugf(ctx, "[%s] Error response normalized to OpenAI format", requestID)
				} else {
					logger.Warnf(ctx, "[%s] Failed to normalize error response: %v", requestID, err)
				}
			} else {
				logger.Warnf(ctx, "[%s] Failed to parse Recraft error for normalization: %v", requestID, err)
			}
		} else {
			// When normalizeResponseFormat is false or not present, use native Recraft error format
			logger.Debugf(ctx, "[%s] Using native Recraft error format", requestID)
			// responseBody is already the native format, no processing needed
		}
	}

	// Pass through the response regardless of status code
	c.Data(response.StatusCode, response.Header.Get("Content-Type"), responseBody)
}

func RelayImageGenerateAsync(c *gin.Context) {
	// ctx := c.Request.Context()
	requestID := c.GetHeader("X-Request-ID")
	c.Set("X-Request-ID", requestID)

	// channelId := c.GetInt("channel_id")
	// userId := c.GetInt("id")
	modelName := c.GetString("original_model")
	bizErr := controller.DoImageRequest(c, modelName)
	if bizErr == nil {
		return
	}
	// 所有重试都失败后的处理
	if bizErr != nil {
		if bizErr.StatusCode == http.StatusTooManyRequests {
			bizErr.Error.Message = "The current group upstream load is saturated, please try again later."
		}
		c.JSON(bizErr.StatusCode, gin.H{
			"error": gin.H{
				"message": util.ProcessString(bizErr.Error.Message),
				"type":    bizErr.Error.Type,
				"param":   bizErr.Error.Param,
				"code":    bizErr.Error.Code,
			},
		})
	}
}

func RelayImageResult(c *gin.Context) {
	taskId := c.Query("taskid")
	bizErr := controller.GetImageResult(c, taskId)
	if bizErr != nil {
		if bizErr.StatusCode == http.StatusTooManyRequests {
			bizErr.Error.Message = "The current group upstream load is saturated, please try again later."
		}
		c.JSON(bizErr.StatusCode, gin.H{
			"error": gin.H{
				"message": util.ProcessString(bizErr.Error.Message),
				"type":    bizErr.Error.Type,
				"param":   bizErr.Error.Param,
				"code":    bizErr.Error.Code,
			},
		})
	}

}
