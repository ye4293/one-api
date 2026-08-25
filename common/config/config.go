package config

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/songquanpeng/one-api/common/env"
)

var GeminiVersion = "v1/beta"
var SystemName = "EZLINK AI"
var ServerAddress = "http://localhost:3000"
var FrontendServerAddress = ""
var DocsAddress = ""
var Footer = ""
var Logo = ""
var TopUpLink = ""
var ChatLink = ""
var QuotaPerUnit = 500 * 1000.0 // $0.002 / 1K tokens
var DisplayInCurrencyEnabled = true
var DisplayTokenStatEnabled = true

// Any options with "Secret", "Token" in its key won't be return by GetOptions

var SessionSecret = uuid.New().String()

var OptionMap map[string]string
var OptionMapRWMutex sync.RWMutex

var ItemsPerPage = 10
var MaxRecentItems = 100

var PasswordLoginEnabled = true
var PasswordRegisterEnabled = true
var EmailVerificationEnabled = false
var GitHubOAuthEnabled = false
var WeChatAuthEnabled = false
var TurnstileCheckEnabled = false
var RegisterEnabled = true

var CryptPaymentEnabled = false
var StripePaymentEnabled = false
var CryptCallbackUrl = ""
var AddressOut = ""
var StripeCallbackUrl = ""
var StripePrivateKey = ""
var StripePublicKey = ""
var StripeEndpointSecret = ""
var StripeApiSecret = ""
var StripeWebhookSecret = ""
var StripePriceId = ""
var StripeUnitPrice = 8.0
var StripeMinTopUp = 1
var StripePromotionCodesEnabled = false

var EpayPaymentEnabled = false
var EpayPayAddress = ""
var EpayId = ""
var EpayKey = ""
var EpayPrice = 7.3
var EpayMinTopUp = 1
var EpayCallbackAddress = ""

var CfR2storeEnabled = true
var CfBucketFileName = "ezlinkai-file"

// var CfFileAccessKey = "42a3d63d1371f46956f7d3de36b3b9a5"
// var CfFileSecretKey = "31db6128dbf10f3a4a823cea1e52af23934e77353d06f1d1f966288e217073f9"
// var CfFileEndpoint = "https://f19328743901865dd8223e016b2ff78d.r2.cloudflarestorage.com"

var CfFileAccessKey = "GQwDfmzqBlTMsBjf"
var CfFileSecretKey = "JrL2Sy9ojWemCKv45iJukOKKrlBw64"
var CfFileEndpoint = "https://hua.cn-nb1.rains3.com"
var CfFilePublicUrl = "" // 公共访问 URL（如自定义域），为空时使用 Endpoint

var CfBucketImageName = ""
var CfImageAccessKey = ""
var CfImageSecretKey = ""
var CfImageEndpoint = ""

var EmailDomainRestrictionEnabled = false
var EmailDomainWhitelist = []string{
	"gmail.com",
	"163.com",
	"126.com",
	"qq.com",
	"outlook.com",
	"hotmail.com",
	"icloud.com",
	"yahoo.com",
	"foxmail.com",
}

var DebugEnabled = strings.ToLower(os.Getenv("DEBUG")) == "true"
var DebugSQLEnabled = strings.ToLower(os.Getenv("DEBUG_SQL")) == "true"
var MemoryCacheEnabled = strings.ToLower(os.Getenv("MEMORY_CACHE_ENABLED")) == "true"

var LogConsumeEnabled = true

var SMTPServer = ""
var SMTPPort = 587
var SMTPAccount = ""
var SMTPFrom = ""
var SMTPToken = ""

var GitHubClientId = ""
var GitHubClientSecret = ""
var GithubRedirectUri = ""

var GoogleOAuthEnabled = true
var GoogleClientId = ""
var GoogleClientSecret = ""
var GoogleRedirectUri = ""
var StripeKey = ""

var WeChatServerAddress = ""
var WeChatServerToken = ""
var WeChatAccountQRCodeImageURL = ""

var MessagePusherAddress = ""
var MessagePusherToken = ""

var TurnstileSiteKey = ""
var TurnstileSecretKey = ""

var QuotaForNewUser int64 = 0
var QuotaForInviter int64 = 0
var QuotaForInvitee int64 = 0
var ChannelDisableThreshold = 5.0
var AutomaticDisableChannelEnabled = false
var AutomaticEnableChannelEnabled = false

