package metrics

import (
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
)

// 模型维度指标（Group A）。
//
// **硬规则：本组指标绝不允许出现 channel_id label。**
// abilities 表有 ~388 个 distinct model，渠道数量级在百，两者相乘再乘上直方图的
// 十几条序列 ≈ 百万条时间序列，单这一个指标就能打爆 Prometheus。
// 需要"某模型在某渠道上的表现"请查 model_metrics 表 —— 它有小时聚合和唯一索引，
// 承受得住三元组，Prometheus 承受不住。渠道维度见 channel.go（那边不带 model）。
//
// 另外刻意不带的 label：user_id / token_id / request_id（基数无界，DB 侧已能下钻）、
// provider（是 channel_id 的函数，见 channel.go 的 channel_info）。

// LLM 请求结果
const (
	OutcomeSuccess = "success"
	OutcomeError   = "error"
)

// CtxRelayFailedKey 是 gin context 里的标记键：relay 层判定"本次用户请求最终失败"时写入，
// 值为 ClassifyReason 的结果。
//
// 存在的理由是 **SSE 流式请求的 200 陷阱**：一旦响应头已经写出，中途失败时
// c.Writer.Status() 仍然是 200（controller/relay.go 里的 c.JSON 只会打一条
// "headers already written" 警告）。中间件若只看状态码，会系统性低估错误率 ——
// 而流式恰好是 LLM 网关的主要流量形态。
//
// 由 controller/retry_log.go 的 recordFinalErrorLog 写入（那里是 Relay / RelayGemini /
// RelayClaude / RelayResponse 四条主链路最终失败的唯一汇聚点），middleware/metrics.go 读取。
const CtxRelayFailedKey = "metrics_relay_failed_reason"

// CtxModelKey 是模型名的兜底来源。
//
// 只在一种情况下需要：Distribute 判定"无可用渠道"时会在 c.Set("model", ...) 之前
// 就 abort 返回，此时 gin context 里没有任何模型名，中间件拿不到 label。
// 若不补这个键，503 请求就完全进不了 llm_requests_total ——
// "某模型丢光全部渠道"这类事故在 SLO 错误率上会显示为零影响，正是要防的盲区。
//
// 注意这个键里的值是**未经 abilities 校验的用户输入**，读取方必须过 SanitizeModel。
const CtxModelKey = "metrics_model"

// token 种类
const (
	TokenKindPrompt     = "prompt"
	TokenKindCompletion = "completion"
	TokenKindCached     = "cached"
)

// defaultLatencyBuckets 前 7 个边界与 model.LatencyBoundaries（model/model_metrics.go:52）
// 逐值对齐，**改动任一侧都必须同步改另一侧**，否则 Prometheus 与 model_metrics 表算出的
// P95 会出现无法解释的偏差。因 common/metrics 是 leaf package（见 registry.go 包注释）
// 不能 import model 复用常量，只能复制。
//
// 后 4 个（60/120/300/600）是 Prometheus 侧独有：生产 RELAY_TIMEOUT=1800、
// STREAMING_TIMEOUT=600，长流式请求是常态。DB 侧上界只到 30s，意味着一个 40s 的请求和一个
// 25 分钟的请求落进同一桶，histogram_quantile 在最后一桶内做线性插值 → P95/P99 是编的。
// 所以 >30s 的场景以 Prometheus 为准。
var defaultLatencyBuckets = []float64{0.5, 1, 2, 3, 5, 10, 30, 60, 120, 300, 600}

// TTFT 与总耗时量级完全不同（首字通常在秒级内），必须单独一套桶。
var defaultTTFTBuckets = []float64{0.2, 0.5, 1, 2, 3, 5, 10, 20}

var (
	llmRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: "llm", Name: "requests_total",
		Help: "Total LLM requests from the user's point of view (one per API call, retries NOT counted). Denominator for SLO.",
	}, []string{"model", "outcome"})

	llmRequestErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: "llm", Name: "request_errors_total",
		Help: "User-visible LLM request failures by reason (after all retries exhausted).",
	}, []string{"model", "reason"})

	llmTokens = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: "llm", Name: "tokens_total",
		Help: "Total tokens by kind. TPM is derived: rate(...[5m])*60.",
	}, []string{"model", "kind"})

	llmQuota = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: "llm", Name: "quota_total",
		Help: "Quota consumed, for real-time cost visibility ONLY. NOT for billing reconciliation - logs.quota is the single source of truth.",
	}, []string{"model"})

	llmDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Subsystem: "llm", Name: "request_duration_seconds",
		Help:    "End-to-end LLM request duration in seconds.",
		Buckets: latencyBuckets(),
	}, []string{"model", "stream"})

	llmFirstToken = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Subsystem: "llm", Name: "first_token_seconds",
		Help:    "Time to first token for streaming requests. Only reported by the openai/gemini/anthropic/xai adaptors.",
		Buckets: defaultTTFTBuckets,
	}, []string{"model"})

	llmNoChannel = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: "llm", Name: "no_channel_total",
		Help: "Requests rejected with 503 because no channel was available for the model in that group.",
	}, []string{"model", "group"})

	llmRequestsByGroup = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: "llm", Name: "requests_by_group_total",
		Help: "LLM requests by user group. Separate from requests_total to keep group out of the high-cardinality model dimension.",
	}, []string{"group", "outcome"})
)

