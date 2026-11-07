package common

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// 新配置独立于原 CacheWriteRatio，避免缓存价格配置触发原服务档位选择。
var openAICacheWrite30mRatio = map[string]float64{}
var CacheWrite12hRatio = map[string]float64{}

// GetCacheWrite12hRatio 独立回退到 2.5，不随 30m 倍率变化。
func GetCacheWrite12hRatio(name string) float64 {
	if ratio, ok := LookupCacheWrite12hRatio(name); ok {
		return ratio
	}
	return 2.5
}

func LookupCacheWrite12hRatio(name string) (float64, bool) {
	return lookupPricingValue(&CacheWrite12hRatio, name)
}

func SetOpenAICacheWrite30mRatio(name string, value float64) {
	setPricingValue(&openAICacheWrite30mRatio, name, value)
}

func SetCacheWrite12hRatio(name string, value float64) {
	setPricingValue(&CacheWrite12hRatio, name, value)
}

func CacheWriteRatio2JSONString() string {
	data, _ := marshalPricingMap(&openAICacheWrite30mRatio)
	return string(data)
}

func CacheWrite12hRatio2JSONString() string {
	data, _ := marshalPricingMap(&CacheWrite12hRatio)
	return string(data)
}

// ValidateCacheWriteRatioJSON 在持久化前校验，防止无效价格进入数据库。
// 只约束新增的 OpenAI 缓存配置，沿用其他价格项的原有保存流程。
func ValidateCacheWriteRatioJSON(value string) error {
	var values map[string]*float64
	if err := json.Unmarshal([]byte(value), &values); err != nil {
		return fmt.Errorf("解析缓存创建倍率失败: %w", err)
	}
	if values == nil {
		return fmt.Errorf("缓存创建倍率必须为 JSON 对象")
	}
	for model, ratio := range values {
		if strings.TrimSpace(model) == "" || ratio == nil || *ratio < 0 || math.IsNaN(*ratio) || math.IsInf(*ratio, 0) {
			return fmt.Errorf("模型 %q 的缓存创建倍率必须为非负有限数字，模型名不能为空", model)
		}
	}
	return nil
}

func UpdateCacheWriteRatioByJSONString(value string) error {
	if err := ValidateCacheWriteRatioJSON(value); err != nil {
		return err
	}
	return replacePricingMap(&openAICacheWrite30mRatio, value)
}

func UpdateCacheWrite12hRatioByJSONString(value string) error {
	if err := ValidateCacheWriteRatioJSON(value); err != nil {
		return err
	}
	return replacePricingMap(&CacheWrite12hRatio, value)
}

// GetOpenAICacheWrite30mRatio 仅供 OpenAI 缓存计费读取，缺省保留原模型价格。
func GetOpenAICacheWrite30mRatio(name string) float64 {
	if ratio, ok := LookupOpenAICacheWrite30mRatio(name); ok {
		return ratio
	}
	return GetCacheWriteRatio(name)
}

func LookupOpenAICacheWrite30mRatio(name string) (float64, bool) {
	return lookupPricingValue(&openAICacheWrite30mRatio, name)
}
