package dynamicprio

import (
	"math"
	"testing"
)

// approxEq 评分是浮点运算，用相对误差容忍比较。
func approxEq(a, b float64) bool {
	return math.Abs(a-b) < 0.5
}

func TestScoreChannels_Empty(t *testing.T) {
	got := ScoreChannels(nil, Weights{Success: 50, Latency: 30, Price: 20})
	if len(got) != 0 {
		t.Fatalf("空输入应返回空切片，got %v", got)
	}
}

func TestScoreChannels_AllZeroWeights(t *testing.T) {
	// 全 0 权重：退化为无偏好，全部中位分，保证选渠道逻辑仍可工作
	stats := []ChannelStat{
		{ChannelId: 1, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgLatencyMs: 200, UnitPrice: 1},
		{ChannelId: 2, TotalCount: 100, SuccessCount: 50, SuccessRate: 0.5, AvgLatencyMs: 2000, UnitPrice: 10},
	}
	got := ScoreChannels(stats, Weights{})
	for _, s := range got {
		if !approxEq(s.Score, 50) {
			t.Fatalf("全 0 权重应给中位分 50，channel %d got %v", s.ChannelId, s.Score)
		}
	}
}

// 单渠道：延迟/价格维度无相对比较对象，回退中位分；成功率维度用 Beta-Binomial 平滑。
// SuccessScore = 100×(45+2.5)/(50+2.5+2.5) = 47.5/55×100 ≈ 86.36
// 综合分 = 0.5×86.36 + 0.3×50 + 0.2×50 = 43.18 + 15 + 10 = 68.18
func TestScoreChannels_SingleChannel(t *testing.T) {
	stats := []ChannelStat{
		{ChannelId: 1, TotalCount: 50, SuccessCount: 45, SuccessRate: 0.9, AvgLatencyMs: 500, UnitPrice: 2},
	}
	got := ScoreChannels(stats, Weights{Success: 50, Latency: 30, Price: 20})
	if len(got) != 1 {
		t.Fatalf("应返回 1 个结果，got %d", len(got))
	}
	// 延迟/价格维度单渠道无相对对象 → 中位分
	if !approxEq(got[0].LatencyScore, 50) {
		t.Fatalf("单渠道延迟维度应回退中位分 50，got %v", got[0].LatencyScore)
	}
	if !approxEq(got[0].PriceScore, 50) {
		t.Fatalf("单渠道价格维度应回退中位分 50，got %v", got[0].PriceScore)
	}
	if !approxEq(got[0].Score, 68.18) {
		t.Fatalf("单渠道综合分应约 68.18（Beta-Binomial 平滑），got %v", got[0].Score)
	}
}

// 核心反抖动测试：三个渠道延迟 800/810/820ms，min-max 会拉成 100/50/0，
// 本算法应给出接近的分数（差距远小于 100）。
func TestScoreChannels_LatencyNoAmplification(t *testing.T) {
	stats := []ChannelStat{
		{ChannelId: 1, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgLatencyMs: 800, UnitPrice: 1},
		{ChannelId: 2, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgLatencyMs: 810, UnitPrice: 1},
		{ChannelId: 3, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgLatencyMs: 820, UnitPrice: 1},
	}
	got := ScoreChannels(stats, Weights{Latency: 100}) // 只看延迟维度

	// 成功率全相同→100；价格全相同→50；只剩延迟维度拉开。
	// 但延迟仅差 10ms，MAD 很小，分数会被 clamp。关键是三者差距不应接近 100。
	maxDiff := 0.0
	min := math.Inf(1)
	max := math.Inf(-1)
	for _, s := range got {
		if s.Score < min {
			min = s.Score
		}
		if s.Score > max {
			max = s.Score
		}
	}
	maxDiff = max - min
	if maxDiff > 60 {
		t.Fatalf("延迟仅差 10ms 不应被放大成 %v 分差（min-max 会变 100），分数: %+v", maxDiff, got)
	}
	// 最快的（800ms）分数应 ≥ 最慢的（820ms）
	if got[0].LatencyScore < got[2].LatencyScore {
		t.Fatalf("更快的渠道延迟分应更高，got[0]=%v got[2]=%v", got[0].LatencyScore, got[2].LatencyScore)
	}
}

