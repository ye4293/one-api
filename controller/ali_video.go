package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	alimodel "github.com/songquanpeng/one-api/relay/channel/ali"
	"github.com/songquanpeng/one-api/relay/util"
)

const (
	aliWanProvider      = "ali-wan"
	aliWanPollingInterval = 10 * time.Minute
)

// ─── 状态映射 ──────────────────────────────────────────────────────────────────

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

// ─── 计费信息解析 ───────────────────────────────────────────────────────────────

type aliWanBillingInfo struct {
	Model      string
	VideoType  string // "text-to-video" or "image-to-video"
	Duration   string
	Resolution string // 480P / 720P / 1080P
	Prompt     string
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

	switch v := params["duration"].(type) {
	case float64:
		info.Duration = strconv.Itoa(int(v))
	case string:
		if v != "" {
			info.Duration = v
		}
	}

	if res, ok := params["resolution"].(string); ok && res != "" {
		info.Resolution = strings.ToUpper(res)
	} else if size, ok := params["size"].(string); ok && size != "" {
		info.Resolution = inferResolutionFromSize(size)
	}

	return info
}

func inferResolutionFromSize(size string) string {
	parts := strings.Split(size, "*")
	if len(parts) != 2 {
		return "1080P"
	}
	w, _ := strconv.Atoi(parts[0])
	h, _ := strconv.Atoi(parts[1])
	pixels := w * h
	switch {
	case pixels <= 520000: // 480P: 832*480=399360
		return "480P"
	case pixels <= 1050000: // 720P: 1280*720=921600
		return "720P"
	default: // 1080P: 1920*1080=2073600
		return "1080P"
	}
}

// ─── 请求上下文 ─────────────────────────────────────────────────────────────────

// aliWanRequest 封装解析后的完整请求上下文
type aliWanRequest struct {
	Billing aliWanBillingInfo
	Quota   int64
	Meta    *util.RelayMeta
	Body    []byte
}

// parseAliWanRequest 统一解析并校验请求，含配额检查和渠道加载
func parseAliWanRequest(c *gin.Context) (*aliWanRequest, error) {
	meta := util.GetRelayMeta(c)

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, fmt.Errorf("read_body_failed: %w", err)
	}

	billing := parseAliWanBillingInfo(body, meta.ActualModelName)
	quota := common.CalculateVideoQuota(billing.Model, billing.VideoType, "", billing.Duration, billing.Resolution, "")

	userQuota, err := dbmodel.CacheGetUserQuota(c.Request.Context(), meta.UserId)
	if err != nil {
		return nil, fmt.Errorf("get_quota_failed: %w", err)
	}
	if userQuota < quota {
		return nil, fmt.Errorf("insufficient_quota")
	}

	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return nil, fmt.Errorf("get_channel_failed: %w", err)
	}

	meta.APIKey = channel.Key
	if channel.BaseURL != nil && *channel.BaseURL != "" {
		meta.BaseURL = *channel.BaseURL
	}

	return &aliWanRequest{
		Billing: billing,
		Quota:   quota,
		Meta:    meta,
		Body:    body,
	}, nil
}

// classifyAliWanError 将 parseAliWanRequest 的错误映射为 HTTP 错误类型和状态码
func classifyAliWanError(err error) (errType string, statusCode int) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "insufficient_quota"):
		return "insufficient_quota", http.StatusPaymentRequired
	case strings.Contains(msg, "get_quota"), strings.Contains(msg, "get_channel"):
		return "server_error", http.StatusInternalServerError
	default:
		return "invalid_request", http.StatusBadRequest
	}
}

// buildAliWanMeta 从渠道信息构造最小 RelayMeta（供 poller 使用）
func buildAliWanMeta(channel *dbmodel.Channel) *util.RelayMeta {
	meta := &util.RelayMeta{
		APIKey:    channel.Key,
		ChannelId: channel.Id,
	}
	if channel.BaseURL != nil && *channel.BaseURL != "" {
		meta.BaseURL = *channel.BaseURL
	}
	return meta
}

