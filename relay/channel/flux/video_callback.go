package flux

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
)

// handleVideoCallback 与客户端查询、后台轮询共用终态结算规则。
func handleVideoCallback(c *gin.Context, task *dbmodel.Video, notification FluxCallbackNotification, rawBody []byte) (bool, int, string) {
	if task.Status == "succeed" || task.Status == "failed" {
		return true, http.StatusOK, "already processed"
	}
	var err error
	switch {
	case IsUpstreamReady(notification.Status):
		result := &relaymodel.GeneralFinalVideoResponse{
			TaskId: task.TaskId, TaskStatus: "succeed", Duration: task.Duration,
			RawResult: string(rawBody), UpstreamCost: notification.Cost,
		}
		fillFromBFLRaw(result, rawBody)
		if result.VideoResult == "" {
			return false, http.StatusBadRequest, "missing result.sample"
		}
		_, err = ApplyVideoSuccess(c.Request.Context(), task, result)
	case IsUpstreamFailed(notification.Status):
		reason := fmt.Sprintf("flux video %s: %s", notification.Status, string(rawBody))
		_, err = ApplyVideoFailure(task, reason, string(rawBody))
	default:
		// 迟到的进度回调不能覆盖已成功或失败的结果。
		err = ApplyVideoProgress(task, string(rawBody))
	}
	if err != nil {
		logger.Errorf(c, "Flux 视频回调更新失败: task_id=%s, err=%v", task.TaskId, err)
		return false, http.StatusInternalServerError, "update failed"
	}
	return true, http.StatusOK, "success"
}

// handleReplicateVideoCallback 复用轮询转换和视频结算，不进入图片计费链路。
func handleReplicateVideoCallback(c *gin.Context, task *dbmodel.Video, prediction ReplicateResponse, rawBody []byte) (bool, int, string) {
	if task.Status == "succeed" || task.Status == "failed" {
		return true, http.StatusOK, "already processed"
	}
	result := buildReplicateVideoResult(task, prediction, rawBody)
	var err error
	switch result.TaskStatus {
	case "succeed":
		if result.VideoResult == "" {
			return false, http.StatusBadRequest, "missing output"
		}
		_, err = ApplyVideoSuccess(c.Request.Context(), task, result)
	case "failed":
		_, err = ApplyVideoFailure(task, result.Message, result.RawResult)
	default:
		err = ApplyVideoProgress(task, result.RawResult)
	}
	if err != nil {
		logger.Errorf(c, "Replicate 视频回调更新失败: task_id=%s, err=%v", task.TaskId, err)
		return false, http.StatusInternalServerError, "update failed"
	}
	return true, http.StatusOK, "success"
}
