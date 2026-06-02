package config

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
	// 手动标记为永久忽略的模型（不再自动加入）
	UpstreamModelUpdateIgnoredModels []string `json:"upstream_model_update_ignored_models,omitempty"`
}