// ─── 创建任务 ──────────────────────────────────────────────────────────────────

// RelayAliVideoCreate 处理视频创建请求，支持 T2V 和 I2V
func RelayAliVideoCreate(c *gin.Context) {
	ctx := c.Request.Context()

	req, err := parseAliWanRequest(c)
	if err != nil {
		errType, code := classifyAliWanError(err)
		respondError(c, err, errType, code)
		return
	}

	adaptor := &alimodel.VideoAdaptor{}
	resp, err := adaptor.DoCreate(ctx, req.Meta, req.Body)
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

	aliResp, err := adaptor.ParseCreateResponse(respBody)
	if err != nil {
		// 无法解析时直接透传原始响应
		c.Data(resp.StatusCode, "application/json", respBody)
		return
	}

	// 上游业务错误：不扣费，直接透传
	if aliResp.Code != "" {
		logger.Error(ctx, fmt.Sprintf("[ali-wan] upstream error: code=%s, msg=%s", aliResp.Code, aliResp.Message))
		c.Data(resp.StatusCode, "application/json", respBody)
		return
	}

	taskID := ""
	if aliResp.Output != nil {
		taskID = aliResp.Output.TaskID
	}

	// 预扣费
	if err := dbmodel.PostConsumeTokenQuota(req.Meta.TokenId, req.Quota); err != nil {
		logger.Error(ctx, fmt.Sprintf("[ali-wan] pre-deduct quota failed: %v", err))
	}
	_ = dbmodel.CacheUpdateUserQuota(ctx, req.Meta.UserId)

	// 写 DB 记录
	video := &dbmodel.Video{
		TaskId:     taskID,
		Provider:   aliWanProvider,
		Model:      req.Billing.Model,
		Type:       req.Billing.VideoType,
		Duration:   req.Billing.Duration,
		Resolution: req.Billing.Resolution,
		Prompt:     req.Billing.Prompt,
		Status:     "processing",
		Quota:      req.Quota,
		UserId:     req.Meta.UserId,
		Username:   dbmodel.GetUsernameById(req.Meta.UserId),
		ChannelId:  req.Meta.ChannelId,
		CreatedAt:  time.Now().Unix(),
	}
	if err := video.Insert(); err != nil {
		logger.Error(ctx, fmt.Sprintf("[ali-wan] insert video record failed: task_id=%s, %v", taskID, err))
	}

	logger.Info(ctx, fmt.Sprintf("[ali-wan] task created: task_id=%s, model=%s, type=%s, user_id=%d, channel_id=%d, quota=%d",
		taskID, req.Billing.Model, req.Billing.VideoType, req.Meta.UserId, req.Meta.ChannelId, req.Quota))

	c.Data(resp.StatusCode, "application/json", respBody)
}

// ─── 查询任务结果 ───────────────────────────────────────────────────────────────

// RelayAliVideoResult 查询任务状态，同步更新 DB 并透传上游响应
func RelayAliVideoResult(c *gin.Context) {
	ctx := c.Request.Context()
	taskID := c.Param("taskId")

	videoTask, err := dbmodel.GetVideoTaskById(taskID)
	if err != nil {
		respondError(c, fmt.Errorf("task not found: %s", taskID), "task_not_found", http.StatusNotFound)
		return
	}

	// 已成功且有缓存 URL，直接返回，避免查已过期链接
	if videoTask.Status == "succeed" && videoTask.StoreUrl != "" {
		c.JSON(http.StatusOK, buildAliWanCachedResponse(videoTask))
		return
	}

	// 参照 Kling 的 GET 渠道选择：先从 Distribute 获取初始渠道，再用任务绑定渠道覆盖
	relayMeta := util.GetRelayMeta(c)
	channel, err := dbmodel.GetChannelById(relayMeta.ChannelId, true)
	if err != nil {
		respondError(c, err, "get_channel_failed", http.StatusInternalServerError)
		return
	}
	if boundChannel := resolveChannelForTaskQuery(c.Request.URL.Path, relayMeta.UserId); boundChannel != nil {
		if boundChannel.Id != channel.Id {
			logger.Info(c, fmt.Sprintf("[ali-wan] channel override: path=%s, original_channel=%d, bound_channel=%d",
				c.Request.URL.Path, channel.Id, boundChannel.Id))
			channel = boundChannel
		}
	}

	adaptor := &alimodel.VideoAdaptor{}
	meta := buildAliWanMeta(channel)

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

	if queryResp, parseErr := adaptor.ParseQueryResponse(respBody); parseErr == nil && queryResp.Output != nil {
		updateAliWanTaskStatus(ctx, taskID, videoTask, queryResp)
	}

	c.Data(resp.StatusCode, "application/json", respBody)
}

