package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/flux"
	"github.com/songquanpeng/one-api/relay/util"
)

const (
	fluxReconcileInterval = 2 * time.Minute
	fluxReconcileBatch    = 50
	// 超过 30 分钟仍未终态，直接判定失败，不再查上游
	fluxReconcileExpireSecs = 30 * 60
)

// reconcilerMu 防止两次 tick 并发执行（上次未完成时跳过）
var reconcilerMu sync.Mutex

// StartFluxReconciler 启动后台 Flux/Replicate 任务对账 goroutine
func StartFluxReconciler(ctx context.Context) {
	ticker := time.NewTicker(fluxReconcileInterval)
	defer ticker.Stop()

	logger.SysLog("[flux-reconciler] started, interval=2m")

	// 启动时立即跑一次
	runFluxReconcile(ctx)

	for {
		select {
		case <-ctx.Done():
			logger.SysLog("[flux-reconciler] stopped")
			return
		case <-ticker.C:
			runFluxReconcile(ctx)
		}
	}
}

func runFluxReconcile(ctx context.Context) {
	if !reconcilerMu.TryLock() {
		logger.Infof(ctx, "[flux-reconciler] 上次扫描尚未完成，跳过本轮")
		return
	}
	defer reconcilerMu.Unlock()

	now := time.Now().Unix()
	statuses := []string{flux.TaskStatusProcessing, flux.TaskStatusSubmitted}

	// ① 超过 1 小时仍未终态 → 直接标失败，不再查上游
	expireBefore := now - fluxReconcileExpireSecs
	expired, err := model.ExpireStuckFluxImages(statuses, expireBefore, "任务超时（1小时未完成）")
	if err != nil {
		logger.Errorf(ctx, "[flux-reconciler] 批量过期失败: %v", err)
	} else if expired > 0 {
		logger.Infof(ctx, "[flux-reconciler] 批量过期 %d 条超时任务", expired)
	}

	// ② 30min 以内的记录 → 查上游尝试对账
	olderThan := now
	newerThan := now - fluxReconcileExpireSecs
	images, err := model.GetStuckFluxImages(statuses, olderThan, newerThan, fluxReconcileBatch)
	if err != nil {
		logger.Errorf(ctx, "[flux-reconciler] 查询卡死任务失败: %v", err)
		return
	}
	if len(images) == 0 {
		return
	}

	logger.Infof(ctx, "[flux-reconciler] 发现 %d 条卡死任务，开始对账", len(images))
	for _, img := range images {
		go reconcileFluxImage(ctx, img)
	}
}

func reconcileFluxImage(ctx context.Context, image *model.Image) {
	if image.TaskId == "" {
		return
	}

	channel, err := model.GetChannelById(image.ChannelId, true)
	if err != nil || channel.BaseURL == nil || *channel.BaseURL == "" {
		logger.Errorf(ctx, "[flux-reconciler] 获取渠道失败: image_id=%d, channel_id=%d, err=%v",
			image.Id, image.ChannelId, err)
		return
	}

	baseURL := *channel.BaseURL
	apiKey := channel.Key

	if strings.Contains(baseURL, "replicate.com") {
		reconcileReplicateImage(ctx, image, baseURL, apiKey)
	} else {
		reconcileBFLImage(ctx, image, baseURL, apiKey)
	}
}

// ─── BFL ────────────────────────────────────────────────────────────────────

