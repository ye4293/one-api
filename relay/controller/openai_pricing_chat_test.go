package controller

import (
	"strings"
	"testing"

	"github.com/songquanpeng/one-api/common"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
)

func TestOpenAIChatTierPricing(t *testing.T) {
	for _, tc := range []struct {
		name, tier string
		long       bool
		quota      int64
		configure  func()
	}{
		{"priority", "priority", false, 2580, nil},
		{"fast", "fast", false, 2580, nil},
		{"ultrafast", "ultrafast", false, 2580, nil},
		{"flex", "flex", false, 523, nil},
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
		{"fixed_override", "priority", true, 10000, func() { common.ModelPrice[pricingTestModel+"_priority"] = 0.02 }},
		{"fixed_fallback", "priority", true, 5000, func() { common.ModelPrice[pricingTestModel] = 0.01 }},
		{"fixed_zero", "priority", false, 0, func() { common.ModelPrice[pricingTestModel+"_priority"] = 0 }},
		{"fixed_minus_one", "priority", false, 2580, func() {
			common.ModelPrice[pricingTestModel] = 0.01
			common.ModelPrice[pricingTestModel+"_priority"] = -1
		}},
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
			c := pricingTestContext()
			c.Set(openai.ResponseServiceTierContextKey, tc.tier)
			pre := int64(100)
			if tc.name == "flex" {
				pre = 3000
			} // 最终费用较低时退回预扣差额。
			if err := dbmodel.PreConsumeTokenQuota(1, pre); err != nil {
				t.Fatal(err)
			}
			postConsumeQuota(c.Request.Context(), c, pricingTestUsage(1000, 100, 200, 100), pricingTestMeta(pricingTestModel), &relaymodel.GeneralOpenAIRequest{Model: pricingTestModel, ServiceTier: "priority"}, 1, pre, 1, 1, 1, "", "", 0)
			logs := int64(1)
			if tc.quota == 0 {
				logs = 0
			}
			log := assertPricingConsumption(t, db, tc.quota, logs)
			if logs == 0 {
				return
			}
			if log.ModelName != pricingTestModel {
				t.Fatal(log.ModelName)
			}
			billing := readPricingDetails(t, log, "billingDetails")
			usage := readPricingDetails(t, log, "usageDetails")
			if tc.tier != "" && usage["service_tier"] != tc.tier {
				t.Fatal(usage)
			}
			if tc.name == "priority" || tc.name == "long_priority" {
				for k, want := range map[string]float64{"model_ratio": 2, "completion_ratio": 4, "cache_ratio": 0.2, "cache_read_ratio": 0.2, "cache_write_ratio": 1.5, "cache_creation_ratio": 1.5} {
					if billing[k] != want {
						t.Fatalf("%s=%v, want %v", k, billing[k], want)
					}
				}
			}
			if tc.long && billing["billing_type"] == "token" && !strings.Contains(log.Content, "long输入倍率 2.0，long输出倍率 1.5") {
				t.Fatal(log.Content)
			}
		})
	}
}

func TestOpenAIChatTierPricingPreservesInputFallback(t *testing.T) {
	setupOpenAIPriceMaps(t)
	db := setupServiceTierLogDB(t)
	// 模拟请求前取得输入倍率 1，响应前基础配置改为 9；只命中输出档位时仍用入参 1。
	common.ModelRatio[pricingTestModel] = 9
	common.CompletionRatio[pricingTestModel+"_partial"] = 4
	c := pricingTestContext()
	c.Set(openai.ResponseServiceTierContextKey, "partial")
	postConsumeQuota(c.Request.Context(), c, pricingTestUsage(1000, 100, 200, 100), pricingTestMeta(pricingTestModel), &relaymodel.GeneralOpenAIRequest{Model: pricingTestModel}, 1, 0, 1, 1, 1, "", "", 0)
	log := assertPricingConsumption(t, db, 1245, 1)
	if got := readPricingDetails(t, log, "billingDetails")["model_ratio"]; got != float64(1) {
		t.Fatal(got)
	}
}

func TestOpenAIChatTierPricingGPT6AndMinimum(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		input, output, read, write int
		model, tier                string
		inputRatio, oldRatio       float64
		quota                      int64
	}{
		{"gpt6_priority", 272001, 100, 1000, 1000, "gpt-6-astra", "priority", 2, 1, 1088004},
		{"gpt6_default", 272001, 100, 1000, 1000, "gpt-6-astra", "default", 2, 1, 543152},
		{"gpt6_absent", 272001, 100, 1000, 1000, "gpt-6-astra", "", 2, 1, 543152},
		{"gpt6_unknown", 272001, 100, 1000, 1000, "gpt-6-astra", "unknown", 2, 1, 543152},
		{"gpt6_boundary", 272000, 1000, 0, 0, "gpt-6-astra", "default", 2, 1, 275000},
		{"negative_minimum", 1, 0, 0, 0, pricingTestModel, "priority", -1, 0, 1},
		{"zero_input_disables_minimum", 1, 0, 0, 0, pricingTestModel, "priority", 0, 1, 0},
		{"zero_tokens_after_minimum", 0, 0, 0, 0, pricingTestModel, "priority", 2, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupOpenAIPriceMaps(t)
			db := setupServiceTierLogDB(t)
			setOpenAITestPrice(tc.model, 1, 3, 0.1, 1.25)
			setOpenAITestPrice(tc.model+"_priority", tc.inputRatio, 4, 0.2, 1.5)
			setOpenAITestPrice(tc.model+"_default", 99, 99, 99, 99)
			c := pricingTestContext()
			c.Set(openai.ResponseServiceTierContextKey, tc.tier)
			postConsumeQuota(c.Request.Context(), c, pricingTestUsage(tc.input, tc.output, tc.read, tc.write), pricingTestMeta(tc.model), &relaymodel.GeneralOpenAIRequest{Model: tc.model}, tc.oldRatio, 0, 1, 1, 1, "", "", 0)
			logs := int64(1)
			if tc.quota == 0 {
				logs = 0
			}
			log := assertPricingConsumption(t, db, tc.quota, logs)
			if logs > 0 && tc.read == 0 && tc.write == 0 {
				b := readPricingDetails(t, log, "billingDetails")
				for _, k := range []string{"cached_tokens", "cache_ratio", "cache_write_tokens", "cache_write_ratio"} {
					if _, ok := b[k]; ok {
						t.Fatalf("unexpected %s", k)
					}
				}
			}
		})
	}
}