// 成功率小样本 Beta-Binomial 平滑：1 次失败不再打 0 分，也不像旧版回退中位分 50，
// 而是给出反映负面信号的低分。
// SuccessScore(1/0) = 100×(0+2.5)/(1+5) = 41.67
func TestScoreChannels_SmallSampleFallback(t *testing.T) {
	stats := []ChannelStat{
		// 渠道1：大样本，99% 成功率 → SuccessScore = 100×(99+2.5)/(100+5) = 96.67
		{ChannelId: 1, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgLatencyMs: 500, UnitPrice: 1},
		// 渠道2：小样本，1 次请求 1 次失败（成功率 0）→ Beta 平滑给 41.67
		{ChannelId: 2, TotalCount: 1, SuccessCount: 0, SuccessRate: 0.0, AvgLatencyMs: 500, UnitPrice: 1},
	}
	got := ScoreChannels(stats, Weights{Success: 100})

	// 渠道2 成功率维度应给 41.67（小样本负面信号，低于中位但不为 0）
	if !approxEq(got[1].SuccessScore, 41.67) {
		t.Fatalf("小样本渠道 Beta 平滑成功率分应约 41.67，got %v", got[1].SuccessScore)
	}
	// 渠道1 大样本高成功率应接近 96~97
	if got[0].SuccessScore < 95 {
		t.Fatalf("大样本高成功率渠道 SuccessScore 应≥95，got %v", got[0].SuccessScore)
	}
	// 有信号即 HasData=true
	if !got[0].HasData {
		t.Fatalf("大样本渠道应有数据")
	}
	if !got[1].HasData {
		t.Fatalf("渠道2 有 1 次请求样本应标记为有数据（新 hasEnoughData 语义）")
	}
}

// 价格绝对比例偏置：最便宜=100，2×最便宜≈50，异常贵的不应把别人压到 0。
func TestScoreChannels_PriceNoMinMaxAmplification(t *testing.T) {
	stats := []ChannelStat{
		{ChannelId: 1, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgLatencyMs: 500, UnitPrice: 1},   // 最低价
		{ChannelId: 2, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgLatencyMs: 500, UnitPrice: 2},   // 2x
		{ChannelId: 3, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgLatencyMs: 500, UnitPrice: 100}, // 异常贵
	}
	got := ScoreChannels(stats, Weights{Price: 100})

	// 最低价 → 100
	if !approxEq(got[0].PriceScore, 100) {
		t.Fatalf("最低价渠道价格分应为 100，got %v", got[0].PriceScore)
	}
	// 2x → 50
	if !approxEq(got[1].PriceScore, 50) {
		t.Fatalf("2倍价格渠道应为 50，got %v", got[1].PriceScore)
	}
	// 异常贵（100x）→ 应被 clamp 到 0，但关键是渠道1 不受它影响仍得 100
	if got[0].PriceScore < 99 {
		t.Fatalf("异常贵渠道不应拉低最低价渠道分数，渠道1 应≈100，got %v", got[0].PriceScore)
	}
	if got[2].PriceScore != 0 {
		t.Fatalf("100倍价格渠道应 clamp 到 0，got %v", got[2].PriceScore)
	}
}

// 全相同指标：所有渠道应得相同分数（无偏好）。
func TestScoreChannels_AllIdentical(t *testing.T) {
	stats := []ChannelStat{
		{ChannelId: 1, TotalCount: 100, SuccessCount: 90, SuccessRate: 0.9, AvgLatencyMs: 500, UnitPrice: 2},
		{ChannelId: 2, TotalCount: 100, SuccessCount: 90, SuccessRate: 0.9, AvgLatencyMs: 500, UnitPrice: 2},
		{ChannelId: 3, TotalCount: 100, SuccessCount: 90, SuccessRate: 0.9, AvgLatencyMs: 500, UnitPrice: 2},
	}
	got := ScoreChannels(stats, Weights{Success: 50, Latency: 30, Price: 20})

	base := got[0].Score
	for i, s := range got {
		if !approxEq(s.Score, base) {
			t.Fatalf("全相同指标应得相同分数，got[%d]=%v base=%v", i, s.Score, base)
		}
	}
	// 延迟维度全相同 → 中位分 50；价格全相同 → 50；成功率相同 → 90
	if !approxEq(got[0].LatencyScore, 50) {
		t.Fatalf("延迟全相同应得中位分 50，got %v", got[0].LatencyScore)
	}
}

