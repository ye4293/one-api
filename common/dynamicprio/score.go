// Package dynamicprio 实现动态优先级评分算法。
//
// 设计目标：基于实时窗口指标（成功率、延迟、单价）为「同一 model 下的多个候选渠道」
// 计算一个 0~100 的分数，供选渠道热路径做偏好排序。它是一个慢变调度信号（5 分钟级），
// 不承担故障转移职责——故障隔离由特性一「模型级禁用」负责，被禁用的 Ability
// （enabled=false）根本不会进评分池。
//
// 为什么不用 min-max 相对归一化：min-max 会把噪声放大成天壤之别
// （三个渠道延迟 800/810/820ms 归一化后是 100/50/0，实际几乎没差别），导致流量剧烈摆动。
// 本实现用「中位数 + MAD（中位绝对偏差）」做分位数归一化，对极端值不敏感；价格维度
// 用绝对比例作固定偏置，不随实时指标重新归一化。
package dynamicprio

// ChannelStat 是单个渠道在一个评分周期内的窗口指标。
// 调用方从 Redis 滑动窗口聚合得到这些值后传入。
type ChannelStat struct {
	ChannelId int

	// 成功率维度
	SuccessCount int     // 窗口内成功请求数
	FailureCount int     // 窗口内失败请求数
	TotalCount   int     // SuccessCount + FailureCount；为 0 表示该窗口无数据
	SuccessRate  float64 // 已聚合好的成功率 [0,1]，无数据时由调用方填 0

	// 延迟维度（毫秒）
	//
	// 必须区分流式 / 非流式：流式请求的 Duration（首字到末字总时长）受生成长度
	// 影响，动辄几十秒，根本不代表「响应快慢」——首字延迟（TTFT）才是流式体验的真实信号。
	// 若混用 duration，所有流式渠道会被误判成慢，动态优先级退化为纯非流式排序。
	//   - 流式：AvgFirstTokenMs（首字延迟）
	//   - 非流式：AvgLatencyMs（端到端总时长）
	// 同一 model 在窗口内通常以单一模式为主；调用方按窗口内主流请求模式填充对应字段，
	// 另一个填 0。HasData 只要任一字段有效即为 true。
	AvgFirstTokenMs float64 // 流式：窗口内平均首字延迟；无流式数据时填 0
	AvgLatencyMs    float64 // 非流式：窗口内平均端到端延迟；无非流式数据时填 0

	// 价格维度（固定偏置，调用方从 Channel.UnitPrice 读）
	// 单价越高分数越低；0 表示未配置价格，价格维度退化为中位分。
	UnitPrice float64

	// DefaultPriority 是管理员手动设的优先级，作为「无数据」渠道的兜底分来源。
	// 透传 *Ability.Priority 的解引用值（nil 视为 0）。
	DefaultPriority int64
}

// Weights 是三个维度的权重，三者之和应为 100（外部 config 注入，已归一化）。
type Weights struct {
	Success float64 // 成功率权重（0-100）
	Latency float64 // 延迟权重（0-100）
	Price   float64 // 价格权重（0-100）
}

// ChannelScore 是单个渠道的评分结果。
type ChannelScore struct {
	ChannelId int

	// Score 是最终综合分 [0,100]，供 dynamic_priority 落库。
	Score float64

	// HasData 标记该渠道在本窗口是否有足够数据参与实时评分。
	// false 表示走兜底逻辑（DefaultPriority 归一化），不应被视为「实时质量信号」。
	HasData bool

	// 分维度得分（调试/可观测用，便于排查为何某渠道分数异常）
	SuccessScore float64
	LatencyScore float64
	PriceScore   float64
}

// MinSampleCount 是成功率维度的参考样本量常量，仅用于文档说明。
// 从 v2 起用 Beta-Binomial 平滑替代硬阈值，任何样本数都能给出合理分数，
// 不再作为"参与评分"的门槛。保留常量便于运维参考"多少样本后分数逼近真实"。
const MinSampleCount = 20

// Beta-Binomial 先验：假设未知渠道的成功率符合 Beta(alpha, beta) 分布。
// alpha=beta=2.5 等价于「加入 2.5 个虚拟成功 + 2.5 个虚拟失败」的平滑，
// 让小样本渠道从"完全不确定 → 50 分"平滑过渡到"有信号但样本少 → 偏离 50"，
// 大样本时先验被真实数据压过，逼近实际成功率。
//
// 具体行为对照见 docs/plans/2026-08-25-dp-fixes.md 附表。
const (
	priorAlpha = 2.5
	priorBeta  = 2.5
)

const (
	// maxScore / midScore 是归一化后的上下界，分数被 clamp 到 [0,100]。
	maxScore = 100.0
	midScore = 50.0
)

