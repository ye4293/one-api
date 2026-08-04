package metrics

import (
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/songquanpeng/one-api/common/config"
)

// 基数守卫。
//
// Prometheus 的每个 label 组合是一条常驻内存的时间序列，而且 **CounterVec 的子指标
// 一旦被 WithLabelValues 创建就永不回收**（会一直出现在每次 /metrics 输出里）。
// 所以序列数的真实上界是"进程启动以来出现过的 label 组合数"，而不是"最近活跃的数量" ——
// 不能因为"日常只有十几个模型有流量"就放弃守卫。
//
// 唯一的攻击面：middleware/distributor.go 无可用渠道的 503 分支里，
// modelRequest.Model 是**直接来自用户请求体、未经 abilities 表校验**的字符串
// （该分支在 c.Set("model", ...) 之前就 return 了）。攻击者连发带随机 model 名的请求
// 即可无限制造序列。本文件是那条路径唯一的保险丝。
//
// 其余埋点取的是 original_model / model（能选到渠道 ⇒ 必然命中 abilities 表），
// 对它们而言守卫是纵深防御。
const (
	labelOverflow = "__other__" // 超出上限后的归并值
	labelUnset    = "__unset__" // 取不到模型名（如 Distribute 提前 abort）
	maxLabelLen   = 96          // 超长模型名直接归并，避免单条 label 撑爆内存
)

var (
	modelSeen      sync.Map // map[string]struct{}，已放行的模型名
	modelSeenCount atomic.Int64

	// labelOverflowTotal 让"守卫被触发"本身可观测、可告警。
	// 没有它，超限就是静默丢数据 —— 图上看着正常，实际模型维度已经全被合并了。
	labelOverflowTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "metrics",
		Name:      "label_overflow_total",
		Help:      "Number of times a label value was collapsed into __other__ because the cardinality limit was reached.",
	})
)

// SanitizeModel 把模型名收敛到有限集合，返回可安全用作 label 的值。
func SanitizeModel(name string) string {
	if name == "" {
		return labelUnset
	}
	if len(name) > maxLabelLen {
		labelOverflowTotal.Inc()
		return labelOverflow
	}
	if _, ok := modelSeen.Load(name); ok {
		return name
	}
	limit := int64(config.MetricsMaxModelLabels)
	if limit <= 0 {
		limit = 500
	}

	// 用 LoadOrStore + Add 的返回值做原子闸门，而不是"先 Load 检查再 Add"。
	// 后者在 Load 与 Add 之间有窗口，并发请求会一起穿过检查导致超配
	// （实测 50 goroutine 下上限 100 会冲到 113）—— 守卫的全部意义是硬上限，
	// 软上限等于没有守卫。
	if _, loaded := modelSeen.LoadOrStore(name, struct{}{}); loaded {
		return name // 已放行过
	}
	// 本 goroutine 是第一个存入该名字的，尝试占用一个配额。
	// 只有 Add 后的值 <= limit 才算占到，否则回退 —— 这样"被放行的名字数"严格不超过 limit。
	if modelSeenCount.Add(1) > limit {
		modelSeen.Delete(name)
		modelSeenCount.Add(-1)
		labelOverflowTotal.Inc()
		return labelOverflow
	}
	return name
}
