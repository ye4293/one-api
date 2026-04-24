#!/usr/bin/env bash
# ------------------------------------------------------------------------------
# test_vertex_claude.sh
#
# 用途：对「Vertex AI 上的 Claude」通道做端到端 smoke 验证。
#       分别打一次非流式请求与一次流式请求，验证 one-api 能否正确把
#       Anthropic `/v1/messages` 协议转发到 GCP Vertex AI，并按 Anthropic
#       原生格式回包/回流。
#
# 前置条件：
#   1. one-api 实例已启动（默认 http://localhost:3000，可通过 ONE_API 覆盖）。
#   2. 已在后台管理 UI 创建 `channel_type=VertexAI` 的渠道：
#        - Key 字段填入 GCP service account JSON
#        - ModelList 至少包含 `claude-opus-4-7`
#   3. 已创建一个 one-api Token（sk-xxx），能路由到上述渠道。
#
# 环境变量：
#   ONE_API   one-api 根地址（默认 http://localhost:3000）
#   TOKEN     one-api Token，必填（export TOKEN=sk-xxx）
#   MODEL     要测试的模型名（默认 claude-opus-4-7）
#
# 期望结果：
#   - 非流式：返回 `{ "id": "msg_…", "type": "message",
#             "content": [{"type":"text",…}], "usage": {...} }`
#   - 流式  ：返回 `event: message_start` / `data: {...}` 等 SSE 帧序列
#
# 用法：
#   export TOKEN=sk-xxx
#   ./scripts/test_vertex_claude.sh
# ------------------------------------------------------------------------------
set -euo pipefail

ONE_API="${ONE_API:-http://localhost:3000}"
TOKEN="${TOKEN:?export TOKEN=sk-xxx}"
MODEL="${MODEL:-claude-opus-4-7}"

echo "=== 非流式 ==="
curl -sS -X POST "$ONE_API/v1/messages" \
	-H "x-api-key: $TOKEN" \
	-H "anthropic-version: 2023-06-01" \
	-H "content-type: application/json" \
	-d "$(jq -n --arg m "$MODEL" '{model:$m,max_tokens:128,messages:[{role:"user",content:"用一句话自我介绍"}]}')" \
	| jq '{id, model, stop_reason, usage, content_types: [.content[]?.type]}'

echo
echo "=== 流式 ==="
curl -sS -N -X POST "$ONE_API/v1/messages" \
	-H "x-api-key: $TOKEN" \
	-H "anthropic-version: 2023-06-01" \
	-H "content-type: application/json" \
	-d "$(jq -n --arg m "$MODEL" '{model:$m,max_tokens:128,stream:true,messages:[{role:"user",content:"数到三"}]}')" \
	| grep -E '^(event|data):' | head -30
