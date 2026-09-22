package flux

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/model"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
)

// fillReplicateVideoBilling 优先使用代理返回的费用；标准 Replicate 按实际输出时长和配置价格结算。
// predict_time 是计算耗时，不能当作视频时长。缺失实际时长时保留原预扣。
func fillReplicateVideoBilling(result *relaymodel.GeneralFinalVideoResponse, task *model.Video, prediction ReplicateResponse) {
	result.UpstreamCost = prediction.Cost
	seconds := prediction.Metrics.VideoOutputDurationSeconds
	if seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return
	}
	result.Duration = strconv.FormatFloat(seconds, 'f', -1, 64)
	if prediction.Cost > 0 {
		return
	}
	resolution := normalizeBillingResolution(task.Resolution)
	// 已知输出档位优先，未知或缺失指标不能把原请求的 FHD 降成 HD。
	if strings.TrimSpace(prediction.Metrics.ResolutionTarget) != "" {
		switch target := normalizeBillingResolution(prediction.Metrics.ResolutionTarget); target {
		case "hd", "fhd", "qhd", "uhd":
			resolution = target
		}
	}
	quota := common.CalculateVideoQuotaForActualDuration(task.Model, task.Type, task.Mode, task.Duration, resolution, task.Sound, seconds)
	result.FluxActualQuota = &quota
}

// ApplyVideoSuccess 是客户端轮询和后台对账共用的成功结算入口。
// 两段 CAS，互斥且各自只在赢得终态转换的那一次执行结算：
//  1. processing→succeed：正常路径，按预扣做差额结算；
//  2. failed→succeed：成功结果复活失败任务。此前失败已退款，故先撤销退款回到预扣基线，
//     再复用同一差额结算，使账务与「从未失败的成功路径」逐项对齐。
//
// 幂等由 RowsAffected==1 门控：同一终态转换只发生一次；竞争落败时读 DB 终态返回 applied=false。
func ApplyVideoSuccess(ctx context.Context, task *model.Video, result *relaymodel.GeneralFinalVideoResponse) (bool, error) {
	if result.TaskStatus != "succeed" {
		return false, fmt.Errorf("只能结算成功的视频任务")
	}
	quota, source := task.Quota, "保持预扣"
	if result.UpstreamCost > 0 {
		quota, source = VideoQuotaFromUpstreamCost(result.UpstreamCost), "上游 cost"
	} else if result.FluxActualQuota != nil {
		quota, source = *result.FluxActualQuota, "实际视频时长 × 配置价格"
	}
	updates := map[string]interface{}{
		"status": "succeed", "quota": quota,
		"updated_at": time.Now().Unix(), "total_duration": time.Now().Unix() - task.CreatedAt,
	}
	if result.VideoResult != "" {
		updates["store_url"] = result.VideoResult
	}
	if result.RawResult != "" {
		updates["result"] = result.RawResult
	}
	if result.Duration != "" {
		updates["duration"] = result.Duration
	}

	// 第一段：正常 processing→succeed（task.Quota 内存仍为预扣旧值，供差额结算）
	res := model.DB.Model(&model.Video{}).Where("task_id = ? AND status = ?", task.TaskId, "processing").Updates(updates)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 1 {
		settleVideoQuotaDiff(ctx, task, quota, source)
		return reloadVideoTask(task, true)
	}

	// 第二段：failed→succeed 复活。撤销失败退款回到预扣基线，再走同一差额结算。
	res = model.DB.Model(&model.Video{}).Where("task_id = ? AND status = ?", task.TaskId, "failed").Updates(updates)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 1 {
		if task.Quota > 0 {
			if err := model.ChargeVideoTaskQuota(task.UserId, task.Quota); err != nil {
				return true, fmt.Errorf("复活撤销用户退款失败: %w", err)
			}
			model.UpdateChannelUsedQuota(task.ChannelId, task.Quota)
		}
		settleVideoQuotaDiff(ctx, task, quota, source+"（失败后复活）")
		return reloadVideoTask(task, true)
	}

	// 两段都未命中：已被其他路径推进到终态，读 DB 终态返回，applied=false。
	return reloadVideoTask(task, false)
}

// reloadVideoTask 用 DB 最新终态回填内存 task，供调用方返回权威结果。
func reloadVideoTask(task *model.Video, applied bool) (bool, error) {
	stored, err := model.GetVideoTaskById(task.TaskId)
	if err != nil {
		return applied, err
	}
	*task = *stored
	return applied, nil
}

// ApplyVideoFailure 由后台轮询和回调共用，只在赢得终态转换时退还用户及渠道配额。
func ApplyVideoFailure(task *model.Video, reason, rawResult string) (bool, error) {
	updates := map[string]any{
		"status": "failed", "fail_reason": reason,
		"total_duration": time.Now().Unix() - task.CreatedAt, "updated_at": time.Now().Unix(),
	}
	if rawResult != "" {
		updates["result"] = rawResult
	}
	res := model.DB.Model(&model.Video{}).
		Where("task_id = ? AND status = ?", task.TaskId, "processing").Updates(updates)
	if res.Error != nil || res.RowsAffected == 0 {
		return false, res.Error
	}
	if task.Quota > 0 {
		userErr := model.CompensateVideoTaskQuota(task.UserId, task.Quota)
		channelErr := model.CompensateChannelQuota(task.ChannelId, task.Quota)
		if userErr != nil {
			return true, fmt.Errorf("退还用户配额失败: %w", userErr)
		}
		if channelErr != nil {
			return true, fmt.Errorf("退还渠道配额失败: %w", channelErr)
		}
	}
	return true, nil
}

// ApplyVideoProgress 只更新未完成任务，避免迟到的轮询或进度回调覆盖终态结果。
// rawResult 为空时（如 404 宽限期内返回的 processing）只推进 updated_at，不把已有的
// processing 原始结果覆盖成空串。
func ApplyVideoProgress(task *model.Video, rawResult string) error {
	updates := map[string]any{"updated_at": time.Now().Unix()}
	if rawResult != "" {
		updates["result"] = rawResult
	}
	return model.DB.Model(&model.Video{}).
		Where("task_id = ? AND status = ?", task.TaskId, "processing").
		Updates(updates).Error
}
