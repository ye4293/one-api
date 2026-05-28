package flux

import (
	"context"
	"fmt"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/util"
)

// ChargeOnCreation 在任务创建成功时统一执行扣费 + 记账：
//  1. 余额校验
//  2. PostConsumeTokenQuota：扣 user.quota + token.remain_quota / +token.used_quota
//  3. UpdateUserUsedQuotaAndRequestCount：累加 user.used_quota + request_count
//  4. UpdateChannelUsedQuota：累加 channel.used_quota
//  5. RecordConsumeLogWithRequestID：写消费日志
//
// 设计取舍：扣费一次完成，不做失败退款（与音频/视频同步路径策略一致）。
// 调用前需保证 image.UserId / ChannelId / Model / RequestId 已就绪。
func ChargeOnCreation(ctx context.Context, image *model.Image, meta *util.RelayMeta, quota int64) error {
	if quota <= 0 {
		return fmt.Errorf("invalid quota: %d", quota)
	}

	// ① 余额预检（PostConsumeTokenQuota 内部直接 SQL 减法不做 enough 校验）
	balance, err := model.CacheGetUserQuota(ctx, image.UserId)
	if err != nil {
		return fmt.Errorf("查询用户余额失败: %w", err)
	}
	if balance < quota {
		return fmt.Errorf("用户余额不足: 需要=%d, 当前=%d", quota, balance)
	}

	// ② 一次性扣 user.quota + token.remain_quota / +token.used_quota
	if err := model.PostConsumeTokenQuota(meta.TokenId, quota); err != nil {
		return fmt.Errorf("扣费失败: %w", err)
	}

	// ③ user.used_quota + request_count（无返回错误）
	model.UpdateUserUsedQuotaAndRequestCount(image.UserId, quota)
	// ④ channel.used_quota（无返回错误）
	model.UpdateChannelUsedQuota(image.ChannelId, quota)
	// ⑤ 消费日志
	logContent := fmt.Sprintf("Flux 任务创建成功，扣费 quota=%d", quota)
	model.RecordConsumeLogWithRequestID(
		ctx,
		image.UserId,
		image.ChannelId,
		0, 0,
		image.Model,
		meta.TokenName,
		quota,
		logContent,
		float64(image.TotalDuration),
		"", // title
		"", // referer
		false,
		0.0,
		image.RequestId,
	)

	image.Quota = quota
	logger.Infof(ctx, "[flux-billing] 扣费完成 user_id=%d token_id=%d channel_id=%d quota=%d model=%s",
		image.UserId, meta.TokenId, image.ChannelId, quota, image.Model)
	return nil
}

// CalculateQuota 根据 Flux API 返回的 cost 计算配额
// cost: Flux API 返回的费用，单位为美分（cents）
// groupRatio: 用户组的计费倍率
// 返回: 配额（quota）
func CalculateQuota(cost float64, groupRatio float64) int64 {
	// cost 单位是美分，需要转换为美元
	// 实际费用（美元）= cost / 100
	actualCostUSD := cost / 100.0

	// 配额计算公式: 实际费用 * 500000 * 分组倍率
	// 但这里我们需要用 500000，因为 $1 = 500 quota，所以 $0.002 = 1 quota
	// 因此 $1 = 500 quota，所以计算时要用 500
	quota := actualCostUSD * 500000 * groupRatio

	return int64(quota)
}

// EstimateQuota 预估配额（在请求前用于余额检查）
// modelName: 模型名称
// groupRatio: 用户组的计费倍率
// 返回: 预估的配额
func EstimateQuota(modelName string, groupRatio float64) int64 {
	// 从 ModelRatio 获取模型的预估价格（单位已经是 quota）
	ratio, exists := common.ModelRatio[modelName]
	if !exists {
		// 如果模型不存在，使用默认价格（flux-pro 的价格）
		ratio = 0.04
	}

	// ratio 已经是按照 $0.002 = 1 quota 计算的
	// 所以直接乘以倍率即可
	return int64(ratio * groupRatio * 1000) // 乘以1000作为预估buffer
}

// ComputeCostUSD 按模型和实际 MP 数计算 USD 费用（无 groupRatio）。
// outputMP==0 时降级到 FluxPriceMap 固定价兜底。
func ComputeCostUSD(modelName string, metrics ReplicateMetrics) float64 {
	if tier, ok := FluxMPPricingMap[modelName]; ok && metrics.ImageOutputMegapixelCount > 0 {
		outputMP := metrics.ImageOutputMegapixelCount
		inputMP := metrics.ImageInputMegapixelCount // 0 表示无参考图或字段缺失
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

// CalculateReplicateQuota 计算 Replicate 配额：
// - flux-2-* 系列：用 metrics 中的实际 MP 数按分级价格计算；
//   outputMP==0（未知）时降级到 FluxPriceMap 固定价兜底，保证不异常。
// - 其他模型：始终用 FluxPriceMap 固定价。
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
