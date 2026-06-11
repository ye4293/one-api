package anthropic

import (
	"encoding/json"
	"strings"

	"github.com/songquanpeng/one-api/model"
)

// BedrockAllowedBetaFlags AWS Bedrock 支持的 beta flags 白名单。
// 更新时请参考 AWS Bedrock 文档确认新 flag 是否已在目标区域可用。
var BedrockAllowedBetaFlags = map[string]struct{}{
	"computer-use-2025-01-24":          {},
	"computer-use-2025-11-24":          {},
	"token-efficient-tools-2025-02-19": {},
	"interleaved-thinking-2025-05-14":  {},
	"output-128k-2025-02-19":           {},
	"dev-full-thinking-2025-05-14":     {},
	"context-1m-2025-08-07":            {},
	"context-management-2025-06-27":    {},
	"task-budgets-2026-03-13":          {},
	"structured-outputs-2025-11-13":    {},
	"effort-2025-11-24":                {},
	"tool-search-tool-2025-10-19":      {},
	"tool-examples-2025-10-29":         {},
}

// VertexAllowedBetaFlags Vertex AI 支持的 beta flags 白名单。
// 更新时请参考 Vertex AI 文档确认新 flag 是否已在目标区域可用。
var VertexAllowedBetaFlags = map[string]struct{}{
	"message-batches-2024-09-24":               {},
	"prompt-caching-2024-07-31":                {},
	"computer-use-2024-10-22":                  {},
	"computer-use-2025-01-24":                  {},
	"computer-use-2025-11-24":                  {},
	"pdfs-2024-09-25":                          {},
	"token-counting-2024-11-01":                {},
	"token-efficient-tools-2025-02-19":         {},
	"output-128k-2025-02-19":                   {},
	"files-api-2025-04-14":                     {},
	"mcp-client-2025-04-04":                    {},
	"mcp-client-2025-11-20":                    {},
	"dev-full-thinking-2025-05-14":             {},
	"interleaved-thinking-2025-05-14":          {},
	"code-execution-2025-05-22":                {},
	"extended-cache-ttl-2025-04-11":            {},
	"context-1m-2025-08-07":                    {},
	"context-management-2025-06-27":            {},
	"task-budgets-2026-03-13":                  {},
	"structured-outputs-2025-11-13":            {},
	"model-context-window-exceeded-2025-08-26": {},
	"skills-2025-10-02":                        {},
	"fast-mode-2026-02-01":                     {},
}

// FilterBetaFlags 根据白名单过滤用户传入的 beta flags（逗号分隔字符串）
func FilterBetaFlags(betaHeader string, allowed map[string]struct{}) []string {
	if betaHeader == "" {
		return nil
	}
	rawValues := strings.Split(betaHeader, ",")
	result := make([]string, 0, len(rawValues))
	for _, v := range rawValues {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			continue
		}
		if _, ok := allowed[trimmed]; ok {
			result = append(result, trimmed)
		}
	}
	return result
}

// InferBetaFlags 根据请求体内容自动推断需要的 beta flags
func InferBetaFlags(body map[string]any) []string {
	if body == nil {
		return nil
	}
	var flags []string

	if _, ok := body["context_management"]; ok {
		flags = append(flags, "context-management-2025-06-27")
	}

	if outputConfig, ok := body["output_config"].(map[string]any); ok {
		if _, ok := outputConfig["task_budget"]; ok {
			flags = append(flags, "task-budgets-2026-03-13")
		}
	}

	if _, ok := body["output_format"]; ok {
		flags = append(flags, "structured-outputs-2025-11-13")
	}

	return flags
}

// MergeBetaFlags 合并用户传入（白名单过滤）+ 自动推断的 beta flags，去重
func MergeBetaFlags(userBetaHeader string, body map[string]any, allowed map[string]struct{}) []string {
	flags := FilterBetaFlags(userBetaHeader, allowed)

	inferred := InferBetaFlags(body)

	seen := make(map[string]struct{}, len(flags))
	for _, f := range flags {
		seen[f] = struct{}{}
	}
	for _, f := range inferred {
		if _, ok := allowed[f]; !ok {
			continue
		}
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		flags = append(flags, f)
	}

	return flags
}

// MarshalBetaFlags 将 beta flags 列表序列化为 JSON 数组，用于写入请求体的 anthropic_beta 字段
func MarshalBetaFlags(flags []string) (json.RawMessage, error) {
	if len(flags) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(flags)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// BedrockVertexAllowedBetaFlags 取 Bedrock 和 Vertex 白名单的交集（最严格过滤）
var BedrockVertexAllowedBetaFlags = func() map[string]struct{} {
	intersection := make(map[string]struct{})
	for k := range BedrockAllowedBetaFlags {
		if _, ok := VertexAllowedBetaFlags[k]; ok {
			intersection[k] = struct{}{}
		}
	}
	return intersection
}()

// FilterBetaHeaderByMode 按渠道配置的 BetaFilterMode 过滤 anthropic-beta header
func FilterBetaHeaderByMode(betaHeader string, mode model.BetaFilterMode) string {
	if betaHeader == "" || mode == model.BetaFilterNone {
		return betaHeader
	}
	var allowed map[string]struct{}
	switch mode {
	case model.BetaFilterBedrock:
		allowed = BedrockAllowedBetaFlags
	case model.BetaFilterVertex:
		allowed = VertexAllowedBetaFlags
	case model.BetaFilterBedrockVertex:
		allowed = BedrockVertexAllowedBetaFlags
	default:
		return betaHeader
	}
	filtered := FilterBetaFlags(betaHeader, allowed)
	return strings.Join(filtered, ",")
}
