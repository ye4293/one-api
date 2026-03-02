package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/middleware"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/monitor"
	"github.com/songquanpeng/one-api/relay/channel/flux"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

// RelayFlux 处理 Flux API 的异步请求（支持失败重试）
func RelayFlux(c *gin.Context) {
	ctx := c.Request.Context()

	requestBody, err := common.GetRequestBody(c)
	if err != nil {
		logger.Errorf(ctx, "Flux: failed to get request body: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "failed to read request body", "type": "invalid_request_error"}})
		return
	}

	channelId := c.GetInt("channel_id")
	channelName := c.GetString("channel_name")
	group := c.GetString("group")
	originalModel := c.GetString("original_model")
	keyIndex := c.GetInt("key_index")

	logger.Infof(ctx, "Flux 请求开始: channel_id=%d, model=%s", channelId, originalModel)

	// 首次尝试
	bizErr := relayFluxHelper(c, requestBody)
	if bizErr == nil {
		monitor.Emit(channelId, true)
		return
	}

	logger.Errorf(ctx, "Flux 首次请求失败: channel_id=%d, status=%d, error=%s",
		channelId, bizErr.StatusCode, bizErr.Error.Message)

	go processFluxChannelErrorAsync(ctx, channelId, channelName, keyIndex, bizErr, originalModel)

	retryTimes := config.RetryTimes
	if !shouldRetry(c, bizErr.StatusCode, bizErr.Error.Message) {
		logger.Errorf(ctx, "Flux: status code %d, won't retry", bizErr.StatusCode)
		retryTimes = 0
	}

	failedChannelIds := []int{}
	initialFailedChannelId := channelId

	for i := retryTimes; i > 0; i-- {
		channel, err := model.CacheGetRandomSatisfiedChannel(group, originalModel, 0, "", failedChannelIds)
		if err != nil {
			logger.Errorf(ctx, "Flux retry: no available channel (excludedChannels: %v): %v", failedChannelIds, err)
			break
		}

		if i == retryTimes {
			failedChannelIds = append(failedChannelIds, initialFailedChannelId)
		}

		attempt := retryTimes - i + 1
		logger.Infof(ctx, "Flux retry %d/%d: channel #%d (%s) -> #%d (%s), model=%s, reason=%s",
			attempt, retryTimes, channelId, channelName, channel.Id, channel.Name, originalModel, bizErr.Error.Message)

		middleware.SetupContextForSelectedChannel(c, channel, originalModel)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))

		bizErr = relayFluxHelper(c, requestBody)
		if bizErr == nil {
			monitor.Emit(channel.Id, true)
			return
		}

		channelId = c.GetInt("channel_id")
		channelName = c.GetString("channel_name")
		keyIndex = c.GetInt("key_index")
		failedChannelIds = append(failedChannelIds, channelId)

		logger.Errorf(ctx, "Flux retry %d/%d failed: channel #%d, status=%d, error=%s",
			attempt, retryTimes, channelId, bizErr.StatusCode, bizErr.Error.Message)

		go processFluxChannelErrorAsync(ctx, channelId, channelName, keyIndex, bizErr, originalModel)
	}

	if bizErr != nil {
		c.JSON(bizErr.StatusCode, gin.H{
			"error": gin.H{
				"message": bizErr.Error.Message,
				"type":    "api_error",
			},
		})
	}
}

// relayFluxHelper 执行单次 Flux 请求（成功时写入客户端响应，失败时仅返回错误）
func relayFluxHelper(c *gin.Context, requestBody []byte) *relaymodel.ErrorWithStatusCode {
	meta := util.GetRelayMeta(c)

	adaptor := &flux.Adaptor{}
	adaptor.Init(meta)

	if err := adaptor.CreatePendingRecord(c, meta); err != nil {
		logger.Errorf(c, "Flux 创建 pending 记录失败: %v", err)
		return &relaymodel.ErrorWithStatusCode{
			StatusCode: http.StatusInternalServerError,
			Error:      relaymodel.Error{Message: "create pending record failed: " + err.Error()},
		}
	}

	c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
	convertedBody, err := adaptor.ConvertFluxRequest(c, meta)
	if err != nil {
		logger.Errorf(c, "Flux 请求转换失败: %v", err)
		return &relaymodel.ErrorWithStatusCode{
			StatusCode: http.StatusBadRequest,
			Error:      relaymodel.Error{Message: "convert request failed: " + err.Error()},
		}
	}

	resp, err := adaptor.DoRequest(c, meta, bytes.NewReader(convertedBody))
	if err != nil {
		logger.Errorf(c, "Flux 请求执行失败: channel_id=%d, error=%v", meta.ChannelId, err)
		return &relaymodel.ErrorWithStatusCode{
			StatusCode: http.StatusInternalServerError,
			Error:      relaymodel.Error{Message: "request failed: " + err.Error()},
		}
	}

	_, errResp := adaptor.DoResponse(c, resp, meta)
	if errResp != nil {
		return errResp
	}

	logger.Infof(c, "Flux 请求成功: channel_id=%d", meta.ChannelId)
	return nil
}

