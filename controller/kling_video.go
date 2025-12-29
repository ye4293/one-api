package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/songquanpeng/one-api/common/config"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/kling"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/util"
)

// RelayKlingVideo 处理 Kling 视频生成请求
func RelayKlingVideo(c *gin.Context) {
	meta := util.GetRelayMeta(c)

	// 确定请求类型
	requestType := kling.DetermineRequestType(c.Request.URL.Path)
	if requestType == "" {
		err := openai.ErrorWrapper(fmt.Errorf("unsupported endpoint"), "invalid_endpoint", http.StatusBadRequest)
		c.JSON(err.StatusCode, err.Error)
		return
	}

	// 读取并解析请求体
	bodyBytes, err := c.GetRawData()
	if err != nil {
		errResp := openai.ErrorWrapper(err, "invalid_request_body", http.StatusBadRequest)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	var requestParams map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &requestParams); err != nil {
		errResp := openai.ErrorWrapper(err, "invalid_request_json", http.StatusBadRequest)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	// 计算预估费用
	quota := kling.CalculateQuota(requestParams, requestType)

	// 检查用户余额（后扣费模式：仅验证余额，不实际扣费）
	userQuota, err := dbmodel.CacheGetUserQuota(c.Request.Context(), meta.UserId)
	if err != nil {
		errResp := openai.ErrorWrapper(err, "get_user_quota_error", http.StatusInternalServerError)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}
	if userQuota < quota {
		errResp := openai.ErrorWrapper(fmt.Errorf("余额不足"), "insufficient_quota", http.StatusPaymentRequired)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	// 获取渠道信息
	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		errResp := openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	meta.APIKey = channel.Key
	if channel.BaseURL != nil && *channel.BaseURL != "" {
		meta.BaseURL = *channel.BaseURL
	}

	// 获取用户信息
	user, err := dbmodel.GetUserById(meta.UserId, false)
	if err != nil {
		logger.SysError(fmt.Sprintf("获取用户信息失败: user_id=%d, error=%v", meta.UserId, err))
		user = &dbmodel.User{Username: ""}
	}

	// 先创建 Video 记录以获取 ID（用于 external_task_id）
	video := &dbmodel.Video{
		TaskId:    "", // 暂时为空，等 Kling 返回后更新
		UserId:    meta.UserId,
		Username:  user.Username,
		ChannelId: meta.ChannelId,
		Model:     kling.GetModelNameFromRequest(requestParams),
		Provider:  "kling",
		Type:      requestType,
		Status:    kling.TaskStatusPending,
		Quota:     quota,
		Prompt:    kling.GetPromptFromRequest(requestParams),
		Duration:  fmt.Sprintf("%d", kling.GetDurationFromRequest(requestParams)),
	}
	if err := video.Insert(); err != nil {
		errResp := openai.ErrorWrapper(err, "database_error", http.StatusInternalServerError)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	// 构建回调URL
	var callbackURL string
	if config.ServerAddress == "" {
		errResp := openai.ErrorWrapper(nil, "invalid_server_address", http.StatusInternalServerError)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}
	callbackURL = fmt.Sprintf("%s/kling/internal/callback", config.ServerAddress)

	// 调用 Kling API
	adaptor := &kling.Adaptor{RequestType: requestType}
	adaptor.Init(meta)

	// 转换请求并注入回调URL和 external_task_id (使用 video.Id)
	convertedBody, err := adaptor.ConvertRequest(c, meta, requestParams, callbackURL, video.Id)
	if err != nil {
		video.Status = kling.TaskStatusFailed
		video.FailReason = err.Error()
		video.Update()
		errResp := openai.ErrorWrapper(err, "convert_request_failed", http.StatusInternalServerError)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	resp, err := adaptor.DoRequest(c, meta, bytes.NewReader(convertedBody))
	if err != nil {
		video.Status = kling.TaskStatusFailed
		video.FailReason = err.Error()
		video.Update()
		errResp := openai.ErrorWrapper(err, "request_failed", http.StatusInternalServerError)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	klingResp, errWithCode := adaptor.DoResponse(c, resp, meta)
	if errWithCode != nil {
		video.Status = kling.TaskStatusFailed
		video.FailReason = errWithCode.Error.Message
		video.Update()
		c.JSON(errWithCode.StatusCode, errWithCode.Error)
		return
	}

	// 更新 Video 记录（使用 Kling 返回的 task_id）
	video.TaskId = klingResp.Data.TaskID
	video.Status = klingResp.Data.TaskStatus
	video.CreatedAt = klingResp.Data.CreatedAt
	video.UpdatedAt = klingResp.Data.UpdatedAt
	video.VideoId = klingResp.Data.TaskID
	if err := video.Update(); err != nil {
		logger.SysError(fmt.Sprintf("更新视频任务失败: id=%d, task_id=%s, error=%v", video.Id, video.TaskId, err))
	}

	logger.SysLog(fmt.Sprintf("Kling video task submitted: id=%d, task_id=%s, external_task_id=%d, user_id=%d, channel_id=%d, quota=%d",
		video.Id, klingResp.Data.TaskID, video.Id, meta.UserId, meta.ChannelId, quota))

	// 透传 Kling 原始响应
	c.JSON(http.StatusOK, klingResp)
}

// RelayKlingVideoResult 查询任务结果（从数据库读取）
func RelayKlingVideoResult(c *gin.Context) {
	taskID := c.Param("id")
	if taskID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "task_id 参数缺失"})
		return
	}

	// 从数据库获取任务信息
	video, err := dbmodel.GetVideoTaskById(taskID)
	if err != nil {
		logger.SysError(fmt.Sprintf("查询任务失败: task_id=%s, error=%v", taskID, err))
		c.JSON(http.StatusNotFound, gin.H{
			"code":    404,
			"message": "任务不存在",
			"error":   err.Error(),
		})
		return
	}

	// 如果 result 字段为空，返回基本状态信息
	if video.Result == "" {
		// 构建基本的查询响应（任务尚未完成回调）
		response := kling.QueryTaskResponse{
			Code:      0,
			Message:   "success",
			RequestID: fmt.Sprintf("query-%s", taskID),
			Data: kling.TaskData{
				TaskID:        video.TaskId,
				TaskStatus:    video.Status,
				TaskStatusMsg: video.FailReason,
				CreatedAt:     video.CreatedAt,
				UpdatedAt:     video.UpdatedAt,
				TaskResult: kling.TaskResult{
					Videos: []kling.Video{},
				},
			},
		}
		c.JSON(http.StatusOK, response)
		return
	}

	// 从 result 字段解析查询响应数据
	var queryResponse kling.QueryTaskResponse
	if err := json.Unmarshal([]byte(video.Result), &queryResponse); err != nil {
		logger.SysError(fmt.Sprintf("解析查询结果失败: task_id=%s, error=%v", taskID, err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "解析查询结果失败",
			"error":   err.Error(),
		})
		return
	}

	// 返回查询响应
	c.JSON(http.StatusOK, queryResponse)
}

// HandleKlingCallback 处理 Kling 回调通知
func HandleKlingCallback(c *gin.Context) {
	// 读取原始body
	bodyBytes, err := c.GetRawData()
	if err != nil {
		logger.SysError(fmt.Sprintf("Kling callback read body error: %v", err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	var notification kling.CallbackNotification
	if unmarshalErr := json.Unmarshal(bodyBytes, &notification); unmarshalErr != nil {
		logger.SysError(fmt.Sprintf("Kling callback parse error: error=%v", unmarshalErr))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	taskID := notification.TaskID
	externalTaskID := notification.ExternalTaskID

	logger.SysLog(fmt.Sprintf("Kling callback received: task_id=%s, external_task_id=%s, status=%s",
		taskID, externalTaskID, notification.TaskStatus))
	logger.SysLog(fmt.Sprintf("Kling callback notification: %+v", notification))

	// 查询任务记录（优先使用 external_task_id，其次使用 task_id）
	var video *dbmodel.Video
	var queryErr error

	if externalTaskID != "" {
		// 尝试通过 external_task_id (即 video.id) 查询
		var internalID int64
		if _, parseErr := fmt.Sscanf(externalTaskID, "%d", &internalID); parseErr == nil {
			video, queryErr = dbmodel.GetVideoTaskByInternalId(internalID)
		}
	}

	// 如果通过 external_task_id 没找到，尝试通过 task_id 查询
	if video == nil && taskID != "" {
		video, queryErr = dbmodel.GetVideoTaskById(taskID)
	}

	if queryErr != nil || video == nil {
		logger.SysError(fmt.Sprintf("Kling callback task not found: task_id=%s, external_task_id=%s", taskID, externalTaskID))
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}

	// 防止重复处理
	currentStatus := video.Status
	if currentStatus == kling.TaskStatusSucceed || currentStatus == kling.TaskStatusFailed {
		logger.SysLog(fmt.Sprintf("Kling callback already processed: task_id=%s, status=%s", taskID, currentStatus))
		c.JSON(http.StatusOK, gin.H{"message": "already processed"})
		return
	}

	// 将回调数据转换成查询数据格式（QueryTaskResponse）
	queryResponse := kling.QueryTaskResponse{
		Code:      0,
		Message:   "success",
		RequestID: fmt.Sprintf("callback-%s", taskID),
		Data: kling.TaskData{
			TaskID:        notification.TaskID,
			TaskStatus:    notification.TaskStatus,
			TaskStatusMsg: notification.TaskStatusMsg,
			CreatedAt:     notification.CreatedAt,
			UpdatedAt:     notification.UpdatedAt,
			TaskResult:    notification.TaskResult,
		},
	}

	// 将转换后的查询格式保存到 result 字段
	queryResponseBytes, err := json.Marshal(queryResponse)
	if err != nil {
		logger.SysError(fmt.Sprintf("Kling callback marshal query response error: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	video.Result = string(queryResponseBytes)

	// 处理回调结果
	if notification.TaskStatus == kling.TaskStatusSucceed {
		video.Status = kling.TaskStatusSucceed

		// 提取视频URL和时长
		if len(notification.TaskResult.Videos) > 0 {
			video.StoreUrl = notification.TaskResult.Videos[0].URL
			video.Duration = notification.TaskResult.Videos[0].Duration
			video.VideoId = notification.TaskResult.Videos[0].ID
		}

		// 后扣费模式：在成功时才扣费
		err := dbmodel.DecreaseUserQuota(video.UserId, video.Quota)
		if err != nil {
			logger.SysError(fmt.Sprintf("Kling callback billing failed: user_id=%d, quota=%d, error=%v", video.UserId, video.Quota, err))
		} else {
			logger.SysLog(fmt.Sprintf("Kling callback billing success: user_id=%d, quota=%d, task_id=%s", video.UserId, video.Quota, taskID))
		}

		video.Update()
	} else if notification.TaskStatus == kling.TaskStatusFailed {
		video.Status = kling.TaskStatusFailed
		video.FailReason = notification.TaskStatusMsg
		video.Update()
		logger.SysLog(fmt.Sprintf("Kling callback task failed: task_id=%s, reason=%s", taskID, notification.TaskStatusMsg))
	} else {
		// 其他状态（processing等），更新状态但不扣费
		video.Status = notification.TaskStatus
		video.Update()
	}

	c.JSON(http.StatusOK, gin.H{"message": "success"})
}

// updateVideoFromKlingResult 从 Kling 查询结果更新视频记录
func updateVideoFromKlingResult(video *dbmodel.Video, result *kling.QueryTaskResponse) {
	video.Status = result.Data.TaskStatus

	if result.Data.TaskStatus == kling.TaskStatusSucceed {
		if len(result.Data.TaskResult.Videos) > 0 {
			video.StoreUrl = result.Data.TaskResult.Videos[0].URL
		}

		// 后扣费模式：查询时如果发现任务成功且未扣费，则扣费
		if video.Quota > 0 {
			err := dbmodel.DecreaseUserQuota(video.UserId, video.Quota)
			if err != nil {
				logger.SysError(fmt.Sprintf("Kling query billing failed: user_id=%d, quota=%d, error=%v", video.UserId, video.Quota, err))
			} else {
				logger.SysLog(fmt.Sprintf("Kling query billing success: user_id=%d, quota=%d, task_id=%s", video.UserId, video.Quota, video.TaskId))
				// 标记已扣费（将 quota 设为负数表示已扣费）
				video.Quota = -video.Quota
			}
		}
	} else if result.Data.TaskStatus == kling.TaskStatusFailed {
		video.FailReason = result.Data.TaskStatusMsg
	}

	video.Update()
}
