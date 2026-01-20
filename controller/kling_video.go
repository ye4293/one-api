package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/songquanpeng/one-api/relay/controller"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/kling"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/util"
)

// isImageRequestType 判断是否为图片类请求
func isImageRequestType(requestType string) bool {
	return requestType == kling.RequestTypeImageGeneration ||
		requestType == kling.RequestTypeOmniImage ||
		requestType == kling.RequestTypeMultiImage2Image ||
		requestType == kling.RequestTypeImageExpand
}

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
	model := kling.GetModelNameFromRequest(requestParams)
	duration := fmt.Sprintf("%d", kling.GetDurationFromRequest(requestParams))
	mode := kling.GetModeFromRequest(requestParams)
	// 计算预估费用
	//quota := kling.CalculateQuota(requestParams, requestType)
	quota := common.CalculateVideoQuota(model, requestType, mode, duration, "")

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

	// 根据请求类型创建不同的任务记录
	var externalTaskID int64
	var taskRecord interface{} // 用于后续更新

	if isImageRequestType(requestType) {
		// 图片类：创建 Image 记录
		image := &dbmodel.Image{
			TaskId:    "", // 暂时为空，等 Kling 返回后更新
			UserId:    meta.UserId,
			Username:  user.Username,
			ChannelId: meta.ChannelId,
			Model:     model,
			Provider:  "kling",
			Status:    "",
			Quota:     quota,
			Mode:      mode,
			Detail:    kling.GetPromptFromRequest(requestParams),
		}
		if err := image.Insert(); err != nil {
			errResp := openai.ErrorWrapper(err, "database_error", http.StatusInternalServerError)
			c.JSON(errResp.StatusCode, errResp.Error)
			return
		}
		externalTaskID = image.Id
		taskRecord = image
	} else {
		// 视频/音频类：创建 Video 记录
		video := &dbmodel.Video{
			TaskId:    "", // 暂时为空，等 Kling 返回后更新
			UserId:    meta.UserId,
			Username:  user.Username,
			ChannelId: meta.ChannelId,
			Model:     model,
			Provider:  "kling",
			Type:      requestType,
			Status:    "",
			Quota:     quota,
			Mode:      mode,
			Prompt:    kling.GetPromptFromRequest(requestParams),
			Duration:  duration,
		}
		if err := video.Insert(); err != nil {
			errResp := openai.ErrorWrapper(err, "database_error", http.StatusInternalServerError)
			c.JSON(errResp.StatusCode, errResp.Error)
			return
		}
		externalTaskID = video.Id
		taskRecord = video
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

	// 转换请求并注入回调URL和 external_task_id
	convertedBody, err := adaptor.ConvertRequest(c, meta, requestParams, callbackURL, externalTaskID)
	if err != nil {
		// 更新失败状态
		if isImageRequestType(requestType) {
			if img, ok := taskRecord.(*dbmodel.Image); ok {
				img.Status = kling.TaskStatusFailed
				img.FailReason = err.Error()
				img.Update()
			}
		} else {
			if vid, ok := taskRecord.(*dbmodel.Video); ok {
				vid.Status = kling.TaskStatusFailed
				vid.FailReason = err.Error()
				vid.Update()
			}
		}
		errResp := openai.ErrorWrapper(err, "convert_request_failed", http.StatusInternalServerError)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	resp, err := adaptor.DoRequest(c, meta, bytes.NewReader(convertedBody))
	if err != nil {
		// 更新失败状态
		if isImageRequestType(requestType) {
			if img, ok := taskRecord.(*dbmodel.Image); ok {
				img.Status = kling.TaskStatusFailed
				img.FailReason = err.Error()
				img.Update()
			}
		} else {
			if vid, ok := taskRecord.(*dbmodel.Video); ok {
				vid.Status = kling.TaskStatusFailed
				vid.FailReason = err.Error()
				vid.Update()
			}
		}
		errResp := openai.ErrorWrapper(err, "request_failed", http.StatusInternalServerError)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	klingResp, errWithCode := adaptor.DoResponse(c, resp, meta)
	if errWithCode != nil {
		// 更新失败状态
		if isImageRequestType(requestType) {
			if img, ok := taskRecord.(*dbmodel.Image); ok {
				img.Status = kling.TaskStatusFailed
				img.FailReason = errWithCode.Error.Message
				img.Update()
			}
		} else {
			if vid, ok := taskRecord.(*dbmodel.Video); ok {
				vid.Status = kling.TaskStatusFailed
				vid.FailReason = errWithCode.Error.Message
				vid.Update()
			}
		}
		c.JSON(errWithCode.StatusCode, errWithCode.Error)
		return
	}

	// 更新任务记录（使用 Kling 返回的 task_id）
	if isImageRequestType(requestType) {
		if img, ok := taskRecord.(*dbmodel.Image); ok {
			img.TaskId = klingResp.Data.TaskID
			img.Status = klingResp.Data.TaskStatus
			img.ImageId = klingResp.Data.TaskID
			if err := img.Update(); err != nil {
				logger.SysError(fmt.Sprintf("更新图片任务失败: id=%d, task_id=%s, error=%v", img.Id, img.TaskId, err))
			}
			logger.SysLog(fmt.Sprintf("Kling image task submitted: id=%d, task_id=%s, external_task_id=%d, user_id=%d, channel_id=%d, quota=%d",
				img.Id, klingResp.Data.TaskID, externalTaskID, meta.UserId, meta.ChannelId, quota))
		}
	} else {
		if vid, ok := taskRecord.(*dbmodel.Video); ok {
			vid.TaskId = klingResp.Data.TaskID
			vid.Status = klingResp.Data.TaskStatus
			vid.VideoId = klingResp.Data.TaskID
			if err := vid.Update(); err != nil {
				logger.SysError(fmt.Sprintf("更新视频任务失败: id=%d, task_id=%s, error=%v", vid.Id, vid.TaskId, err))
			}
			logger.SysLog(fmt.Sprintf("video: id=+%v, ", vid))
			logger.SysLog(fmt.Sprintf("Kling video task submitted: id=%d, task_id=%s, external_task_id=%d, user_id=%d, channel_id=%d, quota=%d",
				vid.Id, klingResp.Data.TaskID, externalTaskID, meta.UserId, meta.ChannelId, quota))
		}
	}

	// 透传 Kling 原始响应
	c.JSON(http.StatusOK, klingResp)
}

// RelayKlingVideoResult 查询任务结果（从数据库读取）
// 统一入口，调用 relay/controller 中的实现
func RelayKlingVideoResult(c *gin.Context) {
	taskID := c.Param("id")
	controller.GetKlingVideoResult(c, taskID)
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

	// Fallback 查询机制：先查 Video 表，再查 Image 表
	var video *dbmodel.Video
	var image *dbmodel.Image

	// 1. 尝试从 Video 表查询
	if externalTaskID != "" {
		var internalID int64
		if _, parseErr := fmt.Sscanf(externalTaskID, "%d", &internalID); parseErr == nil {
			video, _ = dbmodel.GetVideoTaskByInternalId(internalID)
		}
	}
	if video == nil && taskID != "" {
		video, _ = dbmodel.GetVideoTaskById(taskID)
	}

	// 2. 如果 Video 表没找到，尝试从 Image 表查询
	if video == nil {
		if externalTaskID != "" {
			var internalID int64
			if _, parseErr := fmt.Sscanf(externalTaskID, "%d", &internalID); parseErr == nil {
				// Image 表使用 Id 字段，需要转换查询
				image, _ = dbmodel.GetImageById(internalID)
			}
		}
		if image == nil && taskID != "" {
			image, _ = dbmodel.GetImageByTaskId(taskID)
		}
	}

	// 3. 两个表都没找到，返回错误
	if video == nil && image == nil {
		logger.SysError(fmt.Sprintf("Kling callback task not found: task_id=%s, external_task_id=%s", taskID, externalTaskID))
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}

	// 4. 根据找到的类型，调用相应的处理函数
	if video != nil {
		handleVideoCallback(c, video, &notification)
	} else {
		handleImageCallback(c, image, &notification)
	}
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

func DoIdentifyFace(c *gin.Context) {
	controller.DoIdentifyFace(c)
}

func DoAdvancedLipSync(c *gin.Context) {
	controller.DoAdvancedLipSync(c)
}

// handleVideoCallback 处理视频/音频任务的回调
func handleVideoCallback(c *gin.Context, video *dbmodel.Video, notification *kling.CallbackNotification) {
	taskID := notification.TaskID

	// 防止重复处理
	currentStatus := video.Status
	if currentStatus == kling.TaskStatusSucceed || currentStatus == kling.TaskStatusFailed {
		logger.SysLog(fmt.Sprintf("Kling callback already processed: task_id=%s, status=%s", taskID, currentStatus))
		c.JSON(http.StatusOK, gin.H{"message": "already processed"})
		return
	}

	// 将回调数据转换成查询数据格式
	queryResponse := kling.QueryTaskResponse{
		Code:      0,
		Message:   "success",
		RequestID: fmt.Sprintf("callback-%s", taskID),
		Data: kling.TaskData{
			TaskID:        notification.TaskID,
			TaskStatus:    notification.TaskStatus,
			TaskStatusMsg: notification.TaskStatusMsg,
			TaskInfo:      notification.TaskInfo,
			CreatedAt:     notification.CreatedAt,
			UpdatedAt:     notification.UpdatedAt,
			TaskResult:    notification.TaskResult,
		},
	}

	queryResponseBytes, _ := json.Marshal(queryResponse)
	video.Result = string(queryResponseBytes)

	// 处理成功状态
	if notification.TaskStatus == kling.TaskStatusSucceed {
		video.Status = kling.TaskStatusSucceed

		// 提取实际视频时长
		var actualDuration string
		if len(notification.TaskResult.Videos) > 0 {
			video.StoreUrl = notification.TaskResult.Videos[0].URL
			actualDuration = notification.TaskResult.Videos[0].Duration
			video.Duration = actualDuration
			video.VideoId = notification.TaskResult.Videos[0].ID
		}

		// 保存旧的 quota 用于日志
		oldQuota := video.Quota

		// 根据实际 duration 重新计算费用
		newQuota := common.CalculateVideoQuota(
			video.Model,
			video.Type,
			video.Mode,
			actualDuration,
			video.Resolution,
		)
		video.Quota = newQuota

		// 后扣费模式：成功时根据实际 duration 扣费
		err := dbmodel.DecreaseUserQuota(video.UserId, newQuota)
		if err != nil {
			logger.SysError(fmt.Sprintf("Kling callback billing failed: user_id=%d, quota=%d, error=%v", video.UserId, newQuota, err))
		} else {
			logger.SysLog(fmt.Sprintf("Kling callback billing success: user_id=%d, old_quota=%d, new_quota=%d, duration=%s, task_id=%s",
				video.UserId, oldQuota, newQuota, actualDuration, taskID))
		}

		video.TotalDuration = int64(time.Now().Unix() - video.CreatedAt)
		video.Update()

	} else if notification.TaskStatus == kling.TaskStatusFailed {
		// 处理失败状态（不扣费）
		video.Status = kling.TaskStatusFailed
		video.FailReason = notification.TaskStatusMsg
		video.TotalDuration = int64(time.Now().Unix() - video.CreatedAt)
		video.Update()
		logger.SysLog(fmt.Sprintf("Kling callback task failed: task_id=%s, reason=%s", taskID, notification.TaskStatusMsg))

	} else {
		// 其他状态（processing等），更新状态但不扣费
		video.Status = notification.TaskStatus
		video.TotalDuration = int64(time.Now().Unix() - video.CreatedAt)
		video.Update()
	}

	c.JSON(http.StatusOK, gin.H{"message": "success"})
}

// handleImageCallback 处理图片任务的回调
func handleImageCallback(c *gin.Context, image *dbmodel.Image, notification *kling.CallbackNotification) {
	taskID := notification.TaskID

	// 防止重复处理
	currentStatus := image.Status
	if currentStatus == kling.TaskStatusSucceed || currentStatus == kling.TaskStatusFailed {
		logger.SysLog(fmt.Sprintf("Kling image callback already processed: task_id=%s, status=%s", taskID, currentStatus))
		c.JSON(http.StatusOK, gin.H{"message": "already processed"})
		return
	}

	// 将回调数据转换成查询数据格式
	queryResponse := kling.QueryTaskResponse{
		Code:      0,
		Message:   "success",
		RequestID: fmt.Sprintf("callback-%s", taskID),
		Data: kling.TaskData{
			TaskID:        notification.TaskID,
			TaskStatus:    notification.TaskStatus,
			TaskStatusMsg: notification.TaskStatusMsg,
			TaskInfo:      notification.TaskInfo,
			CreatedAt:     notification.CreatedAt,
			UpdatedAt:     notification.UpdatedAt,
			TaskResult:    notification.TaskResult,
		},
	}

	queryResponseBytes, _ := json.Marshal(queryResponse)
	image.Result = string(queryResponseBytes)

	// 处理成功状态
	if notification.TaskStatus == kling.TaskStatusSucceed {
		image.Status = kling.TaskStatusSucceed

		// 提取图片 URL（可能在 Videos 数组中，也可能在其他字段）
		if len(notification.TaskResult.Videos) > 0 {
			image.StoreUrl = notification.TaskResult.Videos[0].URL
			image.ImageId = notification.TaskResult.Videos[0].ID
		}

		// 后扣费模式：成功时扣费
		err := dbmodel.DecreaseUserQuota(image.UserId, image.Quota)
		if err != nil {
			logger.SysError(fmt.Sprintf("Kling image callback billing failed: user_id=%d, quota=%d, error=%v", image.UserId, image.Quota, err))
		} else {
			logger.SysLog(fmt.Sprintf("Kling image callback billing success: user_id=%d, quota=%d, task_id=%s",
				image.UserId, image.Quota, taskID))
		}

		image.TotalDuration = int(time.Now().Unix() - image.CreatedAt)
		image.Update()

	} else if notification.TaskStatus == kling.TaskStatusFailed {
		// 处理失败状态（不扣费）
		image.Status = kling.TaskStatusFailed
		image.FailReason = notification.TaskStatusMsg
		image.TotalDuration = int(time.Now().Unix() - image.CreatedAt)
		image.Update()
		logger.SysLog(fmt.Sprintf("Kling image callback task failed: task_id=%s, reason=%s", taskID, notification.TaskStatusMsg))

	} else {
		// 其他状态（processing等），更新状态但不扣费
		image.Status = notification.TaskStatus
		image.TotalDuration = int(time.Now().Unix() - image.CreatedAt)
		image.Update()
	}

	c.JSON(http.StatusOK, gin.H{"message": "success"})
}

// DoCustomElements 处理自定义元素训练请求（同步接口）
func DoCustomElements(c *gin.Context) {
	meta := util.GetRelayMeta(c)
	requestType := kling.RequestTypeCustomElements

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

	model := kling.GetModelNameFromRequest(requestParams)
	mode := kling.GetModeFromRequest(requestParams)

	// 计算预估费用（custom-elements 按次计费）
	quota := common.CalculateVideoQuota(model, requestType, mode, "0", "")

	// 检查用户余额
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

	// 创建 Video 记录
	video := &dbmodel.Video{
		TaskId:    "", // 同步接口可能没有 task_id
		UserId:    meta.UserId,
		Username:  user.Username,
		ChannelId: meta.ChannelId,
		Model:     model,
		Provider:  "kling",
		Type:      requestType,
		Status:    kling.TaskStatusPending,
		Quota:     quota,
		Mode:      mode,
		Prompt:    kling.GetPromptFromRequest(requestParams),
	}
	if err := video.Insert(); err != nil {
		errResp := openai.ErrorWrapper(err, "database_error", http.StatusInternalServerError)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	// 调用 Kling API（同步调用）
	adaptor := &kling.Adaptor{RequestType: requestType}
	adaptor.Init(meta)

	// 注意：同步接口不需要 callback_url
	convertedBody, err := adaptor.ConvertRequest(c, meta, requestParams, "", video.Id)
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

	// 打印完整的 Kling 响应以便调试
	respJSON, _ := json.Marshal(klingResp)
	logger.SysLog(fmt.Sprintf("Kling custom-elements response: channel_id=%d, user_id=%d, response=%s",
		meta.ChannelId, meta.UserId, string(respJSON)))

	// 检查响应的 message 字段，只有 "success" 才视为成功
	if klingResp.Message != "success" {
		video.Status = kling.TaskStatusFailed
		video.FailReason = fmt.Sprintf("API returned non-success message: %s", klingResp.Message)
		video.TaskId = klingResp.Data.TaskID

		// 保存响应结果（即使失败也保存，便于排查问题）
		resultJSON, _ := json.Marshal(klingResp)
		video.Result = string(resultJSON)

		video.Update()

		logger.Warn(c.Request.Context(), fmt.Sprintf("Custom elements task failed: id=%d, task_id=%s, message=%s, user_id=%d, channel_id=%d",
			video.Id, klingResp.Data.TaskID, klingResp.Message, meta.UserId, meta.ChannelId))

		// 返回响应（不扣费）
		c.JSON(http.StatusOK, klingResp)
		return
	}

	// 同步接口：message 为 "success" 时立即更新为成功状态并扣费
	video.TaskId = klingResp.Data.TaskID
	video.Status = kling.TaskStatusSucceed
	video.VideoId = klingResp.Data.TaskID

	// 保存响应结果
	resultJSON, _ := json.Marshal(klingResp)
	video.Result = string(resultJSON)

	// 立即扣费
	err = dbmodel.DecreaseUserQuota(video.UserId, quota)
	if err != nil {
		logger.SysError(fmt.Sprintf("Custom elements billing failed: user_id=%d, quota=%d, error=%v", video.UserId, quota, err))
	} else {
		logger.SysLog(fmt.Sprintf("Custom elements billing success: user_id=%d, quota=%d, task_id=%s",
			video.UserId, quota, video.TaskId))
	}

	video.TotalDuration = int64(time.Now().Unix() - video.CreatedAt)
	video.Update()

	logger.SysLog(fmt.Sprintf("Custom elements task completed (sync): id=%d, task_id=%s, user_id=%d, channel_id=%d, quota=%d",
		video.Id, klingResp.Data.TaskID, meta.UserId, meta.ChannelId, quota))

	// 返回响应
	c.JSON(http.StatusOK, klingResp)
}
