package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
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
	Sound         string // 是否有声：on/off（视频V2.6模型）
	VoiceList     string // 指定的音色列表（JSON格式，视频V2.6模型）
	Quota         int64
	CallbackUrl   string
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
	userModel := kling.GetModelNameFromRequest(requestParams)
	// 根据 requestType 自动确定 model（在映射表中的使用固定值，不在的使用用户传递的值）
	model := kling.GetModelNameByRequestType(requestType, userModel)
	mode := kling.GetModeFromRequest(requestParams)
	duration := fmt.Sprintf("%d", kling.GetDurationFromRequest(requestParams))

	// 提取用户回调 URL（可选）
	var callbackUrl string
	if cbUrl, ok := requestParams["callback_url"].(string); ok {
		callbackUrl = cbUrl
	}

	// 提取视频V2.6模型的声音参数（可选）
	var sound string
	if soundVal, ok := requestParams["sound"].(string); ok {
		sound = soundVal
	} else if soundBool, ok := requestParams["sound"].(bool); ok {
		if soundBool {
			sound = "on"
		} else {
			sound = "off"
		}
	}

	// 提取音色列表（可选）
	var voiceList string
	if voiceListVal, ok := requestParams["voice_list"]; ok && voiceListVal != nil {
		voiceListBytes, _ := json.Marshal(voiceListVal)
		voiceList = string(voiceListBytes)
	}

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

	// 资源感知渠道覆盖：当请求携带 element_id 或 voice_id 时，
	// 自动路由到创建该资源的渠道，避免多渠道场景下资源不存在的错误
	if boundChannel := resolveChannelForBoundResource(requestParams, meta.UserId); boundChannel != nil {
		if boundChannel.Id != channel.Id {
			logger.Info(c, fmt.Sprintf("Channel override for bound resource: original_channel=%d, bound_channel=%d",
				channel.Id, boundChannel.Id))
			channel = boundChannel
			meta.ChannelId = boundChannel.Id
		}
	}

	meta.APIKey = channel.Key
	if channel.BaseURL != nil && *channel.BaseURL != "" {
		meta.BaseURL = *channel.BaseURL
	}

	// 获取用户信息
	user, err := dbmodel.GetUserById(meta.UserId, false)
	if err != nil {
		logger.Error(c, fmt.Sprintf("获取用户信息失败: user_id=%d, error=%v", meta.UserId, err))
		user = &dbmodel.User{Username: ""}
	}

	return &klingRequest{
		RequestType:   requestType,
		RequestParams: requestParams,
		Model:         model,
		Mode:          mode,
		Duration:      duration,
		Sound:         sound,
		VoiceList:     voiceList,
		Quota:         quota,
		CallbackUrl:   callbackUrl,
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

// extractResourceID 从请求参数中提取资源 ID，兼容数字（float64）和字符串两种 JSON 格式
func extractResourceID(params map[string]interface{}, key string) string {
	val, ok := params[key]
	if !ok || val == nil {
		return ""
	}
	switch v := val.(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%d", int64(v))
	}
	return ""
}

// resolveChannelForBoundResource 检测请求是否携带归属特定渠道的资源 ID，
// 依次检查 video_id、element_id、voice_id，若找到则返回该资源所属渠道。
func resolveChannelForBoundResource(params map[string]interface{}, userID int) *dbmodel.Channel {
	// 1. video_id → 查 Video.video_id 字段（video-extend、motion-control、effects 等场景）
	if videoID := extractResourceID(params, "video_id"); videoID != "" {
		if video, err := dbmodel.GetVideoTaskByVideoIdAndUserId(videoID, userID); err == nil && video != nil && video.ChannelId != 0 {
			if ch, err := dbmodel.GetChannelById(video.ChannelId, true); err == nil {
				return ch
			}
		}
	}

	// 2. session_id / element_id / voice_id → 查 Video.task_id 字段
	taskID := extractResourceID(params, "session_id")
	if taskID == "" {
		taskID = extractResourceID(params, "element_id")
	}
	if taskID == "" {
		taskID = extractResourceID(params, "voice_id")
	}
	if taskID == "" {
		return nil
	}
	if video, err := dbmodel.GetVideoTaskByIdAndUserId(taskID, userID); err == nil && video != nil && video.ChannelId != 0 {
		if ch, err := dbmodel.GetChannelById(video.ChannelId, true); err == nil {
			return ch
		}
	}
	return nil
}

// resolveChannelForTaskQuery 从 URL path 末段提取 task_id，
// 依次在 Video 和 Image 表中查找对应的渠道（用于 GET 查询路由）
func resolveChannelForTaskQuery(urlPath string, userID int) *dbmodel.Channel {
	parts := strings.Split(strings.TrimRight(urlPath, "/"), "/")
	if len(parts) == 0 {
		return nil
	}
	taskID := parts[len(parts)-1]
	if taskID == "" || !looksLikeTaskID(taskID) {
		return nil
	}
	// 先查 Video 表
	if video, err := dbmodel.GetVideoTaskByIdAndUserId(taskID, userID); err == nil && video != nil && video.ChannelId != 0 {
		if ch, err := dbmodel.GetChannelById(video.ChannelId, true); err == nil {
			return ch
		}
	}
	// 再查 Image 表
	if image, err := dbmodel.GetImageByTaskIdAndUserId(taskID, userID); err == nil && image != nil && image.ChannelId != 0 {
		if ch, err := dbmodel.GetChannelById(image.ChannelId, true); err == nil {
			return ch
		}
	}
	return nil
}

// looksLikeTaskID 排除路由固定段（如 "text2video"、"custom-elements"），
// Kling task_id 通常为长数字串或含连字符的 UUID
func looksLikeTaskID(s string) bool {
	return strings.Contains(s, "-") || len(s) > 10
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
		Sound:       req.Sound,
		VoiceList:   req.VoiceList,
		Prompt:      kling.GetPromptFromRequest(req.RequestParams),
		Detail:      kling.GetPromptFromRequest(req.RequestParams),
		Quota:       req.Quota,
		RequestType: req.RequestType,
		CallbackUrl: req.CallbackUrl,
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

	// 检查 Kling API 返回的错误码
	if klingResp.Code != 0 {
		task.MarkFailed(c.Request.Context(), klingResp.Message)
		c.JSON(http.StatusOK, klingResp) // 透传原始响应
		return
	}

	// 更新任务记录
	if err := task.UpdateWithKlingResponse(klingResp); err != nil {
		logger.Error(c, fmt.Sprintf("更新任务失败: id=%d, task_id=%s, error=%v", task.GetID(), klingResp.GetTaskID(), err))
	}

	logger.Info(c, fmt.Sprintf("Kling task submitted: id=%d, task_id=%s, type=%s, user_id=%d, channel_id=%d, quota=%d",
		task.GetID(), klingResp.GetTaskID(), req.RequestType, req.Meta.UserId, req.Meta.ChannelId, req.Quota))

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
		Sound:       req.Sound,
		VoiceList:   req.VoiceList,
		Prompt:      kling.GetPromptFromRequest(req.RequestParams),
		Quota:       req.Quota,
		RequestType: req.RequestType,
		CallbackUrl: req.CallbackUrl,
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
	logger.Info(c, fmt.Sprintf("Kling sync task response: type=%s, channel_id=%d, user_id=%d, response=%s",
		req.RequestType, req.Meta.ChannelId, req.Meta.UserId, string(respJSON)))

	// 检查 code 和 message 字段
	if klingResp.Code != 0 || !kling.IsSuccessMessage(klingResp.Message) {
		failReason := fmt.Sprintf("API returned error: code=%d, message=%s", klingResp.Code, klingResp.Message)
		task.SetTaskID(klingResp.GetTaskID())
		task.MarkFailed(c.Request.Context(), failReason)

		logger.Warn(c.Request.Context(), fmt.Sprintf("Sync task failed: id=%d, task_id=%s, code=%d, message=%s, type=%s",
			task.GetID(), klingResp.GetTaskID(), klingResp.Code, klingResp.Message, req.RequestType))

		// 透传原始响应但不扣费
		c.JSON(http.StatusOK, klingResp)
		return
	}

	// 同步任务成功：立即扣费
	task.SetTaskID(klingResp.GetTaskID())
	task.SetStatus(kling.TaskStatusSucceed)

	// 保存响应结果
	resultJSON, _ := json.Marshal(klingResp)
	task.SetResult(string(resultJSON))

	// 扣费
	err = dbmodel.DecreaseUserQuota(req.Meta.UserId, req.Quota)
	if err != nil {
		logger.Error(c, fmt.Sprintf("Sync task billing failed: user_id=%d, quota=%d, error=%v", req.Meta.UserId, req.Quota, err))
	} else {
		logger.Info(c, fmt.Sprintf("Sync task billing success: user_id=%d, quota=%d, task_id=%s, type=%s",
			req.Meta.UserId, req.Quota, klingResp.GetTaskID(), req.RequestType))
	}

	// 更新任务
	if video := task.GetVideo(); video != nil {
		video.TotalDuration = int64(time.Now().Unix() - video.CreatedAt)
	}
	task.Update()

	logger.Info(c, fmt.Sprintf("Sync task completed: id=%d, task_id=%s, type=%s, user_id=%d, channel_id=%d, quota=%d",
		task.GetID(), klingResp.GetTaskID(), req.RequestType, req.Meta.UserId, req.Meta.ChannelId, req.Quota))

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
		logger.Error(c, fmt.Sprintf("Kling callback read body error: %v", err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	var notification kling.CallbackNotification
	if err := json.Unmarshal(bodyBytes, &notification); err != nil {
		logger.Error(c, fmt.Sprintf("Kling callback parse error: error=%v", err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	logger.Info(c, fmt.Sprintf("Kling callback received: task_id=%s, external_task_id=%s, status=%s",
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
		logger.Error(c, fmt.Sprintf("Kling callback task not found: task_id=%s, external_task_id=%s",
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
		logger.Info(c, fmt.Sprintf("Kling callback already processed: task_id=%s, status=%s", taskID, currentStatus))
		c.JSON(http.StatusOK, gin.H{"message": "already processed"})
		return
	}

	// 直接保存 Kling 回调的原始 JSON（不做转换，不生成虚假的 request_id）
	notificationBytes, _ := json.Marshal(notification)
	task.SetResult(string(notificationBytes))

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

				// 计算费用
				oldQuota := video.Quota
				var newQuota int64

				// 优先使用 Kling 官方返回的计费金额（人民币）
				if notification.FinalUnitDeduction != "" {
					// 解析字符串为 float64
					cnyAmount, parseErr := strconv.ParseFloat(notification.FinalUnitDeduction, 64)
					if parseErr != nil {
						logger.Error(c, fmt.Sprintf("Parse final_unit_deduction failed: value=%s, error=%v, fallback to system billing",
							notification.FinalUnitDeduction, parseErr))
						// 解析失败，使用系统规则
						newQuota = common.CalculateVideoQuota(video.Model, video.Type, video.Mode, actualDuration, video.Resolution)
					} else if cnyAmount > 0 {
						// 转换人民币为美元
						usdAmount, convErr := common.ConvertCNYToUSD(cnyAmount)
						if convErr != nil {
							logger.Error(c, fmt.Sprintf("CNY to USD conversion failed: cny=%.4f, error=%v, using default rate",
								cnyAmount, convErr))
						}
						// 转换为 quota：$1 = 500000 quota
						newQuota = int64(usdAmount * 500000)
						logger.Info(c, fmt.Sprintf("Using Kling official billing: task_id=%s, cny=%s (%.4f), usd=%.4f, quota=%d",
							taskID, notification.FinalUnitDeduction, cnyAmount, usdAmount, newQuota))
					} else {
						// 金额为 0，使用系统规则
						newQuota = common.CalculateVideoQuota(video.Model, video.Type, video.Mode, actualDuration, video.Resolution)
						logger.Info(c, fmt.Sprintf("Using system billing rules (zero amount): task_id=%s, duration=%s, quota=%d",
							taskID, actualDuration, newQuota))
					}
				} else {
					// 使用系统配置的计费规则重新计算
					newQuota = common.CalculateVideoQuota(video.Model, video.Type, video.Mode, actualDuration, video.Resolution)
					logger.Info(c, fmt.Sprintf("Using system billing rules: task_id=%s, duration=%s, quota=%d",
						taskID, actualDuration, newQuota))
				}

				video.Quota = newQuota

				// 扣费
				err := dbmodel.DecreaseUserQuota(video.UserId, newQuota)
				if err != nil {
					logger.Error(c, fmt.Sprintf("Kling callback billing failed: user_id=%d, quota=%d, error=%v", video.UserId, newQuota, err))
				} else {
					logger.Info(c, fmt.Sprintf("Kling callback billing success: user_id=%d, old_quota=%d, new_quota=%d, duration=%s, final_unit_deduction=%s, type=%s, model=%s, task_id=%s",
						video.UserId, oldQuota, newQuota, actualDuration, notification.FinalUnitDeduction, video.Type, video.Model, taskID))
				}

				video.TotalDuration = int64(time.Now().Unix() - video.CreatedAt)
			} else if image := task.GetImage(); image != nil {
				// 设置 Image 特有字段
				image.StoreUrl = notification.TaskResult.Videos[0].URL
				image.ImageId = notification.TaskResult.Videos[0].ID

				// 计算费用
				oldQuota := image.Quota
				var newQuota int64

				// 优先使用 Kling 官方返回的计费金额（人民币）
				if notification.FinalUnitDeduction != "" {
					// 解析字符串为 float64
					cnyAmount, parseErr := strconv.ParseFloat(notification.FinalUnitDeduction, 64)
					if parseErr != nil {
						logger.Error(c, fmt.Sprintf("Parse final_unit_deduction failed: value=%s, error=%v, fallback to original quota",
							notification.FinalUnitDeduction, parseErr))
						// 解析失败，使用原有 quota
						newQuota = image.Quota
					} else if cnyAmount > 0 {
						// 转换人民币为美元
						usdAmount, convErr := common.ConvertCNYToUSD(cnyAmount)
						if convErr != nil {
							logger.Error(c, fmt.Sprintf("CNY to USD conversion failed: cny=%.4f, error=%v, using default rate",
								cnyAmount, convErr))
						}
						// 转换为 quota：$1 = 500000 quota
						newQuota = int64(usdAmount * 500000)
						logger.Info(c, fmt.Sprintf("Using Kling official billing for image: task_id=%s, cny=%s (%.4f), usd=%.4f, quota=%d",
							taskID, notification.FinalUnitDeduction, cnyAmount, usdAmount, newQuota))
					} else {
						// 金额为 0，使用原有 quota
						newQuota = image.Quota
					}
				} else {
					// 使用原有的 quota（图片任务通常已经计算好）
					newQuota = image.Quota
				}

				image.Quota = newQuota

				// 扣费
				err := dbmodel.DecreaseUserQuota(image.UserId, newQuota)
				if err != nil {
					logger.Error(c, fmt.Sprintf("Kling image callback billing failed: user_id=%d, quota=%d, error=%v", image.UserId, newQuota, err))
				} else {
					logger.Info(c, fmt.Sprintf("Kling image callback billing success: user_id=%d, old_quota=%d, new_quota=%d, final_unit_deduction=%s, task_id=%s",
						image.UserId, oldQuota, newQuota, notification.FinalUnitDeduction, taskID))
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
		logger.Info(c, fmt.Sprintf("Kling callback task failed: task_id=%s, reason=%s", taskID, notification.TaskStatusMsg))

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

	// 向用户的回调地址发送通知（如果配置了 callback_url）
	if video := task.GetVideo(); video != nil {
		// 复制 context 用于 goroutine，避免主请求结束时 context 被取消
		// 使用 WithoutCancel 保留 trace/span 信息但不会被父 context 取消
		callbackCtx := context.WithoutCancel(c.Request.Context())
		// 使用 goroutine 异步发送回调，避免阻塞主流程
		go kling.NotifyUserCallback(callbackCtx, video, notificationBytes)
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

// RelayKlingTransparent 透明代理接口（不做数据库操作，不计费）
// 用于 custom-elements 和 custom-voices 的查询和管理操作
func RelayKlingTransparent(c *gin.Context) {
	meta := util.GetRelayMeta(c)

	// 获取渠道信息
	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		logger.Error(c, fmt.Sprintf("Kling transparent get channel error: user_id=%d, channel_id=%d, error=%v",
			meta.UserId, meta.ChannelId, err))
		respondError(c, err, "get_channel_error", http.StatusInternalServerError)
		return
	}

	// GET 查询：从 URL 末段提取 task_id，路由到任务所属渠道
	if c.Request.Method == http.MethodGet {
		if boundChannel := resolveChannelForTaskQuery(c.Request.URL.Path, meta.UserId); boundChannel != nil {
			if boundChannel.Id != channel.Id {
				logger.Info(c, fmt.Sprintf("Transparent channel override: path=%s, original_channel=%d, bound_channel=%d",
					c.Request.URL.Path, channel.Id, boundChannel.Id))
				channel = boundChannel
				meta.ChannelId = boundChannel.Id
			}
		}
	}

	// POST 请求：从 body 中的资源 ID（video_id / session_id / element_id / voice_id）路由到该资源所属渠道
	if c.Request.Method == http.MethodPost {
		bodyBytes, err := io.ReadAll(c.Request.Body)
		if err == nil {
			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			var params map[string]interface{}
			if json.Unmarshal(bodyBytes, &params) == nil {
				if boundChannel := resolveChannelForBoundResource(params, meta.UserId); boundChannel != nil {
					if boundChannel.Id != channel.Id {
						logger.Info(c, fmt.Sprintf("Transparent channel override (POST): path=%s, original_channel=%d, bound_channel=%d",
							c.Request.URL.Path, channel.Id, boundChannel.Id))
						channel = boundChannel
						meta.ChannelId = boundChannel.Id
					}
				}
			}
		}
	}

	meta.APIKey = channel.Key
	if channel.BaseURL != nil && *channel.BaseURL != "" {
		meta.BaseURL = *channel.BaseURL
	}

	// 确定请求类型
	requestType := kling.DetermineRequestType(c.Request.URL.Path)
	if requestType == "" {
		respondError(c, fmt.Errorf("unsupported endpoint"), "invalid_endpoint", http.StatusBadRequest)
		return
	}

	// 提取完整路径（去掉 /kling 前缀）
	// 例如：/kling/v1/videos/text2video/123 -> /v1/videos/text2video/123
	fullPath := strings.TrimPrefix(c.Request.URL.Path, "/kling")

	// 初始化 Adaptor（透传模式：使用完整路径）
	adaptor := &kling.Adaptor{
		RequestType: requestType,
		FullPath:    fullPath,
	}
	adaptor.Init(meta)

	// 准备请求体（对于 GET 和 DELETE 请求可能为空）
	var bodyReader io.Reader
	if c.Request.Method == http.MethodPost || c.Request.Method == http.MethodPut {
		bodyBytes, err := c.GetRawData()
		if err != nil {
			logger.Error(c, fmt.Sprintf("Kling transparent read body error: user_id=%d, error=%v", meta.UserId, err))
			respondError(c, err, "invalid_request_body", http.StatusBadRequest)
			return
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	// 发送请求
	resp, err := adaptor.DoRequestWithMethod(c, meta, c.Request.Method, bodyReader)
	if err != nil {
		logger.Error(c, fmt.Sprintf("Kling transparent request error: user_id=%d, channel_id=%d, error=%v",
			meta.UserId, meta.ChannelId, err))
		respondError(c, err, "request_failed", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// 读取并透传响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error(c, fmt.Sprintf("Kling transparent read response error: user_id=%d, error=%v", meta.UserId, err))
		respondError(c, err, "read_response_failed", http.StatusInternalServerError)
		return
	}

	// 解析响应用于日志记录
	var klingResp kling.KlingResponse
	if unmarshalErr := json.Unmarshal(body, &klingResp); unmarshalErr == nil {
		logger.Info(c, fmt.Sprintf("Kling transparent success: method=%s, path=%s, user_id=%d, channel_id=%d, code=%d",
			c.Request.Method, c.Request.URL.Path, meta.UserId, meta.ChannelId, klingResp.Code))
	}

	// 透传响应（保持原始状态码和内容）
	c.Data(resp.StatusCode, "application/json", body)
}