// HandleFluxCallback 处理 Flux API 回调通知
func HandleFluxCallback(c *gin.Context) {
	bodyBytes, err := c.GetRawData()
	if err != nil {
		logger.Errorf(c, "Flux callback read body error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	logger.Infof(c, "Flux callback raw JSON: %s", string(bodyBytes))

	var notification flux.FluxCallbackNotification
	if err := json.Unmarshal(bodyBytes, &notification); err != nil {
		logger.Errorf(c, "Flux callback parse error: %v, raw body: %s", err, string(bodyBytes))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	logger.Debugf(c, "Flux callback parsed: TaskId=%s, Status=%s, Progress=%d, Cost=%.4f, Error=%s",
		notification.TaskId, notification.Status, notification.Progress, notification.Cost, notification.Error)

	success, statusCode, message := flux.HandleCallback(c, notification)

	if success {
		c.JSON(statusCode, gin.H{"message": message})
	} else {
		c.JSON(statusCode, gin.H{"error": message})
	}
}

// GetFlux 查询 Flux 任务结果
func GetFlux(c *gin.Context) {
	taskID := c.Param("id")
	if taskID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "task_id is required"})
		return
	}

	fromSource := c.DefaultQuery("from_source", "false")
	isFromSource := fromSource == "true" || fromSource == "1"

	logger.Infof(c, "查询 Flux 任务: task_id=%s, from_source=%v", taskID, isFromSource)

	image, err := model.GetImageByTaskId(taskID)
	if err != nil {
		logger.Errorf(c, "Flux 任务不存在: task_id=%s, error=%v", taskID, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}

	if !isFromSource {
		logger.Infof(c, "从本地数据库返回 Flux 任务: task_id=%s, status=%s", taskID, image.Status)

		if image.Status == flux.TaskStatusSucceed && image.Result != "" {
			c.Data(http.StatusOK, "application/json", []byte(image.Result))
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"id":     image.TaskId,
			"status": image.Status,
			"error":  image.FailReason,
		})
		return
	}

	channel, err := model.GetChannelById(image.ChannelId, true)
	if err != nil {
		logger.Errorf(c, "获取 channel 失败: channel_id=%d, error=%v", image.ChannelId, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get channel"})
		return
	}

	if channel.BaseURL == nil || *channel.BaseURL == "" {
		logger.Errorf(c, "Channel base_url 为空: channel_id=%d", image.ChannelId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid channel configuration"})
		return
	}

	adaptor := &flux.Adaptor{}
	statusCode, responseBody, err := adaptor.QueryResult(c, taskID, *channel.BaseURL, channel.Key)
	if err != nil {
		logger.Errorf(c, "查询 Flux 结果失败: task_id=%s, error=%v", taskID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Data(statusCode, "application/json", responseBody)
	logger.Infof(c, "Flux 查询完成（源站）: task_id=%s, status=%d", taskID, statusCode)
}

// processFluxChannelErrorAsync 异步处理 Flux 渠道错误（用于 goroutine 调用，参数安全）
func processFluxChannelErrorAsync(ctx context.Context, channelId int, channelName string, keyIndex int, errResp *relaymodel.ErrorWithStatusCode, modelName string) {
	if !util.ShouldDisableChannel(&errResp.Error, errResp.StatusCode) {
		monitor.Emit(channelId, false)
		return
	}

	channel, err := model.GetChannelById(channelId, true)
	if err != nil {
		logger.Errorf(ctx, "Flux auto-disable: failed to get channel %d: %v", channelId, err)
		monitor.Emit(channelId, false)
		return
	}

	if channel.MultiKeyInfo.IsMultiKey || channel.MultiKeyInfo.KeyCount > 1 {
		keyErr := channel.HandleKeyError(keyIndex, errResp.Error.Message, errResp.StatusCode, modelName)
		if keyErr != nil {
			logger.Errorf(ctx, "Flux auto-disable: failed to handle key error for channel %d, key %d: %v",
				channel.Id, keyIndex, keyErr)
		}
	} else {
		if channel.AutoDisabled {
			monitor.DisableChannelWithStatusCode(channelId, channelName, errResp.Error.Message, modelName, errResp.StatusCode)
		} else {
			logger.Infof(ctx, "Flux: channel #%d (%s) should be disabled but auto-disable is turned off", channelId, channelName)
		}
	}
	monitor.Emit(channelId, false)
}
