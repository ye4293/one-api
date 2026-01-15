package flux

// FluxRequest 表示 Flux API 的请求结构（透传模式，保留原始字段）
type FluxRequest struct {
	Prompt        string         `json:"prompt"`
	Model         string         `json:"model,omitempty"`
	Width         int            `json:"width,omitempty"`
	Height        int            `json:"height,omitempty"`
	Steps         int            `json:"steps,omitempty"`
	PromptUpscale bool           `json:"prompt_upscale,omitempty"`
	Seed          int64          `json:"seed,omitempty"`
	Guidance      float64        `json:"guidance,omitempty"`
	SafetyCheck   bool           `json:"safety_check,omitempty"`
	OutputFormat  string         `json:"output_format,omitempty"`
	AspectRatio   string         `json:"aspect_ratio,omitempty"`
	// 其他可能的字段可以用 map 接收
	Extra         map[string]any `json:"-"`
}

// FluxResponse 表示 Flux API 的异步响应结构
type FluxResponse struct {
	ID         string  `json:"id"`          // 任务ID
	PollingURL string  `json:"polling_url"` // 轮询URL
	Cost       float64 `json:"cost"`        // 费用（美分）
	InputMP    float64 `json:"input_mp"`    // 输入兆像素
	OutputMP   float64 `json:"output_mp"`   // 输出兆像素
	Error      string  `json:"error,omitempty"`
}

// FluxPollingResponse 表示轮询查询的响应结构
type FluxPollingResponse struct {
	ID     string  `json:"id"`
	Status string  `json:"status"` // pending, processing, succeed, failed
	Result *Result `json:"result,omitempty"`
	Cost   float64 `json:"cost,omitempty"`
	Error  string  `json:"error,omitempty"`
}

// Result 表示生成结果
type Result struct {
	TaskId         string   `json:"task_id,omitempty"`         // 任务ID
	Sample         string   `json:"sample,omitempty"`          // 图片URL
	Prompt         string   `json:"prompt,omitempty"`          // 使用的提示词
	Seed           int64    `json:"seed,omitempty"`            // 使用的种子
	Width          int      `json:"width,omitempty"`           // 图片宽度
	Height         int      `json:"height,omitempty"`          // 图片高度
	StartTime      float64  `json:"start_time,omitempty"`      // 开始时间（Unix时间戳）
	GenerationTime float64  `json:"generation_time,omitempty"` // 生成耗时（秒）
	Config         string   `json:"config,omitempty"`          // 配置名称
	ScorerScores   []string `json:"scorer_scores,omitempty"`   // 评分
	PreFiltered    bool     `json:"pre_filtered,omitempty"`    // 是否预过滤
}

// ErrorResponse 表示 Flux API 的错误响应
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// FluxCallbackNotification 表示 Flux API 的回调通知
type FluxCallbackNotification struct {
	TaskId     string  `json:"task_id"`                   // 任务ID（注意：Flux 使用 task_id 而不是 id）
	Status     string  `json:"status"`                    // 任务状态：processing, SUCCESS, FAILED（注意大小写）
	Progress   int     `json:"progress,omitempty"`        // 进度（0-100）
	Result     *Result `json:"result,omitempty"`          // 生成结果（SUCCESS 时有值）
	PollingURL string  `json:"polling_url,omitempty"`     // 轮询URL
	Cost       float64 `json:"cost,omitempty"`            // 费用（美分）
	InputMP    float64 `json:"input_mp,omitempty"`        // 输入兆像素
	OutputMP   float64 `json:"output_mp,omitempty"`       // 输出兆像素
	Error      string  `json:"error,omitempty"`           // 错误信息
}
