package controller

import (
	"math"
	"testing"

	"github.com/songquanpeng/one-api/common"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
)

const defaultTierTestModel = "gpt-tier-default-test"

func setupDefaultTierPrices(t *testing.T) {
	t.Helper()
	setupOpenAIPriceMaps(t)
	setOpenAITestPrice(defaultTierTestModel, 1, 3, 0.1, 1.25)
	common.ModelPrice[defaultTierTestModel] = -1
}

func TestOpenAIDefaultTierSelection(t *testing.T) {
	for _, tc := range []struct {
		name, model string
		channel     int
		configure   func()
		want        float64
	}{
		{"openai", defaultTierTestModel, common.ChannelTypeOpenAI, nil, 2},
		{"reasoning", "o3-mini", common.ChannelTypeOpenAI, nil, 2},
		{"chatgpt", "chatgpt-test", common.ChannelTypeOpenAI, nil, 2},
		{"compatible_model", "qwen-plus", common.ChannelTypeOpenAI, nil, 0},
		{"compatible_channel", defaultTierTestModel, common.ChannelTypeAli, nil, 0},
		{"azure", defaultTierTestModel, common.ChannelTypeAzure, nil, 0},
		{"unknown_standard_price", defaultTierTestModel, common.ChannelTypeOpenAI, func() { delete(common.ModelRatio, defaultTierTestModel) }, 0},
		{"default_standard_price", defaultTierTestModel, common.ChannelTypeOpenAI, func() {
			delete(common.ModelRatio, defaultTierTestModel)
			common.DefaultModelPrice[defaultTierTestModel] = 1
		}, 2},
		{"zero_standard_price", defaultTierTestModel, common.ChannelTypeOpenAI, func() { common.ModelRatio[defaultTierTestModel] = 0 }, 2},
		{"negative_standard_price", defaultTierTestModel, common.ChannelTypeOpenAI, func() { common.ModelRatio[defaultTierTestModel] = -1 }, 0},
		{"nan_standard_price", defaultTierTestModel, common.ChannelTypeOpenAI, func() { common.ModelRatio[defaultTierTestModel] = math.NaN() }, 0},
		{"infinite_standard_price", defaultTierTestModel, common.ChannelTypeOpenAI, func() { common.ModelRatio[defaultTierTestModel] = math.Inf(1) }, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupDefaultTierPrices(t)
			old, exists := common.DefaultModelPrice[defaultTierTestModel]
			t.Cleanup(func() {
				if exists {
					common.DefaultModelPrice[defaultTierTestModel] = old
				} else {
					delete(common.DefaultModelPrice, defaultTierTestModel)
				}
			})
			setOpenAITestPrice(tc.model, 1, 3, 0.1, 1.25)
			if tc.configure != nil {
				tc.configure()
			}
			meta := pricingTestMeta(tc.model)
			meta.ChannelType = tc.channel
			for _, entry := range []openAIPricingEntry{openAIChatPricing, openAIResponsePricing} {
				got := selectOpenAIPrices(tc.model, "priority", entry, meta)
				if got.any() || got.defaultMultiplier != tc.want {
					t.Fatalf("entry=%d: %+v, want multiplier %v", entry, got, tc.want)
				}
				// 自动倍率范围限制不影响兼容模型/渠道已有的显式配置，包括零值。
				common.CacheRatio[tc.model+"_priority"] = 0
				got = selectOpenAIPrices(tc.model, "priority", entry, meta)
				if !got.cacheRatio.set || got.cacheRatio.value != 0 || got.defaultMultiplier != 0 {
					t.Fatal(got)
				}
				delete(common.CacheRatio, tc.model+"_priority")
			}
		})
	}
	t.Run("exact_tier_and_mapped_model", func(t *testing.T) {
		setupDefaultTierPrices(t)
		meta := pricingTestMeta(defaultTierTestModel)
		meta.ChannelType = common.ChannelTypeOpenAI
		meta.OriginModelName = "public-model-alias"
		common.ModelRatio[defaultTierTestModel+"_priority"] = 9
		if got := selectOpenAIPrices(defaultTierTestModel, "fast", openAIChatPricing, meta); got.any() || got.defaultMultiplier != 2 {
			t.Fatal(got)
		}
		for _, tier := range []string{"", "default", "auto", "future", "Fast", " priority"} {
			if got := selectOpenAIPrices(defaultTierTestModel, tier, openAIChatPricing, meta); got.applies() {
				t.Fatalf("%q: %+v", tier, got)
			}
		}
		meta.ActualModelName = "qwen-plus"
		if got := selectOpenAIPrices(defaultTierTestModel, "fast", openAIResponsePricing, meta); got.applies() {
			t.Fatal(got)
		}
	})
}

