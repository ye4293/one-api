package controller

import (
	"encoding/json"
	"maps"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
	"gorm.io/gorm"
)

const pricingTestModel = "tier_test_model"

// 测试只修改克隆的内存价格表，结束后恢复，不触及运行配置。
func setupOpenAIPriceMaps(t *testing.T) {
	t.Helper()
	m, c, r, w, p := common.ModelRatio, common.CompletionRatio, common.CacheRatio, common.CacheWriteRatio, common.ModelPrice
	l, q := common.LongContextThreshold, config.QuotaPerUnit
	g := common.GroupRatio
	common.GroupRatio = maps.Clone(g)
	common.GroupRatio["default"] = 1
	common.ModelRatio, common.CompletionRatio = maps.Clone(m), maps.Clone(c)
	common.CacheRatio, common.CacheWriteRatio, common.ModelPrice = maps.Clone(r), maps.Clone(w), maps.Clone(p)
	common.LongContextThreshold = maps.Clone(l)
	config.QuotaPerUnit = 500000
	t.Cleanup(func() {
		common.ModelRatio, common.CompletionRatio = m, c
		common.CacheRatio, common.CacheWriteRatio, common.ModelPrice = r, w, p
		common.LongContextThreshold, config.QuotaPerUnit = l, q
		common.GroupRatio = g
	})
	setOpenAITestPrice(pricingTestModel, 1, 3, 0.1, 1.25)
	common.ModelPrice[pricingTestModel] = -1
}

func setOpenAITestPrice(key string, input, output, read, write float64) {
	common.ModelRatio[key], common.CompletionRatio[key] = input, output
	common.CacheRatio[key], common.CacheWriteRatio[key] = read, write
}

func pricingTestContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	return c
}

func pricingTestMeta(name string) *util.RelayMeta {
	return &util.RelayMeta{UserId: 1, TokenId: 1, ChannelId: 1, TokenName: "test", Group: "default", OriginModelName: name, ActualModelName: name, ChannelDiscount: 1, UserChannelRatio: 1}
}

func pricingTestUsage(input, output, read, write int) *relaymodel.Usage {
	u := &relaymodel.Usage{PromptTokens: input, CompletionTokens: output, TotalTokens: input + output}
	u.PromptTokensDetails.CachedTokens, u.PromptTokensDetails.CacheWriteTokens = read, write
	return u
}

func pricingTestResponseUsage(input, output, read, write int) *openai.ResponseUsage {
	return &openai.ResponseUsage{InputTokens: input, OutputTokens: output, TotalTokens: input + output, InputTokensDetails: &openai.InputTokensDetails{CachedTokens: read, CacheWriteTokens: write}}
}

func readPricingDetails(t *testing.T, log dbmodel.Log, prefix string) map[string]any {
	t.Helper()
	for _, part := range strings.Split(log.Other, ";") {
		if strings.HasPrefix(part, prefix+":") {
			var result map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(part, prefix+":")), &result); err != nil {
				t.Fatal(err)
			}
			return result
		}
	}
	t.Fatalf("missing %s: %s", prefix, log.Other)
	return nil
}

func assertPricingConsumption(t *testing.T, db *gorm.DB, quota int64, wantLogs int64) dbmodel.Log {
	t.Helper()
	var token dbmodel.Token
	var user dbmodel.User
	var channel dbmodel.Channel
	for _, row := range []any{&token, &user, &channel} {
		if err := db.First(row, 1).Error; err != nil {
			t.Fatal(err)
		}
	}
	if token.RemainQuota != 1000000-quota || token.UsedQuota != quota || user.UsedQuota != quota || channel.UsedQuota != quota || user.RequestCount != int(wantLogs) {
		t.Fatalf("quota=%d token=%d/%d user=%d requests=%d channel=%d", quota, token.RemainQuota, token.UsedQuota, user.UsedQuota, user.RequestCount, channel.UsedQuota)
	}
	var count int64
	db.Model(&dbmodel.Log{}).Count(&count)
	if count != wantLogs {
		t.Fatalf("log count=%d, want %d", count, wantLogs)
	}
	var log dbmodel.Log
	if wantLogs > 0 {
		if err := db.Last(&log).Error; err != nil {
			t.Fatal(err)
		}
		if int64(log.Quota) != quota {
			t.Fatalf("log quota=%d, want %d", log.Quota, quota)
		}
	}
	return log
}

