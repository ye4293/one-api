# Anthropic Beta Flags 适配 Vertex AI / AWS Bedrock - 代码修改计划

## 问题根因

线上报错 `invalid beta flag` 是因为 Claude Code 等客户端在 `anthropic-beta` header 中传入了 Bedrock 不支持的 beta flag（如 `context-management-2025-06-27`），当前代码 `buildAnthropicBetaHeader` 直接透传所有 flags，没有任何白名单过滤。

## 当前代码行为

| 平台 | beta 处理逻辑 | 问题 |
|------|--------------|------|
| **AWS Bedrock** | `relay/channel/aws/claude/main.go:196` 直接从 `c.GetHeader("anthropic-beta")` 取值，split 后全部写入 `anthropic_beta` body 字段 | 未过滤，不支持的 flag 导致 400 |
| **Vertex AI** | `relay/channel/vertexai/adaptor.go:255` 在 `SetupRequestHeader` 中 `req.Header.Del("anthropic-beta")`，`rewriteBodyForVertexClaude` 不处理 beta | **完全丢弃 beta**，`context_management` 等功能无法生效 |
| **Anthropic 原生** | 直接透传 header | 无问题（Anthropic 自身容错） |

## 其他问题

1. **AWS `Request` 结构体（OpenAI 格式转换路径）** 缺少 `ContextManagement` 字段 → copier.Copy 会丢失该字段
2. **Vertex `rewriteBodyForVertexClaude`** 不注入 `anthropic_beta` → 即使请求体有 `context_management` 也无法生效
3. **无自动推断逻辑**：请求体含 `context_management` / `output_config.task_budget` 时应自动补对应 beta flag

---

## 修改计划

### Step 1：新建 beta flags 白名单与工具函数

**文件**：`relay/channel/anthropic/beta.go`（新建）

内容：

```go
package anthropic

import "strings"

// BedrockAllowedBetaFlags Bedrock 支持的 beta flags 白名单
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

// VertexAllowedBetaFlags Vertex AI 支持的 beta flags 白名单
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

// FilterBetaFlags 根据白名单过滤用户传入的 beta flags
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
    var flags []string

    // context_management -> context-management-2025-06-27
    if _, ok := body["context_management"]; ok {
        flags = append(flags, "context-management-2025-06-27")
    }

    // output_config.task_budget -> task-budgets-2026-03-13
    if outputConfig, ok := body["output_config"].(map[string]any); ok {
        if _, ok := outputConfig["task_budget"]; ok {
            flags = append(flags, "task-budgets-2026-03-13")
        }
    }

    // output_format -> structured-outputs-2025-11-13
    if _, ok := body["output_format"]; ok {
        flags = append(flags, "structured-outputs-2025-11-13")
    }

    return flags
}

// MergeBetaFlags 合并用户传入 + 推断的 beta flags，过滤白名单并去重
func MergeBetaFlags(userBetaHeader string, body map[string]any, allowed map[string]struct{}) []string {
    // 1. 过滤用户传入的 flags
    flags := FilterBetaFlags(userBetaHeader, allowed)

    // 2. 推断必需的 flags
    inferred := InferBetaFlags(body)

    // 3. 合并去重（推断的 flag 也必须在白名单内）
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
```

---

### Step 2：修改 AWS Bedrock 适配器

**文件**：`relay/channel/aws/claude/main.go`

#### 2a. 修改 `buildNativeClaudeRequestBody` 函数（原生 Claude 格式路径）

替换当前第 196-200 行的 beta 处理逻辑：

```go
// 当前代码:
betaJSON, err := buildAnthropicBetaHeader(c.GetHeader("anthropic-beta"))
if err != nil {
    return nil, err
}
if len(betaJSON) > 0 {
    awsClaudeReq["anthropic_beta"] = json.RawMessage(betaJSON)
}

// 替换为:
betaFlags := anthropic.MergeBetaFlags(c.GetHeader("anthropic-beta"), awsClaudeReq, anthropic.BedrockAllowedBetaFlags)
if len(betaFlags) > 0 {
    betaJSON, marshalErr := json.Marshal(betaFlags)
    if marshalErr != nil {
        return nil, errors.Wrap(marshalErr, "marshal anthropic-beta")
    }
    awsClaudeReq["anthropic_beta"] = json.RawMessage(betaJSON)
}
```

