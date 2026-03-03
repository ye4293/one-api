package common

import (
	"encoding/json"
	"sync"

	"github.com/songquanpeng/one-api/common/config"
)

// Claude 配置相关的锁
var claudeConfigMutex sync.RWMutex

// 默认的 Claude MaxTokens 配置
var defaultClaudeDefaultMaxTokens = map[string]int{
	"default":                             8192,
	"claude-3-haiku-20240307":             4096,
	"claude-3-sonnet-20240229":            4096,
	"claude-3-opus-20240229":              4096,
	"claude-3-5-sonnet-20240620":          8192,
	"claude-3-5-sonnet-20241022":          8192,
	"claude-3-5-haiku-20241022":           8192,
	"claude-3-7-sonnet-20250219":          8192,
	"claude-3-7-sonnet-20250219-thinking": 64000,
	"claude-opus-4-20250514":              8192,
	"claude-opus-4-20250514-thinking":     32000,
	"claude-sonnet-4-20250514":            8192,
	"claude-sonnet-4-20250514-thinking":   64000,
	"claude-opus-4-1-20250805":            8192,
	"claude-opus-4-1-20250805-thinking":   64000,
	"claude-haiku-4-5-20251001":           8192,
	"claude-haiku-4-5-20251001-thinking":  64000,
	"claude-sonnet-4-5-20250929":          8192,
	"claude-sonnet-4-5-20250929-thinking": 64000,
	"claude-opus-4-5-20251101":            8192,
	"claude-opus-4-5-20251101-thinking":   128000,
	"claude-opus-4-6":                     8192,
	"claude-opus-4-6-thinking":            128000,
}

// 默认的 ReasoningEffort 到百分比的映射
var defaultClaudeReasoningEffortMap = map[string]float64{
	"none":    0,
	"minimal": 0.2,
	"low":     0.4,
	"medium":  0.6,
	"high":    0.8,
	"xhigh":   0.95,
}

// 初始化 Claude 配置
func init() {
	config.ClaudeDefaultMaxTokens = make(map[string]int)
	for k, v := range defaultClaudeDefaultMaxTokens {
		config.ClaudeDefaultMaxTokens[k] = v
	}

	config.ClaudeReasoningEffortMap = make(map[string]float64)
	for k, v := range defaultClaudeReasoningEffortMap {
		config.ClaudeReasoningEffortMap[k] = v
	}

	config.ClaudeRequestHeaders = make(map[string]map[string]string)
}

// GetClaudeDefaultMaxTokens 获取模型默认 MaxTokens
func GetClaudeDefaultMaxTokens(modelName string) int {
	claudeConfigMutex.RLock()
	defer claudeConfigMutex.RUnlock()

	if maxTokens, ok := config.ClaudeDefaultMaxTokens[modelName]; ok {
		return maxTokens
	}
	if defaultMaxTokens, ok := config.ClaudeDefaultMaxTokens["default"]; ok {
		return defaultMaxTokens
	}
	return 8192 // 兜底默认值
}

// GetClaudeThinkingBudgetRatio 根据 reasoning_effort 获取百分比
func GetClaudeThinkingBudgetRatio(reasoningEffort string) float64 {
	claudeConfigMutex.RLock()
	defer claudeConfigMutex.RUnlock()

	if ratio, ok := config.ClaudeReasoningEffortMap[reasoningEffort]; ok {
		return ratio
	}
	// 如果未找到，返回默认百分比
	return config.ClaudeThinkingBudgetRatio
}

// GetClaudeRequestHeaders 获取模型的请求头覆盖
func GetClaudeRequestHeaders(modelName string) map[string]string {
	claudeConfigMutex.RLock()
	defer claudeConfigMutex.RUnlock()

	if headers, ok := config.ClaudeRequestHeaders[modelName]; ok {
		return headers
	}
	return nil
}

