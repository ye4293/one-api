package vertexai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/songquanpeng/one-api/relay/channel/anthropic"
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
//   - 对 4.7+（IsNoSamplingModel）做兼容修正：thinking.type "enabled" → "adaptive"、
//     删掉 thinking.budget_tokens 与 temperature/top_p/top_k（Vertex Anthropic 不接受）
//   - 对 4.6 及更早且 body 含 thinking 的：强制 temperature=1（Anthropic 官方要求）
//   - 其他字段（messages、system、max_tokens、stream、tools 等）原样保留
//
// modelName 可以是带或不带 `-thinking` 后缀的名字；内部用 anthropic.IsNoSamplingModel
// 透明处理。
func rewriteBodyForVertexClaude(body []byte, modelName string) ([]byte, error) {
	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("rewriteBodyForVertexClaude: invalid json: %w", err)
	}
	delete(m, "model")
	m["anthropic_version"] = anthropicVersion

	if anthropic.IsNoSamplingModel(modelName) {
		if thinking, ok := m["thinking"].(map[string]interface{}); ok {
			if t, _ := thinking["type"].(string); t == "enabled" {
				thinking["type"] = "adaptive"
			}
			delete(thinking, "budget_tokens")
		}
		delete(m, "temperature")
		delete(m, "top_p")
		delete(m, "top_k")
	} else if thinking, exists := m["thinking"]; exists && thinking != nil {
		// 非 4.7：只要 body 显式打开 thinking，就按官方要求强制 temperature=1
		m["temperature"] = 1.0
	}

	out, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("rewriteBodyForVertexClaude: marshal failed: %w", err)
	}
	return out, nil
}
