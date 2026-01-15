package flux

import (
	"github.com/songquanpeng/one-api/common"
)

// CalculateQuota 根据 Flux API 返回的 cost 计算配额
// cost: Flux API 返回的费用，单位为美分（cents）
// groupRatio: 用户组的计费倍率
// 返回: 配额（quota）
func CalculateQuota(cost float64, groupRatio float64) int64 {
	// cost 单位是美分，需要转换为美元
	// 实际费用（美元）= cost / 100
	actualCostUSD := cost / 100.0

	// 配额计算公式: 实际费用 * 500000 * 分组倍率
	// common.USD = 500，表示 $1 = 500 quota
	// 但这里我们需要用 500000，因为 $1 = 500 quota，所以 $0.002 = 1 quota
	// 因此 $1 = 500 quota，所以计算时要用 500
	quota := actualCostUSD * float64(common.USD) * groupRatio

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