// ClaudeDefaultMaxTokens2JSONString 将 ClaudeDefaultMaxTokens 转换为 JSON 字符串
func ClaudeDefaultMaxTokens2JSONString() string {
	claudeConfigMutex.RLock()
	defer claudeConfigMutex.RUnlock()

	jsonBytes, err := json.Marshal(config.ClaudeDefaultMaxTokens)
	if err != nil {
		return "{}"
	}
	return string(jsonBytes)
}

// ClaudeReasoningEffortMap2JSONString 将 ClaudeReasoningEffortMap 转换为 JSON 字符串
func ClaudeReasoningEffortMap2JSONString() string {
	claudeConfigMutex.RLock()
	defer claudeConfigMutex.RUnlock()

	jsonBytes, err := json.Marshal(config.ClaudeReasoningEffortMap)
	if err != nil {
		return "{}"
	}
	return string(jsonBytes)
}

// ClaudeRequestHeaders2JSONString 将 ClaudeRequestHeaders 转换为 JSON 字符串
func ClaudeRequestHeaders2JSONString() string {
	claudeConfigMutex.RLock()
	defer claudeConfigMutex.RUnlock()

	jsonBytes, err := json.Marshal(config.ClaudeRequestHeaders)
	if err != nil {
		return "{}"
	}
	return string(jsonBytes)
}

// AddNewMissingClaudeDefaultMaxTokens 将代码中新增的模型默认 MaxTokens 合并到数据库值中
func AddNewMissingClaudeDefaultMaxTokens(jsonStr string) string {
	var dbMap map[string]int
	if err := json.Unmarshal([]byte(jsonStr), &dbMap); err != nil {
		return jsonStr
	}
	for k, v := range defaultClaudeDefaultMaxTokens {
		if _, ok := dbMap[k]; !ok {
			dbMap[k] = v
		}
	}
	jsonBytes, err := json.Marshal(dbMap)
	if err != nil {
		return jsonStr
	}
	return string(jsonBytes)
}

// AddNewMissingClaudeReasoningEffortMap 将代码中新增的 ReasoningEffort 映射合并到数据库值中
func AddNewMissingClaudeReasoningEffortMap(jsonStr string) string {
	var dbMap map[string]float64
	if err := json.Unmarshal([]byte(jsonStr), &dbMap); err != nil {
		return jsonStr
	}
	for k, v := range defaultClaudeReasoningEffortMap {
		if _, ok := dbMap[k]; !ok {
			dbMap[k] = v
		}
	}
	jsonBytes, err := json.Marshal(dbMap)
	if err != nil {
		return jsonStr
	}
	return string(jsonBytes)
}

// UpdateClaudeDefaultMaxTokensByJSONString 通过 JSON 字符串更新 ClaudeDefaultMaxTokens
func UpdateClaudeDefaultMaxTokensByJSONString(jsonStr string) error {
	claudeConfigMutex.Lock()
	defer claudeConfigMutex.Unlock()

	var newMap map[string]int
	err := json.Unmarshal([]byte(jsonStr), &newMap)
	if err != nil {
		return err
	}
	config.ClaudeDefaultMaxTokens = newMap
	return nil
}

// UpdateClaudeReasoningEffortMapByJSONString 通过 JSON 字符串更新 ClaudeReasoningEffortMap
func UpdateClaudeReasoningEffortMapByJSONString(jsonStr string) error {
	claudeConfigMutex.Lock()
	defer claudeConfigMutex.Unlock()

	var newMap map[string]float64
	err := json.Unmarshal([]byte(jsonStr), &newMap)
	if err != nil {
		return err
	}
	config.ClaudeReasoningEffortMap = newMap
	return nil
}

// UpdateClaudeRequestHeadersByJSONString 通过 JSON 字符串更新 ClaudeRequestHeaders
func UpdateClaudeRequestHeadersByJSONString(jsonStr string) error {
	claudeConfigMutex.Lock()
	defer claudeConfigMutex.Unlock()

	var newMap map[string]map[string]string
	err := json.Unmarshal([]byte(jsonStr), &newMap)
	if err != nil {
		return err
	}
	config.ClaudeRequestHeaders = newMap
	return nil
}
