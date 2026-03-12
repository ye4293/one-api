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

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	alimodel "github.com/songquanpeng/one-api/relay/channel/ali"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/util"
)

const (
	aliWanProvider       = "ali-wan"
	aliWanPollingInterval = 10 * time.Minute // 轮询间隔
)

// mapAliWanStatus 将 DashScope 任务状态映射为 DB status
func mapAliWanStatus(dashStatus string) string {
	switch dashStatus {
	case "SUCCEEDED":
		return "succeed"
	case "FAILED", "UNKNOWN", "CANCELED":
		return "failed"
	default: // PENDING, RUNNING
		return "processing"
	}
}

// aliWanBillingInfo 从请求体中提取计费相关字段
type aliWanBillingInfo struct {
	Model      string
	VideoType  string // "text-to-video" or "image-to-video"
	Duration   string
	Resolution string // 统一转换为档位: 480P / 720P / 1080P
	Prompt     string // 用户提示词
}

func parseAliWanBillingInfo(body []byte, metaModel string) aliWanBillingInfo {
	info := aliWanBillingInfo{
		Model:      metaModel,
		VideoType:  "text-to-video",
		Duration:   "5",
		Resolution: "1080P",
	}

	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		return info
	}

	if m, ok := req["model"].(string); ok && m != "" {
		info.Model = m
	}

	// 判断 T2V / I2V，并提取 prompt
	if input, ok := req["input"].(map[string]interface{}); ok {
		if imgURL, ok := input["img_url"].(string); ok && imgURL != "" {
			info.VideoType = "image-to-video"
		}
		if prompt, ok := input["prompt"].(string); ok {
			info.Prompt = prompt
		}
	}

	params, ok := req["parameters"].(map[string]interface{})
	if !ok {
		return info
	}

	// duration
	switch v := params["duration"].(type) {
	case float64:
		info.Duration = strconv.Itoa(int(v))
	case string:
		if v != "" {
			info.Duration = v
		}
	}

	// I2V 用 resolution 字段（"720P"），T2V 用 size 字段（"1280*720"）
	if res, ok := params["resolution"].(string); ok && res != "" {
		info.Resolution = strings.ToUpper(res)
	} else if size, ok := params["size"].(string); ok && size != "" {
		info.Resolution = inferResolutionFromSize(size)
	}

	return info
}

// inferResolutionFromSize 从 "宽*高" 格式推断分辨率档位
func inferResolutionFromSize(size string) string {
	parts := strings.Split(size, "*")
	if len(parts) != 2 {
		return "1080P"
	}
	w, _ := strconv.Atoi(parts[0])
	h, _ := strconv.Atoi(parts[1])
	pixels := w * h
	switch {
	case pixels <= 520000: // 480P: 832*480=399360, 624*624=389376, 480*832=399360
		return "480P"
	case pixels <= 1050000: // 720P: 1280*720=921600, 960*960=921600, 1088*832=905216
		return "720P"
	default: // 1080P: 1920*1080=2073600, 1440*1440=2073600, etc.
		return "1080P"
	}
}

// ─── 创建任务 ──────────────────────────────────────────────────────────────────