// ModelScopeAutoDisableEnabled 为 true 时，自动禁用只禁「该渠道上的该模型」（abilities 行），
// 渠道保持启用；当渠道所有模型都被禁用时才禁用整个渠道。
// 为 false 时回退到旧行为：命中禁用条件直接禁用整个渠道（线上快速回滚开关）。
// 仅作用于单 Key 渠道，多 Key 渠道维持既有 key 级禁用逻辑。
var ModelScopeAutoDisableEnabled = true
var AutoTestChannelFrequency = 0           // 自动测试渠道的频率（分钟），0 表示禁用
var UpstreamModelUpdateIntervalMinutes = 0 // 上游模型巡检间隔（分钟），0 表示使用默认值（5 分钟 / 300 秒）

// 上游模型巡检的批量删除比例保护：单轮待删模型数占本地模型总数的比例超过
// UpstreamRemoveGuardPercent 时，不自动删除，转为待人工审核。
// 防的是上游返回了一份不相干的模型列表（换 API 版本、空壳列表等）—— 现有的
// "上游返回空列表则拒绝"只挡得住 len==0，挡不住"返回 1 个无关模型"。
//
// MinLocalModels 是必需的下限：本地只有 1-3 个模型时任何删除都 ≥50%，
// 无下限会把"模型全删 → 自动禁用渠道"那条链路永久拦掉（见
// channel_upstream_update.go 的 allModelsRemoved 分支）。
var UpstreamRemoveGuardPercent = 50       // 0 表示关闭比例保护
var UpstreamRemoveGuardMinLocalModels = 5 // 本地模型数低于此值时不启用比例保护

// 上游模型同步的真实请求探针：对 diff 出的 pendingAdd / pendingRemove 逐个发一次
// 最小 chat 请求验证，只有上游明确说「模型不存在」才允许删除，探测通过才允许新增。
// 默认关闭（opt-in）—— 探针会产生真实上游请求与真实 token 成本。
var UpstreamModelProbeEnabled = false
var UpstreamModelProbeMaxPerChannel = 30      // 单渠道单轮探测次数上限
var UpstreamModelProbeMaxPerRound = 200       // 全局单轮探测次数上限
var UpstreamModelProbeTimeoutSeconds = 10     // 单次探测 wall-clock 超时（秒）
var UpstreamModelProbeChannelBudgetSecs = 120 // 单渠道探测总时长预算（秒）

// 上游巡检并行度。上游为高并发聚合站时可调大以缩短整轮巡检耗时。
// ChannelConcurrency：同时巡检的渠道数（不同渠道=不同 key/端点，无互相限流）。
// ProbeModelConcurrency：单渠道内同时探测的模型数（同一 key 并发，上游承受力弱时应设 1）。
// 两者最小有效值为 1；配 0 或非法值时代码使用处兜底为 1。
var UpstreamModelUpdateChannelConcurrency = 5
var UpstreamModelProbeModelConcurrency = 5

// 健康巡检：对已在本地 models 列表里的模型做周期性可达性探测。
// 连续失败 N 次后自动删除该模型；全部模型都失败时禁用渠道。
// 默认关闭（opt-in）—— 会对每个启用渠道的每个模型定期发真实付费请求。
var UpstreamModelHealthProbeEnabled = false
var UpstreamModelHealthProbeFastIntervalMinutes = 10  // 未定型 / 有失败嫌疑时的探测间隔
var UpstreamModelHealthProbeSteadyIntervalMinutes = 60 // 连续 3 次成功后的固定间隔
var UpstreamModelHealthProbeFailThreshold = 3          // 连续失败几次触发删除
var FeishuWebhookUrls = ""

// 动态优先级评分：基于实时窗口指标（成功率/延迟/价格）为同 model 下的多个渠道
// 计算 dynamic_priority，供选渠道热路径做偏好排序。慢变调度信号（默认 5 分钟级），
// 不承担故障转移——故障隔离由「模型级禁用」负责，被禁用的 Ability 不参与评分。
// 算法实现见 common/dynamicprio。默认关闭（opt-in）。
var DynamicPriorityEnabled = false
// DynamicPriorityApplyEnabled 控制选渠道热路径是否真正切换到动态优先级排序。
// 与 DynamicPriorityEnabled 解耦：可只开计算落库（Enabled=true, Apply=false）做旁路观察，
// 在 Model 页确认分数合理后再开 Apply 切换分发。默认关闭。
var DynamicPriorityApplyEnabled = false
var DynamicPriorityWeightSuccess = 50.0 // 成功率权重（0-100）
var DynamicPriorityWeightLatency = 30.0 // 延迟权重（0-100）
var DynamicPriorityWeightPrice = 20.0   // 价格权重（0-100）
var DynamicPriorityCalcIntervalMinutes = 5  // Master 节点评分计算周期（分钟）
var DynamicPriorityTopThreshold = 10        // 选渠道时同档阈值（%）：top X% 视为同档加权随机
var DynamicPriorityWindowMinutes = 10       // 滑动窗口长度（分钟），评分只看该窗口内数据
var DynamicPriorityExploreSlots = 2         // top 档预留几个未评分探索位（首选生效，重试关闭）
var DynamicPriorityExplorationTTLHours = 24 // 新加渠道享受探索位优待的时长（小时）
var PingIntervalEnabled = false
var PingIntervalSeconds = 0