// ScoreChannels 对「同一 model 下的候选渠道」计算动态优先级分数。
//
// 输入 stats 顺序任意，输出顺序与输入一致。空输入返回空切片，不报错。
// 本函数是纯函数：无 IO、无随机数、无全局状态，便于 table 测试。
func ScoreChannels(stats []ChannelStat, weights Weights) []ChannelScore {
	if len(stats) == 0 {
		return []ChannelScore{}
	}

	// 归一化权重，避免外部配成 60+60+0 这种和>100 的情况让分数膨胀。
	wSum := weights.Success + weights.Latency + weights.Price
	if wSum <= 0 {
		// 全 0 权重：退化成无偏好，全部给中位分，保证选渠道逻辑仍可工作。
		out := make([]ChannelScore, len(stats))
		for i, s := range stats {
			out[i] = ChannelScore{ChannelId: s.ChannelId, Score: midScore, HasData: hasEnoughData(s)}
		}
		return out
	}
	ws := weights.Success / wSum
	wl := weights.Latency / wSum
	wp := weights.Price / wSum

	successScores := scoreSuccess(stats)
	latencyScores := scoreLatency(stats)
	priceScores := scorePrice(stats)

	out := make([]ChannelScore, len(stats))
	for i, s := range stats {
		hd := hasEnoughData(s)
		out[i] = ChannelScore{
			ChannelId:    s.ChannelId,
			HasData:      hd,
			SuccessScore: successScores[i],
			LatencyScore: latencyScores[i],
			PriceScore:   priceScores[i],
			Score:        ws*successScores[i] + wl*latencyScores[i] + wp*priceScores[i],
		}
		if out[i].Score < 0 {
			out[i].Score = 0
		} else if out[i].Score > maxScore {
			out[i].Score = maxScore
		}
	}
	return out
}

// hasEnoughData 判断渠道是否有实时数据参与评分。
//
// 放宽策略（v2）：只要窗口内有任何请求样本（含全失败）或延迟数据，即视为有信号。
// 关键场景：某渠道 15 次全 429 但样本数 <20 时，旧策略判为无数据、写入 dp=0、被
// static priority=100 兜底后又被首选 → 恶性循环。新策略下 TotalCount=15 也算有数据，
// Beta-Binomial 平滑后拿到低分（约 10~25）参与正常排序，不再假装未评分。
func hasEnoughData(s ChannelStat) bool {
	return s.TotalCount > 0 || s.AvgLatencyMs > 0 || s.AvgFirstTokenMs > 0
}

// scoreSuccess 成功率维度评分，用 Beta-Binomial 平滑。
//
// 公式：score = 100 × (SuccessCount + priorAlpha) / (TotalCount + priorAlpha + priorBeta)
//
// 与原硬阈值实现相比的优势：
//   - 无阈值断层：不再有「20 次样本前后分数从 50 突变到 SuccessRate×100」的跳跃
//   - 小样本负面信号可被识别：0/5 失败 → 25 分（旧版给 50 分误判"中等"）
//   - 大样本收敛真实值：100/0 → 97.6 分，逼近 100
//   - 完全无样本仍是 50 分（先验中立），兼容原兜底语义
//
// 对照表（priorAlpha=priorBeta=2.5）：
//
//	0/5   → 25.0    小样本纯失败
//	0/20  → 10.0    大样本纯失败
//	5/5   → 50.0    5:5 中立
//	100/0 → 97.6    大样本纯成功
//	0/0   → 50.0    无数据
func scoreSuccess(stats []ChannelStat) []float64 {
	out := make([]float64, len(stats))
	for i, s := range stats {
		denom := float64(s.TotalCount) + priorAlpha + priorBeta
		if denom <= 0 {
			out[i] = midScore
			continue
		}
		smoothed := (float64(s.SuccessCount) + priorAlpha) / denom
		out[i] = smoothed * maxScore
		if out[i] < 0 {
			out[i] = 0
		} else if out[i] > maxScore {
			out[i] = maxScore
		}
	}
	return out
}

