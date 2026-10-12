package controller

import (
	"testing"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/relay/channel/openai"
)

func TestOpenAIResponseTierPricing(t *testing.T) {
	for _, tc := range []struct {
		name, tier string
		long       bool
		quota      int64
		configure  func()
	}{
		{"priority", "priority", false, 2580, nil},
		{"fast", "fast", false, 2580, nil},
		{"ultrafast", "ultrafast", false, 2580, nil},
		{"flex", "flex", false, 522, nil},
		{"partial", "partial", false, 2290, nil},
		{"default", "default", false, 1145, nil},
		{"absent", "", false, 1145, nil},
		{"unknown", "unknown", false, 1145, nil},
		{"long_priority", "priority", true, 4760, nil},
		{"long_flex", "flex", true, 995, nil},
		{"long_default", "default", true, 2140, nil},
		{"long_absent", "", true, 2140, nil},
		{"long_unknown", "unknown", true, 2140, nil},
		{"long_partial", "partial", true, 4280, nil},
		{"zero_input", "zero", false, 0, func() { common.ModelRatio[pricingTestModel+"_zero"] = 0 }},
		{"zero_output", "priority", false, 1780, func() { common.CompletionRatio[pricingTestModel+"_priority"] = 0 }},
		{"zero_read", "priority", false, 2500, func() { common.CacheRatio[pricingTestModel+"_priority"] = 0 }},
		{"zero_write", "priority", false, 2280, func() { common.CacheWriteRatio[pricingTestModel+"_priority"] = 0 }},
		{"ignore_fixed", "fixed", false, 1145, func() { common.ModelPrice[pricingTestModel+"_fixed"] = 0.02 }},
		{"quota_per_unit", "priority", false, 5160, func() { config.QuotaPerUnit = 1000000 }},
		{"completion_only", "completion", false, 1245, func() { common.CompletionRatio[pricingTestModel+"_completion"] = 4 }},
		{"read_only", "read", false, 1165, func() { common.CacheRatio[pricingTestModel+"_read"] = 0.2 }},
		{"write_only", "write", false, 1170, func() { common.CacheWriteRatio[pricingTestModel+"_write"] = 1.5 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupOpenAIPriceMaps(t)
			db := setupServiceTierLogDB(t)
			for _, tier := range []string{"priority", "fast", "ultrafast"} {
				setOpenAITestPrice(pricingTestModel+"_"+tier, 2, 4, 0.2, 1.5)
			}
			setOpenAITestPrice(pricingTestModel+"_flex", 0.5, 2, 0.1, 1.25)
			setOpenAITestPrice(pricingTestModel+"_default", 99, 99, 99, 99)
			common.ModelRatio[pricingTestModel+"_partial"] = 2
			if tc.long {
				common.LongContextThreshold[pricingTestModel] = 999
			}
			if tc.configure != nil {
				tc.configure()
			}
			u := pricingTestResponseUsage(1000, 100, 200, 100)
			c := pricingTestContext()
			c.Set(openai.ResponseServiceTierContextKey, tc.tier)
			overrides := lookupOpenAIPriceOverrides(pricingTestModel, tc.tier, openAIResponsePricing)
			quota, cost, prices := calculateOpenaiResponseQuotaWithOverrides(u, pricingTestModel, 1, overrides)
			if quota != tc.quota || cost.ModelName != pricingTestModel {
				t.Fatalf("quota=%d cost=%+v, want quota=%d", quota, cost, tc.quota)
			}
			if overrides.any() {
				// 异步记录前价格变化不能替换本次已选参数，包含部分命中的基础回退值。
				setOpenAITestPrice(pricingTestModel, 9, 9, 9, 9)
				setOpenAITestPrice(pricingTestModel+"_"+tc.tier, 8, 8, 8, 8)
				recordOpenaiResponseConsumptionWithPrices(c.Request.Context(), 1, 1, 1, pricingTestModel, "test", 1000, 100, 1100, 200, 100, quota, "/v1/responses", 1, false, c, u, 0, 1, 1, &prices)
			} else {
				recordOpenaiResponseConsumption(c.Request.Context(), 1, 1, 1, pricingTestModel, "test", 1000, 100, 1100, 200, 100, quota, "/v1/responses", 1, false, c, u, 0, 1, 1)
			}
			log := assertPricingConsumption(t, db, tc.quota, 1)
			billing := readPricingDetails(t, log, "billingDetails")
			for k, want := range map[string]float64{"model_ratio": prices.modelRatio, "completion_ratio": prices.completionRatio, "cache_ratio": prices.cacheRatio, "cache_read_ratio": prices.cacheRatio, "cache_write_ratio": prices.cacheWriteRatio, "cache_creation_ratio": prices.cacheWriteRatio} {
				if billing[k] != want {
					t.Fatalf("%s=%v, want %v", k, billing[k], want)
				}
			}
			if billing["billing_type"] != "token" {
				t.Fatal(billing)
			}
		})
	}
}

