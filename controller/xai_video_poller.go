package controller

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/xai"
)

const (
	xaiVideoProvider        = "xai"
	xaiVideoPollingInterval = 300 * time.Second
)

func StartXaiVideoTaskPoller(ctx context.Context) {
	if !isXaiVideoPollerEnabled() {
		logger.SysLog("[xai-video-poller] disabled by ENABLE_XAI_VIDEO_POLLER env, not starting")
		return
	}

	ticker := time.NewTicker(xaiVideoPollingInterval)
	defer ticker.Stop()

	logger.SysLog(fmt.Sprintf("[xai-video-poller] started, interval=%v", xaiVideoPollingInterval))

	pollXaiVideoTasks(ctx)

	for {
		select {
		case <-ctx.Done():
			logger.SysLog("[xai-video-poller] stopped")
			return
		case <-ticker.C:
			pollXaiVideoTasks(ctx)
		}
	}
}

func isXaiVideoPollerEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("ENABLE_XAI_VIDEO_POLLER")))
	return v == "true" || v == "1"
}

func pollXaiVideoTasks(ctx context.Context) {
	var tasks []dbmodel.Video
	if err := dbmodel.DB.Where("provider = ? AND status IN ?", xaiVideoProvider, []string{"processing", "pending"}).
		Order("id ASC").Limit(20).
		Find(&tasks).Error; err != nil {
		logger.Error(ctx, fmt.Sprintf("[xai-video-poller] query tasks failed: %v", err))
		return
	}

	if len(tasks) == 0 {
		return
	}

	logger.Info(ctx, fmt.Sprintf("[xai-video-poller] found %d processing tasks", len(tasks)))

	for _, task := range tasks {
		go pollSingleXaiVideoTask(ctx, &task)
	}
}

func pollSingleXaiVideoTask(ctx context.Context, task *dbmodel.Video) {
	defer func() {
		if r := recover(); r != nil {
			logger.SysError(fmt.Sprintf("[xai-video-poller] panic: task_id=%s, err=%v", task.TaskId, r))
		}
	}()

	if task.Result != "" && (task.Status == "succeed" || task.Status == "failed" || task.Status == "expired") {
		return
	}

	channel, err := dbmodel.GetChannelById(task.ChannelId, true)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("[xai-video-poller] get channel failed: task_id=%s, channel_id=%d, err=%v",
			task.TaskId, task.ChannelId, err))
		return
	}

	apiKey := xai.ResolveAPIKey(task, channel)
	baseURL := xai.ResolveBaseURL(channel)

	resp, err := xai.FetchNativeVideoResult(baseURL, apiKey, task.TaskId)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("[xai-video-poller] request failed: task_id=%s, err=%v", task.TaskId, err))
		return
	}

	if resp.StatusCode == 200 || resp.StatusCode == 202 {
		xai.UpdateNativeVideoTaskStatus(task.TaskId, resp.Body, task)
		logger.Info(ctx, fmt.Sprintf("[xai-video-poller] updated: task_id=%s, status=%s", task.TaskId, task.Status))
	} else {
		logger.Error(ctx, fmt.Sprintf("[xai-video-poller] upstream error: task_id=%s, status_code=%d", task.TaskId, resp.StatusCode))
	}
}
