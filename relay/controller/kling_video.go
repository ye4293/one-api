package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/kling"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/util"
)

// DoIdentifyFace 处理人脸识别请求（同步接口，不计费）
func DoIdentifyFace(c *gin.Context) {
	meta := util.GetRelayMeta(c)

	// 读取并解析请求体
	bodyBytes, err := c.GetRawData()
	if err != nil {
		logger.SysError(fmt.Sprintf("Kling identify-face read body error: user_id=%d, error=%v", meta.UserId, err))
		errResp := openai.ErrorWrapper(err, "invalid_request_body", http.StatusBadRequest)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	var request map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &request); err != nil {
		logger.SysError(fmt.Sprintf("Kling identify-face parse json error: user_id=%d, error=%v", meta.UserId, err))
		errResp := openai.ErrorWrapper(err, "invalid_request_json", http.StatusBadRequest)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	// 验证参数：video_id 和 video_url 必须二选一
	_, hasVideoID := request["video_id"]
	_, hasVideoURL := request["video_url"]

	if (!hasVideoID && !hasVideoURL) || (hasVideoID && hasVideoURL) {
		logger.SysError(fmt.Sprintf("Kling identify-face invalid parameters: user_id=%d, has_video_id=%v, has_video_url=%v",
			meta.UserId, hasVideoID, hasVideoURL))
		errResp := openai.ErrorWrapper(
			fmt.Errorf("video_id 和 video_url 必须二选一填写"),
			"invalid_parameters",
			http.StatusBadRequest,
		)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	// 获取渠道信息
	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		logger.SysError(fmt.Sprintf("Kling identify-face get channel error: user_id=%d, channel_id=%d, error=%v",
			meta.UserId, meta.ChannelId, err))
		errResp := openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	meta.APIKey = channel.Key
	if channel.BaseURL != nil && *channel.BaseURL != "" {
		meta.BaseURL = *channel.BaseURL
	}

	// 调用 Kling API
	adaptor := &kling.Adaptor{RequestType: kling.RequestTypeIdentifyFace}
	adaptor.Init(meta)

	convertedBody, err := json.Marshal(request)
	if err != nil {
		logger.SysError(fmt.Sprintf("Kling identify-face marshal request error: user_id=%d, error=%v", meta.UserId, err))
		errResp := openai.ErrorWrapper(err, "marshal_request_failed", http.StatusInternalServerError)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	resp, err := adaptor.DoRequest(c, meta, bytes.NewReader(convertedBody))
	if err != nil {
		logger.SysError(fmt.Sprintf("Kling identify-face request error: user_id=%d, channel_id=%d, error=%v",
			meta.UserId, meta.ChannelId, err))
		errResp := openai.ErrorWrapper(err, "request_failed", http.StatusInternalServerError)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.SysError(fmt.Sprintf("Kling identify-face read response error: user_id=%d, error=%v", meta.UserId, err))
		errResp := openai.ErrorWrapper(err, "read_response_failed", http.StatusInternalServerError)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	var klingResp kling.IdentifyFaceResponse
	if err := json.Unmarshal(body, &klingResp); err != nil {
		logger.SysError(fmt.Sprintf("Kling identify-face parse response error: user_id=%d, body=%s, error=%v",
			meta.UserId, string(body), err))
		errResp := openai.ErrorWrapper(err, "parse_response_failed", http.StatusInternalServerError)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	if klingResp.Code != 0 {
		logger.SysError(fmt.Sprintf("Kling identify-face API error: user_id=%d, code=%d, message=%s",
			meta.UserId, klingResp.Code, klingResp.Message))
		errResp := openai.ErrorWrapper(
			fmt.Errorf(klingResp.Message),
			"kling_api_error",
			resp.StatusCode,
		)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	logger.SysLog(fmt.Sprintf("Kling identify-face: session_id=%s, faces=%d, user_id=%d",
		klingResp.Data.SessionID, len(klingResp.Data.FaceData), meta.UserId))

	// 直接返回 Kling 响应（不保存到数据库，不计费）
	c.JSON(http.StatusOK, klingResp)
}

// DoAdvancedLipSync 处理对口型任务创建（异步接口，后扣费）
func DoAdvancedLipSync(c *gin.Context) {
	meta := util.GetRelayMeta(c)
	requestType := kling.RequestTypeAdvancedLipSync

	// 读取并解析请求体
	bodyBytes, err := c.GetRawData()
	if err != nil {
		logger.SysError(fmt.Sprintf("Kling advanced-lip-sync read body error: user_id=%d, error=%v", meta.UserId, err))
		errResp := openai.ErrorWrapper(err, "invalid_request_body", http.StatusBadRequest)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	var requestParams map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &requestParams); err != nil {
		logger.SysError(fmt.Sprintf("Kling advanced-lip-sync parse json error: user_id=%d, error=%v", meta.UserId, err))
		errResp := openai.ErrorWrapper(err, "invalid_request_json", http.StatusBadRequest)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	// 获取模型信息用于计费
	model := kling.GetModelNameFromRequest(requestParams)
	if model == "" {
		model = "kling-v1" // 默认模型
	}

	// 计算预估费用
	quota := common.CalculateVideoQuota(model, requestType, "", "", "")

	// 检查用户余额（后扣费模式：仅验证余额）
	userQuota, err := dbmodel.CacheGetUserQuota(c.Request.Context(), meta.UserId)
	if err != nil {
		logger.SysError(fmt.Sprintf("Kling advanced-lip-sync get user quota error: user_id=%d, error=%v", meta.UserId, err))
		errResp := openai.ErrorWrapper(err, "get_user_quota_error", http.StatusInternalServerError)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}
	if userQuota < quota {
		logger.SysError(fmt.Sprintf("Kling advanced-lip-sync insufficient quota: user_id=%d, user_quota=%d, required_quota=%d",
			meta.UserId, userQuota, quota))
		errResp := openai.ErrorWrapper(
			fmt.Errorf("余额不足"),
			"insufficient_quota",
			http.StatusPaymentRequired,
		)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	// 获取渠道信息
	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		logger.SysError(fmt.Sprintf("Kling advanced-lip-sync get channel error: user_id=%d, channel_id=%d, error=%v",
			meta.UserId, meta.ChannelId, err))
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
		TaskId:    "",
		UserId:    meta.UserId,
		Username:  user.Username,
		ChannelId: meta.ChannelId,
		Model:     model,
		Provider:  "kling",
		Type:      requestType,
		Status:    "",
		Quota:     quota,
	}
	if err := video.Insert(); err != nil {
		logger.SysError(fmt.Sprintf("Kling advanced-lip-sync insert video record error: user_id=%d, error=%v", meta.UserId, err))
		errResp := openai.ErrorWrapper(err, "database_error", http.StatusInternalServerError)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	// 构建回调URL
	var callbackURL string
	if config.ServerAddress != "" {
		callbackURL = fmt.Sprintf("%s/kling/internal/callback", config.ServerAddress)
	}

	// 调用 Kling API
	adaptor := &kling.Adaptor{RequestType: requestType}
	adaptor.Init(meta)

	// 转换请求并注入回调URL和 external_task_id
	convertedBody, err := adaptor.ConvertRequest(c, meta, requestParams, callbackURL, video.Id)
	if err != nil {
		logger.SysError(fmt.Sprintf("Kling advanced-lip-sync convert request error: user_id=%d, video_id=%d, error=%v",
			meta.UserId, video.Id, err))
		video.Status = kling.TaskStatusFailed
		video.FailReason = err.Error()
		video.Update()
		errResp := openai.ErrorWrapper(err, "convert_request_failed", http.StatusInternalServerError)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	resp, err := adaptor.DoRequest(c, meta, bytes.NewReader(convertedBody))
	if err != nil {
		logger.SysError(fmt.Sprintf("Kling advanced-lip-sync request error: user_id=%d, video_id=%d, channel_id=%d, error=%v",
			meta.UserId, video.Id, meta.ChannelId, err))
		video.Status = kling.TaskStatusFailed
		video.FailReason = err.Error()
		video.Update()
		errResp := openai.ErrorWrapper(err, "request_failed", http.StatusInternalServerError)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	klingResp, errWithCode := adaptor.DoResponse(c, resp, meta)
	if errWithCode != nil {
		logger.SysError(fmt.Sprintf("Kling advanced-lip-sync response error: user_id=%d, video_id=%d, code=%s, message=%s",
			meta.UserId, video.Id, errWithCode.Error.Code, errWithCode.Error.Message))
		video.Status = kling.TaskStatusFailed
		video.FailReason = errWithCode.Error.Message
		video.Update()
		c.JSON(errWithCode.StatusCode, errWithCode.Error)
		return
	}

	// 更新 Video 记录
	video.TaskId = klingResp.Data.TaskID
	video.Status = klingResp.Data.TaskStatus
	video.VideoId = klingResp.Data.TaskID
	if err := video.Update(); err != nil {
		logger.SysError(fmt.Sprintf("更新对口型任务失败: id=%d, task_id=%s, error=%v",
			video.Id, video.TaskId, err))
	}

	logger.SysLog(fmt.Sprintf("Kling advanced-lip-sync task created: id=%d, task_id=%s, user_id=%d, quota=%d",
		video.Id, klingResp.Data.TaskID, meta.UserId, quota))

	// 返回 Kling 原始响应
	c.JSON(http.StatusOK, klingResp)
}
