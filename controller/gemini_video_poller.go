package controller

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/gemini"
	"github.com/songquanpeng/one-api/relay/model"
)

const (
	geminiOmniVideoProvider        = "gemini-omni"
	geminiOmniVideoPollingInterval = 5 * time.Minute
	geminiOmniVideoExpireSecs      = 24 * 60 * 60
)

func StartGeminiOmniVideoTaskPoller(ctx context.Context) {
	if !isGeminiOmniVideoPollerEnabled() {
		logger.Info(ctx, "[gemini-omni-poller] disabled by ENABLE_VIDEO_TASK_POLLER env, not starting")
		return
	}

	ticker := time.NewTicker(geminiOmniVideoPollingInterval)
	defer ticker.Stop()

	logger.Info(ctx, fmt.Sprintf("[gemini-omni-poller] started, interval=%v", geminiOmniVideoPollingInterval))

	pollGeminiOmniVideoTasks(ctx)

	for {
		select {
		case <-ctx.Done():
			logger.Info(ctx, "[gemini-omni-poller] stopped")
			return
		case <-ticker.C:
			pollGeminiOmniVideoTasks(ctx)
		}
	}
}

func isGeminiOmniVideoPollerEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("ENABLE_VIDEO_TASK_POLLER")))
	return v == "true" || v == "1"
}

func pollGeminiOmniVideoTasks(ctx context.Context) {
	now := time.Now().Unix()
	expireBefore := now - geminiOmniVideoExpireSecs

	dbmodel.DB.Model(&dbmodel.Video{}).
		Where("provider = ? AND status = ? AND created_at < ?", geminiOmniVideoProvider, "processing", expireBefore).
		Updates(map[string]interface{}{
			"status":      "failed",
			"fail_reason": "任务超时(24小时未完成)",
			"updated_at":  now,
		})

	var tasks []dbmodel.Video
	if err := dbmodel.DB.Where("provider = ? AND status = ? AND created_at >= ?",
		geminiOmniVideoProvider, "processing", expireBefore).
		Order("id ASC").Limit(100).
		Find(&tasks).Error; err != nil {
		logger.Error(ctx, fmt.Sprintf("[gemini-omni-poller] query tasks failed: %v", err))
		return
	}

	if len(tasks) == 0 {
		return
	}

	logger.Info(ctx, fmt.Sprintf("[gemini-omni-poller] found %d processing tasks", len(tasks)))

	for _, task := range tasks {
		go pollSingleGeminiOmniVideoTask(ctx, &task)
	}
}

func pollSingleGeminiOmniVideoTask(ctx context.Context, task *dbmodel.Video) {
	defer func() {
		if r := recover(); r != nil {
			logger.SysError(fmt.Sprintf("[gemini-omni-poller] panic: task_id=%s, err=%v", task.TaskId, r))
		}
	}()

	if task.Status == "succeed" || task.Status == "failed" {
		return
	}

	channel, err := dbmodel.GetChannelById(task.ChannelId, true)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("[gemini-omni-poller] get channel failed: task_id=%s, err=%v", task.TaskId, err))
		return
	}

	apiKey := task.Credentials
	if apiKey == "" {
		apiKey = channel.Key
	}

	baseURL := channel.GetBaseURL()
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	status, videoURL, failReason, rawJSON, fetchErr := gemini.FetchAndStoreVideoResult(baseURL, apiKey, task.TaskId, task.UserId)
	if fetchErr != nil {
		logger.Error(ctx, fmt.Sprintf("[gemini-omni-poller] fetch failed: task_id=%s, err=%v", task.TaskId, fetchErr))
		return
	}

	switch status {
	case "succeed":
		// 按真实 token 用量计费：解析 usage → 通过 CAS 原子转终态并扣费，
		// 与用户主动查询路径共用 applyGeminiOmniSuccess，CAS 保证只扣一次。
		usage, parseErr := gemini.ParseGeminiOmniUsage(rawJSON)
		if parseErr != nil {
			logger.Error(ctx, fmt.Sprintf("[gemini-omni-poller] parse usage failed: task_id=%s, err=%v", task.TaskId, parseErr))
		}
		applyResult := &model.GeneralFinalVideoResponse{
			TaskId:            task.TaskId,
			TaskStatus:        "succeed",
			VideoResult:       videoURL,
			RawResult:         rawJSON,
			InputTokens:       usage.InputTokens,
			OutputTextTokens:  usage.OutputTextTokens,
			OutputVideoTokens: usage.OutputVideoTokens,
		}
		gemini.ApplyGeminiOmniSuccess(ctx, task, applyResult)
		logger.Info(ctx, fmt.Sprintf("[gemini-omni-poller] task completed: task_id=%s", task.TaskId))

	case "failed":
		updates := map[string]interface{}{
			"status":      "failed",
			"fail_reason": failReason,
			"updated_at":  time.Now().Unix(),
		}
		if rawJSON != "" {
			updates["result"] = rawJSON
		}
		dbmodel.DB.Model(&dbmodel.Video{}).
			Where("task_id = ? AND status = ?", task.TaskId, "processing").
			Updates(updates)
		if task.Quota > 0 {
			_ = dbmodel.CompensateVideoTaskQuota(task.UserId, task.Quota)
			_ = dbmodel.CompensateChannelQuota(task.ChannelId, task.Quota)
		}
		logger.Info(ctx, fmt.Sprintf("[gemini-omni-poller] task failed: task_id=%s, reason=%s", task.TaskId, failReason))

	case "processing":
		// 仍在处理中，等待下一轮轮询
	}
}
