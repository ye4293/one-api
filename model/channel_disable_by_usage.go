package model

import (
	"time"

	"github.com/songquanpeng/one-api/common/config"
)

// channelDisableUsageWindowSeconds 「最近使用模型集」的时间窗，单位秒。
//
// 硬编码 1 天：低频模型（1 天内未被调用）会被排除在分母外——业务上已确认可接受
// （高频模型不可用时，低频模型即便看似正常也很难独善其身，误伤成本低于「渠道躺平
// 但系统仍在派单」的现有痛点）。若未来需要按渠道差异化，再抽为配置项。
const channelDisableUsageWindowSeconds int64 = 24 * 3600

// channelDisableStabilizeProbeCycles 抖动窗口倍数：判定禁用模型时，要求其 auto_disabled_time
// 距今至少经过 N 个探针周期。这样保证「统一恢复链路」有足够时间尝试把瞬时抖动导致
// 的禁用模型救回来，避免上游 30 秒抖动就把整个渠道禁掉。
//
// 与探针周期挂钩而非硬编码「10 分钟」，是为了自动适应 AutoTestChannelFrequency 的变更。
const channelDisableStabilizeProbeCycles = 2

// channelDisableDefaultProbeFreqMinutes 探针未配置时的兜底周期（分钟）。
// AutoTestChannelFrequency<=0 时代表未启用自动探针，理论上不会走到本判定；
// 保守起见给一个合理默认值，避免 stabilizeCutoff 退化为 now 造成误禁。
const channelDisableDefaultProbeFreqMinutes = 5

// ShouldDisableChannelByRecentUsage 判定渠道是否应因「最近使用的模型全部被自动禁用」而被禁。
//
// 判定规则：
//
//	used     = 过去 channelDisableUsageWindowSeconds 内 model_metrics 里 total_requests>0 的模型集合
//	disabled = used ∩ (abilities 中 auto_disabled=1 且 auto_disabled_time <= stabilizeCutoff)
//	should   = len(used) > 0 && len(used) == len(disabled)
//
// used=0 时返回 should=false（该渠道最近没有真实流量，禁与不禁对用户无差别，
// 让「统一恢复链路」按现状继续巡检即可）。
//
// 分两次查询而非 JOIN：model_metrics 在 LOG_DB 上（LOG_SQL_DSN 未设置时 LOG_DB==DB，
// 分库部署时则不同库），abilities 恒在 DB 上——跨库 JOIN 无法保证语义。
// 两次查询走各自索引（idx_mm_channel_hour / abilities 主键 + channel_id 索引），
// 单渠道涉及的模型数一般 <100，代价亚毫秒到个位毫秒级。
func ShouldDisableChannelByRecentUsage(channelId int) (should bool, used, disabled int, err error) {
	now := time.Now().Unix()
	windowStart := now - channelDisableUsageWindowSeconds

	freq := config.AutoTestChannelFrequency
	if freq <= 0 {
		freq = channelDisableDefaultProbeFreqMinutes
	}
	stabilizeCutoff := now - int64(channelDisableStabilizeProbeCycles*freq*60)

	var usedModels []string
	if err = LOG_DB.Model(&ModelMetrics{}).
		Where("channel_id = ? AND hour_timestamp >= ? AND total_requests > 0", channelId, windowStart).
		Distinct("model_name").
		Pluck("model_name", &usedModels).Error; err != nil {
		return false, 0, 0, err
	}
	used = len(usedModels)
	if used == 0 {
		return false, 0, 0, nil
	}

	var disabledCount int64
	if err = DB.Model(&Ability{}).
		Where("channel_id = ? AND model IN ? AND auto_disabled = ? AND auto_disabled_time > 0 AND auto_disabled_time <= ?",
			channelId, usedModels, true, stabilizeCutoff).
		Distinct("model").
		Count(&disabledCount).Error; err != nil {
		return false, used, 0, err
	}
	disabled = int(disabledCount)
	should = disabled == used
	return
}