// 综合场景：高分渠道应在多个维度领先。
func TestScoreChannels_IntegratedRanking(t *testing.T) {
	stats := []ChannelStat{
		// 渠道1：成功率高、延迟低、价格低 → 应为最高分
		{ChannelId: 1, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgLatencyMs: 200, UnitPrice: 1},
		// 渠道2：中等
		{ChannelId: 2, TotalCount: 100, SuccessCount: 95, SuccessRate: 0.95, AvgLatencyMs: 500, UnitPrice: 2},
		// 渠道3：成功率低、延迟高、价格高 → 应为最低分
		{ChannelId: 3, TotalCount: 100, SuccessCount: 80, SuccessRate: 0.80, AvgLatencyMs: 2000, UnitPrice: 5},
	}
	got := ScoreChannels(stats, Weights{Success: 50, Latency: 30, Price: 20})

	if got[0].Score <= got[1].Score || got[1].Score <= got[2].Score {
		t.Fatalf("综合排名应 channel1 > channel2 > channel3，got %+v", got)
	}
	if got[0].Score < 70 {
		t.Fatalf("最优渠道综合分应较高，got %v", got[0].Score)
	}
}

// 无数据渠道（窗口内无请求）：延迟和成功率都回退中位分，价格若配置仍生效。
func TestScoreChannels_NoDataChannel(t *testing.T) {
	stats := []ChannelStat{
		{ChannelId: 1, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgLatencyMs: 500, UnitPrice: 2},
		{ChannelId: 2, TotalCount: 0, SuccessCount: 0, SuccessRate: 0, AvgLatencyMs: 0, UnitPrice: 1}, // 无数据
	}
	got := ScoreChannels(stats, Weights{Success: 50, Latency: 30, Price: 20})

	if got[1].HasData {
		t.Fatalf("无数据渠道 HasData 应为 false")
	}
	// 成功率维度回退 50，延迟维度回退 50
	if !approxEq(got[1].SuccessScore, 50) {
		t.Fatalf("无数据渠道成功率分应回退 50，got %v", got[1].SuccessScore)
	}
	if !approxEq(got[1].LatencyScore, 50) {
		t.Fatalf("无数据渠道延迟分应回退 50，got %v", got[1].LatencyScore)
	}
	// 价格仍配置了（1，最低），应得 100
	if !approxEq(got[1].PriceScore, 100) {
		t.Fatalf("无数据但配置了最低价的渠道价格分应为 100，got %v", got[1].PriceScore)
	}
}

// 验证权重归一化：权重和>100 不应让分数膨胀。
func TestScoreChannels_WeightNormalization(t *testing.T) {
	stats := []ChannelStat{
		{ChannelId: 1, TotalCount: 100, SuccessCount: 100, SuccessRate: 1.0, AvgLatencyMs: 100, UnitPrice: 1},
	}
	got := ScoreChannels(stats, Weights{Success: 200, Latency: 100, Price: 100})
	// 单渠道仍应得中位分（无相对对象），但验证不因权重过大报错/膨胀
	if got[0].Score > 100 || got[0].Score < 0 {
		t.Fatalf("分数应 clamp 在 [0,100]，got %v", got[0].Score)
	}
}

// 延迟维度：明确验证「快于中位得分>50，慢于中位得分<50」。
func TestScoreLatency_RelativeRanking(t *testing.T) {
	stats := []ChannelStat{
		{ChannelId: 1, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgLatencyMs: 100, UnitPrice: 1},
		{ChannelId: 2, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgLatencyMs: 500, UnitPrice: 1},
		{ChannelId: 3, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgLatencyMs: 2000, UnitPrice: 1},
	}
	got := ScoreChannels(stats, Weights{Latency: 100})

	// 中位数是 500ms → 渠道2 得 50，渠道1（100）>50，渠道3（2000）<50
	if !approxEq(got[1].LatencyScore, 50) {
		t.Fatalf("中位数渠道延迟分应为 50，got %v", got[1].LatencyScore)
	}
	if got[0].LatencyScore <= 50 {
		t.Fatalf("快于中位数的渠道延迟分应>50，got %v", got[0].LatencyScore)
	}
	if got[2].LatencyScore >= 50 {
		t.Fatalf("慢于中位数的渠道延迟分应<50，got %v", got[2].LatencyScore)
	}
}

