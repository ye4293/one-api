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
	Enabled:           false, // 临时关闭亲和性,排查渠道不均衡问题
	DefaultTTLSeconds: 3600,
	Rules: []ChannelAffinityRule{
		{
			// Claude CLI / Claude Code：优先通过 metadata.user_id 识别会话（Claude Code 自动携带）；
			// 普通 curl 请求无此字段时，回退到认证用户 ID，保证所有 Claude 请求都能触发亲和。
			Name:       "claude-cli",
			ModelRegex: []string{`^claude-`},
			PathRegex:  []string{`/v1/messages`},
			KeySources: []ChannelAffinityKeySource{
				{Type: "gjson", Path: "metadata.user_id"}, // Claude Code 会话 ID（优先）
				//{Type: "context_int", Key: "id"},           // 兜底：认证用户 ID
			},
			TTLSeconds:         0,
			SkipRetryOnFailure: true, // 如果你的场景是限速分散优先于 prompt cache，可以改为
			// false，让渠道限速后允许重试到其他渠道，亲和缓存会在成功后更新到新渠道。
			IncludeRuleName:   true,
			IncludeModelName:  true, // key 变为 claude-cli:{group}:{model}:{user_id or id}
			IncludeUsingGroup: true,
		},
		{
			// OpenAI Responses API：优先通过 prompt_cache_key 识别缓存上下文；
			// 无此字段时回退到用户 ID。
			Name:       "openai-responses",
			ModelRegex: []string{`^gpt-`, `^o1`, `^o3`, `^o4`},
			PathRegex:  []string{`/v1/responses`},
			KeySources: []ChannelAffinityKeySource{
				{Type: "gjson", Path: "prompt_cache_key"}, // 显式缓存 key（优先）
				//{Type: "context_int", Key: "id"},           // 兜底：认证用户 ID
			},
			TTLSeconds:         0,
			SkipRetryOnFailure: true,
			IncludeRuleName:    true,
			IncludeUsingGroup:  true,
		},
		{
			// Gemini（OpenAI 兼容格式）：优先通过 user 字段识别；无此字段时回退到用户 ID。
			Name:       "gemini-chat",
			ModelRegex: []string{`^gemini-`},
			PathRegex:  []string{`/v1/chat/completions`},
			KeySources: []ChannelAffinityKeySource{
				{Type: "gjson", Path: "user"}, // 显式 user 字段（优先）
				//{Type: "context_int", Key: "id"}, // 兜底：认证用户 ID
			},
			TTLSeconds:         0,
			SkipRetryOnFailure: false,
			IncludeRuleName:    true,
			IncludeUsingGroup:  true,
		},
	},
}
