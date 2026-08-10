package config

// ModelHealthState 单个模型的健康巡检状态。
// JSON key 刻意用单字母：这个 map 每渠道可能装 30+ 模型，
// 长 key 会让 settings 列（type:text）无谓膨胀 3 倍。
type ModelHealthState struct {
	Fails     int   `json:"f,omitempty"` // 连续失败次数
	Successes int   `json:"s,omitempty"` // 连续成功次数
	LastProbe int64 `json:"t,omitempty"` // 上次探测时间（Unix 秒）
}

// ChannelOtherSettings 渠道扩展设置，序列化为 JSON 存储在 channels.settings 列
type ChannelOtherSettings struct {
	// 是否开启上游模型巡检
	UpstreamModelUpdateCheckEnabled bool `json:"upstream_model_update_check_enabled,omitempty"`
	// 是否自动将检测到的新增模型同步到渠道
	UpstreamModelUpdateAutoSyncEnabled bool `json:"upstream_model_update_auto_sync_enabled,omitempty"`
	// 是否自动删除上游已移除的模型
	UpstreamModelUpdateAutoDeleteEnabled bool `json:"upstream_model_update_auto_delete_enabled,omitempty"`
	// 上次巡检时间（Unix 秒）
	UpstreamModelUpdateLastCheckTime int64 `json:"upstream_model_update_last_check_time,omitempty"`
	// 上次检测到的待加入模型列表
	UpstreamModelUpdateLastDetectedModels []string `json:"upstream_model_update_last_detected_models,omitempty"`
	// 上次检测到的待删除模型列表
	UpstreamModelUpdateLastRemovedModels []string `json:"upstream_model_update_last_removed_models,omitempty"`
	// 手动标记为永久忽略的模型（不再自动加入，也不会被自动删除）
	UpstreamModelUpdateIgnoredModels []string `json:"upstream_model_update_ignored_models,omitempty"`
	// 关闭本渠道的真实请求探针（负极性：默认 false = 跟随全局开关）。
	// 用负极性 + omitempty 是为了让现有渠道的 settings JSON 一个字节都不变，
	// 零迁移风险。
	UpstreamModelProbeDisabled bool `json:"upstream_model_probe_disabled,omitempty"`
	// 健康巡检状态，key 为模型名。omitempty + 全新字段 → 现有渠道 settings JSON 零变化。
	UpstreamModelHealth map[string]ModelHealthState `json:"upstream_model_health,omitempty"`
}