func TestPercentile(t *testing.T) {
	cases := []struct {
		samples []float64
		p       float64
		want    float64
	}{
		{[]float64{1}, 0.5, 1},
		{[]float64{1, 2, 3}, 0.5, 2},
		{[]float64{1, 2, 3, 4}, 0.5, 2.5}, // 线性插值
		{[]float64{1, 2, 3, 4}, 0.0, 1},
		{[]float64{1, 2, 3, 4}, 1.0, 4},
		{[]float64{}, 0.5, 0},
	}
	for _, c := range cases {
		if got := percentile(c.samples, c.p); got != c.want {
			t.Fatalf("percentile(%v, %v) = %v, want %v", c.samples, c.p, got, c.want)
		}
	}
}

// 流式场景：用首字延迟（AvgFirstTokenMs）评分，AvgLatencyMs=0 不参与。
// 验证流式场景下首字快慢能正确拉开分数。
func TestScoreLatency_StreamUsesFirstToken(t *testing.T) {
	stats := []ChannelStat{
		{ChannelId: 1, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgFirstTokenMs: 200, AvgLatencyMs: 0, UnitPrice: 1},
		{ChannelId: 2, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgFirstTokenMs: 800, AvgLatencyMs: 0, UnitPrice: 1},
		{ChannelId: 3, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgFirstTokenMs: 2000, AvgLatencyMs: 0, UnitPrice: 1},
	}
	got := ScoreChannels(stats, Weights{Latency: 100})

	// 中位数 800ms → 渠道2 得 50，渠道1（200）>50，渠道3（2000）<50
	if !approxEq(got[1].LatencyScore, 50) {
		t.Fatalf("中位数渠道延迟分应为 50，got %v", got[1].LatencyScore)
	}
	if got[0].LatencyScore <= 50 {
		t.Fatalf("首字更快的渠道延迟分应>50，got %v", got[0].LatencyScore)
	}
	if got[2].LatencyScore >= 50 {
		t.Fatalf("首字更慢的渠道延迟分应<50，got %v", got[2].LatencyScore)
	}
	if got[0].Score <= got[2].Score {
		t.Fatalf("首字快的渠道综合分应更高，got %+v", got)
	}
}

// 核心修正场景：流式渠道的 Duration（几十秒）绝不能用来判慢。
// 渠道1 流式首字 200ms（快），但若误用 Duration=30s 会被判极慢。
// 渠道2 非流式 2s。两者不可混比——多数派是流式，应选首字延迟，
// 渠道1 得高分，渠道2 首字数据缺失回退中位分 50。
func TestScoreLatency_StreamNotPunishedByDuration(t *testing.T) {
	stats := []ChannelStat{
		// 渠道1：流式，首字 200ms，duration 未填（不参与）
		{ChannelId: 1, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgFirstTokenMs: 200, AvgLatencyMs: 0, UnitPrice: 1},
		{ChannelId: 2, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgFirstTokenMs: 1500, AvgLatencyMs: 0, UnitPrice: 1},
		{ChannelId: 3, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgFirstTokenMs: 3000, AvgLatencyMs: 0, UnitPrice: 1},
	}
	got := ScoreChannels(stats, Weights{Latency: 100})

	// 选定首字延迟：200ms 渠道1 应为最高分，3000ms 渠道3 最低
	if got[0].LatencyScore <= got[1].LatencyScore || got[1].LatencyScore <= got[2].LatencyScore {
		t.Fatalf("首字延迟排序应 200<1500<3000 对应分数递减，got %+v", got)
	}
}

// 混合模式：2 个流式 + 1 个非流式。多数派是流式 → 选首字延迟。
// 非流式渠道首字数据为 0，回退中位分 50，不被流式渠道拉扯。
func TestScoreLatency_MixedModeMajorityStream(t *testing.T) {
	stats := []ChannelStat{
		{ChannelId: 1, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgFirstTokenMs: 200, AvgLatencyMs: 0, UnitPrice: 1},   // 流式快
		{ChannelId: 2, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgFirstTokenMs: 1000, AvgLatencyMs: 0, UnitPrice: 1},  // 流式慢
		{ChannelId: 3, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgFirstTokenMs: 0, AvgLatencyMs: 3000, UnitPrice: 1},  // 非流式，首字缺
	}
	got := ScoreChannels(stats, Weights{Latency: 100})

	// 多数派流式 → 选首字延迟。渠道3 首字=0 回退 50。
	if !approxEq(got[2].LatencyScore, 50) {
		t.Fatalf("非流式渠道在流式多数派下首字数据缺失应回退中位分 50，got %v", got[2].LatencyScore)
	}
	// 流式两渠道按首字拉开
	if got[0].LatencyScore <= got[1].LatencyScore {
		t.Fatalf("首字更快的流式渠道延迟分应更高，got[0]=%v got[1]=%v", got[0].LatencyScore, got[1].LatencyScore)
	}
}