func TestOpenAIDefaultTierSettlement(t *testing.T) {
	for _, tc := range []struct {
		name, tier, source     string
		group                  float64
		chat, responses        int64
		multiplier, inputRatio float64
		configure              func()
	}{
		{"flex", "flex", "default_multiplier", 1, 573, 572, 0.5, 0.5, nil},
		{"fast", "fast", "default_multiplier", 1, 2290, 2290, 2, 2, nil},
		{"priority", "priority", "default_multiplier", 1, 2290, 2290, 2, 2, nil},
		{"ultrafast", "ultrafast", "default_multiplier", 1, 6870, 6870, 6, 6, nil},
		{"default", "default", "base", 1, 1145, 1145, 0, 1, nil},
		{"missing", "", "base", 1, 1145, 1145, 0, 1, nil},
		{"unknown", "future", "base", 1, 1145, 1145, 0, 1, nil},
		{"discount", "fast", "default_multiplier", 0.5, 1145, 1145, 2, 2, nil},
		{"long", "fast", "default_multiplier", 1, 4280, 4280, 2, 2, func() { common.LongContextThreshold[defaultTierTestModel] = 999 }},
		{"partial_input", "fast", "explicit", 1, 3435, 3435, 0, 3, func() { common.ModelRatio[defaultTierTestModel+"_fast"] = 3 }},
		{"partial_output", "fast", "explicit", 1, 1245, 1245, 0, 1, func() { common.CompletionRatio[defaultTierTestModel+"_fast"] = 4 }},
		{"partial_cache", "fast", "explicit", 1, 1165, 1165, 0, 1, func() { common.CacheRatio[defaultTierTestModel+"_fast"] = 0.2 }},
		{"partial_write", "fast", "explicit", 1, 1170, 1170, 0, 1, func() { common.CacheWriteRatio[defaultTierTestModel+"_fast"] = 1.5 }},
		{"explicit_zero", "fast", "explicit", 1, 0, 0, 0, 0, func() { common.ModelRatio[defaultTierTestModel+"_fast"] = 0 }},
		{"zero_base", "fast", "default_multiplier", 1, 0, 0, 2, 0, func() { common.ModelRatio[defaultTierTestModel] = 0 }},
	} {
		for _, entry := range []openAIPricingEntry{openAIChatPricing, openAIResponsePricing} {
			entryName := "chat"
			if entry == openAIResponsePricing {
				entryName = "responses"
			}
			t.Run(tc.name+"/"+entryName, func(t *testing.T) {
				setupDefaultTierPrices(t)
				db := setupServiceTierLogDB(t)
				if tc.configure != nil {
					tc.configure()
				}
				meta := pricingTestMeta(defaultTierTestModel)
				meta.ChannelType = common.ChannelTypeOpenAI
				c := pricingTestContext()
				c.Set(openai.ResponseServiceTierContextKey, tc.tier)
				want, logs := tc.chat, int64(1)
				if entry == openAIChatPricing {
					if err := dbmodel.PreConsumeTokenQuota(1, 3000); err != nil {
						t.Fatal(err)
					}
					base := common.GetModelRatio(defaultTierTestModel)
					postConsumeQuota(c.Request.Context(), c, pricingTestUsage(1000, 100, 200, 100), meta, &relaymodel.GeneralOpenAIRequest{Model: defaultTierTestModel, ServiceTier: "ultrafast"}, base*tc.group, 3000, base, tc.group, 1, "", "", 0)
					if want == 0 {
						logs = 0
					}
				} else {
					want = tc.responses
					u := pricingTestResponseUsage(1000, 100, 200, 100)
					selection := selectOpenAIPrices(defaultTierTestModel, tc.tier, entry, meta)
					quota, _, prices := calculateOpenaiResponseQuotaWithOverrides(u, defaultTierTestModel, tc.group, selection)
					if quota != want {
						t.Fatalf("quota=%d, want %d", quota, want)
					}
					// 记录前修改基础价/档位价，验证日志只使用本次计算的价格及来源。
					setOpenAITestPrice(defaultTierTestModel, 9, 9, 9, 9)
					setOpenAITestPrice(defaultTierTestModel+"_"+tc.tier, 8, 8, 8, 8)
					recordOpenaiResponseConsumptionWithPrices(c.Request.Context(), 1, 1, 1, defaultTierTestModel, "test", 1000, 100, 1100, 200, 100, quota, "/v1/responses", 1, false, c, u, 0, tc.group, 1, &prices)
				}
				log := assertPricingConsumption(t, db, want, logs)
				if logs == 0 {
					return
				}
				b := readPricingDetails(t, log, "billingDetails")
				if b["service_tier_price_source"] != tc.source || b["model_ratio"] != tc.inputRatio {
					t.Fatal(b)
				}
				if tc.multiplier > 0 {
					if b["service_tier_multiplier"] != tc.multiplier || b["completion_ratio"] != float64(3) || b["cache_ratio"] != 0.1 || b["cache_write_ratio"] != 1.25 {
						t.Fatal(b)
					}
				} else if _, exists := b["service_tier_multiplier"]; exists {
					t.Fatal(b)
				}
			})
		}
	}
}

