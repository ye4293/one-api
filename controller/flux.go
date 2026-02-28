package controller

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/monitor"
	"github.com/songquanpeng/one-api/relay/channel/flux"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

// RelayFlux 处理 Flux API 的异步请求
func RelayFlux(c *gin.Context) {
	// 1. 获取 meta 信息
	meta := util.GetRelayMeta(c)

	// 记录请求开始
	logger.Infof(c, "Flux 请求开始: user_id=%d, channel_id=%d, model=%s, path=%s",
		meta.UserId, meta.ChannelId, meta.OriginModelName, meta.RequestURLPath)

	// 2. 创建 Flux 适配器
	adaptor := &flux.Adaptor{}
	adaptor.Init(meta)

	// 3. 在发起请求前创建 pending 状态的数据库记录
	if err := adaptor.CreatePendingRecord(c, meta); err != nil {
		logger.Errorf(c, "Flux 创建 pending 记录失败: user_id=%d, error=%v", meta.UserId, err)
		errResp := openai.ErrorWrapper(err, "create_pending_record_failed", http.StatusInternalServerError)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	// 4. 转换请求（移除不需要的字段）
	convertedBody, err := adaptor.ConvertFluxRequest(c, meta)
	if err != nil {
		logger.Errorf(c, "Flux 请求转换失败: user_id=%d, error=%v", meta.UserId, err)
		errResp := openai.ErrorWrapper(err, "convert_request_failed", http.StatusBadRequest)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	// 5. 执行请求
	resp, err := adaptor.DoRequest(c, meta, bytes.NewReader(convertedBody))
	if err != nil {
		logger.Errorf(c, "Flux 请求执行失败: user_id=%d, channel_id=%d, error=%v",
			meta.UserId, meta.ChannelId, err)
		errResp := openai.ErrorWrapper(err, "request_failed", http.StatusInternalServerError)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	// 6. 处理响应（包括计费、更新记录、透传响应）
	_, errResp := adaptor.DoResponse(c, resp, meta)
	if errResp != nil {
		logger.Errorf(c, "Flux 响应处理失败: user_id=%d, error=%v", meta.UserId, errResp.Error.Message)
		processFluxChannelError(c, meta, errResp)
		return
	}

	logger.Infof(c, "Flux 请求完成: user_id=%d, channel_id=%d", meta.UserId, meta.ChannelId)
}

// HandleFluxCallback 处理 Flux API 回调通知
func HandleFluxCallback(c *gin.Context) {
	// 读取原始 body
	bodyBytes, err := c.GetRawData()
	if err != nil {
		logger.Errorf(c, "Flux callback read body error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// 【调试日志】打印原始 JSON 数据
	logger.Infof(c, "Flux callback raw JSON: %s", string(bodyBytes))

	// 解析回调通知
	var notification flux.FluxCallbackNotification
	if err := json.Unmarshal(bodyBytes, &notification); err != nil {
		logger.Errorf(c, "Flux callback parse error: %v, raw body: %s", err, string(bodyBytes))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	// 【调试日志】打印解析后的结构体（包含所有字段的值）
	logger.Debugf(c, "Flux callback parsed notification: TaskId=%s, Status=%s, Progress=%d, Cost=%.4f, InputMP=%.2f, OutputMP=%.2f, Error=%s, PollingURL=%s, Result=%+v",
		notification.TaskId, notification.Status, notification.Progress, notification.Cost, notification.InputMP, notification.OutputMP, notification.Error, notification.PollingURL, notification.Result)

	// 调用业务逻辑处理回调
	success, statusCode, message := flux.HandleCallback(c, notification)

	// 返回响应
	if success {
		c.JSON(statusCode, gin.H{"message": message})
	} else {
		c.JSON(statusCode, gin.H{"error": message})
	}
}

// GetFlux 查询 Flux 任务结果
// 查询参数 from_source: true - 从 Flux API 源站获取，false(默认) - 从本地数据库读取
func GetFlux(c *gin.Context) {
	// 1. 获取任务 ID
	taskID := c.Param("id")
	if taskID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "task_id is required"})
		return
	}

	// 2. 获取查询参数 from_source（默认为 false，从数据库读取）
	fromSource := c.DefaultQuery("from_source", "false")
	isFromSource := fromSource == "true" || fromSource == "1"

	logger.Infof(c, "查询 Flux 任务: task_id=%s, from_source=%v", taskID, isFromSource)

	// 3. 从数据库查询任务记录
	image, err := model.GetImageByTaskId(taskID)
	if err != nil {
		logger.Errorf(c, "Flux 任务不存在: task_id=%s, error=%v", taskID, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}

	// 4. 如果不从源站获取，直接返回数据库中存储的数据
	if !isFromSource {
		logger.Infof(c, "从本地数据库返回 Flux 任务: task_id=%s, status=%s", taskID, image.Status)

		// 只有状态为成功时才返回完整的 result 数据
		if image.Status == flux.TaskStatusSucceed && image.Result != "" {
			// 直接返回存储的完整 API 响应 JSON
			c.Data(http.StatusOK, "application/json", []byte(image.Result))
			return
		}

		// 其他状态返回基本信息
		c.JSON(http.StatusOK, gin.H{
			"id":     image.TaskId,
			"status": image.Status,
			"error":  image.FailReason,
		})
		return
	}

	// 5. 从源站获取 - 查询渠道信息（获取 API key 和 base_url）
	channel, err := model.GetChannelById(image.ChannelId, true)
	if err != nil {
		logger.Errorf(c, "获取 channel 失败: channel_id=%d, error=%v", image.ChannelId, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get channel"})
		return
	}

	// 6. 验证 channel 信息
	if channel.BaseURL == nil || *channel.BaseURL == "" {
		logger.Errorf(c, "Channel base_url 为空: channel_id=%d", image.ChannelId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid channel configuration"})
		return
	}

	// 7. 调用 Adaptor 层从源站查询结果
	adaptor := &flux.Adaptor{}
	statusCode, responseBody, err := adaptor.QueryResult(c, taskID, *channel.BaseURL, channel.Key)
	if err != nil {
		logger.Errorf(c, "查询 Flux 结果失败: task_id=%s, error=%v", taskID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 8. 透传源站响应给客户端
	c.Data(statusCode, "application/json", responseBody)
	logger.Infof(c, "Flux 查询完成（源站）: task_id=%s, status=%d", taskID, statusCode)
}

// processFluxChannelError 处理 Flux 渠道错误，触发自动禁用逻辑
func processFluxChannelError(c *gin.Context, meta *util.RelayMeta, errResp *relaymodel.ErrorWithStatusCode) {
	if !util.ShouldDisableChannel(&errResp.Error, errResp.StatusCode) {
		monitor.Emit(meta.ChannelId, false)
		return
	}

	channel, err := model.GetChannelById(meta.ChannelId, true)
	if err != nil {
		logger.Errorf(c, "Flux auto-disable: failed to get channel %d: %v", meta.ChannelId, err)
		monitor.Emit(meta.ChannelId, false)
		return
	}

	keyIndex := 0
	if meta.KeyIndex != nil {
		keyIndex = *meta.KeyIndex
	}

	if channel.MultiKeyInfo.IsMultiKey || channel.MultiKeyInfo.KeyCount > 1 {
		keyErr := channel.HandleKeyError(keyIndex, errResp.Error.Message, errResp.StatusCode, meta.OriginModelName)
		if keyErr != nil {
			logger.Errorf(c, "Flux auto-disable: failed to handle key error for channel %d, key %d: %v",
				channel.Id, keyIndex, keyErr)
		}
	} else {
		if channel.AutoDisabled {
			monitor.DisableChannelWithStatusCode(meta.ChannelId, channel.Name, errResp.Error.Message, meta.OriginModelName, errResp.StatusCode)
		} else {
			logger.Infof(c, "Flux: channel #%d (%s) should be disabled but auto-disable is turned off", meta.ChannelId, channel.Name)
		}
	}
	monitor.Emit(meta.ChannelId, false)
}
