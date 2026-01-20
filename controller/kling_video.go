package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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

// ============================================================================
// 辅助函数
// ============================================================================

// klingRequest 请求上下文
type klingRequest struct {
	RequestType   string
	RequestParams map[string]interface{}
	Model         string
	Mode          string
	Duration      string
	Quota         int64
	Meta          *util.RelayMeta
	Channel       *dbmodel.Channel
	User          *dbmodel.User
}

// parseKlingRequest 解析和验证 Kling 请求
func parseKlingRequest(c *gin.Context, requestType string) (*klingRequest, error) {
	meta := util.GetRelayMeta(c)

	// 读取并解析请求体
	bodyBytes, err := c.GetRawData()
	if err != nil {
		return nil, fmt.Errorf("invalid request body: %w", err)
	}

	var requestParams map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &requestParams); err != nil {
		return nil, fmt.Errorf("invalid request json: %w", err)
	}

	// 提取请求参数
	model := kling.GetModelNameFromRequest(requestParams)
	mode := kling.GetModeFromRequest(requestParams)
	duration := fmt.Sprintf("%d", kling.GetDurationFromRequest(requestParams))

	// 计算预估费用
	quota := common.CalculateVideoQuota(model, requestType, mode, duration, "")

	// 检查用户余额
	userQuota, err := dbmodel.CacheGetUserQuota(c.Request.Context(), meta.UserId)
	if err != nil {
		return nil, fmt.Errorf("get user quota error: %w", err)
	}
	if userQuota < quota {
		return nil, fmt.Errorf("insufficient quota")
	}

	// 获取渠道信息
	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return nil, fmt.Errorf("get channel error: %w", err)
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

	return &klingRequest{
		RequestType:   requestType,
		RequestParams: requestParams,
		Model:         model,
		Mode:          mode,
		Duration:      duration,
		Quota:         quota,
		Meta:          meta,
		Channel:       channel,
		User:          user,
	}, nil
}

// respondError 统一错误响应
func respondError(c *gin.Context, err error, errType string, statusCode int) {
	errResp := openai.ErrorWrapper(err, errType, statusCode)
	c.JSON(errResp.StatusCode, errResp.Error)
}

// buildCallbackURL 构建回调URL
func buildCallbackURL() (string, error) {
	if config.ServerAddress == "" {
		return "", fmt.Errorf("invalid server address")
	}
	return fmt.Sprintf("%s/kling/internal/callback", config.ServerAddress), nil
}

// RelayKlingVideo 处理 Kling 视频/音频/图片生成请求（统一入口）
func RelayKlingVideo(c *gin.Context) {
	// 确定请求类型
	requestType := kling.DetermineRequestType(c.Request.URL.Path)
	if requestType == "" {
		respondError(c, fmt.Errorf("unsupported endpoint"), "invalid_endpoint", http.StatusBadRequest)
		return
	}

	// 解析请求
	req, err := parseKlingRequest(c, requestType)
	if err != nil {
		errType := "invalid_request"
		statusCode := http.StatusBadRequest
		if strings.Contains(err.Error(), "quota") {
			errType = "insufficient_quota"
			statusCode = http.StatusPaymentRequired
		} else if strings.Contains(err.Error(), "channel") || strings.Contains(err.Error(), "user") {
			errType = "server_error"
			statusCode = http.StatusInternalServerError
		}
		respondError(c, err, errType, statusCode)
		return
	}

	// 判断同步/异步处理
	if kling.IsSyncRequestType(requestType) {
		processSyncTask(c, req)
	} else {
		processAsyncTask(c, req)
	}
}