func reconcileBFLImage(ctx context.Context, image *model.Image, baseURL, apiKey string) {
	queryURL := fmt.Sprintf("%s/v1/get_result?id=%s", baseURL, image.TaskId)
	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
	if err != nil {
		logger.Errorf(ctx, "[flux-reconciler] BFL 创建请求失败: task_id=%s, err=%v", image.TaskId, err)
		return
	}
	req.Header.Set("x-key", apiKey)

	resp, err := util.HTTPClient.Do(req)
	if err != nil {
		logger.Errorf(ctx, "[flux-reconciler] BFL 请求失败: task_id=%s, err=%v", image.TaskId, err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	logger.Infof(ctx, "[flux-reconciler] BFL 查询结果: task_id=%s, status=%d, body=%s",
		image.TaskId, resp.StatusCode, string(body))

	var poll flux.FluxPollingResponse
	if err := json.Unmarshal(body, &poll); err != nil {
		logger.Errorf(ctx, "[flux-reconciler] BFL 解析响应失败: task_id=%s, err=%v", image.TaskId, err)
		return
	}

	switch strings.ToLower(poll.Status) {
	case "ready":
		if poll.Result == nil || poll.Result.Sample == "" {
			logger.Errorf(ctx, "[flux-reconciler] BFL Ready 但 sample 为空: task_id=%s", image.TaskId)
			return
		}
		applyFluxBFLSuccess(ctx, image, poll)
	default:
		// BFL 服务不稳定（task not found / error 均可能是瞬时故障），
		// 对账窗口内只处理成功，非 Ready 状态一律跳过，由 30 分钟超时过期兜底。
		logger.Debugf(ctx, "[flux-reconciler] BFL 非 Ready 状态，跳过本轮: task_id=%s, status=%s", image.TaskId, poll.Status)
	}
}

func applyFluxBFLSuccess(ctx context.Context, image *model.Image, poll flux.FluxPollingResponse) {
	group, err := model.CacheGetUserGroup(image.UserId)
	if err != nil || group == "" {
		group = "Lv1"
	}
	groupRatio := util.GetAsyncBillingGroupRatio(group, image.UserId, image.ChannelId, common.ChannelTypeFlux)
	quota := flux.CalculateQuota(poll.Cost, groupRatio)
	if quota == 0 {
		quota = flux.EstimateQuota(image.Model, groupRatio)
	}

	resultBytes, _ := json.Marshal(poll)
	image.Status = flux.TaskStatusSucceed
	image.StoreUrl = poll.Result.Sample
	image.Quota = quota
	image.Result = string(resultBytes)

	applied, dbErr := image.UpdateIfNotTerminal()
	if dbErr != nil {
		logger.Errorf(ctx, "[flux-reconciler] BFL 更新成功记录失败: task_id=%s, err=%v", image.TaskId, dbErr)
		return
	}
	if !applied {
		logger.Infof(ctx, "[flux-reconciler] BFL 已被其他路径处理，跳过扣费: task_id=%s", image.TaskId)
		return
	}
	if err := model.DecreaseUserQuota(image.UserId, quota); err != nil {
		logger.Errorf(ctx, "[flux-reconciler] BFL 扣费失败: user_id=%d, quota=%d, err=%v",
			image.UserId, quota, err)
	} else {
		logger.Infof(ctx, "[flux-reconciler] BFL 对账成功: task_id=%s, user_id=%d, quota=%d",
			image.TaskId, image.UserId, quota)
	}
}

func applyFluxBFLFailed(ctx context.Context, image *model.Image, poll flux.FluxPollingResponse) {
	image.Status = flux.TaskStatusFailed
	image.FailReason = poll.Error
	if image.FailReason == "" {
		image.FailReason = fmt.Sprintf("BFL status: %s", poll.Status)
	}
	if err := image.Update(); err != nil {
		logger.Errorf(ctx, "[flux-reconciler] BFL 更新失败记录失败: task_id=%s, err=%v", image.TaskId, err)
	} else {
		logger.Infof(ctx, "[flux-reconciler] BFL 任务标记失败: task_id=%s, reason=%s", image.TaskId, image.FailReason)
	}
}

// ─── Replicate ───────────────────────────────────────────────────────────────

func reconcileReplicateImage(ctx context.Context, image *model.Image, baseURL, apiKey string) {
	queryURL := fmt.Sprintf("%s/v1/predictions/%s", baseURL, image.TaskId)
	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
	if err != nil {
		logger.Errorf(ctx, "[flux-reconciler] Replicate 创建请求失败: task_id=%s, err=%v", image.TaskId, err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := util.HTTPClient.Do(req)
	if err != nil {
		logger.Errorf(ctx, "[flux-reconciler] Replicate 请求失败: task_id=%s, err=%v", image.TaskId, err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	logger.Infof(ctx, "[flux-reconciler] Replicate 查询结果: task_id=%s, status=%d, body=%s",
		image.TaskId, resp.StatusCode, string(body))

	var repl flux.ReplicateResponse
	if err := json.Unmarshal(body, &repl); err != nil {
		logger.Errorf(ctx, "[flux-reconciler] Replicate 解析响应失败: task_id=%s, err=%v", image.TaskId, err)
		return
	}

	switch repl.Status {
	case "succeeded":
		imageURL := repl.Output
		if imageURL == "" {
			logger.Errorf(ctx, "[flux-reconciler] Replicate succeeded 但 output 为空: task_id=%s", image.TaskId)
			return
		}
		applyFluxReplicateSuccess(ctx, image, repl, imageURL)
	case "failed", "canceled":
		applyFluxReplicateFailed(ctx, image, repl)
	default:
		logger.Debugf(ctx, "[flux-reconciler] Replicate 未终态，跳过: task_id=%s, status=%s", image.TaskId, repl.Status)
	}
}

func applyFluxReplicateSuccess(ctx context.Context, image *model.Image, repl flux.ReplicateResponse, imageURL string) {
	group, err := model.CacheGetUserGroup(image.UserId)
	if err != nil || group == "" {
		group = "Lv1"
	}
	groupRatio := util.GetAsyncBillingGroupRatio(group, image.UserId, image.ChannelId, common.ChannelTypeFlux)
	quota := flux.CalculateReplicateQuota(image.Model, 1, groupRatio)

	queryResult := map[string]any{
		"id":     repl.ID,
		"status": "Ready",
		"result": map[string]any{"sample": imageURL},
	}
	resultBytes, _ := json.Marshal(queryResult)

	image.Status = flux.TaskStatusSucceed
	image.StoreUrl = imageURL
	image.Quota = quota
	image.Result = string(resultBytes)

	applied, dbErr := image.UpdateIfNotTerminal()
	if dbErr != nil {
		logger.Errorf(ctx, "[flux-reconciler] Replicate 更新成功记录失败: task_id=%s, err=%v", image.TaskId, dbErr)
		return
	}
	if !applied {
		logger.Infof(ctx, "[flux-reconciler] Replicate 已被其他路径处理，跳过扣费: task_id=%s", image.TaskId)
		return
	}
	if err := model.DecreaseUserQuota(image.UserId, quota); err != nil {
		logger.Errorf(ctx, "[flux-reconciler] Replicate 扣费失败: user_id=%d, quota=%d, err=%v",
			image.UserId, quota, err)
	} else {
		logger.Infof(ctx, "[flux-reconciler] Replicate 对账成功: task_id=%s, user_id=%d, quota=%d",
			image.TaskId, image.UserId, quota)
	}
}

func applyFluxReplicateFailed(ctx context.Context, image *model.Image, repl flux.ReplicateResponse) {
	image.Status = flux.TaskStatusFailed
	image.FailReason = fmt.Sprintf("%v", repl.Error)
	if image.FailReason == "<nil>" || image.FailReason == "" {
		image.FailReason = fmt.Sprintf("Replicate 任务 %s", repl.Status)
	}
	if err := image.Update(); err != nil {
		logger.Errorf(ctx, "[flux-reconciler] Replicate 更新失败记录失败: task_id=%s, err=%v", image.TaskId, err)
	} else {
		logger.Infof(ctx, "[flux-reconciler] Replicate 任务标记失败: task_id=%s, reason=%s", image.TaskId, image.FailReason)
	}
}