// LLMEnabled 报告模型维度指标是否开启。独立于 METRICS_ENABLED 的第二道开关，
// 作用是**回滚阀门**：线上出问题改环境变量重启即可，不必回滚二进制。
func LLMEnabled() bool {
	return Enabled() && config.MetricsLLMEnabled
}

// latencyBuckets 解析 METRICS_LATENCY_BUCKETS，解析失败则回落到默认值。
// 做成可配置是为了避免"想调一下桶边界"就得重新发版。
func latencyBuckets() []float64 {
	raw := strings.TrimSpace(config.MetricsLatencyBuckets)
	if raw == "" {
		return defaultLatencyBuckets
	}
	parts := strings.Split(raw, ",")
	buckets := make([]float64, 0, len(parts))
	for _, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			logger.SysError("invalid METRICS_LATENCY_BUCKETS, falling back to defaults: " + err.Error())
			return defaultLatencyBuckets
		}
		buckets = append(buckets, v)
	}
	if len(buckets) == 0 {
		return defaultLatencyBuckets
	}
	return buckets
}

func registerLLMMetrics() {
	Registry().MustRegister(
		llmRequests, llmRequestErrors, llmTokens, llmQuota,
		llmDuration, llmFirstToken, llmNoChannel, llmRequestsByGroup,
		labelOverflowTotal,
	)
}

// ObserveConsume 记录一次成功完成计费的 LLM 调用（tokens / quota / 耗时 / 首字延迟）。
//
// 调用点：model/log.go 的 RecordConsumeLogWithOtherAndRequestID，
// **必须放在 `if !config.LogConsumeEnabled { return }` 之前** —— 那是个可后台动态关闭的开关，
// 而运维关掉它的场景（DB 压力大、磁盘吃紧）恰好是最需要监控的时刻。详见该处注释。
//
// duration 与 firstWordLatency 的单位都是**秒**。
func ObserveConsume(model string, isStream bool, promptTokens, completionTokens, cachedTokens int,
	quota int64, duration, firstWordLatency float64) {
	if !LLMEnabled() {
		return
	}
	m := SanitizeModel(model)

	if promptTokens > 0 {
		llmTokens.WithLabelValues(m, TokenKindPrompt).Add(float64(promptTokens))
	}
	if completionTokens > 0 {
		llmTokens.WithLabelValues(m, TokenKindCompletion).Add(float64(completionTokens))
	}
	if cachedTokens > 0 {
		llmTokens.WithLabelValues(m, TokenKindCached).Add(float64(cachedTokens))
	}
	if quota > 0 {
		llmQuota.WithLabelValues(m).Add(float64(quota))
	}
	if duration > 0 {
		llmDuration.WithLabelValues(m, strconv.FormatBool(isStream)).Observe(duration)
	}
	// 只有流式请求才有首字延迟，且仅 4 个适配器上报；> 0 才记，避免把 0 拉低分位数
	if firstWordLatency > 0 {
		llmFirstToken.WithLabelValues(m).Observe(firstWordLatency)
	}
}

// ObserveVideoConsume 记录视频任务的消费。
//
// 视频链路（RecordVideoConsumeLog）独立写库，不经过 RecordConsumeLogWithOtherAndRequestID，
// 所以需要单独埋点，否则单价最高的模态完全没有指标。
//
// **刻意不记 duration 直方图**：视频是异步任务，那里的 duration 是"提交请求耗时"而非
// "视频生成耗时"，混进 llm_request_duration_seconds 会污染 P95。
func ObserveVideoConsume(model string, promptTokens, completionTokens int, quota int64) {
	if !LLMEnabled() {
		return
	}
	m := SanitizeModel(model)
	if promptTokens > 0 {
		llmTokens.WithLabelValues(m, TokenKindPrompt).Add(float64(promptTokens))
	}
	if completionTokens > 0 {
		llmTokens.WithLabelValues(m, TokenKindCompletion).Add(float64(completionTokens))
	}
	if quota > 0 {
		llmQuota.WithLabelValues(m).Add(float64(quota))
	}
}

// IncRequest 记录一次用户请求的最终结果。由 middleware/metrics.go 调用，
// 保证"每个用户请求恰好一次"—— 这是 SLO 的分母，与含重试的渠道调用计数（channel.go）语义不同。
func IncRequest(model, group, outcome string) {
	if !LLMEnabled() {
		return
	}
	llmRequests.WithLabelValues(SanitizeModel(model), outcome).Inc()
	if group != "" {
		llmRequestsByGroup.WithLabelValues(group, outcome).Inc()
	}
}

// IncFinalError 记录用户可见的最终失败原因（所有重试都失败之后）。
// 调用点 controller/retry_log.go 的 recordFinalErrorLog —— 与写 DB 错误日志同一个函数体，
// 因此本指标与 logs 表 LogTypeError 的行数由构造保证一致，对账不需要额外解释。
func IncFinalError(model, reason string) {
	if !LLMEnabled() {
		return
	}
	llmRequestErrors.WithLabelValues(SanitizeModel(model), reason).Inc()
}

// IncNoChannel 记录"该模型在该分组下无可用渠道"（503）。
//
// 注意：这里的 model 是**未经 abilities 表校验的用户输入**
// （distributor 的 503 分支在 c.Set("model", ...) 之前就 return 了），
// 是全部指标里唯一 attacker-controlled 的 label —— SanitizeModel 的守卫在此处是必需的，不是可选。
func IncNoChannel(model, group string) {
	if !LLMEnabled() {
		return
	}
	llmNoChannel.WithLabelValues(SanitizeModel(model), group).Inc()
}
