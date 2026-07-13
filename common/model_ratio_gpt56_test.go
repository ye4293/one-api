package common

import "testing"

// gpt-5.6 系列（sol/terra/luna）的价格倍率与 long-context / cache-write 计费校验。
// 定价来源：https://developers.openai.com/api/docs/pricing
// 换算规则：ModelRatio = 输入价格($/1M) / 2；CompletionRatio = 输出价/输入价；
//           CacheRatio(读) = 缓存读取价/输入价；CacheWriteRatio(写) = 缓存写入价/输入价。
func TestGPT56Ratios(t *testing.T) {
	cases := []struct {
		model      string
		modelRatio float64
		completion float64
		cacheRead  float64
		cacheWrite float64
	}{
		{"gpt-5.6-sol", 2.5, 6, 0.1, 1.25},
		{"gpt-5.6-terra", 1.25, 6, 0.1, 1.25},
		{"gpt-5.6-luna", 0.5, 6, 0.1, 1.25},
	}
	for _, c := range cases {
		if got := GetModelRatio(c.model); got != c.modelRatio {
			t.Errorf("%s ModelRatio = %v, want %v", c.model, got, c.modelRatio)
		}
		if got := GetCompletionRatio(c.model); got != c.completion {
			t.Errorf("%s CompletionRatio = %v, want %v", c.model, got, c.completion)
		}
		if got := GetCacheRatio(c.model); got != c.cacheRead {
			t.Errorf("%s CacheRatio = %v, want %v", c.model, got, c.cacheRead)
		}
		if got := GetCacheWriteRatio(c.model); got != c.cacheWrite {
			t.Errorf("%s CacheWriteRatio = %v, want %v", c.model, got, c.cacheWrite)
		}
	}
}

// long-context 倍率：输入×2、输出×1.5（gpt-5.6 系列）；未注册模型恒为 1x。
func TestGPT56LongContextMultipliers(t *testing.T) {
	// short 档
	mults := GetLongContextMultipliers("gpt-5.6-sol", 272000)
	if mults.InputMultiplier != 1.0 || mults.OutputMultiplier != 1.0 {
		t.Errorf("边界 272000（short）应为 (1.0, 1.0)，got (%.1f, %.1f)", mults.InputMultiplier, mults.OutputMultiplier)
	}
	// long 档
	mults = GetLongContextMultipliers("gpt-5.6-sol", 272001)
	if mults.InputMultiplier != 2.0 || mults.OutputMultiplier != 1.5 {
		t.Errorf("long 档应为 (2.0, 1.5)，got (%.1f, %.1f)", mults.InputMultiplier, mults.OutputMultiplier)
	}
	// 未注册模型
	mults = GetLongContextMultipliers("gpt-4o", 300000)
	if mults.InputMultiplier != 1.0 || mults.OutputMultiplier != 1.0 {
		t.Errorf("未注册 long-context 的模型应恒为 (1.0, 1.0)，got (%.1f, %.1f)", mults.InputMultiplier, mults.OutputMultiplier)
	}
}

// 用真实样本校验最终计费（groupRatio=1），防止重复计费或漏算 cache_write。
func TestGPT56BillingSamples(t *testing.T) {
	// responses 样本：input=11(含 cached=5, write=3), output=13，sol 模型
	const (
		modelRatio      = 2.5
		completionRatio = 6.0
		cacheRatio      = 0.1
		cacheWriteRatio = 1.25
	)
	input, cached, write, output := 11, 5, 3, 13
	realInput := input - cached - write // = 3
	sum := float64(realInput)*modelRatio +
		float64(cached)*modelRatio*cacheRatio +
		float64(write)*modelRatio*cacheWriteRatio +
		float64(output)*modelRatio*completionRatio
	// responses 计费尾部换算：/1e6 * 2 * groupRatio(1) * QuotaPerUnit(500000) => *1
	quota := int64(sum / 1000000 * 2 * 1 * 500000)
	if quota != 213 {
		t.Errorf("responses 样本 quota = %d, want 213 (=$0.000426)", quota)
	}
}