#### 2b. 修改 `Handler` 和 `StreamHandler` 中的 beta 处理（OpenAI 格式转换路径）

替换第 257-263 行和第 333-339 行：

```go
// 当前代码:
betaJSON, betaErr := buildAnthropicBetaHeader(c.GetHeader("anthropic-beta"))
if betaErr != nil {
    return utils.WrapErr(betaErr), nil
}
if len(betaJSON) > 0 {
    awsClaudeReq.AnthropicBeta = betaJSON
}

// 替换为:
// 从原始请求体推断 beta（OpenAI 路径需要额外检查原始 body）
var inferBody map[string]any
if rawBody, rawErr := common.GetRequestBody(c); rawErr == nil {
    _ = json.Unmarshal(rawBody, &inferBody)
}
betaFlags := anthropic.MergeBetaFlags(c.GetHeader("anthropic-beta"), inferBody, anthropic.BedrockAllowedBetaFlags)
if len(betaFlags) > 0 {
    betaJSON, marshalErr := json.Marshal(betaFlags)
    if marshalErr != nil {
        return utils.WrapErr(errors.Wrap(marshalErr, "marshal anthropic-beta")), nil
    }
    awsClaudeReq.AnthropicBeta = betaJSON
}
```

#### 2c. `buildAnthropicBetaHeader` 可保留但标记为 deprecated，或直接删除

---

### Step 3：修改 AWS Request 结构体

**文件**：`relay/channel/aws/claude/model.go`

在 `Request` 结构体中添加字段，确保 native 路径以外的 copier.Copy 不丢失数据：

```go
type Request struct {
    AnthropicVersion  string              `json:"anthropic_version"`
    AnthropicBeta     json.RawMessage     `json:"anthropic_beta,omitempty"`
    Messages          []anthropic.Message `json:"messages"`
    System            any                 `json:"system,omitempty"`
    MaxTokens         int                 `json:"max_tokens,omitempty"`
    Temperature       *float64            `json:"temperature,omitempty"`
    TopP              float64             `json:"top_p,omitempty"`
    TopK              int                 `json:"top_k,omitempty"`
    StopSequences     []string            `json:"stop_sequences,omitempty"`
    Tools             []anthropic.Tool    `json:"tools,omitempty"`
    ToolChoice        *anthropic.ToolChoice    `json:"tool_choice,omitempty"`
    Thinking          *anthropic.ThinkingConfig `json:"thinking,omitempty"`
    OutputConfig      *anthropic.OutputConfig   `json:"output_config,omitempty"`
    // 新增字段：
    ContextManagement json.RawMessage `json:"context_management,omitempty"` // context editing
    OutputFormat      json.RawMessage `json:"output_format,omitempty"`      // structured outputs
}
```

> 注意：`ContextManagement` 使用 `json.RawMessage` 而非具体结构体，因为这些字段只需透传不需解析。

---

### Step 4：修改 Vertex AI 适配器

**文件**：`relay/channel/vertexai/claude.go`

#### 4a. 修改 `rewriteBodyForVertexClaude` 签名和逻辑

```go
// 修改函数签名，增加 betaHeader 参数
func rewriteBodyForVertexClaude(body []byte, modelName string, betaHeader string) ([]byte, error) {
    var m map[string]interface{}
    if err := json.Unmarshal(body, &m); err != nil {
        return nil, fmt.Errorf("rewriteBodyForVertexClaude: invalid json: %w", err)
    }
    delete(m, "model")
    m["anthropic_version"] = anthropicVersion

    // 注入 anthropic_beta（白名单过滤 + 自动推断）
    betaFlags := anthropic.MergeBetaFlags(betaHeader, m, anthropic.VertexAllowedBetaFlags)
    if len(betaFlags) > 0 {
        m["anthropic_beta"] = betaFlags
    }

    // 4.7+ 模型兼容处理（现有逻辑保持不变）
    if anthropic.IsNoSamplingModel(modelName) {
        // ... 现有 thinking 处理 ...
    } else if thinking, exists := m["thinking"]; exists && thinking != nil {
        m["temperature"] = 1.0
    }

    out, err := json.Marshal(m)
    if err != nil {
        return nil, fmt.Errorf("rewriteBodyForVertexClaude: marshal failed: %w", err)
    }
    return out, nil
}
```

