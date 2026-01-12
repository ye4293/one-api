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

// RelayKlingVideo 处理 Kling 视频生成请求
// @Summary Kling 视频生成（多端点）
// @Description 使用 Kling AI 生成视频，支持多种模式：
// @Description - text2video: 文本描述生成视频
// @Description - omni-video: 文本+图片混合生成，支持镜头控制
// @Description - image2video: 单张或首尾帧图片生成视频
// @Description - multi-image2video: 多张图片生成连贯视频
// @Description
// @Description **通用参数**: model(必填), mode(可选: std/pro), duration(可选: 5/10秒), aspect_ratio(可选: 16:9/9:16/1:1), cfg_scale(可选: 0-1), negative_prompt(可选)
// @Description
// @Description **text2video特有**: prompt(必填)
// @Description **omni-video特有**: prompt(必填), image(可选-首帧), image_tail(可选-尾帧), camera_control(可选-镜头控制)
// @Description **image2video特有**: image(必填-首帧), image_tail(可选-尾帧), prompt(可选)
// @Description **multi-image2video特有**: image_list(必填-图片数组), prompt(必填)
// @Tags Kling视频生成
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer Token" default(Bearer sk-xxxxx)
// @Param request body object true "视频生成请求参数（JSON格式，参数因端点而异）示例见文档"
// @Success 200 {object} object{code=int,message=string,request_id=string,data=object{task_id=string,task_status=string,created_at=int,updated_at=int}} "任务已创建，返回 task_id 用于查询结果"
// @Failure 400 {object} object{error=object{message=string,type=string,code=string}} "请求参数错误"
// @Failure 401 {object} object{error=object{message=string,type=string,code=string}} "认证失败"
// @Failure 402 {object} object{error=object{message=string,type=string,code=string}} "余额不足"
// @Failure 500 {object} object{error=object{message=string,type=string,code=string}} "服务器错误"
// @Router /kling/v1/videos/text2video [post]
// @Router /kling/v1/videos/omni-video [post]
// @Router /kling/v1/videos/image2video [post]
// @Router /kling/v1/videos/multi-image2video [post]
// @Security Bearer
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

	// 先创建 Video 记录以获取 ID（用于 external_task_id）
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
	//video.CreatedAt = klingResp.Data.CreatedAt
	//video.UpdatedAt = klingResp.Data.UpdatedAt
	video.VideoId = klingResp.Data.TaskID
	if err := video.Update(); err != nil {
		logger.SysError(fmt.Sprintf("更新视频任务失败: id=%d, task_id=%s, error=%v", video.Id, video.TaskId, err))
	}
	logger.SysLog(fmt.Sprintf("video: id=+%v, ", video))
	logger.SysLog(fmt.Sprintf("Kling video task submitted: id=%d, task_id=%s, external_task_id=%d, user_id=%d, channel_id=%d, quota=%d",
		video.Id, klingResp.Data.TaskID, video.Id, meta.UserId, meta.ChannelId, quota))

	// 透传 Kling 原始响应
	c.JSON(http.StatusOK, klingResp)
}

