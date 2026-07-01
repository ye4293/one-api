package gemini

import (
	"context"
	"fmt"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/model"
)

// ApplyGeminiOmniSuccess 处理 Gemini Omni 任务成功：按真实 token 用量算 quota，
// 通过 UpdateIfNotTerminal CAS 原子转为终态，仅当赢得竞争（applied=true）时才异步扣费。
// 后台 poller 与用户主动查询两条路径都调用此函数，CAS 保证只扣一次。
// 参考flux 的 applyFluxBFLSuccess + ChargeOnSuccess 模式。
func ApplyGeminiOmniSuccess(ctx context.Context, videoTask *dbmodel.Video, result *model.GeneralFinalVideoResponse) {
	quota := common.CalculateGeminiOmniQuota(result.InputTokens, result.OutputTextTokens, result.OutputVideoTokens)

	// 设置终态字段，交由 CAS 原子落库
	videoTask.Status = "succeed"
	videoTask.Quota = quota
	if result.VideoResult != "" {
		videoTask.StoreUrl = result.VideoResult
	}
	if result.RawResult != "" {
		videoTask.Result = result.RawResult
	}

	applied, err := videoTask.UpdateIfNotTerminal()
	if err != nil {
		logger.Errorf(ctx, "[gemini-omni-billing] CAS 更新失败: task_id=%s, err=%v", videoTask.TaskId, err)
		return
	}
	if !applied {
		// 已被另一条路径转为终态，跳过扣费，避免重复计费
		logger.Infof(ctx, "[gemini-omni-billing] 任务已被其他路径处理，跳过扣费: task_id=%s", videoTask.TaskId)
		return
	}

	// 异步扣费：PostConsumeTokenQuota + 多表统计写入较慢，放 goroutine 避免阻塞 poller / 查询响应。
	// CAS 已保证只此一处启动扣费，不会重复。
	go chargeGeminiOmniOnSuccess(videoTask, quota, result)
}

// chargeGeminiOmniOnSuccess 异步执行扣费与记 log。
// token_id 来自 video 表（创建时落库），用 PostConsumeTokenQuota 记入 Token 维度
// （同时扣用户余额和 token 额度）；无 TokenId 时降级为只扣用户余额。
func chargeGeminiOmniOnSuccess(task *dbmodel.Video, quota int64, result *model.GeneralFinalVideoResponse) {
	ctx := context.Background()
	if quota <= 0 {
		logger.Warnf(ctx, "[gemini-omni-billing] quota=0 跳过扣费: task_id=%s, input=%d, output_text=%d, output_video=%d",
			task.TaskId, result.InputTokens, result.OutputTextTokens, result.OutputVideoTokens)
		return
	}

	// 扣费：有 token_id 走 Token 维度，否则降级扣用户余额
	if task.TokenId > 0 {
		if err := dbmodel.PostConsumeTokenQuota(task.TokenId, quota); err != nil {
			logger.Errorf(ctx, "[gemini-omni-billing] 扣费失败(token): task_id=%s, token_id=%d, err=%v", task.TaskId, task.TokenId, err)
			return
		}
	} else {
		if err := dbmodel.DecreaseUserQuota(task.UserId, quota); err != nil {
			logger.Errorf(ctx, "[gemini-omni-billing] 扣费失败(user): task_id=%s, err=%v", task.TaskId, err)
			return
		}
	}
	_ = dbmodel.CacheUpdateUserQuota(ctx, task.UserId)
	dbmodel.UpdateUserUsedQuotaAndRequestCount(task.UserId, quota)
	dbmodel.UpdateChannelUsedQuota(task.ChannelId, quota)

	// 记消费 log：真实 token 用量与费用
	totalOutput := result.OutputTextTokens + result.OutputVideoTokens
	logContent := fmt.Sprintf("Gemini Omni Video model=%s, input=%d, output_text=%d, output_video=%d, cost=$%.6f",
		task.Model, result.InputTokens, result.OutputTextTokens, result.OutputVideoTokens, float64(quota)/config.QuotaPerUnit)
	dbmodel.RecordVideoConsumeLog(ctx, task.UserId, task.ChannelId,
		int(result.InputTokens), int(totalOutput), task.Model, "", quota, logContent,
		0, "", "", task.TaskId)

	logger.Infof(ctx, "[gemini-omni-billing] 成功扣费 user_id=%d channel_id=%d token_id=%d quota=%d model=%s task_id=%s",
		task.UserId, task.ChannelId, task.TokenId, quota, task.Model, task.TaskId)
}
