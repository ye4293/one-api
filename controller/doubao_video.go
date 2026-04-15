package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	doubaomodel "github.com/songquanpeng/one-api/relay/channel/doubao"
	"github.com/songquanpeng/one-api/relay/util"
)

const (
	doubaoVideoProvider     = "doubao"
	doubaoPollingIntervalMin = 10 * time.Minute
)

// ─── 状态映射 ─────────────────────────────────────────────────────────────────

func mapDoubaoStatus(status string) string {
	switch status {
	case "succeeded":
		return "succeed"
	case "failed":
		return "failed"
	default: // queued, running
		return "processing"
	}
}

// ─── 请求上下文 ───────────────────────────────────────────────────────────────

// buildDoubaoMeta 从渠道信息构造最小 RelayMeta（供 poller 使用）
func buildDoubaoMeta(channel *dbmodel.Channel) *util.RelayMeta {
	meta := &util.RelayMeta{
		APIKey:    channel.Key,
		ChannelId: channel.Id,
	}
	if channel.BaseURL != nil && *channel.BaseURL != "" {
		meta.BaseURL = *channel.BaseURL
	}
	return meta
}

// ─── 创建任务 ─────────────────────────────────────────────────────────────────

// RelayDoubaoVideoCreate 处理 POST doubao/api/v3/contents/generations/tasks
func RelayDoubaoVideoCreate(c *gin.Context) {
	ctx := c.Request.Context()
	meta := util.GetRelayMeta(c)

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		respondError(c, err, "read_body_failed", http.StatusBadRequest)
		return
	}

	// 预扣费额度检查
	quota := doubaomodel.CalcPrePayQuota()
	userQuota, err := dbmodel.CacheGetUserQuota(ctx, meta.UserId)
	if err != nil {
		respondError(c, err, "get_quota_failed", http.StatusInternalServerError)
		return
	}
	if userQuota < quota {
		respondError(c, fmt.Errorf("insufficient quota"), "insufficient_quota", http.StatusPaymentRequired)
		return
	}

	// 获取渠道信息
	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		respondError(c, err, "get_channel_failed", http.StatusInternalServerError)
		return
	}
	meta.APIKey = channel.Key
	if channel.BaseURL != nil && *channel.BaseURL != "" {
		meta.BaseURL = *channel.BaseURL
	}

	adaptor := &doubaomodel.VideoAdaptor{}
	resp, err := adaptor.DoCreate(ctx, meta, body)
	if err != nil {
		respondError(c, err, "upstream_request_failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		respondError(c, err, "read_upstream_response_failed", http.StatusInternalServerError)
		return
	}

	createResp, parseErr := adaptor.ParseCreateResponse(respBody)
	if parseErr != nil {
		// 解析失败：透传原始响应，不扣费
		c.Data(resp.StatusCode, "application/json", respBody)
		return
	}

	// 上游业务错误：不扣费
	if createResp.Error != nil {
		logger.Error(ctx, fmt.Sprintf("[doubao] upstream error: code=%s, msg=%s", createResp.Error.Code, createResp.Error.Message))
		c.Data(resp.StatusCode, "application/json", respBody)
		return
	}

	taskID := createResp.ID

	// 预扣费
	if err := dbmodel.PostConsumeTokenQuota(meta.TokenId, quota); err != nil {
		logger.Error(ctx, fmt.Sprintf("[doubao] pre-deduct quota failed: %v", err))
	}
	_ = dbmodel.CacheUpdateUserQuota(ctx, meta.UserId)

	// 解析模型和提示词（供 DB 记录）
	model, prompt := parseDoubaoRequestMeta(body)

	// 写 DB 记录
	video := &dbmodel.Video{
		TaskId:    taskID,
		Provider:  doubaoVideoProvider,
		Model:     model,
		Type:      "text-to-video",
		Prompt:    prompt,
		Status:    "processing",
		Quota:     quota,
		UserId:    meta.UserId,
		Username:  dbmodel.GetUsernameById(meta.UserId),
		ChannelId: meta.ChannelId,
		CreatedAt: time.Now().Unix(),
	}
	if err := video.Insert(); err != nil {
		logger.Error(ctx, fmt.Sprintf("[doubao] insert video record failed: task_id=%s, %v", taskID, err))
	}

	logger.Info(ctx, fmt.Sprintf("[doubao] task created: task_id=%s, model=%s, user_id=%d, channel_id=%d, quota=%d",
		taskID, model, meta.UserId, meta.ChannelId, quota))

	c.Data(resp.StatusCode, "application/json", respBody)
}

