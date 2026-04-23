package vertexai

import (
	"encoding/json"
	"fmt"
	"strings"
)

// isClaudeModel 判断模型名是否属于 Claude 系列（大小写不敏感）。
func isClaudeModel(modelName string) bool {
	return strings.HasPrefix(strings.ToLower(modelName), "claude")
}

// mapClaudeModelForURL 把 Anthropic 官方模型 ID 转成 Vertex URL 要求的 "@日期" 格式。
// 不在 map 的模型原样返回（让新模型也能透传，风险由调用方承担）。
func mapClaudeModelForURL(modelName string) string {
	if v, ok := claudeModelMap[modelName]; ok {
		return v
	}
	return modelName
}

// claudeSuffix 返回 Vertex Anthropic publisher 的 action 段。
//   - 非流式：rawPredict
//   - 流式：  streamRawPredict?alt=sse
func claudeSuffix(isStream bool) string {
	if isStream {
		return "streamRawPredict?alt=sse"
	}
	return "rawPredict"
}

// rewriteBodyForVertexClaude 把 Anthropic 原生 /v1/messages 的请求体改写成 Vertex Anthropic
// publisher 能接受的格式：
//   - 注入顶层 "anthropic_version": "vertex-2023-10-16"
//   - 删除顶层 "model" 字段（Vertex 用 URL 决定模型，body 里带 model 会被拒）
//   - 其他字段（messages、system、max_tokens、stream、temperature、tools、thinking 等）原样保留
func rewriteBodyForVertexClaude(body []byte) ([]byte, error) {
	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("rewriteBodyForVertexClaude: invalid json: %w", err)
	}
	delete(m, "model")
	m["anthropic_version"] = anthropicVersion
	out, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("rewriteBodyForVertexClaude: marshal failed: %w", err)
	}
	return out, nil
}