// scoreLatency 延迟维度评分，用中位数 + MAD 做分位数归一化。
//
// 必须先选定统一指标：流式用首字延迟（AvgFirstTokenMs），非流式用端到端延迟（AvgLatencyMs）。
// 二者不可混比——流式 duration 动辄几十秒是生成长度造成的，跟「响应快慢」无关。
// 选哪个由「本批渠道哪个指标有数据的渠道更多」决定（多数派），少数派渠道该指标为 0 时
// 回退到中位分，不参与拉扯。
//
// 选定指标后，以该指标所有有效值的中位数 median 为「基准点」，MAD 为「尺度」：
//
//	raw = 50 + (median - latency) / mad * scale
//
// mad=0（所有渠道该指标接近相同）时退化为：等于中位数得 50，快于中位数得 100，慢得 0。
// 无该指标数据的渠道回退到中位分 50。
//
// scale=25 是经验值：让「偏离一个 MAD」对应 ±25 分，既不至于过度拉平，
// 也不像 min-max 那样把微小差异放大成 0~100。
func scoreLatency(stats []ChannelStat) []float64 {
	out := make([]float64, len(stats))

	// 统计两个指标各自有多少渠道提供有效数据，选多数派
	streamCnt, nonStreamCnt := 0, 0
	for _, s := range stats {
		if s.AvgFirstTokenMs > 0 {
			streamCnt++
		}
		if s.AvgLatencyMs > 0 {
			nonStreamCnt++
		}
	}

	// 选定本次评分的延迟指标；两者都无数据则统一中位分
	var selector func(s ChannelStat) float64
	if streamCnt >= nonStreamCnt && streamCnt > 0 {
		selector = func(s ChannelStat) float64 { return s.AvgFirstTokenMs }
	} else if nonStreamCnt > 0 {
		selector = func(s ChannelStat) float64 { return s.AvgLatencyMs }
	} else {
		for i := range out {
			out[i] = midScore
		}
		return out
	}

	// 收集有效延迟（仅用选定指标）
	latencies := make([]float64, 0, len(stats))
	for _, s := range stats {
		if v := selector(s); v > 0 {
			latencies = append(latencies, v)
		}
	}

	median := percentile(latencies, 0.5)

	// MAD = median(|x - median|)
	devs := make([]float64, len(latencies))
	for i, v := range latencies {
		devs[i] = abs(v - median)
	}
	mad := percentile(devs, 0.5)

	const scale = 25.0
	for i, s := range stats {
		v := selector(s)
		if v <= 0 {
			out[i] = midScore // 该渠道无选定指标数据，回退
			continue
		}
		var raw float64
		if mad == 0 {
			// 所有有效延迟相同或接近：用中位数做硬切分
			if v < median {
				raw = maxScore
			} else if v > median {
				raw = 0
			} else {
				raw = midScore
			}
		} else {
			raw = midScore + (median-v)/mad*scale
		}
		out[i] = clamp(raw, 0, maxScore)
	}
	return out
}

// scorePrice 价格维度评分，绝对比例作固定偏置。
//
// 找到窗口内最低价 minPrice，越接近最低价分越高：
//
//	minPrice  → 100
//	2×minPrice → 50
//	越贵 → 趋近 0
//
// 用 1 - (price-min)/(k*min) 的形式，k 是「价格翻倍对应的衰减系数」。
// 当价格全部为 0（未配置）或全部相同时，统一给中位分 50——价格维度不产生偏好。
//
// 不用 min-max 是因为：价格是静态偏置，不应被一个异常贵的渠道拉成全组归一化
// （min-max 下只要有一个贵 100 倍的渠道，其他渠道价格分都会被压到接近 0）。
func scorePrice(stats []ChannelStat) []float64 {
	out := make([]float64, len(stats))

	// 收集有效（>0）价格
	var prices []float64
	for _, s := range stats {
		if s.UnitPrice > 0 {
			prices = append(prices, s.UnitPrice)
		}
	}

	if len(prices) == 0 {
		// 全部未配置价格：价格维度退化为中位分，不产生偏好
		for i := range out {
			out[i] = midScore
		}
		return out
	}

	minPrice := prices[0]
	allSame := true
	for _, p := range prices[1:] {
		if p < minPrice {
			minPrice = p
		}
		if p != prices[0] {
			allSame = false
		}
	}
	if allSame {
		// 价格全部相同：无偏好
		for i := range out {
			out[i] = midScore
		}
		return out
	}

	// 衰减系数：价格每增加 1 个 minPrice 单位扣 50 分（即「翻倍扣 50」）。
	// minPrice → 100，2×minPrice → 50，3×minPrice → 0，更贵 → clamp 0。
	// 用绝对比例而非 min-max，避免一个异常贵的渠道把全组其他渠道价格分压到 0。
	const decayPerMinUnit = 50.0
	for i, s := range stats {
		if s.UnitPrice <= 0 {
			out[i] = midScore // 未配置价格回退
			continue
		}
		ratio := (s.UnitPrice - minPrice) / minPrice
		out[i] = clamp(maxScore-ratio*decayPerMinUnit, 0, maxScore)
	}
	return out
}

// percentile 计算非空样本的 p 分位数（p∈[0,1]），用线性插值。
// 输入会被复制后排序，不修改调用方的切片。
func percentile(samples []float64, p float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	if len(samples) == 1 {
		return samples[0]
	}
	sorted := make([]float64, len(samples))
	copy(sorted, samples)
	sortFloat64s(sorted)

	// 线性插值法（与 numpy default 一致）
	idx := p * float64(len(sorted)-1)
	lo := int(idx)
	hi := lo + 1
	if hi >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	frac := idx - float64(lo)
	return sorted[lo] + (sorted[hi]-sorted[lo])*frac
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// sortFloat64s 插入排序：评分场景样本量很小（同 model 渠道数通常 <20），
// 避免引入 sort 包依赖。如需扩展可换 sort.Float64s。
func sortFloat64s(a []float64) {
	for i := 1; i < len(a); i++ {
		key := a[i]
		j := i - 1
		for j >= 0 && a[j] > key {
			a[j+1] = a[j]
			j--
		}
		a[j+1] = key
	}
}