// parseDoubaoRequestMeta 从请求体提取 model 和 prompt
func parseDoubaoRequestMeta(body []byte) (model, prompt string) {
	var req struct {
		Model   string `json:"model"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return "doubao-unknown", ""
	}
	model = req.Model
	if model == "" {
		model = "doubao-unknown"
	}
	for _, item := range req.Content {
		if item.Type == "text" && item.Text != "" {
			prompt = item.Text
			break
		}
	}
	return
}

// ─── 查询任务结果 ──────────────────────────────────────────────────────────────

// RelayDoubaoVideoResult 查询任务状态，同步更新 DB 并透传上游响应
func RelayDoubaoVideoResult(c *gin.Context) {
	ctx := c.Request.Context()
	taskID := c.Param("taskId")

	videoTask, err := dbmodel.GetVideoTaskById(taskID)
	if err != nil {
		respondError(c, fmt.Errorf("task not found: %s", taskID), "task_not_found", http.StatusNotFound)
		return
	}

	// 已成功且有缓存 URL，直接返回
	if videoTask.Status == "succeed" && videoTask.StoreUrl != "" {
		c.JSON(http.StatusOK, buildDoubaoCachedResponse(videoTask))
		return
	}

	relayMeta := util.GetRelayMeta(c)
	channel, err := dbmodel.GetChannelById(relayMeta.ChannelId, true)
	if err != nil {
		respondError(c, err, "get_channel_failed", http.StatusInternalServerError)
		return
	}

	adaptor := &doubaomodel.VideoAdaptor{}
	meta := buildDoubaoMeta(channel)

	resp, err := adaptor.DoQuery(ctx, meta, taskID)
	if err != nil {
		respondError(c, err, "upstream_request_failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		respondError(c, err, "read_response_failed", http.StatusInternalServerError)
		return
	}

	if queryResp, parseErr := adaptor.ParseQueryResponse(respBody); parseErr == nil {
		updateDoubaoTaskStatus(ctx, taskID, videoTask, queryResp)
	}

	c.Data(resp.StatusCode, "application/json", respBody)
}

func buildDoubaoCachedResponse(v *dbmodel.Video) map[string]interface{} {
	return map[string]interface{}{
		"id":     v.TaskId,
		"status": "succeeded",
		"content": map[string]interface{}{
			"video_url": v.StoreUrl,
		},
	}
}

// ─── 共享：任务状态更新 ────────────────────────────────────────────────────────

// updateDoubaoTaskStatus 处理状态变更并更新 DB（handler 和 poller 共用）
func updateDoubaoTaskStatus(ctx context.Context, taskID string, videoTask *dbmodel.Video, queryResp *doubaomodel.DoubaoVideoResult) {
	dbStatus := mapDoubaoStatus(queryResp.Status)

	if dbStatus == videoTask.Status {
		return
	}

	updates := map[string]interface{}{
		"status":     dbStatus,
		"updated_at": time.Now().Unix(),
	}
	if queryResp.Status == "succeeded" && queryResp.Content != nil && queryResp.Content.VideoURL != "" {
		updates["store_url"] = queryResp.Content.VideoURL
	}
	if dbStatus == "failed" {
		updates["fail_reason"] = buildDoubaoFailMessage(queryResp)
	}

	if err := dbmodel.DB.Model(&dbmodel.Video{}).
		Where("task_id = ?", taskID).
		Updates(updates).Error; err != nil {
		logger.Error(ctx, fmt.Sprintf("[doubao] update task status failed: task_id=%s, %v", taskID, err))
		return
	}

	logger.Info(ctx, fmt.Sprintf("[doubao] status updated: task_id=%s, %s -> %s", taskID, videoTask.Status, dbStatus))

	// 失败时异步退款补偿
	if dbStatus == "failed" && videoTask.Status == "processing" {
		go compensateDoubaoTask(taskID, videoTask)
	}
}

func buildDoubaoFailMessage(resp *doubaomodel.DoubaoVideoResult) string {
	if resp.Error != nil && resp.Error.Message != "" {
		if resp.Error.Code != "" {
			return fmt.Sprintf("[%s] %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Error.Message
	}
	return "task failed"
}

func compensateDoubaoTask(taskID string, v *dbmodel.Video) {
	ctx := context.Background()
	if v.Quota <= 0 {
		return
	}
	if err := dbmodel.IncreaseUserQuota(v.UserId, v.Quota); err != nil {
		logger.Error(ctx, fmt.Sprintf("[doubao] compensate quota failed: task_id=%s, user_id=%d, quota=%d, err=%v",
			taskID, v.UserId, v.Quota, err))
		return
	}
	logger.Info(ctx, fmt.Sprintf("[doubao] compensated: task_id=%s, user_id=%d, quota=%d", taskID, v.UserId, v.Quota))
}

// ─── 定时轮询器 ───────────────────────────────────────────────────────────────

// StartDoubaoTaskPoller 启动轮询器，定期扫描 processing 状态的 doubao 任务
func StartDoubaoTaskPoller(ctx context.Context) {
	ticker := time.NewTicker(doubaoPollingIntervalMin)
	defer ticker.Stop()

	logger.Info(ctx, fmt.Sprintf("[doubao-poller] started, interval=%v", doubaoPollingIntervalMin))

	pollDoubaoTasks(ctx)

	for {
		select {
		case <-ctx.Done():
			logger.Info(ctx, "[doubao-poller] stopped")
			return
		case <-ticker.C:
			pollDoubaoTasks(ctx)
		}
	}
}

func pollDoubaoTasks(ctx context.Context) {
	var tasks []dbmodel.Video
	if err := dbmodel.DB.Where("provider = ? AND status = ?", doubaoVideoProvider, "processing").
		Find(&tasks).Error; err != nil {
		logger.Error(ctx, fmt.Sprintf("[doubao-poller] query tasks failed: %v", err))
		return
	}

	if len(tasks) == 0 {
		logger.Info(ctx, "[doubao-poller] no processing tasks found")
		return
	}

	logger.Info(ctx, fmt.Sprintf("[doubao-poller] found %d processing tasks", len(tasks)))

	for i := range tasks {
		go pollSingleDoubaoTask(ctx, &tasks[i])
	}
}

func pollSingleDoubaoTask(ctx context.Context, task *dbmodel.Video) {
	channel, err := dbmodel.GetChannelById(task.ChannelId, true)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("[doubao-poller] get channel failed: task_id=%s, channel_id=%d, err=%v",
			task.TaskId, task.ChannelId, err))
		return
	}

	adaptor := &doubaomodel.VideoAdaptor{}
	meta := buildDoubaoMeta(channel)

	resp, err := adaptor.DoQuery(ctx, meta, task.TaskId)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("[doubao-poller] request failed: task_id=%s, err=%v", task.TaskId, err))
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("[doubao-poller] read response failed: task_id=%s, err=%v", task.TaskId, err))
		return
	}

	queryResp, err := adaptor.ParseQueryResponse(respBody)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("[doubao-poller] parse response failed: task_id=%s, err=%v", task.TaskId, err))
		return
	}

	logger.Info(ctx, fmt.Sprintf("[doubao-poller] polled: task_id=%s, status=%s", task.TaskId, queryResp.Status))
	updateDoubaoTaskStatus(ctx, task.TaskId, task, queryResp)
}