func TestOpenAIResponseTierPricingLegacyLogTiming(t *testing.T) {
	setupOpenAIPriceMaps(t)
	db := setupServiceTierLogDB(t)
	u := pricingTestResponseUsage(1000, 100, 200, 100)
	quota, _ := CalculateResponseQuotaFromUsageMetadata(u, pricingTestModel, 1)
	setOpenAITestPrice(pricingTestModel, 9, 4, 0.2, 1.5)
	c := pricingTestContext()
	recordOpenaiResponseConsumption(c.Request.Context(), 1, 1, 1, pricingTestModel, "test", 1000, 100, 1100, 200, 100, quota, "/v1/responses", 1, false, c, u, 0, 1, 1)
	log := assertPricingConsumption(t, db, 1145, 1)
	billing := readPricingDetails(t, log, "billingDetails")
	if billing["model_ratio"] != float64(1) || billing["completion_ratio"] != float64(4) || billing["cache_ratio"] != 0.2 || billing["cache_write_ratio"] != 1.5 {
		t.Fatal(billing)
	}
}

func TestOpenAIResponseTierPricingGPT6LongContext(t *testing.T) {
	for _, name := range []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna", "gpt-7-test", "gpt-10.2-test"} {
		for _, tier := range []string{"priority", "flex", "partial", "default", "", "unconfigured"} {
			t.Run(name+"/"+tier, func(t *testing.T) {
				setupOpenAIPriceMaps(t)
				setOpenAITestPrice(name, 1, 3, 0.1, 1.25)
				setOpenAITestPrice(name+"_priority", 2, 4, 0.2, 1.5)
				setOpenAITestPrice(name+"_flex", 0.5, 2, 0.1, 1.25)
				setOpenAITestPrice(name+"_default", 99, 99, 99, 99)
				common.ModelRatio[name+"_partial"] = 2
				u := pricingTestResponseUsage(272001, 100, 1000, 1000)
				want := int64(543152)
				switch tier {
				case "priority":
					want = 1088004
				case "flex":
					want = 271501
				case "partial":
					want = 1086304
				}
				quota, _, _ := calculateOpenaiResponseQuotaWithOverrides(u, name, 1, lookupOpenAIPriceOverrides(name, tier, openAIResponsePricing))
				if quota != want {
					t.Fatalf("quota=%d, want %d", quota, want)
				}
				// 公开旧函数仍只按基础价格，但也获得新阈值。
				if q, _ := CalculateResponseQuotaFromUsageMetadata(u, name, 1); q != 543152 {
					t.Fatal(q)
				}
			})
		}
	}
	t.Run("input_excludes_output", func(t *testing.T) {
		setupOpenAIPriceMaps(t)
		setOpenAITestPrice("gpt-6-astra", 1, 3, 0.1, 1.25)
		if q, _ := CalculateResponseQuotaFromUsageMetadata(pricingTestResponseUsage(272000, 1000, 0, 0), "gpt-6-astra", 1); q != 275000 {
			t.Fatal(q)
		}
	})
}

// 仅验证设计稿价格快照的换算，不代表已经联网核实官方现价。
func TestOpenAIResponseTierPricingDesignPriceConversion(t *testing.T) {
	for _, tc := range []struct {
		tier                       string
		input, output, read, write float64
		shortQuota, longQuota      int64
	}{
		{"default", 10, 50, 1, 12.5, 6725, 2717260},
		{"priority", 20, 100, 2, 25, 13450, 5434520},
		{"fast", 20, 100, 2, 25, 13450, 5434520},
		{"flex", 5, 25, 0.5, 6.25, 3362, 1358630},
		{"ultrafast", 60, 300, 6, 75, 40350, 16303560},
	} {
		t.Run(tc.tier, func(t *testing.T) {
			setupOpenAIPriceMaps(t)
			const name = "gpt-6-astra"
			setOpenAITestPrice(name, 5, 5, 0.1, 1.25)
			key := name + "_" + tc.tier
			common.ModelRatio[key] = tc.input / 2
			common.CompletionRatio[key] = tc.output / tc.input
			common.CacheRatio[key] = tc.read / tc.input
			// 现有管理接口没有 CacheWriteRatio；刻意缺少档位项，验证基础回退。
			delete(common.CacheWriteRatio, key)
			overrides := lookupOpenAIPriceOverrides(name, tc.tier, openAIResponsePricing)
			short, _, prices := calculateOpenaiResponseQuotaWithOverrides(pricingTestResponseUsage(1000, 100, 200, 100), name, 1, overrides)
			long, _, _ := calculateOpenaiResponseQuotaWithOverrides(pricingTestResponseUsage(272001, 100, 1000, 1000), name, 1, overrides)
			if short != tc.shortQuota || long != tc.longQuota {
				t.Fatalf("short/long=%d/%d, want %d/%d", short, long, tc.shortQuota, tc.longQuota)
			}
			if prices.modelRatio*2 != tc.input || prices.modelRatio*2*prices.completionRatio != tc.output || prices.modelRatio*2*prices.cacheRatio != tc.read || prices.modelRatio*2*prices.cacheWriteRatio != tc.write {
				t.Fatalf("价格换算或缓存写入回退不符: %+v", prices)
			}
		})
	}
}