func TestOpenAIDefaultTierChatFixedPrice(t *testing.T) {
	for _, tc := range []struct {
		name     string
		base     float64
		override *float64
		quota    int64
		source   string
	}{
		{"fixed", 0.01, nil, 10000, "default_multiplier"},
		{"fixed_zero", 0, nil, 0, "default_multiplier"},
		{"token_sentinel", -1, nil, 2290, "default_multiplier"},
		{"explicit_fixed", 0.01, floatPtr(0.03), 15000, "explicit"},
		{"explicit_zero", 0.01, floatPtr(0), 0, "explicit"},
		{"explicit_token_sentinel", 0.01, floatPtr(-1), 1145, "explicit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupDefaultTierPrices(t)
			db := setupServiceTierLogDB(t)
			common.ModelPrice[defaultTierTestModel] = tc.base
			common.LongContextThreshold[defaultTierTestModel] = 999
			if tc.override != nil {
				common.ModelPrice[defaultTierTestModel+"_fast"] = *tc.override
			}
			// 固定价不乘长上下文系数；token 用例使用普通上下文。
			if tc.base == -1 || (tc.override != nil && *tc.override == -1) {
				delete(common.LongContextThreshold, defaultTierTestModel)
			}
			meta := pricingTestMeta(defaultTierTestModel)
			meta.ChannelType = common.ChannelTypeOpenAI
			c := pricingTestContext()
			c.Set(openai.ResponseServiceTierContextKey, "fast")
			postConsumeQuota(c.Request.Context(), c, pricingTestUsage(1000, 100, 200, 100), meta, &relaymodel.GeneralOpenAIRequest{Model: defaultTierTestModel}, 1, 0, 1, 1, 1, "", "", 0)
			logs := int64(1)
			if tc.quota == 0 {
				logs = 0
			}
			log := assertPricingConsumption(t, db, tc.quota, logs)
			if logs > 0 && readPricingDetails(t, log, "billingDetails")["service_tier_price_source"] != tc.source {
				t.Fatal(log.Other)
			}
			// Responses 不读取按次配置，始终以 token 基础价应用默认倍率。
			delete(common.LongContextThreshold, defaultTierTestModel)
			p := selectOpenAIPrices(defaultTierTestModel, "fast", openAIResponsePricing, meta)
			if q, _, _ := calculateOpenaiResponseQuotaWithOverrides(pricingTestResponseUsage(1000, 100, 200, 100), defaultTierTestModel, 1, p); q != 2290 {
				t.Fatal(q)
			}
		})
	}
}