// 自动禁用关键词配置（一行一个关键词）
var AutoDisableKeywords = `api key not valid
invalid_api_key
incorrect api key provided
authentication_error
permission denied
account_deactivated
insufficient_quota
credit balance is too low
not_enough_credits
credit
balance
used all available credits
reached its monthly spending limit
resource pack exhausted
billing to be enabled
permission_denied
unauthenticated
operation not allowed
organization has been disabled
consumer
has been suspended
service account
project not found
billing account
imagen api
generativelanguage.googleapis.com
console.x.ai`

// 跨渠道重试关键词配置（一行一个关键词，匹配到则触发跨渠道重试）
var RetryKeywords = `api key not valid
invalid_api_key
authentication_error
api key not found
invalid api key
billing_hard_limit_reached
billing hard limit has been reached
billing limit has been reached
hard limit has been reached
operation not allowed
your resource has been blocked because we detected unusual behavior`

var QuotaRemindThreshold int64 = 1000
var PreConsumedQuota int64 = 500
var ApproximateTokenEnabled = false
var RetryTimes = 0

var RootUserEmail = ""

var IsMasterNode = os.Getenv("NODE_TYPE") == "master"

var requestInterval, _ = strconv.Atoi(os.Getenv("POLLING_INTERVAL"))
var RequestInterval = time.Duration(requestInterval) * time.Second

var SyncFrequency = env.Int("SYNC_FREQUENCY", 10*60) // unit is second

var BatchUpdateEnabled = false
var BatchUpdateInterval = env.Int("BATCH_UPDATE_INTERVAL", 5)

var RelayTimeout = env.Int("RELAY_TIMEOUT", 0)           // unit is second
var StreamingTimeout = env.Int("STREAMING_TIMEOUT", 300) // unit is second

var GeminiSafetySetting = env.String("GEMINI_SAFETY_SETTING", "BLOCK_NONE")

var Theme = env.String("THEME", "default")
var ValidThemes = map[string]bool{
	"default": true,
	"berry":   true,
	"air":     true,
}

// All duration's unit is seconds
// Shouldn't larger then RateLimitKeyExpirationDuration
var (
	GlobalApiRateLimitNum            = env.Int("GLOBAL_API_RATE_LIMIT", 180000)
	GlobalApiRateLimitDuration int64 = 30 * 60

	GlobalWebRateLimitNum            = env.Int("GLOBAL_WEB_RATE_LIMIT", 6000)
	GlobalWebRateLimitDuration int64 = 30 * 60

	UploadRateLimitNum            = 10
	UploadRateLimitDuration int64 = 600

	DownloadRateLimitNum            = 10
	DownloadRateLimitDuration int64 = 60

	CriticalRateLimitNum            = 200
	CriticalRateLimitDuration int64 = 200 * 60
)

var RateLimitKeyExpirationDuration = 20 * time.Minute

var EnableMetric = env.Bool("ENABLE_METRIC", false)
var MetricQueueSize = env.Int("METRIC_QUEUE_SIZE", 10)
var MetricSuccessRateThreshold = env.Float64("METRIC_SUCCESS_RATE_THRESHOLD", 0.8)
var MetricSuccessChanSize = env.Int("METRIC_SUCCESS_CHAN_SIZE", 1024)
var MetricFailChanSize = env.Int("METRIC_FAIL_CHAN_SIZE", 128)

var InitialRootToken = os.Getenv("INITIAL_ROOT_TOKEN")

// 模型监控指标配置
var ModelMetricsEnabled = env.Bool("MODEL_METRICS_ENABLED", true)
var ModelMetricsAggregationInterval = env.Int("MODEL_METRICS_AGGREGATION_INTERVAL", 300) // 聚合间隔（秒）
var ModelMetricsRetentionDays = env.Int("MODEL_METRICS_RETENTION_DAYS", 30)              // 数据保留天数
var ModelMetricsBackfillDays = env.Int("MODEL_METRICS_BACKFILL_DAYS", 7)                 // 首次回填天数

// Claude Thinking 模型配置
var ClaudeThinkingEnabled = true                      // 是否启用 Claude 思考适配（-thinking 后缀）
var ClaudeThinkingBudgetRatio = 0.8                   // 默认思考 token 百分比（80%）
var ClaudeDefaultMaxTokens map[string]int             // 模型默认 MaxTokens
var ClaudeReasoningEffortMap map[string]float64       // reasoning_effort 到百分比的映射
var ClaudeRequestHeaders map[string]map[string]string // 请求头覆盖（模型名 -> 请求头键值对）

