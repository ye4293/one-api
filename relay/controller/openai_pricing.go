package controller

import "github.com/songquanpeng/one-api/common"

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
}

func (p openAIPriceOverrides) any() bool {
	return p.modelRatio.set || p.completionRatio.set || p.cacheRatio.set || p.cacheWriteRatio.set || p.modelPrice.set
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

// 仅保存本次已用于计算的倍率，供命中档位分支的消费日志复用。
type openAITokenPrices struct {
	modelRatio, completionRatio, cacheRatio, cacheWriteRatio float64
}