// RelayAliVideoCreate 处理 POST /ali/api/v1/services/aigc/video-generation/video-synthesis
// 支持文生视频 (T2V) 和图生视频 (I2V)，共用同一 DashScope 端点
func RelayAliVideoCreate(c *gin.Context) {
	ctx := c.Request.Context()
	meta := util.GetRelayMeta(c)

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, openai.ErrorWrapper(err, "read_body_failed", http.StatusBadRequest).Error)
		return
	}

	billing := parseAliWanBillingInfo(bodyBytes, meta.ActualModelName)

	quota := common.CalculateVideoQuota(billing.Model, billing.VideoType, "", billing.Duration, billing.Resolution)

	userQuota, err := dbmodel.CacheGetUserQuota(ctx, meta.UserId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, openai.ErrorWrapper(err, "get_quota_failed", http.StatusInternalServerError).Error)
		return
	}
	if userQuota < quota {
		c.JSON(http.StatusPaymentRequired, openai.ErrorWrapper(
			fmt.Errorf("insufficient quota: need %d, have %d", quota, userQuota),
			"insufficient_quota", http.StatusPaymentRequired).Error)
		return
	}

	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, openai.ErrorWrapper(err, "get_channel_failed", http.StatusInternalServerError).Error)
		return
	}

	baseURL := "https://dashscope.aliyuncs.com"
	if channel.BaseURL != nil && *channel.BaseURL != "" {
		baseURL = strings.TrimRight(*channel.BaseURL, "/")
	}
	upstreamURL := baseURL + "/api/v1/services/aigc/video-generation/video-synthesis"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(bodyBytes))
	if err != nil {
		c.JSON(http.StatusInternalServerError, openai.ErrorWrapper(err, "build_request_failed", http.StatusInternalServerError).Error)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+channel.Key)
	req.Header.Set("X-DashScope-Async", "enable")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, openai.ErrorWrapper(err, "upstream_request_failed", http.StatusBadGateway).Error)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, openai.ErrorWrapper(err, "read_upstream_response_failed", http.StatusInternalServerError).Error)
		return
	}

	var aliResp alimodel.AliVideoResponse
	if err := json.Unmarshal(respBody, &aliResp); err != nil {
		c.Data(resp.StatusCode, "application/json", respBody)
		return
	}

	// 上游业务错误，直接透传，不扣费不建记录
	if aliResp.Code != "" {
		logger.Error(c.Request.Context(), fmt.Sprintf("[ali-wan] upstream error: code=%s, msg=%s", aliResp.Code, aliResp.Message))
		c.Data(resp.StatusCode, "application/json", respBody)
		return
	}

	taskID := ""
	if aliResp.Output != nil {
		taskID = aliResp.Output.TaskID
	}

	// 预扣费
	if err := dbmodel.PostConsumeTokenQuota(meta.TokenId, quota); err != nil {
		logger.Error(c.Request.Context(), fmt.Sprintf("[ali-wan] pre-deduct quota failed: %v", err))
	}
	_ = dbmodel.CacheUpdateUserQuota(ctx, meta.UserId)

	// 写 DB 记录
	video := &dbmodel.Video{
		TaskId:     taskID,
		Provider:   aliWanProvider,
		Model:      billing.Model,
		Type:       billing.VideoType,
		Duration:   billing.Duration,
		Resolution: billing.Resolution,
		Prompt:     billing.Prompt,
		Status:     "processing",
		Quota:      quota,
		UserId:     meta.UserId,
		Username:   dbmodel.GetUsernameById(meta.UserId),
		ChannelId:  meta.ChannelId,
		CreatedAt:  time.Now().Unix(),
	}
	if err := video.Insert(); err != nil {
		logger.Error(c.Request.Context(), fmt.Sprintf("[ali-wan] insert video record failed: task_id=%s, %v", taskID, err))
	}

	logger.Info(c.Request.Context(), fmt.Sprintf("[ali-wan] task created: task_id=%s, model=%s, type=%s, user_id=%d, channel_id=%d, quota=%d",
		taskID, billing.Model, billing.VideoType, meta.UserId, meta.ChannelId, quota))

	c.Data(resp.StatusCode, "application/json", respBody)
}

// ─── 查询任务结果 ───────────────────────────────────────────────────────────────