// RelayKlingVideoResult 查询任务结果（从数据库读取）
// @Summary 查询 Kling 视频生成结果
// @Description 根据任务 ID 查询视频生成任务的执行状态和结果
// @Description
// @Description **任务状态说明**:
// @Description - submitted: 已提交，等待处理
// @Description - processing: 处理中
// @Description - succeed: 成功，可获取视频 URL
// @Description - failed: 失败，查看 task_status_msg 了解原因
// @Description
// @Description **使用流程**: 创建任务后获得 task_id，使用此接口轮询查询，直到状态为 succeed 或 failed
// @Tags Kling视频生成
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer Token" default(Bearer sk-xxxxx)
// @Param id path string true "任务ID（创建任务时返回的 task_id）"
// @Success 200 {object} object{code=int,message=string,request_id=string,data=object{task_id=string,task_status=string,task_status_msg=string,created_at=int,updated_at=int,task_result=object{videos=[]object{id=string,url=string,duration=string}}}} "任务状态和结果，succeed 时包含视频 URL"
// @Failure 400 {object} object{code=int,message=string,error=string} "请求参数错误"
// @Failure 401 {object} object{error=object{message=string,type=string}} "认证失败"
// @Failure 404 {object} object{code=int,message=string,error=string} "任务不存在"
// @Failure 500 {object} object{code=int,message=string,error=string} "服务器错误"
// @Router /kling/v1/videos/{id} [get]
// @Security Bearer
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
// @Summary Kling 回调处理（内部接口）
// @Description 接收 Kling 平台的异步回调通知，自动更新视频生成任务状态和结果
// @Description
// @Description **注意事项**:
// @Description - 此接口由 Kling 平台自动调用，无需手动请求
// @Description - 不需要 Authorization 认证
// @Description - 回调成功后会自动完成计费
// @Description - 需要在系统配置中设置 ServerAddress 为公网可访问地址
// @Description
// @Description **回调时机**: 任务完成（成功或失败）时触发
// @Tags Kling视频生成
// @Accept json
// @Produce json
// @Param request body object true "Kling 回调数据（由 Kling 平台发送）"
// @Success 200 {object} object{message=string} "回调处理成功"
// @Failure 400 {object} object{error=string} "请求参数错误"
// @Failure 500 {object} object{error=string} "服务器错误"
// @Router /kling/internal/callback [post]
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
			TaskInfo:      notification.TaskInfo, // 保存 task_info（包含 parent_video 等信息）
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

		// 更新 quota 字段
		video.Quota = newQuota

		// 后扣费模式：在成功时根据实际 duration 扣费
		err := dbmodel.DecreaseUserQuota(video.UserId, newQuota)
		if err != nil {
			logger.SysError(fmt.Sprintf("Kling callback billing failed: user_id=%d, quota=%d, error=%v", video.UserId, newQuota, err))
		} else {
			logger.SysLog(fmt.Sprintf("Kling callback billing success: user_id=%d, old_quota=%d, new_quota=%d, duration=%s, task_id=%s",
				video.UserId, oldQuota, newQuota, actualDuration, taskID))
		}

		// 计算总耗时（秒）
		video.TotalDuration = time.Now().Unix() - video.CreatedAt

		video.Update()
	} else if notification.TaskStatus == kling.TaskStatusFailed {
		video.Status = kling.TaskStatusFailed
		video.FailReason = notification.TaskStatusMsg

		// 计算总耗时（秒）
		video.TotalDuration = time.Now().Unix() - video.CreatedAt

		video.Update()
		logger.SysLog(fmt.Sprintf("Kling callback task failed: task_id=%s, reason=%s", taskID, notification.TaskStatusMsg))
	} else {
		// 其他状态（processing等），更新状态但不扣费
		video.Status = notification.TaskStatus

		// 计算总耗时（秒）
		video.TotalDuration = time.Now().Unix() - video.CreatedAt

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

// DoIdentifyFace 人脸识别
// @Summary Kling 人脸识别（同步接口）
// @Description 从视频中识别人脸信息，获取 session_id 和 face_id，用于高级对口型功能
// @Description
// @Description **参数说明**:
// @Description - video_id: Kling 视频任务 ID（与 video_url 二选一）
// @Description - video_url: 视频文件 URL（与 video_id 二选一）
// @Description
// @Description **返回数据**:
// @Description - session_id: 会话 ID，用于对口型接口
// @Description - face_data: 人脸数据数组，每个包含 face_id、face_image、start_time、end_time
// @Description
// @Description **计费说明**: 请求成功立即计费（按次）
// @Description
// @Description **使用场景**: 在使用高级对口型功能前，需要先调用此接口识别视频中的人脸
// @Tags Kling视频生成
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer Token" default(Bearer sk-xxxxx)
// @Param request body object{video_id=string,video_url=string} true "人脸识别请求参数（video_id 或 video_url 二选一）"
// @Success 200 {object} object{code=int,message=string,request_id=string,data=object{session_id=string,face_data=[]object{face_id=string,face_image=string,start_time=int,end_time=int}}} "人脸识别成功，返回 session_id 和人脸数据"
// @Failure 400 {object} object{error=object{message=string,type=string,code=string}} "参数错误（如未提供 video_id 或 video_url）"
// @Failure 401 {object} object{error=object{message=string,type=string}} "认证失败"
// @Failure 500 {object} object{error=object{message=string,type=string}} "服务器错误"
// @Router /kling/v1/videos/identify-face [post]
// @Security Bearer
func DoIdentifyFace(c *gin.Context) {
	controller.DoIdentifyFace(c)
}

// DoAdvancedLipSync 高级唇形同步
// @Summary Kling 高级对口型（异步接口）
// @Description 为视频中的人脸添加对口型效果，使人物口型与音频同步
// @Description
// @Description **前置要求**: 必须先调用人脸识别接口获取 session_id 和 face_id
// @Description
// @Description **参数说明**:
// @Description - model: 模型名称（必填）如 "kling-v1"
// @Description - session_id: 人脸识别返回的会话 ID（必填）
// @Description - face_choose: 人脸选择数组（必填）
// @Description
// @Description **face_choose 参数**:
// @Description - face_id: 人脸 ID（必填，从人脸识别接口获取）
// @Description - audio_id 或 sound_file: 音频 ID 或音频文件 URL（二选一必填）
// @Description - sound_start_time: 音频开始时间，毫秒（必填）
// @Description - sound_end_time: 音频结束时间，毫秒（必填）
// @Description - sound_insert_time: 音频插入到视频的时间点，毫秒（必填）
// @Description - sound_volume: 音频音量 0-1（可选，默认 1.0）
// @Description - original_audio_volume: 原始音频音量 0-1（可选，默认 1.0）
// @Description
// @Description **计费说明**: 任务成功后通过回调结束时根据视频时长计费（后扣费）
// @Description
// @Description **使用流程**: 1. 人脸识别 → 2. 创建对口型任务 → 3. 查询任务结果或等待回调
// @Tags Kling视频生成
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer Token" default(Bearer sk-xxxxx)
// @Param request body object{model=string,session_id=string,face_choose=[]object{face_id=string,audio_id=string,sound_file=string,sound_start_time=int,sound_end_time=int,sound_insert_time=int,sound_volume=number,original_audio_volume=number}} true "对口型请求参数"
// @Success 200 {object} object{code=int,message=string,request_id=string,data=object{task_id=string,task_status=string,created_at=int,updated_at=int}} "对口型任务已创建，返回 task_id"
// @Failure 400 {object} object{error=object{message=string,type=string,code=string}} "请求参数错误"
// @Failure 401 {object} object{error=object{message=string,type=string}} "认证失败"
// @Failure 402 {object} object{error=object{message=string,type=string}} "余额不足"
// @Failure 500 {object} object{error=object{message=string,type=string}} "服务器错误"
// @Router /kling/v1/videos/advanced-lip-sync [post]
// @Security Bearer
func DoAdvancedLipSync(c *gin.Context) {
	controller.DoAdvancedLipSync(c)
}
