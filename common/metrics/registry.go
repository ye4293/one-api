// Package metrics 提供 Prometheus 指标导出能力（P0：进程与依赖健康度）。
//
// 设计约束（改动本包前请先读）：
//
//  1. 本包是 leaf package，只允许 import common/config、common/env、common/logger。
//     禁止 import model / monitor / middleware / controller / relay。
//     原因：未来若需在 model/log.go 里埋点，而 model 已被全项目依赖，
//     反向 import 会形成循环依赖。需要读 DB / Redis 状态的地方一律由 main.go 注入。
//
//  2. 只导出「DB 里算不出来、或 DB 算得太慢」的指标。判据（完整版见
//     deploy/prometheus/README.md 的五条裁决规则）：
//     - 账单 / 对客数字 / 财务对账 → **只用 DB `logs.quota`**。Prometheus counter
//     进程重启归零，increase() 是外推估算，永远不等于真值。
//     - 分钟级告警 / SLO / 实时排障 → **只用 Prometheus**。model_metrics 是小时粒度
//     且有 300s 聚合延迟，物理上做不到分钟级。
//     - 按 user / token / request_id 下钻 → **只用 DB**。这些维度基数无界，
//     刻意不做成 label。
//     - 渠道调用级（含重试）错误率 → **只有 Prometheus 有**。DB 侧只写一条聚合记录，
//     重试明细在 other 字段的 JSON 里无法聚合查询。
//     P0 阶段本包只有进程与连接池指标（DB 里完全没有），P1 起增加了业务指标
//     （请求数 / tokens / 延迟 / 错误率），与 model_metrics 表存在**有意的重叠** ——
//     那不是违规，边界由上面的判据划定。
package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/songquanpeng/one-api/common/config"
)

// namespace 是本项目自定义指标的统一前缀。
// 注意：复用的官方 collector 用的是自己的前缀（go_*、process_*、go_sql_*），不带 namespace。
const namespace = "oneapi"

var (
	registry *prometheus.Registry
	initOnce sync.Once
)

// Enabled 报告指标导出是否开启。所有注册与埋点都应先判断它。
// 该开关只在启动时读取，不接入 options 表的动态配置：运行时改开关会让序列突然消失，
// 导致 Grafana 图断裂、rate() 出现假 reset。
func Enabled() bool {
	return config.MetricsEnabled
}

// Registry 返回本项目私有的 registry，并在首次调用时注册进程级 collector。
//
// 故意不使用 prometheus.DefaultRegisterer：那是全局单例，任何间接依赖都可能往里
// 注册指标，导致 /metrics 内容不可控。
func Registry() *prometheus.Registry {
	initOnce.Do(func() {
		registry = prometheus.NewRegistry()
		registry.MustRegister(
			// go_goroutines / go_memstats_* / go_gc_duration_seconds ...
			// 额外开启 /sched/* 运行时指标，用于观测 goroutine 调度延迟（饥饿的先兆）。
			collectors.NewGoCollector(
				collectors.WithGoCollectorRuntimeMetrics(collectors.MetricsScheduler),
			),
			// process_open_fds / process_max_fds / process_resident_memory_bytes ...
			collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		)
	})
	return registry
}

// RegisterBusinessMetrics 注册 P1 的业务指标（模型维度 + 渠道维度）。
// 由 main.go 在启动时调用一次。
//
// 之所以要显式注册而不是在 var 初始化时自动注册：注册顺序需要在 Enabled() 可判定之后，
// 且要让"哪些指标被暴露"这件事在 main.go 里一眼可见。
//
// 两组指标各有独立开关，关掉后对应的指标族完全不出现在 /metrics 里
// （而不是出现但恒为 0）—— 这样 Grafana 上"没有数据"和"值为 0"不会混淆。
func RegisterBusinessMetrics() {
	if !Enabled() {
		return
	}
	if config.MetricsLLMEnabled {
		registerLLMMetrics()
	}
	if config.MetricsChannelEnabled {
		registerChannelMetrics()
	}
}
