package common

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"

	"github.com/songquanpeng/one-api/common/config"
)

// 计费类型常量
const (
	PricingTypePerSecond = "per_second" // 按秒计费
	PricingTypeFixed     = "fixed"      // 固定价格
)

// VideoPricingRule 视频定价规则
type VideoPricingRule struct {
	Model       string  `json:"model"`        // 模型名或通配符 (如 wan*, kling-v1)
	Type        string  `json:"type"`         // 类型: image-to-video, text-to-video, *
	Mode        string  `json:"mode"`         // 模式: standard, professional, *
	Duration    string  `json:"duration"`     // 时长: 5, 10, 15, *
	Resolution  string  `json:"resolution"`   // 分辨率: 480P, 720P, 1080P, *
	PricingType string  `json:"pricing_type"` // per_second 或 fixed
	Price       float64 `json:"price"`        // 价格
	Currency    string  `json:"currency"`     // 货币: USD, CNY
	Priority    int     `json:"priority"`     // 优先级，数字越大优先级越高
}

// 内存缓存
var VideoPricingRules = []VideoPricingRule{}
var videoPricingMutex sync.RWMutex

// 默认视频定价规则（空列表，通过后台配置）
var DefaultVideoPricingRules = []VideoPricingRule{}

func init() {
	VideoPricingRules = make([]VideoPricingRule, len(DefaultVideoPricingRules))
	copy(VideoPricingRules, DefaultVideoPricingRules)
}

// VideoPricingRules2JSONString 转换为JSON字符串（给 OptionMap 用）
func VideoPricingRules2JSONString() string {
	videoPricingMutex.RLock()
	defer videoPricingMutex.RUnlock()
	data, _ := json.Marshal(VideoPricingRules)
	return string(data)
}

// UpdateVideoPricingRulesByJSONString 从JSON更新规则（数据库加载时调用）
func UpdateVideoPricingRulesByJSONString(jsonStr string) error {
	var rules []VideoPricingRule
	if err := json.Unmarshal([]byte(jsonStr), &rules); err != nil {
		log.Printf("[VideoPricing] 解析规则JSON失败: %v", err)
		return err
	}
	videoPricingMutex.Lock()
	defer videoPricingMutex.Unlock()
	VideoPricingRules = rules
	log.Printf("[VideoPricing] 成功加载 %d 条视频计费规则", len(rules))
	return nil
}

// AddNewMissingVideoPricingRules 添加缺失的默认规则
func AddNewMissingVideoPricingRules(oldRules string) string {
	var rules []VideoPricingRule
	if err := json.Unmarshal([]byte(oldRules), &rules); err != nil {
		// 如果解析失败，返回默认规则
		data, _ := json.Marshal(DefaultVideoPricingRules)
		return string(data)
	}

	// 检查默认规则是否存在，不存在则添加
	existingKeys := make(map[string]bool)
	for _, r := range rules {
		key := fmt.Sprintf("%s:%s:%s:%s:%s", r.Model, r.Type, r.Mode, r.Duration, r.Resolution)
		existingKeys[key] = true
	}
	for _, def := range DefaultVideoPricingRules {
		key := fmt.Sprintf("%s:%s:%s:%s:%s", def.Model, def.Type, def.Mode, def.Duration, def.Resolution)
		if !existingKeys[key] {
			rules = append(rules, def)
		}
	}
	data, _ := json.Marshal(rules)
	return string(data)
}

// matchPattern 通配符匹配
// pattern: 规则中的模式 (如 "wan*", "standard", "*", "" 空字符串也当作通配符)
// value: 实际值 (如 "wan2.5-i2v", "standard", "")
func matchPattern(pattern, value string) bool {
	// pattern 是通配符或空字符串，匹配任何值（包括空）
	// 空字符串表示"不关心这个字段"，等同于 "*"
	if pattern == "*" || pattern == "" {
		return true
	}

	// value 是空值，只能匹配通配符（上面已处理），不匹配精确值
	if value == "" {
		return false
	}

	// 前缀通配符 (如 wan*)
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix))
	}

	// 精确匹配（忽略大小写）
	return strings.EqualFold(pattern, value)
}

// CalculateVideoQuota 计算视频费用
// 参数: model, videoType, mode, duration(秒), resolution
// 返回: quota (内部积分单位)
func CalculateVideoQuota(model, videoType, mode, duration, resolution string) int64 {
	videoPricingMutex.RLock()
	defer videoPricingMutex.RUnlock()

	durationInt, _ := strconv.Atoi(duration)
	if durationInt <= 0 {
		durationInt = 5 // 默认5秒
	}

	// 查找匹配的规则（按优先级排序）
	var matchedRule *VideoPricingRule
	highestPriority := -1

	for i := range VideoPricingRules {
		rule := &VideoPricingRules[i]

		// 检查所有条件是否匹配
		if !matchPattern(rule.Model, model) ||
			!matchPattern(rule.Type, videoType) ||
			!matchPattern(rule.Mode, mode) ||
			!matchPattern(rule.Duration, duration) ||
			!matchPattern(rule.Resolution, resolution) {
			continue
		}

		// 选择优先级最高的规则
		if rule.Priority > highestPriority {
			highestPriority = rule.Priority
			matchedRule = rule
		}
	}

	// 没找到规则，降级到 DefaultModelPrice
	if matchedRule == nil {
		// 尝试从 DefaultModelPrice 获取
		if price, ok := DefaultModelPrice[model]; ok {
			return int64(price * config.QuotaPerUnit)
		}
		// 最终兜底
		return int64(0.1 * config.QuotaPerUnit)
	}

	// 计算价格
	basePrice := matchedRule.Price

	// 货币转换：如果是 CNY，转换为 USD
	if strings.ToUpper(matchedRule.Currency) == "CNY" {
		usdPrice, err := ConvertCNYToUSD(basePrice)
		if err != nil {
			usdPrice = basePrice * DefaultCNYToUSDRate
		}
		basePrice = usdPrice
	}

	// 根据计费类型计算总价
	var totalPrice float64
	switch matchedRule.PricingType {
	case PricingTypePerSecond:
		totalPrice = basePrice * float64(durationInt)
	case PricingTypeFixed:
		totalPrice = basePrice
	default:
		totalPrice = basePrice
	}

	return int64(totalPrice * config.QuotaPerUnit)
}

// GetVideoPricingRuleInfo 获取匹配的规则信息（用于调试）
func GetVideoPricingRuleInfo(model, videoType, mode, duration, resolution string) *VideoPricingRule {
	videoPricingMutex.RLock()
	defer videoPricingMutex.RUnlock()

	var matchedRule *VideoPricingRule
	highestPriority := -1

	for i := range VideoPricingRules {
		rule := &VideoPricingRules[i]

		if !matchPattern(rule.Model, model) {
			continue
		}
		if !matchPattern(rule.Type, videoType) {
			continue
		}
		if !matchPattern(rule.Mode, mode) {
			continue
		}
		if !matchPattern(rule.Duration, duration) {
			continue
		}
		if !matchPattern(rule.Resolution, resolution) {
			continue
		}

		if rule.Priority > highestPriority {
			highestPriority = rule.Priority
			matchedRule = rule
		}
	}

	return matchedRule
}