// processAsyncTask 处理异步任务（视频、音频、图片）
func processAsyncTask(c *gin.Context, req *klingRequest) {
	// 创建任务记录
	taskManager := kling.NewTaskManager()
	task, err := taskManager.CreateTask(&kling.CreateTaskRequest{
		UserID:      req.Meta.UserId,
		Username:    req.User.Username,
		ChannelID:   req.Meta.ChannelId,
		Model:       req.Model,
		Type:        req.RequestType,
		Mode:        req.Mode,
		Duration:    req.Duration,
		Prompt:      kling.GetPromptFromRequest(req.RequestParams),
		Detail:      kling.GetPromptFromRequest(req.RequestParams),
		Quota:       req.Quota,
		RequestType: req.RequestType,
	})
	if err != nil {
		respondError(c, err, "database_error", http.StatusInternalServerError)
		return
	}

	// 构建回调URL
	callbackURL, err := buildCallbackURL()
	if err != nil {
		task.MarkFailed(c.Request.Context(), "invalid server address")
		respondError(c, err, "invalid_server_address", http.StatusInternalServerError)
		return
	}

	// 调用 Kling API
	adaptor := &kling.Adaptor{RequestType: req.RequestType}
	adaptor.Init(req.Meta)

	// 转换请求
	convertedBody, err := adaptor.ConvertRequest(c, req.Meta, req.RequestParams, callbackURL, task.GetID())
	if err != nil {
		task.MarkFailed(c.Request.Context(), err.Error())
		respondError(c, err, "convert_request_failed", http.StatusInternalServerError)
		return
	}

	// 发送请求
	resp, err := adaptor.DoRequest(c, req.Meta, bytes.NewReader(convertedBody))
	if err != nil {
		task.MarkFailed(c.Request.Context(), err.Error())
		respondError(c, err, "request_failed", http.StatusInternalServerError)
		return
	}

	// 处理响应
	klingResp, errWithCode := adaptor.DoResponse(c, resp, req.Meta)
	if errWithCode != nil {
		task.MarkFailed(c.Request.Context(), errWithCode.Error.Message)
		c.JSON(errWithCode.StatusCode, errWithCode.Error)
		return
	}

	// 更新任务记录
	if err := task.UpdateWithKlingResponse(klingResp); err != nil {
		logger.SysError(fmt.Sprintf("更新任务失败: id=%d, task_id=%s, error=%v", task.GetID(), klingResp.Data.TaskID, err))
	}

	logger.SysLog(fmt.Sprintf("Kling task submitted: id=%d, task_id=%s, type=%s, user_id=%d, channel_id=%d, quota=%d",
		task.GetID(), klingResp.Data.TaskID, req.RequestType, req.Meta.UserId, req.Meta.ChannelId, req.Quota))

	// 返回响应
	c.JSON(http.StatusOK, klingResp)
}

// processSyncTask 处理同步任务（custom-elements, custom-voices 等）
func processSyncTask(c *gin.Context, req *klingRequest) {
	// 创建任务记录
	taskManager := kling.NewTaskManager()
	task, err := taskManager.CreateTask(&kling.CreateTaskRequest{
		UserID:      req.Meta.UserId,
		Username:    req.User.Username,
		ChannelID:   req.Meta.ChannelId,
		Model:       req.Model,
		Type:        req.RequestType,
		Mode:        req.Mode,
		Prompt:      kling.GetPromptFromRequest(req.RequestParams),
		Quota:       req.Quota,
		RequestType: req.RequestType,
	})
	if err != nil {
		respondError(c, err, "database_error", http.StatusInternalServerError)
		return
	}

	// 调用 Kling API（同步接口不需要 callback_url）
	adaptor := &kling.Adaptor{RequestType: req.RequestType}
	adaptor.Init(req.Meta)

	convertedBody, err := adaptor.ConvertRequest(c, req.Meta, req.RequestParams, "", task.GetID())
	if err != nil {
		task.MarkFailed(c.Request.Context(), err.Error())
		respondError(c, err, "convert_request_failed", http.StatusInternalServerError)
		return
	}

	resp, err := adaptor.DoRequest(c, req.Meta, bytes.NewReader(convertedBody))
	if err != nil {
		task.MarkFailed(c.Request.Context(), err.Error())
		respondError(c, err, "request_failed", http.StatusInternalServerError)
		return
	}

	klingResp, errWithCode := adaptor.DoResponse(c, resp, req.Meta)
	if errWithCode != nil {
		task.MarkFailed(c.Request.Context(), errWithCode.Error.Message)
		c.JSON(errWithCode.StatusCode, errWithCode.Error)
		return
	}

	// 打印完整响应以便调试
	respJSON, _ := json.Marshal(klingResp)
	logger.SysLog(fmt.Sprintf("Kling sync task response: type=%s, channel_id=%d, user_id=%d, response=%s",
		req.RequestType, req.Meta.ChannelId, req.Meta.UserId, string(respJSON)))

	// 检查 message 字段
	if !kling.IsSuccessMessage(klingResp.Message) {
		failReason := fmt.Sprintf("API returned non-success message: %s", klingResp.Message)
		task.SetTaskID(klingResp.Data.TaskID)
		task.MarkFailed(c.Request.Context(), failReason)

		logger.Warn(c.Request.Context(), fmt.Sprintf("Sync task failed: id=%d, task_id=%s, message=%s, type=%s",
			task.GetID(), klingResp.Data.TaskID, klingResp.Message, req.RequestType))

		// 返回响应但不扣费
		c.JSON(http.StatusOK, klingResp)
		return
	}

	// 同步任务成功：立即扣费
	task.SetTaskID(klingResp.Data.TaskID)
	task.SetStatus(kling.TaskStatusSucceed)

	// 保存响应结果
	resultJSON, _ := json.Marshal(klingResp)
	task.SetResult(string(resultJSON))

	// 扣费
	err = dbmodel.DecreaseUserQuota(req.Meta.UserId, req.Quota)
	if err != nil {
		logger.SysError(fmt.Sprintf("Sync task billing failed: user_id=%d, quota=%d, error=%v", req.Meta.UserId, req.Quota, err))
	} else {
		logger.SysLog(fmt.Sprintf("Sync task billing success: user_id=%d, quota=%d, task_id=%s, type=%s",
			req.Meta.UserId, req.Quota, klingResp.Data.TaskID, req.RequestType))
	}

	// 更新任务
	if video := task.GetVideo(); video != nil {
		video.TotalDuration = int64(time.Now().Unix() - video.CreatedAt)
	}
	task.Update()

	logger.SysLog(fmt.Sprintf("Sync task completed: id=%d, task_id=%s, type=%s, user_id=%d, channel_id=%d, quota=%d",
		task.GetID(), klingResp.Data.TaskID, req.RequestType, req.Meta.UserId, req.Meta.ChannelId, req.Quota))

	c.JSON(http.StatusOK, klingResp)
}