// RelayAliVideoResult 处理 GET /ali/api/v1/tasks/:taskId
// 向 DashScope 查询，透传响应，同时更新 DB 状态
func RelayAliVideoResult(c *gin.Context) {
	ctx := c.Request.Context()
	taskID := c.Param("taskId")

	videoTask, err := dbmodel.GetVideoTaskById(taskID)
	if err != nil {
		c.JSON(http.StatusNotFound, openai.ErrorWrapper(
			fmt.Errorf("task not found: %s", taskID), "task_not_found", http.StatusNotFound).Error)
		return
	}

	// 已终态且有缓存 URL，直接返回 DB 结果，避免访问已过期的视频链接
	if videoTask.Status == "succeed" && videoTask.StoreUrl != "" {
		c.JSON(http.StatusOK, buildAliWanCachedResponse(videoTask))
		return
	}

	channel, err := dbmodel.GetChannelById(videoTask.ChannelId, true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, openai.ErrorWrapper(err, "get_channel_failed", http.StatusInternalServerError).Error)
		return
	}

	baseURL := "https://dashscope.aliyuncs.com"
	if channel.BaseURL != nil && *channel.BaseURL != "" {
		baseURL = strings.TrimRight(*channel.BaseURL, "/")
	}
	upstreamURL := fmt.Sprintf("%s/api/v1/tasks/%s", baseURL, taskID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, openai.ErrorWrapper(err, "build_request_failed", http.StatusInternalServerError).Error)
		return
	}
	req.Header.Set("Authorization", "Bearer "+channel.Key)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, openai.ErrorWrapper(err, "upstream_request_failed", http.StatusBadGateway).Error)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, openai.ErrorWrapper(err, "read_response_failed", http.StatusInternalServerError).Error)
		return
	}

	// 解析并更新 DB 状态
	var queryResp alimodel.AliVideoQueryResponse
	if jsonErr := json.Unmarshal(respBody, &queryResp); jsonErr == nil && queryResp.Output != nil {
		dashStatus := queryResp.Output.TaskStatus
		dbStatus := mapAliWanStatus(dashStatus)

		if dbStatus != videoTask.Status {
			updates := map[string]interface{}{
				"status":     dbStatus,
				"updated_at": time.Now().Unix(),
			}
			if dashStatus == "SUCCEEDED" && queryResp.Output.VideoURL != "" {
				updates["store_url"] = queryResp.Output.VideoURL
			}
			if dbStatus == "failed" {
				updates["fail_reason"] = buildAliWanFailMessage(queryResp)
			}

			if err := dbmodel.DB.Model(&dbmodel.Video{}).
				Where("task_id = ?", taskID).
				Updates(updates).Error; err != nil {
				logger.Error(c.Request.Context(), fmt.Sprintf("[ali-wan] update task status failed: task_id=%s, %v", taskID, err))
			}

			// 失败时异步退款补偿
			if dbStatus == "failed" && videoTask.Status == "processing" {
				go compensateAliWanTask(taskID, videoTask)
			}
		}
	}

	c.Data(resp.StatusCode, "application/json", respBody)
}

// ─── 辅助函数 ──────────────────────────────────────────────────────────────────

func buildAliWanFailMessage(resp alimodel.AliVideoQueryResponse) string {
	if resp.Output != nil {
		if resp.Output.Code != "" && resp.Output.Message != "" {
			return fmt.Sprintf("[%s] %s", resp.Output.Code, resp.Output.Message)
		}
		if resp.Output.Message != "" {
			return resp.Output.Message
		}
	}
	if resp.Message != "" {
		return resp.Message
	}
	return "task failed"
}

// buildAliWanCachedResponse 从 DB 构造已缓存的成功响应（DashScope 格式）
func buildAliWanCachedResponse(v *dbmodel.Video) map[string]interface{} {
	return map[string]interface{}{
		"request_id": "",
		"output": map[string]interface{}{
			"task_id":     v.TaskId,
			"task_status": "SUCCEEDED",
			"video_url":   v.StoreUrl,
		},
	}
}

// compensateAliWanTask 退款补偿（异步执行）
func compensateAliWanTask(taskID string, v *dbmodel.Video) {
	ctx := context.Background()
	if v.Quota <= 0 {
		return
	}
	if err := dbmodel.IncreaseUserQuota(v.UserId, v.Quota); err != nil {
		logger.Error(ctx, fmt.Sprintf("[ali-wan] compensate quota failed: task_id=%s, user_id=%d, quota=%d, err=%v",
			taskID, v.UserId, v.Quota, err))
		return
	}
	logger.Info(ctx, fmt.Sprintf("[ali-wan] compensated: task_id=%s, user_id=%d, quota=%d", taskID, v.UserId, v.Quota))
}

// ─── 定时轮询器 ────────────────────────────────────────────────────────────────

