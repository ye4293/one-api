package controller

import (
	"math"
	"regexp"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/relay/util"
)

type openAIPricingEntry uint8

const (
	openAIChatPricing openAIPricingEntry = iota
	openAIResponsePricing
)

// set 单独保存配置是否存在，显式配置 0 仍然有效。
type openAIPriceOverride struct {
	value float64
	set   bool
}

func (p openAIPriceOverride) resolve(model string, fallback func(string) float64) float64 {
	if p.set {
		return p.value
	}
	return fallback(model)
}

type openAIPriceOverrides struct {
	modelRatio, completionRatio, cacheRatio, cacheWriteRatio, modelPrice openAIPriceOverride
	// 仅在该入口完全没有档位配置时启用；0 表示不使用默认倍率。
	defaultMultiplier float64
}

func (p openAIPriceOverrides) any() bool {
	return p.modelRatio.set || p.completionRatio.set || p.cacheRatio.set || p.cacheWriteRatio.set || p.modelPrice.set
}

func (p openAIPriceOverrides) applies() bool {
	return p.any() || p.defaultMultiplier > 0
}

// 平台默认计费策略；显式档位配置仍按响应原值查找，fast/priority 不互相回退。
func defaultOpenAIServiceTierMultiplier(tier string) float64 {
	switch tier {
	case "flex":
		return 0.5
	case "fast", "priority":
		return 2
	case "ultrafast":
		return 6
	default:
		return 0
	}
}

var openAIDefaultTierModelPattern = regexp.MustCompile(`^(gpt-|chatgpt-|o[1-9][0-9]*(?:$|-))`)

// 自动倍率只用于 OpenAI 渠道的已知模型系列；兼容渠道的显式档位配置继续生效。
// model 是入口原有的计费模型，meta 中的实际模型仅用于判断自动倍率适用范围。
func selectOpenAIPrices(model, responseTier string, entry openAIPricingEntry, meta *util.RelayMeta) openAIPriceOverrides {
	p := lookupOpenAIPriceOverrides(model, responseTier, entry)
	multiplier := defaultOpenAIServiceTierMultiplier(responseTier)
	if p.any() || multiplier == 0 || meta == nil || meta.ChannelType != common.ChannelTypeOpenAI ||
		!openAIDefaultTierModelPattern.MatchString(meta.BillingModelName()) {
		return p
	}
	// 不把未知模型 getter 的通用兜底值当成已配置的标准价。
	base, configured := common.ModelRatio[model]
	if !configured {
		base, configured = common.DefaultModelPrice[model]
	}
	if entry == openAIChatPricing {
		if fixedPrice := common.GetModelPrice(model, false); fixedPrice != -1 {
			base, configured = fixedPrice, true
		}
	}
	if configured && base >= 0 && !math.IsNaN(base) && !math.IsInf(base, 0) {
		p.defaultMultiplier = multiplier
	}
	return p
}

// 只精确查响应档位键；回退由各结算入口在原取价位置执行。
func lookupOpenAIPriceOverrides(model, responseTier string, entry openAIPricingEntry) (p openAIPriceOverrides) {
	if responseTier == "" || responseTier == "default" {
		return p
	}
	key := model + "_" + responseTier
	p.modelRatio.value, p.modelRatio.set = common.ModelRatio[key]
	p.completionRatio.value, p.completionRatio.set = common.CompletionRatio[key]
	p.cacheRatio.value, p.cacheRatio.set = common.CacheRatio[key]
	p.cacheWriteRatio.value, p.cacheWriteRatio.set = common.CacheWriteRatio[key]
	if entry == openAIChatPricing {
		p.modelPrice.value, p.modelPrice.set = common.ModelPrice[key]
	}
	return p
}

type openAITierPricingInfo struct {
	source     string
	multiplier float64
}

func (p openAIPriceOverrides) pricingInfo() openAITierPricingInfo {
	if p.any() {
		return openAITierPricingInfo{source: "explicit"}
	}
	if p.defaultMultiplier > 0 {
		return openAITierPricingInfo{source: "default_multiplier", multiplier: p.defaultMultiplier}
	}
	return openAITierPricingInfo{source: "base"}
}

func (p openAITierPricingInfo) appendBillingDetails(details map[string]interface{}) {
	details["service_tier_price_source"] = p.source
	if p.multiplier > 0 {
		details["service_tier_multiplier"] = p.multiplier
	}
}

// 保存本次已用于计算的倍率及来源，供异步消费日志复用。
type openAITokenPrices struct {
	modelRatio, completionRatio, cacheRatio, cacheWriteRatio float64
	tierPricing                                              openAITierPricingInfo
}
