package flux

import (
	"context"
	"fmt"

	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
)

// ChargeOnSuccess 在任务成功时扣费，仅操作用户维度额度，不依赖 TokenId。
// 应在 UpdateIfNotTerminal CAS 成功（applied=true）后调用。
func ChargeOnSuccess(ctx context.Context, image *model.Image, quota int64) error {
	if quota <= 0 {
		return fmt.Errorf("invalid quota: %d", quota)
	}

	if err := model.DecreaseUserQuota(image.UserId, quota); err != nil {
		return fmt.Errorf("扣费失败: %w", err)
	}

	model.UpdateUserUsedQuotaAndRequestCount(image.UserId, quota)
	model.UpdateChannelUsedQuota(image.ChannelId, quota)

	logContent := fmt.Sprintf("Flux 任务成功，扣费 quota=%d", quota)
	model.RecordConsumeLogWithRequestID(
		ctx,
		image.UserId,
		image.ChannelId,
		0, 0,
		image.Model,
		"",
		quota,
		logContent,
		float64(image.TotalDuration),
		"", "",
		false,
		0.0,
		image.RequestId,
	)

	logger.Infof(ctx, "[flux-billing] 成功扣费 user_id=%d channel_id=%d quota=%d model=%s task_id=%s",
		image.UserId, image.ChannelId, quota, image.Model, image.TaskId)
	return nil
}

// CalculateQuota 根据 BFL API 返回的 cost（美分）计算配额。
func CalculateQuota(cost float64, groupRatio float64) int64 {
	return int64(cost / 100.0 * 500000 * groupRatio)
}

// ComputeCostUSD 按模型和实际 MP 数计算 USD 费用（无 groupRatio）。
// outputMP==0 时降级到 FluxPriceMap 固定价兜底。
func ComputeCostUSD(modelName string, metrics ReplicateMetrics) float64 {
	if tier, ok := FluxMPPricingMap[modelName]; ok && metrics.ImageOutputMegapixelCount > 0 {
		outputMP := metrics.ImageOutputMegapixelCount
		inputMP := metrics.ImageInputMegapixelCount
		var costUSD float64
		if outputMP <= 1.0 {
			costUSD += tier.FirstMPPrice
		} else {
			costUSD += tier.FirstMPPrice + (outputMP-1.0)*tier.SubsequentMPPrice
		}
		if inputMP > 0 {
			costUSD += inputMP * tier.RefMPPrice
		}
		return costUSD
	}
	price, ok := FluxPriceMap[modelName]
	if !ok {
		price = 0.05
	}
	return price
}

// CalculateReplicateQuota 计算 Replicate 配额。
func CalculateReplicateQuota(modelName string, metrics ReplicateMetrics, groupRatio float64) int64 {
	return usdToQuota(ComputeCostUSD(modelName, metrics), groupRatio)
}

// usdToQuota 将 USD 金额转为内部 quota（$1 = 500000 quota）
func usdToQuota(usd float64, groupRatio float64) int64 {
	if usd <= 0 {
		return 0
	}
	return int64(usd * 500000 * groupRatio)
}

// VideoQuotaFromUpstreamCost 把上游权威 cost（美分）换算为视频侧 quota。
// 口径必须与提交预扣的 common.CalculateVideoQuota 完全一致：price(USD) × QuotaPerUnit，
// 且视频侧不含 groupRatio，故此处同样不乘 ratio，否则完成结算与预扣口径不对齐。
func VideoQuotaFromUpstreamCost(costCents float64) int64 {
	if costCents <= 0 {
		return 0
	}
	return int64(costCents / 100.0 * config.QuotaPerUnit)
}

// SettleVideoCostDiff 视频任务完成时按上游权威 cost 做「多退少补」差额结算。
//
// 前提与幂等：
//   - 视频是提交时预扣费（videoTask.Quota 为预扣额）；此函数只算并结算「上游实收 - 预扣」的差额。
//   - 必须由「赢得 processing→succeed 的 CAS 转换」的唯一一次调用（RowsAffected==1），
//     否则客户端查询与对账器双路径会重复补/退。调用方负责 CAS 门控。
//   - CAS 已把 DB 的 quota 列改为 newQuota；本函数用传入 videoTask.Quota（内存旧值=预扣额）算差额，
//     调用方切勿在调用前改写内存里的 videoTask.Quota。
//
// 返回实际差额 diff：>0 少补(再扣)，<0 多退，==0 不动。
func SettleVideoCostDiff(ctx context.Context, videoTask *model.Video, upstreamCostCents float64) int64 {
	if upstreamCostCents <= 0 {
		return 0 // 上游未返回 cost（标准 replicate.com / 存量任务）→ 保持提交预扣，不动
	}
	newQuota := VideoQuotaFromUpstreamCost(upstreamCostCents)
	diff := newQuota - videoTask.Quota
	if diff == 0 {
		return 0 // 预扣==上游实收（固定 duration 主路径），无需结算
	}

	// 用户余额 + Token 维度：PostConsumeTokenQuota 正负通吃（正=扣，负=退），一次调用搞定双维度。
	if err := model.PostConsumeTokenQuota(videoTask.TokenId, diff); err != nil {
		logger.Errorf(ctx, "[flux-video-billing] 差额结算调整用户/Token 配额失败: task_id=%s diff=%d err=%v",
			videoTask.TaskId, diff, err)
	}
	// 用户已使用配额差额（不动 request_count：提交时已计一次，差额不重复计数）。
	model.UpdateUserUsedQuota(videoTask.UserId, diff)
	// 渠道已使用配额差额（gorm.Expr("used_quota + ?") 支持负数）。
	model.UpdateChannelUsedQuota(videoTask.ChannelId, diff)

	// 追加一条差额消费日志（quota=±diff），用符号区分少补/多退，便于事后核账。
	sign := "少补"
	if diff < 0 {
		sign = "多退"
	}
	logContent := fmt.Sprintf("上游 cost 结算：%s %+d（上游 cost=%.2f$，预扣 quota=%d → 实收 quota=%d）",
		sign, diff, upstreamCostCents/100.0, videoTask.Quota, newQuota)
	// 把 task id 覆盖进 ctx 的 RequestIdKey，使这条差额日志的 x_request_id = task id，
	// 与提交预扣日志（video.go 同样覆盖）共享检索键，现有日志搜索框按 x_request_id 即可一并搜出。
	logCtx := context.WithValue(ctx, logger.RequestIdKey, videoTask.TaskId)
	model.RecordVideoConsumeLog(logCtx, videoTask.UserId, videoTask.ChannelId, 0, 0,
		videoTask.Model, "", diff, logContent, float64(videoTask.TotalDuration), "", "", videoTask.TaskId)

	logger.Infof(ctx, "[flux-video-billing] 完成结算 task_id=%s user_id=%d channel_id=%d 预扣=%d 上游cost=%.2f$ 实收=%d 差额=%+d",
		videoTask.TaskId, videoTask.UserId, videoTask.ChannelId, videoTask.Quota, upstreamCostCents/100.0, newQuota, diff)
	return diff
}