// 审计模块配置（环境变量为初始默认值，运行时从 options 表覆盖）
var AuditEnabled = env.Bool("AUDIT_ENABLED", false)
var AuditAWSRegion = env.String("AUDIT_AWS_REGION", "")
var AuditAWSAccessKey = env.String("AUDIT_AWS_ACCESS_KEY", "")
var AuditAWSSecretKey = env.String("AUDIT_AWS_SECRET_KEY", "")
var AuditFirehoseStream = env.String("AUDIT_FIREHOSE_STREAM", "")
var AuditAthenaDatabase = env.String("AUDIT_ATHENA_DATABASE", "audit")
var AuditAthenaTable = env.String("AUDIT_ATHENA_TABLE", "request_logs")
var AuditAthenaWorkgroup = env.String("AUDIT_ATHENA_WORKGROUP", "primary")
var AuditS3OutputLocation = env.String("AUDIT_S3_OUTPUT_LOCATION", "")
var AuditS3DataLocation = env.String("AUDIT_S3_DATA_LOCATION", "")
var AuditChannelSize = env.Int("AUDIT_CHANNEL_SIZE", 2000)
var AuditMaxBufferMB = env.Int("AUDIT_MAX_BUFFER_MB", 1024)
var AuditDiskBufferDir = env.String("AUDIT_DISK_BUFFER_DIR", "./data/audit_spill")
var AuditDiskBufferMaxGB = env.Int("AUDIT_DISK_BUFFER_MAX_GB", 40)
var AuditBatchSize = env.Int("AUDIT_BATCH_SIZE", 500)
var AuditFlushIntervalSec = env.Int("AUDIT_FLUSH_INTERVAL_SEC", 10)
var AuditMaxBodyKB = env.Int("AUDIT_MAX_BODY_KB", 10240)
var AuditMaxRespKB = env.Int("AUDIT_MAX_RESP_KB", 4096)
var AuditRetentionDays = env.Int("AUDIT_RETENTION_DAYS", 0)
var AuditRedactHeaders = env.String("AUDIT_REDACT_HEADERS", "Authorization,Api-Key,X-Api-Key,Cookie,Set-Cookie")

// 日志标识，用于 JSON 日志中的 service/instance 字段
var ServiceName = env.String("SERVICE_NAME", "one-api")
var InstanceId = env.String("INSTANCE_ID", getHostname())

// 慢请求阈值：200 请求超过此毫秒数才写 access log，0 表示不记录 200
var SlowRequestThresholdMs = env.Int("SLOW_REQUEST_THRESHOLD_MS", 5000)

// Prometheus 指标导出（详见 common/metrics 包注释）
// 这几项故意不接入 options 表的动态配置：指标注册在启动时完成，运行时改开关会让
// 时间序列突然消失，导致 Grafana 图断裂、rate() 出现假 reset。开关应为重启级。
var MetricsEnabled = env.Bool("METRICS_ENABLED", false)
var MetricsPort = env.Int("METRICS_PORT", 9090)

// 为空时 /metrics 只接受 loopback 请求，避免忘配 token 变成匿名公开端点
var MetricsToken = env.String("METRICS_TOKEN", "")

// pprof 独立开关：/debug/pprof/heap 可能 dump 出含 API key 的内存内容，默认关闭
var PprofEnabled = env.Bool("PPROF_ENABLED", false)

// 业务指标开关（P1）。做成两个独立开关而非一个，是为了当**回滚阀门**：
// 线上出问题只需改环境变量重启，不必回滚二进制。
var MetricsLLMEnabled = env.Bool("METRICS_LLM_ENABLED", true)
var MetricsChannelEnabled = env.Bool("METRICS_CHANNEL_ENABLED", true)

// 模型名 label 的基数上限，超出后归并为 __other__。
// abilities 表实测约 388 个 distinct model；留一倍余量。
// 注意 Prometheus 的 CounterVec 子指标一旦创建就永不回收，所以上限按"历史出现过的总数"计。
var MetricsMaxModelLabels = env.Int("METRICS_MAX_MODEL_LABELS", 500)

// 延迟直方图桶边界（秒，逗号分隔）。做成可配置以免"想调桶"就得重新发版。
// 前 7 个与 model.LatencyBoundaries 对齐，后 4 个覆盖长流式请求（STREAMING_TIMEOUT=600）。
var MetricsLatencyBuckets = env.String("METRICS_LATENCY_BUCKETS", "0.5,1,2,3,5,10,30,60,120,300,600")

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return hostname
}
