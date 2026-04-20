package common

import "encoding/json"

// ─── 数据结构 ─────────────────────────────────────────────────────────────────

// ChannelAffinityKeySource 定义从请求中提取亲和 key 的方式
type ChannelAffinityKeySource struct {
	Type string // "context_int" | "context_string" | "gjson"
	Key  string // for context_int / context_string
	Path string // for gjson: JSON path in request body
}

// ChannelAffinityRule 一条亲和规则
type ChannelAffinityRule struct {
	Name             string
	ModelRegex       []string // 模型名正则，任一匹配即生效
	PathRegex        []string // 请求路径正则，为空则不限制
	UserAgentInclude []string // UA 包含检测，为空则不限制

	KeySources []ChannelAffinityKeySource // 按序尝试，取第一个非空值

	ValueRegex string // 提取到的 key 值必须匹配此正则（为空不限制）
	TTLSeconds int    // 缓存过期时间；0 表示使用全局默认

	SkipRetryOnFailure bool // true: 亲和渠道失败后不重试其他渠道

	IncludeRuleName   bool // key 包含规则名
	IncludeModelName  bool // key 包含模型名
	IncludeUsingGroup bool // key 包含用户分组
}

// ChannelAffinitySetting 全局亲和配置
type ChannelAffinitySetting struct {
	Enabled                 bool
	MaxSize                 int // 内存最大条目数，0 表示后端默认 100000
	DefaultTTLSeconds       int
	SwitchAffinityOnSuccess bool // 亲和渠道失败重试到其他渠道成功后，是否更新亲和
	Rules                   []ChannelAffinityRule
}

// ChannelAffinityConfig 运行时全局状态，由 model/option.go 从数据库加载，通过前端 UI 管理。
var ChannelAffinityConfig = ChannelAffinitySetting{
	Enabled:                 false,
	MaxSize:                 100000,
	DefaultTTLSeconds:       3600,
	SwitchAffinityOnSuccess: false,
	Rules:                   []ChannelAffinityRule{},
}

// AffinityConfigToJSON 序列化为 JSON 字符串，失败返回空串
func AffinityConfigToJSON(cfg ChannelAffinitySetting) string {
	b, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	return string(b)
}

// AffinityConfigFromJSON 反序列化，失败返回默认值
func AffinityConfigFromJSON(s string) (ChannelAffinitySetting, error) {
	var cfg ChannelAffinitySetting
	if err := json.Unmarshal([]byte(s), &cfg); err != nil {
		return ChannelAffinityConfig, err
	}
	return cfg, nil
}