func floatPtr(v float64) *float64 { return &v }

func TestOpenAIDefaultTierRoundingAndZeroUsage(t *testing.T) {
	for _, tc := range []struct {
		name, tier                 string
		input, output, read, write int
		chat, responses            int64
	}{
		{"round_once", "flex", 1, 0, 0, 0, 1, 0},
		{"zero_usage", "ultrafast", 0, 0, 0, 0, 0, 0},
		{"uncached", "priority", 1000, 100, 0, 0, 2600, 2600},
		// Responses 原始金额 26.8，先乘 2 再截断为 53，不能将已截断的 26 乘 2。
		{"invalid_cache", "priority", 10, 2, 8, 8, 34, 53},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupDefaultTierPrices(t)
			db := setupServiceTierLogDB(t)
			meta := pricingTestMeta(defaultTierTestModel)
			meta.ChannelType = common.ChannelTypeOpenAI
			c := pricingTestContext()
			c.Set(openai.ResponseServiceTierContextKey, tc.tier)
			postConsumeQuota(c.Request.Context(), c, pricingTestUsage(tc.input, tc.output, tc.read, tc.write), meta, &relaymodel.GeneralOpenAIRequest{Model: defaultTierTestModel}, 1, 0, 1, 1, 1, "", "", 0)
			logs := int64(1)
			if tc.chat == 0 {
				logs = 0
			}
			assertPricingConsumption(t, db, tc.chat, logs)
			p := selectOpenAIPrices(defaultTierTestModel, tc.tier, openAIResponsePricing, meta)
			q, _, _ := calculateOpenaiResponseQuotaWithOverrides(pricingTestResponseUsage(tc.input, tc.output, tc.read, tc.write), defaultTierTestModel, 1, p)
			if q != tc.responses {
				t.Fatalf("Responses quota=%d, want %d", q, tc.responses)
			}
		})
	}
}

func TestOpenAIDefaultTierChatPreservesInputSnapshot(t *testing.T) {
	for _, base := range []float64{0, 1} {
		t.Run(map[float64]string{0: "zero_disables_minimum", 1: "request_price"}[base], func(t *testing.T) {
			setupDefaultTierPrices(t)
			db := setupServiceTierLogDB(t)
			// 请求时输入倍率是 base，响应前变为 9。仍用请求时的值乘 2。
			common.ModelRatio[defaultTierTestModel] = 9
			meta := pricingTestMeta(defaultTierTestModel)
			meta.ChannelType = common.ChannelTypeOpenAI
			c := pricingTestContext()
			c.Set(openai.ResponseServiceTierContextKey, "fast")
			postConsumeQuota(c.Request.Context(), c, pricingTestUsage(1000, 100, 0, 0), meta, &relaymodel.GeneralOpenAIRequest{Model: defaultTierTestModel}, 1, 0, base, 1, 1, "", "", 0)
			logs := int64(1)
			if base == 0 {
				logs = 0
			}
			log := assertPricingConsumption(t, db, int64(base*2600), logs)
			if logs > 0 && readPricingDetails(t, log, "billingDetails")["model_ratio"] != float64(2) {
				t.Fatal(log.Other)
			}
		})
	}
}