// ─── 共享：任务状态更新 ─────────────────────────────────────────────────────────

// updateAliWanTaskStatus 处理任务状态变更并更新 DB（HTTP handler 和 poller 共用）
func updateAliWanTaskStatus(ctx context.Context, taskID string, videoTask *dbmodel.Video, queryResp *alimodel.AliVideoQueryResponse) {
	dashStatus := queryResp.Output.TaskStatus
	dbStatus := mapAliWanStatus(dashStatus)

	if dbStatus == videoTask.Status {
		return
	}

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
		logger.Error(ctx, fmt.Sprintf("[ali-wan] update task status failed: task_id=%s, %v", taskID, err))
		return
	}

	logger.Info(ctx, fmt.Sprintf("[ali-wan] status updated: task_id=%s, %s -> %s", taskID, videoTask.Status, dbStatus))

	// 失败时异步退款补偿
	if dbStatus == "failed" && videoTask.Status == "processing" {
		go compensateAliWanTask(taskID, videoTask)
	}
}

// ─── 辅助函数 ──────────────────────────────────────────────────────────────────

func buildAliWanFailMessage(resp *alimodel.AliVideoQueryResponse) string {
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

func isAliWanTaskPollerEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("ENABLE_VIDEO_TASK_POLLER")))
	return v == "true" || v == "1"
}

// StartAliWanTaskPoller 启动轮询器，定期扫描 processing 状态的任务
func StartAliWanTaskPoller(ctx context.Context) {
	if !isAliWanTaskPollerEnabled() {
		logger.SysLog("[ali-wan-poller] disabled by ENABLE_ALI_WAN_POLLER env, not starting")
		return
	}
	ticker := time.NewTicker(aliWanPollingInterval)
	defer ticker.Stop()

	logger.Info(ctx, fmt.Sprintf("[ali-wan-poller] started, interval=%v", aliWanPollingInterval))

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

func pollSingleAliWanTask(ctx context.Context, task *dbmodel.Video) {
	channel, err := dbmodel.GetChannelById(task.ChannelId, true)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("[ali-wan-poller] get channel failed: task_id=%s, channel_id=%d, err=%v",
			task.TaskId, task.ChannelId, err))
		return
	}

	adaptor := &alimodel.VideoAdaptor{}
	meta := buildAliWanMeta(channel)

	resp, err := adaptor.DoQuery(ctx, meta, task.TaskId)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("[ali-wan-poller] request failed: task_id=%s, err=%v", task.TaskId, err))
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("[ali-wan-poller] read response failed: task_id=%s, err=%v", task.TaskId, err))
		return
	}

	queryResp, err := adaptor.ParseQueryResponse(respBody)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("[ali-wan-poller] parse response failed: task_id=%s, err=%v", task.TaskId, err))
		return
	}

	if queryResp.Output == nil {
		logger.Error(ctx, fmt.Sprintf("[ali-wan-poller] empty output: task_id=%s", task.TaskId))
		return
	}

	logger.Info(ctx, fmt.Sprintf("[ali-wan-poller] polled: task_id=%s, status=%s", task.TaskId, queryResp.Output.TaskStatus))
	updateAliWanTaskStatus(ctx, task.TaskId, task, queryResp)
}
