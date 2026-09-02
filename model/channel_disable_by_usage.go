package model

import (
	"time"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
)

// shouldDisableByRecentUsage 判定渠道是否应因「最近使用的模型全部被自动禁用」而被禁。
//
// 判定规则：
//
//	used     = 过去 config.ChannelUsageWindowMinutes 分钟内 model_metrics 里 total_requests>0 的模型集合
//	           ∪ extraUsed（调用方强制并入的模型，用于兜底 model_metrics 聚合滞后）
//	disabled = used ∩ (abilities 中 auto_disabled=1 且 auto_disabled_time>0)
//	should   = len(used) > 0 && len(used) == len(disabled)
//
// 与旧版差异（方案 C）：
//   - 时间窗从硬编码 24h 改为 config.ChannelUsageWindowMinutes（默认 60 分钟，可运营配置）
//   - 去掉 stabilizeCutoff 抖动窗口：刚被禁（auto_disabled_time≈now）的模型立即计入 disabled，
//     使「模型被禁即刻触发整渠道禁用」成为可能。误禁风险由恢复探针（recoverAutoDisabledModels）
//     逐模型探测救回兜底。
//   - extraUsed：model_metrics 每 5 分钟才从 logs 聚合一次，即刻判定时刚失败的模型可能尚未进
//     used 集合。调用方（monitor 即刻触发）显式并入当前 triggerModel，避免因聚合滞后漏判。
//
// 短路：渠道已非 enabled（已被禁）时直接返回 should=false，避免即刻触发与周期兜底重复禁用/通知。
//
// used=0 时返回 should=false（该渠道最近没有真实流量，禁与不禁对用户无差别）。
//
// 分两次查询而非 JOIN：model_metrics 在 LOG_DB 上（LOG_SQL_DSN 未设置时 LOG_DB==DB，
// 分库部署时则不同库），abilities 恒在 DB 上——跨库 JOIN 无法保证语义。
func shouldDisableByRecentUsage(channelId int, extraUsed []string) (should bool, used, disabled int, err error) {
	// 短路：渠道已非 enabled 无需再判定
	var status int
	if err = DB.Model(&Channel{}).Where("id = ?", channelId).Limit(1).Pluck("status", &status).Error; err != nil {
		return false, 0, 0, err
	}
	if status != common.ChannelStatusEnabled {
		return false, 0, 0, nil
	}

	now := time.Now().Unix()
	windowStart := now - int64(config.ChannelUsageWindowMinutes)*60

	var usedModels []string
	if err = LOG_DB.Model(&ModelMetrics{}).
		Where("channel_id = ? AND hour_timestamp >= ? AND total_requests > 0", channelId, windowStart).
		Distinct("model_name").
		Pluck("model_name", &usedModels).Error; err != nil {
		return false, 0, 0, err
	}

	// 并入 extraUsed（去重）——兜底 model_metrics 聚合滞后
	usedSet := make(map[string]struct{}, len(usedModels)+len(extraUsed))
	for _, m := range usedModels {
		usedSet[m] = struct{}{}
	}
	for _, m := range extraUsed {
		if m != "" {
			usedSet[m] = struct{}{}
		}
	}
	if len(usedSet) == 0 {
		return false, 0, 0, nil
	}
	usedList := make([]string, 0, len(usedSet))
	for m := range usedSet {
		usedList = append(usedList, m)
	}
	used = len(usedList)

	var disabledCount int64
	if err = DB.Model(&Ability{}).
		Where("channel_id = ? AND model IN ? AND auto_disabled = ? AND auto_disabled_time > 0",
			channelId, usedList, true).
		Distinct("model").
		Count(&disabledCount).Error; err != nil {
		return false, used, 0, err
	}
	disabled = int(disabledCount)
	should = disabled == used
	return
}

// ShouldDisableChannelByRecentUsageImmediate 即刻判定入口：由 monitor 在模型级禁用的那一刻同步调用。
// triggerModel 为刚被自动禁用的模型，强制并入 used 集合，兜底 model_metrics 5 分钟聚合滞后。
func ShouldDisableChannelByRecentUsageImmediate(channelId int, triggerModel string) (should bool, used, disabled int, err error) {
	return shouldDisableByRecentUsage(channelId, []string{triggerModel})
}

// ShouldDisableChannelByRecentUsage 周期兜底入口：由恢复探针尾部调用。
// 不并入额外模型，完全依据 model_metrics 已聚合的近窗口真实流量判定。
func ShouldDisableChannelByRecentUsage(channelId int) (should bool, used, disabled int, err error) {
	return shouldDisableByRecentUsage(channelId, nil)
}