// 这些固定预期先在改动前运行，锁住两个入口的既有分支差异。
func TestOpenAITierPricingCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		input, output, read, write      int
		group, inputRatio, ratio, fixed float64
		chat, responses                 int64
	}{
		{"cached", 1000, 100, 200, 100, 1, 1, 1, -1, 1145, 1145},
		{"uncached", 1000, 100, 0, 0, 1, 1, 1, -1, 1300, 1300},
		{"rounding", 1, 0, 0, 0, 0.5, 1, 0.5, -1, 1, 0},
		{"invalid_cache", 10, 2, 8, 8, 1, 1, 1, -1, 17, 26},
		{"zero_tokens", 0, 0, 0, 0, 1, 1, 1, -1, 0, 0},
		{"minimum", 1, 0, 0, 0, 1, 0, 1, -1, 1, 0},
		{"zero_ratio", 1, 0, 0, 0, 1, 0, 0, -1, 0, 0},
		{"fixed", 1000, 100, 200, 100, 1, 1, 1, 0.01, 5000, 1145},
		{"fixed_zero", 1000, 100, 200, 100, 1, 1, 1, 0, 0, 1145},
	} {
		for _, tier := range []string{"", "default", "unconfigured"} {
			t.Run(tc.name+"/"+tier, func(t *testing.T) {
				setupOpenAIPriceMaps(t)
				db := setupServiceTierLogDB(t)
				common.ModelRatio[pricingTestModel] = tc.inputRatio
				common.ModelPrice[pricingTestModel] = tc.fixed
				c := pricingTestContext()
				c.Set(openai.ResponseServiceTierContextKey, tier)
				if err := dbmodel.PreConsumeTokenQuota(1, 100); err != nil {
					t.Fatal(err)
				}
				postConsumeQuota(c.Request.Context(), c, pricingTestUsage(tc.input, tc.output, tc.read, tc.write), pricingTestMeta(pricingTestModel), &relaymodel.GeneralOpenAIRequest{Model: pricingTestModel, ServiceTier: "priority"}, tc.ratio, 100, tc.inputRatio, tc.group, 1, "", "", 0)
				logs := int64(1)
				if tc.chat == 0 {
					logs = 0
				}
				log := assertPricingConsumption(t, db, tc.chat, logs)
				if logs > 0 {
					details := readPricingDetails(t, log, "usageDetails")
					if tier == "" {
						if _, ok := details["service_tier"]; ok {
							t.Fatal("unexpected tier")
						}
					} else if details["service_tier"] != tier {
						t.Fatal(details)
					}
				}
				quota, _ := CalculateResponseQuotaFromUsageMetadata(pricingTestResponseUsage(tc.input, tc.output, tc.read, tc.write), pricingTestModel, tc.group)
				if quota != tc.responses {
					t.Fatalf("Responses quota=%d, want %d", quota, tc.responses)
				}
			})
		}
	}
	t.Run("nil_usage", func(t *testing.T) {
		setupOpenAIPriceMaps(t)
		db := setupServiceTierLogDB(t)
		c := pricingTestContext()
		postConsumeQuota(c.Request.Context(), c, nil, pricingTestMeta(pricingTestModel), &relaymodel.GeneralOpenAIRequest{Model: pricingTestModel}, 1, 0, 1, 1, 1, "", "", 0)
		assertPricingConsumption(t, db, 0, 0)
		if quota, _ := CalculateResponseQuotaFromUsageMetadata(nil, pricingTestModel, 1); quota != 0 {
			t.Fatal(quota)
		}
	})
}

func TestOpenAIPriceOverrides(t *testing.T) {
	setupOpenAIPriceMaps(t)
	for _, tier := range []string{"priority", "fast", "flex", "ultrafast", "Fast", "Standard", "auto", "future_tier", "default"} {
		setOpenAITestPrice(pricingTestModel+"_"+tier, 2, 4, 0.2, 1.5)
	}
	common.ModelRatio[pricingTestModel+"_zero"] = 0
	common.ModelPrice[pricingTestModel+"_fixed"] = -1
	for _, entry := range []openAIPricingEntry{openAIChatPricing, openAIResponsePricing} {
		for _, tier := range []string{"", "default", "unconfigured", "PRIORITY", " priority"} {
			if got := lookupOpenAIPriceOverrides(pricingTestModel, tier, entry); got.any() {
				t.Fatalf("unexpected override for %q: %+v", tier, got)
			}
		}
		for _, tier := range []string{"priority", "fast", "flex", "ultrafast", "Fast", "Standard", "auto", "future_tier"} {
			got := lookupOpenAIPriceOverrides(pricingTestModel, tier, entry)
			if !got.any() || got.modelRatio != (openAIPriceOverride{2, true}) || got.completionRatio != (openAIPriceOverride{4, true}) || got.cacheRatio != (openAIPriceOverride{0.2, true}) || got.cacheWriteRatio != (openAIPriceOverride{1.5, true}) || got.modelPrice.set {
				t.Fatalf("%s: %+v", tier, got)
			}
		}
		got := lookupOpenAIPriceOverrides(pricingTestModel, "zero", entry)
		if !got.any() || !got.modelRatio.set || got.modelRatio.value != 0 || got.completionRatio.set {
			t.Fatal(got)
		}
		got = lookupOpenAIPriceOverrides(pricingTestModel, "fixed", entry)
		if got.any() != (entry == openAIChatPricing) {
			t.Fatal(got)
		}
	}
	delete(common.ModelRatio, pricingTestModel+"_fast")
	delete(common.CompletionRatio, pricingTestModel+"_fast")
	delete(common.CacheRatio, pricingTestModel+"_fast")
	delete(common.CacheWriteRatio, pricingTestModel+"_fast")
	if got := lookupOpenAIPriceOverrides(pricingTestModel, "fast", openAIChatPricing); got.any() {
		t.Fatal("fast must not fall back to priority")
	}
	if got := lookupOpenAIPriceOverrides("unknown_model", "priority", openAIChatPricing); got.any() {
		t.Fatal("getter default must not count as a match")
	}
	for _, prices := range []map[string]float64{common.ModelRatio, common.CompletionRatio, common.CacheRatio, common.CacheWriteRatio, common.ModelPrice} {
		prices[pricingTestModel+"_all_zero"] = 0
	}
	got := lookupOpenAIPriceOverrides(pricingTestModel, "all_zero", openAIChatPricing)
	for _, price := range []openAIPriceOverride{got.modelRatio, got.completionRatio, got.cacheRatio, got.cacheWriteRatio, got.modelPrice} {
		if !price.set || price.value != 0 {
			t.Fatal(got)
		}
		if v := price.resolve(pricingTestModel, func(string) float64 { t.Fatal("matched zero invoked fallback"); return 9 }); v != 0 {
			t.Fatal(v)
		}
	}
}
