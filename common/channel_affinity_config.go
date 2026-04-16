package common

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
	Enabled           bool
	DefaultTTLSeconds int
	Rules             []ChannelAffinityRule
}

// ─── 规则配置（在此处添加/修改支持亲和性的模型） ──────────────────────────────

// ChannelAffinityConfig 是全局亲和配置，修改此处即可控制哪些模型触发亲和。
var ChannelAffinityConfig = ChannelAffinitySetting{
	Enabled:           true,
	DefaultTTLSeconds: 3600,
	Rules: []ChannelAffinityRule{
		{
			// Claude CLI / Claude Code：通过请求体 metadata.user_id 识别会话
			Name:       "claude-cli",
			ModelRegex: []string{`^claude-`},
			PathRegex:  []string{`/v1/messages`},
			KeySources: []ChannelAffinityKeySource{
				{Type: "gjson", Path: "metadata.user_id"},
			},
			TTLSeconds:         0,
			SkipRetryOnFailure: true, //如果你的场景是限速分散优先于 prompt cache，可以直接把 claude-cli 规则的 SkipRetryOnFailure 改为
			// false，让渠道限速后允许重试到其他渠道，亲和缓存会在成功后更新到新渠道。
			IncludeRuleName:   true,
			IncludeModelName:  true, // key 变为 claude-cli:{group}:{model}:{user_id}
			IncludeUsingGroup: true,
		},
		{
			// OpenAI Responses API：通过 prompt_cache_key 识别缓存上下文
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
			// Gemini（OpenAI 兼容格式）：通过请求体 user 字段识别会话。
			// 仅限 /v1/chat/completions；客户端不传 user 字段则不触发亲和。
			// 原生 Gemini 路径（/v1beta/models/）暂不支持，缺乏会话级 key。
			Name:       "gemini-chat",
			ModelRegex: []string{`^gemini-`},
			PathRegex:  []string{`/v1/chat/completions`},
			KeySources: []ChannelAffinityKeySource{
				{Type: "gjson", Path: "user"},
			},
			TTLSeconds:         0,
			SkipRetryOnFailure: false,
			IncludeRuleName:    true,
			IncludeUsingGroup:  true,
		},
	},
}
