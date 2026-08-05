package metrics

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/songquanpeng/one-api/common/config"
)

// 渠道维度指标（Group B）。
//
// **硬规则：本组指标绝不允许出现 model label。**（理由见 llm.go 顶部）
//
// 与 Group A 的语义区别是本方案的核心，务必分清：
//
//	Group A（oneapi_llm_*）        = 用户请求级。一次 API 调用计 1 次，重试不计。这是 SLO。
//	Group B（oneapi_channel_*）    = 渠道调用级。含重试，RetryTimes=2 时一次用户请求最多计 3 次。
//	                                 这是渠道质量评估，与用户体验无关。
//
// 一次用户请求重试 3 次全失败时：Group A 记 1 次失败，Group B 记 3 次失败。
// **任何除法表达式的分子分母都不得跨这两组** —— 混用会得到一个谁都不能用的数字。
// 为此在 deploy/prometheus/rules.yml 里把两种错误率固化成 recording rule，
// Grafana 只引用那两个名字，不手写除法。
//
// 渠道调用级数据 DB 侧算不出来：controller/retry_log.go 的 recordFinalErrorLog
// 只写一条聚合记录，重试明细塞在 other 字段的 JSON 里，无法做聚合查询。
// 所以这组指标不存在"和 DB 哪个对"的争议 —— 只有 Prometheus 有。

var (
	channelAttempts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: "channel", Name: "attempts_total",
		Help: "Channel invocation attempts, including retries. Denominator for channel-level error rate (NOT for SLO).",
	}, []string{"channel_id"})

	channelCallErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: "channel", Name: "call_errors_total",
		Help: "Failed channel invocations by reason, including retries. NOT the user-visible error count.",
	}, []string{"channel_id", "reason"})

	// channelInfo 是 info 指标模式：值恒为 1，信息全在 label 里，供 group_left 关联。
	// 这样 provider 只需一份序列，不必冗余到每个渠道指标上。
	channelInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace, Subsystem: "channel", Name: "info",
		Help: "Channel metadata (always 1). Join via: on(channel_id) group_left(provider) max by(channel_id,provider)(oneapi_channel_info).",
	}, []string{"channel_id", "provider", "channel_type"})
)

// ChannelEnabled 报告渠道维度指标是否开启。独立开关：渠道数量若远超预期
// （比如上千个），可以单独关掉本组而保留模型维度指标。
func ChannelEnabled() bool {
	return Enabled() && config.MetricsChannelEnabled
}

func registerChannelMetrics() {
	Registry().MustRegister(channelAttempts, channelCallErrors, channelInfo)
}

// IncChannelAttempt 记录一次渠道调用尝试。
//
// **隐式契约（本方案最脆弱的一环，无法用代码强制）**：
// 该函数的唯一调用点是 middleware.SetupContextForSelectedChannel —— 目前 12 个重试循环
// 加 distributor 共 13 处都会经过它，所以它是"渠道调用尝试"的完整分母。
// 将来若有人新增 relay 入口而直接 GetChannelById + 自行拼装 context，就会漏计分母，
// 而分子（IncChannelError）照常增长 → 渠道错误率虚高甚至超过 100%。
// 改动 SetupContextForSelectedChannel 或新增 relay 入口时请一并检查这里。
func IncChannelAttempt(channelID int) {
	if !ChannelEnabled() || channelID <= 0 {
		return
	}
	channelAttempts.WithLabelValues(strconv.Itoa(channelID)).Inc()
}

// IncChannelError 记录一次渠道调用失败。
//
// 调用点是 controller/relay.go 的 processChannelRelayError 函数体内部 —— 它是全部 12 个
// 重试循环、30 个失败点唯一的汇聚函数。在函数体内埋点而非在调用点埋点，是为了让新增
// relay 入口时自动被覆盖（在调用点埋会漏掉 11/12 条链路）。
func IncChannelError(channelID int, reason string) {
	if !ChannelEnabled() || channelID <= 0 {
		return
	}
	channelCallErrors.WithLabelValues(strconv.Itoa(channelID), reason).Inc()
}

// SetChannelInfo 登记渠道元信息，用于按 provider 聚合。
// 在选中渠道时顺手调用，channel.Type 是现成字段，**零额外查询**。
func SetChannelInfo(channelID, channelType int, provider string) {
	if !ChannelEnabled() || channelID <= 0 {
		return
	}
	if provider == "" {
		provider = "Other"
	}
	channelInfo.WithLabelValues(
		strconv.Itoa(channelID), provider, strconv.Itoa(channelType),
	).Set(1)
}
