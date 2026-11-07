package controller

import "github.com/songquanpeng/one-api/common"

// 缓存配置仅覆盖创建单价，不参与 selectOpenAIPrices 的整档价格选择。
// 保留既有档位覆盖和自动倍率；新增配置不会改变普通输入、输出及读取费用。
func resolveOpenAICacheWritePrices(model string, overrides openAIPriceOverrides) (float64, float64) {
	short := overrides.cacheWriteRatio.resolve(model, common.GetOpenAICacheWrite30mRatio)
	if overrides.cacheWrite30mOverride.set {
		short = overrides.cacheWrite30mOverride.value
	}
	long := overrides.cacheWrite12hOverride.resolve(model, common.GetCacheWrite12hRatio)
	return short, long
}