// RelayKlingVideoResult 查询任务结果（从数据库读取）
// 统一入口，调用 relay/controller 中的实现
func RelayKlingVideoResult(c *gin.Context) {
	taskID := c.Param("id")
	controller.GetKlingVideoResult(c, taskID)
}

// HandleKlingCallback 处理 Kling 回调通知（统一处理）
func HandleKlingCallback(c *gin.Context) {
	// 读取原始body
	bodyBytes, err := c.GetRawData()
	if err != nil {
		logger.SysError(fmt.Sprintf("Kling callback read body error: %v", err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	var notification kling.CallbackNotification
	if err := json.Unmarshal(bodyBytes, &notification); err != nil {
		logger.SysError(fmt.Sprintf("Kling callback parse error: error=%v", err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	logger.SysLog(fmt.Sprintf("Kling callback received: task_id=%s, external_task_id=%s, status=%s",
		notification.TaskID, notification.ExternalTaskID, notification.TaskStatus))

	// 使用 TaskManager 查找任务（自动 Fallback）
	taskManager := kling.NewTaskManager()
	var task *kling.TaskWrapper

	// 1. 先尝试通过 external_task_id 查找
	if notification.ExternalTaskID != "" {
		task, _ = taskManager.FindTaskByExternalID(notification.ExternalTaskID, "")
	}

	// 2. 如果没找到，通过 task_id 查找（自动 Fallback Video->Image）
	if task == nil && notification.TaskID != "" {
		task, _ = taskManager.FindTaskByTaskID(notification.TaskID)
	}

	// 3. 都没找到，返回错误
	if task == nil {
		logger.SysError(fmt.Sprintf("Kling callback task not found: task_id=%s, external_task_id=%s",
			notification.TaskID, notification.ExternalTaskID))
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}

	// 4. 统一处理回调
	handleCallback(c, task, &notification)
}

// handleCallback 统一处理回调（Video 和 Image）
func handleCallback(c *gin.Context, task *kling.TaskWrapper, notification *kling.CallbackNotification) {
	taskID := notification.TaskID

	// 防止重复处理
	currentStatus := task.GetStatus()
	if currentStatus == kling.TaskStatusSucceed || currentStatus == kling.TaskStatusFailed {
		logger.SysLog(fmt.Sprintf("Kling callback already processed: task_id=%s, status=%s", taskID, currentStatus))
		c.JSON(http.StatusOK, gin.H{"message": "already processed"})
		return
	}

	// 转换回调数据为查询数据格式
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
	task.SetResult(string(queryResponseBytes))

	// 处理成功状态
	if notification.TaskStatus == kling.TaskStatusSucceed {
		task.SetStatus(kling.TaskStatusSucceed)

		// 提取 URL 和其他信息
		if len(notification.TaskResult.Videos) > 0 {
			// 设置 Video 特有字段
			if video := task.GetVideo(); video != nil {
				video.StoreUrl = notification.TaskResult.Videos[0].URL
				video.VideoId = notification.TaskResult.Videos[0].ID

				// 提取实际时长
				actualDuration := notification.TaskResult.Videos[0].Duration
				video.Duration = actualDuration

				// 根据实际时长重新计算费用
				oldQuota := video.Quota
				newQuota := common.CalculateVideoQuota(video.Model, video.Type, video.Mode, actualDuration, video.Resolution)
				video.Quota = newQuota

				// 扣费
				err := dbmodel.DecreaseUserQuota(video.UserId, newQuota)
				if err != nil {
					logger.SysError(fmt.Sprintf("Kling callback billing failed: user_id=%d, quota=%d, error=%v", video.UserId, newQuota, err))
				} else {
					logger.SysLog(fmt.Sprintf("Kling callback billing success: user_id=%d, old_quota=%d, new_quota=%d, duration=%s, task_id=%s",
						video.UserId, oldQuota, newQuota, actualDuration, taskID))
				}

				video.TotalDuration = int64(time.Now().Unix() - video.CreatedAt)
			} else if image := task.GetImage(); image != nil {
				// 设置 Image 特有字段
				image.StoreUrl = notification.TaskResult.Videos[0].URL
				image.ImageId = notification.TaskResult.Videos[0].ID

				// 扣费
				err := dbmodel.DecreaseUserQuota(image.UserId, image.Quota)
				if err != nil {
					logger.SysError(fmt.Sprintf("Kling image callback billing failed: user_id=%d, quota=%d, error=%v", image.UserId, image.Quota, err))
				} else {
					logger.SysLog(fmt.Sprintf("Kling image callback billing success: user_id=%d, quota=%d, task_id=%s",
						image.UserId, image.Quota, taskID))
				}

				image.TotalDuration = int(time.Now().Unix() - image.CreatedAt)
			}
		}

		task.Update()

	} else if notification.TaskStatus == kling.TaskStatusFailed {
		// 处理失败状态（不扣费）
		task.SetStatus(kling.TaskStatusFailed)
		task.SetFailReason(notification.TaskStatusMsg)

		if video := task.GetVideo(); video != nil {
			video.TotalDuration = int64(time.Now().Unix() - video.CreatedAt)
		} else if image := task.GetImage(); image != nil {
			image.TotalDuration = int(time.Now().Unix() - image.CreatedAt)
		}

		task.Update()
		logger.SysLog(fmt.Sprintf("Kling callback task failed: task_id=%s, reason=%s", taskID, notification.TaskStatusMsg))

	} else {
		// 其他状态（processing等），更新状态但不扣费
		task.SetStatus(notification.TaskStatus)

		if video := task.GetVideo(); video != nil {
			video.TotalDuration = int64(time.Now().Unix() - video.CreatedAt)
		} else if image := task.GetImage(); image != nil {
			image.TotalDuration = int(time.Now().Unix() - image.CreatedAt)
		}

		task.Update()
	}

	c.JSON(http.StatusOK, gin.H{"message": "success"})
}

func DoIdentifyFace(c *gin.Context) {
	controller.DoIdentifyFace(c)
}

func DoAdvancedLipSync(c *gin.Context) {
	controller.DoAdvancedLipSync(c)
}

// DoCustomElements 处理自定义元素训练请求（同步接口）
// 注意：已合并到 RelayKlingVideo 的同步处理分支中
func DoCustomElements(c *gin.Context) {
	// 直接调用统一的 RelayKlingVideo 入口
	// 会自动通过 IsSyncRequestType() 判断并使用同步处理分支
	RelayKlingVideo(c)
}