// 混合模式：2 个非流式 + 1 个流式。多数派非流式 → 选端到端延迟。
// 流式渠道端到端数据为 0，回退中位分 50。
func TestScoreLatency_MixedModeMajorityNonStream(t *testing.T) {
	stats := []ChannelStat{
		{ChannelId: 1, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgFirstTokenMs: 0, AvgLatencyMs: 500, UnitPrice: 1},   // 非流式快
		{ChannelId: 2, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgFirstTokenMs: 0, AvgLatencyMs: 2000, UnitPrice: 1},  // 非流式慢
		{ChannelId: 3, TotalCount: 100, SuccessCount: 99, SuccessRate: 0.99, AvgFirstTokenMs: 200, AvgLatencyMs: 0, UnitPrice: 1},   // 流式，端到端缺
	}
	got := ScoreChannels(stats, Weights{Latency: 100})

	// 多数派非流式 → 选端到端。渠道3 端到端=0 回退 50。
	if !approxEq(got[2].LatencyScore, 50) {
		t.Fatalf("流式渠道在非流式多数派下端到端缺失应回退中位分 50，got %v", got[2].LatencyScore)
	}
	if got[0].LatencyScore <= got[1].LatencyScore {
		t.Fatalf("更快的非流式渠道延迟分应更高，got[0]=%v got[1]=%v", got[0].LatencyScore, got[1].LatencyScore)
	}
}

// Beta-Binomial 平滑：0/5 全失败小样本应给低分（约 25），不再回退中位分。
// 场景：渠道 window 内 5 次全 429，样本少但信号明确 —— 修 Bug B 的核心验证。
func TestScoreSuccess_BetaSmoothingSmallSampleAllFail(t *testing.T) {
	stats := []ChannelStat{
		{ChannelId: 1, TotalCount: 5, SuccessCount: 0, SuccessRate: 0.0},
	}
	got := ScoreChannels(stats, Weights{Success: 100})
	// (0+2.5)/(5+5) = 0.25 → 25 分
	if !approxEq(got[0].SuccessScore, 25) {
		t.Fatalf("0/5 全失败应约 25 分，got %v", got[0].SuccessScore)
	}
	if !got[0].HasData {
		t.Fatalf("有 5 次样本应视为有数据（新 hasEnoughData）")
	}
}

// Beta-Binomial 平滑：0/20 全失败大样本应给约 10 分（更贴近真实成功率 0%）。
func TestScoreSuccess_BetaSmoothingLargeSampleAllFail(t *testing.T) {
	stats := []ChannelStat{
		{ChannelId: 1, TotalCount: 20, SuccessCount: 0, SuccessRate: 0.0},
	}
	got := ScoreChannels(stats, Weights{Success: 100})
	// (0+2.5)/(20+5) = 0.10 → 10 分
	if !approxEq(got[0].SuccessScore, 10) {
		t.Fatalf("0/20 全失败应约 10 分，got %v", got[0].SuccessScore)
	}
}

// Beta-Binomial 平滑：完全无样本仍是 50 分（兼容原兜底语义，先验中立）。
func TestScoreSuccess_BetaSmoothingNoSample(t *testing.T) {
	stats := []ChannelStat{
		{ChannelId: 1, TotalCount: 0, SuccessCount: 0, SuccessRate: 0.0},
	}
	got := ScoreChannels(stats, Weights{Success: 100})
	// (0+2.5)/(0+5) = 0.5 → 50 分
	if !approxEq(got[0].SuccessScore, 50) {
		t.Fatalf("0 样本应给中位分 50，got %v", got[0].SuccessScore)
	}
	// 完全无请求也无延迟 → HasData=false
	if got[0].HasData {
		t.Fatalf("0 样本无延迟应 HasData=false")
	}
}

// hasEnoughData 边界：延迟维度有数据但成功率维度无 → HasData=true。
func TestHasEnoughData_OnlyLatency(t *testing.T) {
	stats := []ChannelStat{
		{ChannelId: 1, TotalCount: 0, SuccessCount: 0, AvgLatencyMs: 500},
	}
	got := ScoreChannels(stats, Weights{Success: 100})
	if !got[0].HasData {
		t.Fatalf("有延迟数据的渠道应 HasData=true")
	}
}