**文件**：`relay/channel/vertexai/adaptor.go`

#### 4b. 修改 `DoRequest` 中调用处

```go
// 当前代码 (adaptor.go:325):
rewritten, rewriteErr := rewriteBodyForVertexClaude(raw, claudeModel)

// 替换为:
betaHeader := c.Request.Header.Get("anthropic-beta")
rewritten, rewriteErr := rewriteBodyForVertexClaude(raw, claudeModel, betaHeader)
```

#### 4c. `SetupRequestHeader` 中继续删除 `anthropic-beta` header

保持不变（Vertex 用 body 字段，不认 header）:
```go
req.Header.Del("anthropic-beta")
```

---

### Step 5：编写测试

**文件**：`relay/channel/anthropic/beta_test.go`（新建）

```go
package anthropic

import "testing"

func TestFilterBetaFlags(t *testing.T) {
    // 测试正常过滤
    // 测试空值
    // 测试全部不在白名单
    // 测试部分在白名单
}

func TestInferBetaFlags(t *testing.T) {
    // 测试 context_management 推断
    // 测试 output_config.task_budget 推断
    // 测试 output_format 推断
    // 测试无推断
}

func TestMergeBetaFlags(t *testing.T) {
    // 测试用户传入 + 推断合并
    // 测试去重
    // 测试推断的 flag 不在白名单时被过滤
}
```

**文件**：`relay/channel/aws/claude/main_test.go`（追加）

- 测试 `buildNativeClaudeRequestBody` 在有 `context_management` 时正确注入 `anthropic_beta`
- 测试不支持的 flag 被过滤
- 测试 `context_management` 字段不被删除

**文件**：`relay/channel/vertexai/claude_test.go`（追加）

- 测试 `rewriteBodyForVertexClaude` 正确注入 `anthropic_beta`
- 测试白名单过滤
- 测试自动推断

---

## 修改优先级

| 优先级 | 修改 | 原因 |
|--------|------|------|
| **P0** | Step 1 + Step 2 (Bedrock 白名单过滤 + 自动推断) | 线上报错直接原因 |
| **P1** | Step 3 (Request 结构体补字段) | copier.Copy 路径的字段保留 |
| **P1** | Step 4 (Vertex 注入 beta) | 当前功能不生效但不报错 |
| **P2** | Step 5 (测试) | 回归保护 |

---

## 涉及文件清单

| 文件 | 操作 | 说明 |
|------|------|------|
| `relay/channel/anthropic/beta.go` | 新建 | 白名单定义 + 工具函数 |
| `relay/channel/anthropic/beta_test.go` | 新建 | 单元测试 |
| `relay/channel/aws/claude/main.go` | 修改 | beta 过滤 + 推断逻辑 |
| `relay/channel/aws/claude/model.go` | 修改 | 添加 ContextManagement/OutputFormat 字段 |
| `relay/channel/aws/claude/main_test.go` | 修改 | 追加测试 |
| `relay/channel/vertexai/claude.go` | 修改 | 函数签名改变 + 注入 beta |
| `relay/channel/vertexai/claude_test.go` | 修改 | 追加测试 |
| `relay/channel/vertexai/adaptor.go` | 修改 | 传递 betaHeader 参数 |

---

## 风险评估

1. **白名单维护**：新 beta 发布时需要更新白名单 → 文件顶部加注释说明更新流程
2. **向后兼容**：修改后只会**减少**发送给上游的 beta flags，不会增加 → 不会破坏已有功能
3. **context_management 推断**：自动补 beta 只在检测到对应字段时触发 → 无副作用
4. **Vertex 测试**：修改 `rewriteBodyForVertexClaude` 签名需要同步修改现有测试用例

---

## 验证方式

1. `go build ./...` 编译通过
2. `go test ./relay/channel/anthropic/... ./relay/channel/aws/... ./relay/channel/vertexai/...` 测试通过
3. 手动测试：
   - Bedrock 路径发送含 `context_management` 的请求 → 正确注入 `context-management-2025-06-27`
   - Bedrock 路径发送未知 beta flag → 被过滤，不触发 400
   - Vertex 路径发送含 `context_management` 的请求 → body 中出现 `anthropic_beta`