// StartAliWanTaskPoller 启动阿里云万相视频任务轮询器
// 每隔 10 分钟扫描一次数据库中 provider='ali-wan' 且 status='processing' 的任务
func StartAliWanTaskPoller(ctx context.Context) {
	ticker := time.NewTicker(aliWanPollingInterval)
	defer ticker.Stop()

	logger.Info(ctx, fmt.Sprintf("[ali-wan-poller] started, interval=%v", aliWanPollingInterval))

	// 立即执行一次
	pollAliWanTasks(ctx)

	for {
		select {
		case <-ctx.Done():
			logger.Info(ctx, "[ali-wan-poller] stopped")
			return
		case <-ticker.C:
			pollAliWanTasks(ctx)
		}
	}
}

// pollAliWanTasks 轮询所有处理中的阿里云万相视频任务
func pollAliWanTasks(ctx context.Context) {
	var tasks []dbmodel.Video
	if err := dbmodel.DB.Where("provider = ? AND status = ?", aliWanProvider, "processing").
		Find(&tasks).Error; err != nil {
		logger.Error(ctx, fmt.Sprintf("[ali-wan-poller] query tasks failed: %v", err))
		return
	}

	if len(tasks) == 0 {
		logger.Info(ctx, "[ali-wan-poller] no processing tasks found")
		return
	}

	logger.Info(ctx, fmt.Sprintf("[ali-wan-poller] found %d processing tasks", len(tasks)))

	for _, task := range tasks {
		go pollSingleAliWanTask(ctx, &task)
	}
}

// pollSingleAliWanTask 轮询单个任务状态
func pollSingleAliWanTask(ctx context.Context, task *dbmodel.Video) {
	channel, err := dbmodel.GetChannelById(task.ChannelId, true)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("[ali-wan-poller] get channel failed: task_id=%s, channel_id=%d, err=%v",
			task.TaskId, task.ChannelId, err))
		return
	}

	baseURL := "https://dashscope.aliyuncs.com"
	if channel.BaseURL != nil && *channel.BaseURL != "" {
		baseURL = strings.TrimRight(*channel.BaseURL, "/")
	}
	upstreamURL := fmt.Sprintf("%s/api/v1/tasks/%s", baseURL, task.TaskId)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL, nil)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("[ali-wan-poller] build request failed: task_id=%s, err=%v",
			task.TaskId, err))
		return
	}
	req.Header.Set("Authorization", "Bearer "+channel.Key)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("[ali-wan-poller] request failed: task_id=%s, err=%v",
			task.TaskId, err))
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("[ali-wan-poller] read response failed: task_id=%s, err=%v",
			task.TaskId, err))
		return
	}

	var queryResp alimodel.AliVideoQueryResponse
	if err := json.Unmarshal(respBody, &queryResp); err != nil {
		logger.Error(ctx, fmt.Sprintf("[ali-wan-poller] parse response failed: task_id=%s, err=%v",
			task.TaskId, err))
		return
	}

	if queryResp.Output == nil {
		logger.Error(ctx, fmt.Sprintf("[ali-wan-poller] empty output: task_id=%s", task.TaskId))
		return
	}

	dashStatus := queryResp.Output.TaskStatus
	dbStatus := mapAliWanStatus(dashStatus)

	// 状态未变化，跳过
	if dbStatus == task.Status {
		logger.Info(ctx, fmt.Sprintf("[ali-wan-poller] status unchanged: task_id=%s, status=%s",
			task.TaskId, dbStatus))
		return
	}

	// 更新数据库
	updates := map[string]interface{}{
		"status":     dbStatus,
		"updated_at": time.Now().Unix(),
	}

	if dashStatus == "SUCCEEDED" && queryResp.Output.VideoURL != "" {
		updates["store_url"] = queryResp.Output.VideoURL
	}

	if dbStatus == "failed" {
		updates["fail_reason"] = buildAliWanFailMessage(queryResp)
	}

	if err := dbmodel.DB.Model(&dbmodel.Video{}).
		Where("task_id = ?", task.TaskId).
		Updates(updates).Error; err != nil {
		logger.Error(ctx, fmt.Sprintf("[ali-wan-poller] update status failed: task_id=%s, err=%v",
			task.TaskId, err))
		return
	}

	logger.Info(ctx, fmt.Sprintf("[ali-wan-poller] updated: task_id=%s, %s -> %s",
		task.TaskId, task.Status, dbStatus))

	// 失败时补偿退款
	if dbStatus == "failed" {
		go compensateAliWanTask(task.TaskId, task)
	}
}

