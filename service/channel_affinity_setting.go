package service

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
	Enabled           bool
	SwitchOnSuccess   bool // true: 重试成功后更新缓存到实际成功渠道
	DefaultTTLSeconds int
	Rules             []ChannelAffinityRule
}

var defaultChannelAffinitySetting = ChannelAffinitySetting{
	Enabled:           true,
	SwitchOnSuccess:   true,
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
		{
			Name:       "gemini-chat",
			ModelRegex: []string{`^gemini-`},
			PathRegex:  []string{},
			KeySources: []ChannelAffinityKeySource{
				{Type: "context_string", Key: "id"},
				{Type: "context_int", Key: "id"},
			},
			TTLSeconds:         1800,
			SkipRetryOnFailure: false,
			IncludeRuleName:    true,
			IncludeModelName:   true,
			IncludeUsingGroup:  true,
		},
	},
}

var channelAffinitySetting = defaultChannelAffinitySetting

// GetChannelAffinitySetting 返回当前亲和配置
func GetChannelAffinitySetting() *ChannelAffinitySetting {
	return &channelAffinitySetting
}
