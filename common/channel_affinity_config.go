package common

import (
	"encoding/json"
	"sync"
)

// ─── 数据结构 ─────────────────────────────────────────────────────────────────

// ChannelAffinityKeySource 定义从请求中提取亲和 key 的方式
type ChannelAffinityKeySource struct {
	Type string `json:"type"` // "context_int" | "context_string" | "gjson"
	Key  string `json:"key"`  // for context_int / context_string
	Path string `json:"path"` // for gjson: JSON path in request body
}

// ChannelAffinityRule 一条亲和规则
type ChannelAffinityRule struct {
	Name             string   `json:"name"`
	ModelRegex       []string `json:"model_regex"`        // 模型名正则，任一匹配即生效
	PathRegex        []string `json:"path_regex"`         // 请求路径正则，为空则不限制
	UserAgentInclude []string `json:"user_agent_include"` // UA 包含检测，为空则不限制

	KeySources []ChannelAffinityKeySource `json:"key_sources"` // 按序尝试，取第一个非空值

	ValueRegex string `json:"value_regex"` // 提取到的 key 值必须匹配此正则（为空不限制）
	TTLSeconds int    `json:"ttl_seconds"` // 缓存过期时间；0 表示使用全局默认

	SkipRetryOnFailure bool `json:"skip_retry_on_failure"` // true: 亲和渠道失败后不重试其他渠道

	IncludeRuleName   bool `json:"include_rule_name"`   // key 包含规则名
	IncludeModelName  bool `json:"include_model_name"`  // key 包含模型名
	IncludeUsingGroup bool `json:"include_using_group"` // key 包含用户分组
}

// ChannelAffinitySetting 全局亲和配置
type ChannelAffinitySetting struct {
	Enabled           bool                  `json:"enabled"`
	DefaultTTLSeconds int                   `json:"default_ttl_seconds"`
	Rules             []ChannelAffinityRule `json:"rules"`
}

// ─── 默认配置 ─────────────────────────────────────────────────────────────────

var defaultChannelAffinitySetting = ChannelAffinitySetting{
	Enabled:           true,
	DefaultTTLSeconds: 3600,
	Rules: []ChannelAffinityRule{
		{
			Name:       "claude-cli",
			ModelRegex: []string{`^claude-`},
			PathRegex:  []string{`/v1/messages`},
			KeySources: []ChannelAffinityKeySource{
				{Type: "gjson", Path: "metadata.user_id"},
			},
			TTLSeconds:         0,
			SkipRetryOnFailure: true,
			IncludeRuleName:    true,
			IncludeUsingGroup:  true,
		},
		{
			Name:       "openai-responses",
			ModelRegex: []string{`^gpt-`, `^o1`, `^o3`, `^o4`},
			PathRegex:  []string{`/v1/responses`},
			KeySources: []ChannelAffinityKeySource{
				{Type: "gjson", Path: "prompt_cache_key"},
			},
			TTLSeconds:         0,
			SkipRetryOnFailure: true,
			IncludeRuleName:    true,
			IncludeUsingGroup:  true,
		},
	},
}

// ─── 内存状态（读写锁保护） ────────────────────────────────────────────────────

var (
	channelAffinityMu      sync.RWMutex
	channelAffinitySetting = defaultChannelAffinitySetting
)

// GetChannelAffinitySetting 返回当前配置的快照（线程安全）。
func GetChannelAffinitySetting() ChannelAffinitySetting {
	channelAffinityMu.RLock()
	defer channelAffinityMu.RUnlock()
	return channelAffinitySetting
}

// ─── JSON helpers（供 model/option.go 调用） ──────────────────────────────────

// ChannelAffinitySetting2JSONString 将当前配置序列化为 JSON 字符串。
func ChannelAffinitySetting2JSONString() string {
	channelAffinityMu.RLock()
	defer channelAffinityMu.RUnlock()
	b, _ := json.Marshal(channelAffinitySetting)
	return string(b)
}

// UpdateChannelAffinitySettingByJSONString 从 JSON 字符串更新配置（热加载时调用）。
func UpdateChannelAffinitySettingByJSONString(jsonStr string) error {
	var s ChannelAffinitySetting
	if err := json.Unmarshal([]byte(jsonStr), &s); err != nil {
		return err
	}
	channelAffinityMu.Lock()
	defer channelAffinityMu.Unlock()
	channelAffinitySetting = s
	return nil
}
