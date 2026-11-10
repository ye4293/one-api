# 修复 Gemini 流式 tool call 缺少 index

## 背景与目标

客户使用 OpenAI Java SDK 解析 `gemini-4-argon` 流式响应时，报错：

```text
OpenAIInvalidDataException: `index` is not set
```

客户提供的 chunk 显示外层 `choices[0].index` 存在，但内层 `choices[0].delta.tool_calls[0].index` 缺失。OpenAI 兼容的流式 tool call chunk 需要为每个 `tool_calls[]` 项提供 `index`。

目标：在 Gemini 转 OpenAI 的流式 tool call 输出中补齐 `tool_calls[].index`，避免严格 SDK 解析失败。

## 方案设计

涉及文件：

- `relay/model/tool.go`
  - 为通用 `model.Tool` 增加可选 `Index *int` 字段，JSON tag 为 `index,omitempty`。
  - 使用指针避免非流式 message tool call 默认输出 `index:0`，保持原有非流式响应形状。
- `relay/channel/gemini/main.go`
  - 在 `streamResponseGeminiChat2OpenAI` 构造流式 `Delta.ToolCalls` 时设置 `Index`。
  - 单个 chunk 内多个 function call 按 `funcCallIdx` 输出 `0,1,2...`。
- `relay/channel/gemini/stream_tool_call_test.go`
  - 增加回归测试，断言 Gemini 流式 tool call 结构体和 JSON 输出都包含 `index:0`。

## 影响范围

- 仅影响 Gemini Chat Completions 流式 tool call 输出。
- 非流式响应不设置 `Index`，不会额外输出 `index` 字段。
- 不涉及数据库 schema、计费逻辑、渠道选择或上游请求参数。

## 验证方式

- `gofmt` 格式化修改文件。
- `go test ./relay/channel/gemini ./relay/model ./relay/channel/openai` 验证专项包。
- 提交前按项目规范执行：

```bash
go build ./... && go vet ./...
```
