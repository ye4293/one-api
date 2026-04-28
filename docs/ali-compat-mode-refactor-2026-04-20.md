# 阿里通义千问渠道 compat-mode 重构

**日期**：2026-04-20
**Commit**：`ba57530c`
**Tag**：`alphaas20260420-1`
**范围**：`relay/channel/ali/`、`relay/constant/relay_mode.go`、`relay/model/general.go`

---

## 一、动机

原实现调用老版 DashScope 原生接口 `/api/v1/services/aigc/text-generation/generation`，使用自定义 `ChatRequest`/`ChatResponse` 结构体做请求/响应转换，存在三个硬伤：

1. **接口能力落后**：老接口不支持 qwen3 系列的新特性（vision、tool_choice、reasoning/thinking）
2. **维护成本高**：自定义 SSE 流式解析、embedding 响应适配器需要和 OpenAI 标准同步，三套代码
3. **生态缺失**：未对接阿里官方已开放的 Anthropic Messages 兼容接口和 OpenAI Responses 兼容接口

阿里百炼（Model Studio）已原生支持：
- OpenAI 兼容端点 `/compatible-mode/v1/chat/completions`、`/compatible-mode/v1/embeddings`
- Anthropic Messages 兼容端点 `/apps/anthropic/v1/messages`
- OpenAI Responses 兼容端点 `/api/v2/apps/protocols/compatible-mode/v1/responses`
- Qwen3 深度思考参数 `enable_thinking` / `thinking_budget`

本次重构全面对接以上能力，并做了必要的 DTO 裁剪。

---

## 二、路由矩阵

同一个阿里渠道（`ChannelTypeAli = 17`）、同一个百炼 APIKey，根据客户端请求路径自动分流：

| 客户端请求 | 适配器转发到 | 处理方式 |
|---|---|---|
| `POST /v1/chat/completions` | `{base}/compatible-mode/v1/chat/completions` | 委托 `openai.Handler`/`StreamHandler` |
| `POST /v1/embeddings` | `{base}/compatible-mode/v1/embeddings` | 委托 `openai.Handler` |
| `POST /v1/messages`（Anthropic 格式） | `{base}/apps/anthropic/v1/messages` | `RelayClaudeNative` 控制器 passthrough |
| `POST /v1/responses`（OpenAI Responses 格式） | `{base}/api/v2/apps/protocols/compatible-mode/v1/responses` | `RelayOpenaiResponseNative` 控制器 passthrough |

其中 `{base}` 默认 `https://dashscope.aliyuncs.com`，海外用 `https://dashscope-intl.aliyuncs.com`，可在渠道配置覆盖。

---

## 三、文件级改动

### 3.1 新增：`RelayModeOpenaiResponse`
**文件**：`relay/constant/relay_mode.go`

```go
const (
    ...
    RelayModeClaude
    RelayModeOpenaiResponse  // 新增
)

// Path2RelayMode 新增分支：
} else if strings.HasPrefix(path, "/v1/responses") {
    relayMode = RelayModeOpenaiResponse
}
```

### 3.2 重写：`relay/channel/ali/adaptor.go`

核心是 `GetRequestURL` 的 4-way switch：

```go
func (a *Adaptor) GetRequestURL(meta *util.RelayMeta) (string, error) {
    base := strings.TrimRight(meta.BaseURL, "/")
    switch meta.Mode {
    case constant.RelayModeClaude:
        return base + "/apps/anthropic/v1/messages", nil
    case constant.RelayModeOpenaiResponse:
        return base + "/api/v2/apps/protocols/compatible-mode/v1/responses", nil
    case constant.RelayModeEmbeddings:
        return base + "/compatible-mode/v1/embeddings", nil
    default:
        return base + "/compatible-mode/v1/chat/completions", nil
    }
}
```

`SetupRequestHeader`：
- 统一 `Authorization: Bearer {APIKey}`（compat-mode 和 Anthropic 兼容端点都接受）
- 流式时 `Accept: text/event-stream`
- Messages 模式额外透传 `anthropic-version`（默认 `2023-06-01`）和 `anthropic-beta`
- **移除** `X-DashScope-SSE`（compat-mode 使用 OpenAI 风格 SSE）
- **移除** `X-DashScope-Plugin`（review 确认新路由不支持此头，dead code）

`ConvertRequest`：
- Messages / Responses 模式直接返回原 request（控制器会 passthrough 原始 body）
- 其他模式委托 `(&openai.Adaptor{}).ConvertRequest`
- 保留 `-internet` 模型后缀语义：去后缀后用 `compatibleChatRequest` wrapper 注入 `enable_search: true`

`DoResponse`：
- 直接委托 `openai.StreamHandler` / `openai.Handler`（compat-mode 返回 OpenAI 格式，无需自定义解析）

### 3.3 删除：`relay/channel/ali/main.go`（-267 行）

全部废弃：
- `ConvertRequest`（OpenAI → 老 DashScope `ChatRequest`）
- `ConvertEmbeddingRequest`
- `Handler` / `StreamHandler` / `EmbeddingHandler`
- `responseAli2OpenAI` / `streamResponseAli2OpenAI` / `embeddingResponseAli2OpenAI`

### 3.4 裁剪：`relay/channel/ali/model.go`

**删除**：`Message`、`Input`、`Parameters`、`ChatRequest`、`EmbeddingRequest`、`Embedding`、`EmbeddingResponse`、`Output`、`ChatResponse`

**保留**（video 适配器仍使用）：`Error`、`Usage`、全部 `AliVideo*` DTO

### 3.5 补齐：`relay/channel/ali/constants.go` 模型列表

```go
var ModelList = []string{
    // 旗舰
    "qwen3-max", "qwen3-max-preview",
    "qwen-max", "qwen-max-latest", "qwen-max-longcontext",
    // 通用
    "qwen-plus", "qwen-plus-latest",
    "qwen-flash",
    "qwen-turbo", "qwen-turbo-latest",
    // 推理 / 代码
    "qwq-plus", "qwq-32b",
    "qwen3-coder-plus",
    // 视觉
    "qwen-vl-plus", "qwen-vl-max",
    // 嵌入
    "text-embedding-v1", "text-embedding-v2", "text-embedding-v3",
}
```

同时清除 `ModelDetails` 中误填的 Claude Haiku 占位条目。

### 3.6 扩展：`relay/model/general.go` 新增深度思考字段

```go
type GeneralOpenAIRequest struct {
    ...
    // EnableThinking 控制 Qwen3 / DeepSeek 等思考模型是否开启思维链。
    // 阿里百炼 compatible-mode、OpenRouter reasoning 等均识别此字段。
    EnableThinking *bool `json:"enable_thinking,omitempty"`
    // ThinkingBudget 限制思维链最大 token 数，0 表示不传（由上游按模型默认处理）。
    ThinkingBudget int `json:"thinking_budget,omitempty"`
}
```

为什么 `*bool`：区分用户显式传 `false`（关闭思考）和"未设置"（让上游按模型默认行为）。

### 3.7 新增：`relay/channel/ali/adaptor_test.go`

4 个 subtest 锁定 `compatibleChatRequest` wrapper 的 JSON 序列化形状：

1. `enable_search=true` 字段正确注入并扁平化到顶层
2. `enable_search=false` 被 `omitempty` 省略
3. `EnableThinking=true` + `ThinkingBudget=8192` 通过嵌入结构体正常透传
4. `EnableThinking=false` 显式值被保留（指针字段的关键作用）

**注意**：项目整体 `go test` 因 `common/init.go:28` 的无条件 `flag.Parse()` 吞掉 `-test.*` 标志而无法运行（全项目预存限制，`kling`、`aws/claude` 等测试同样受影响）。测试行为已通过一次性 `go run` 脚本实测验证。

---

## 四、关键技术点

### 4.1 `compatibleChatRequest` wrapper

阿里 compat-mode 除标准 OpenAI 字段外，还接受百炼专有字段 `enable_search`。但 `GeneralOpenAIRequest` 不包含此字段，若侵入通用结构会影响其他渠道。解决方式：

```go
type compatibleChatRequest struct {
    *model.GeneralOpenAIRequest  // 嵌入指针，JSON 序列化时字段扁平化到顶层
    EnableSearch bool `json:"enable_search,omitempty"`
}
```

Go JSON 对嵌入指针的处理：`*GeneralOpenAIRequest` 的字段被 promote 到外层同级，`EnableSearch` 与之并列输出。

**风险与应对**：若未来 `GeneralOpenAIRequest` 内部新增同名 `EnableSearch` 字段，Go 的 JSON 编码器会让外层字段静默覆盖内嵌字段。已通过 `adaptor_test.go` 的 marshal 形状测试守护这个不变量。

### 4.2 passthrough 与 convert 混合路由

Messages 和 Responses 使用原生控制器（`RelayClaudeNative` / `RelayOpenaiResponseNative`）走 passthrough：控制器读取原始 body bytes 直接调 `adaptor.DoRequest`，完全绕开 `ConvertRequest` 和 `DoResponse`。因此 ali 适配器只需要正确实现 `GetRequestURL` 和 `SetupRequestHeader` 即可。

Chat 和 Embedding 走标准流程，进 `ConvertRequest`（复用 openai 的实现）和 `DoResponse`（复用 `openai.Handler`）。

### 4.3 兼容性设计

- **老版 `-internet` 后缀**：保留原有语义，自动转换为 `enable_search: true`
- **video 接口**：`wan*` 系列通过独立的 `VideoAdaptor` 路由，本次零改动，完全隔离
- **渠道类型**：仍是 `ChannelTypeAli = 17`，用户无需更改渠道配置

---

## 五、验证

### 5.1 编译
- `go build ./...` → clean
- `go vet ./relay/channel/ali/... ./relay/constant/... ./relay/model/...` → clean

### 5.2 实测 marshal 行为
用一次性 `go run` 脚本验证（已清理）：
```
enable_thinking=true, budget=8192 → {"model":"qwen3-max","enable_thinking":true,"thinking_budget":8192}
enable_thinking=false            → {"model":"qwen3-max","enable_thinking":false}
未设置                            → {"model":"qwen3-max"}（两字段都被 omitempty 省略）
组合 enable_search + thinking     → 三字段并列
round-trip（unmarshal→marshal）   → 完整保留
```

### 5.3 代码审查
两轮 code-reviewer 审查，第一轮发现并修复 2 项建议：
1. 移除 `X-DashScope-Plugin` 头处理（dead code）
2. 新增 marshal pin 测试防 `GeneralOpenAIRequest` 未来加同名字段静默覆盖

最终审查结论：**Ready to push: Yes**，0 Critical、0 Important。

---

## 六、调用示例

### 6.1 Chat + 思考模式
```bash
curl -X POST https://你的-ezlinkai/v1/chat/completions \
  -H "Authorization: Bearer sk-xxx" \
  -d '{
    "model": "qwen3-max",
    "messages": [{"role":"user","content":"9.11 和 9.9 哪个大？"}],
    "enable_thinking": true,
    "thinking_budget": 8192
  }'
```

### 6.2 Chat + 互联网搜索
```bash
curl -X POST https://你的-ezlinkai/v1/chat/completions \
  -H "Authorization: Bearer sk-xxx" \
  -d '{
    "model": "qwen-plus-internet",
    "messages": [{"role":"user","content":"今天上海天气"}]
  }'
# -internet 后缀会被剥离，自动注入 enable_search:true
```

### 6.3 Anthropic Messages 格式
```bash
curl -X POST https://你的-ezlinkai/v1/messages \
  -H "Authorization: Bearer sk-xxx" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "qwen3-max",
    "max_tokens": 1024,
    "messages": [{"role":"user","content":"hi"}]
  }'
```

### 6.4 OpenAI Responses 格式
```bash
curl -X POST https://你的-ezlinkai/v1/responses \
  -H "Authorization: Bearer sk-xxx" \
  -d '{
    "model": "qwen3-max",
    "input": "写一首五言绝句"
  }'
```

---

## 七、回归风险与已知限制

| 风险项 | 评估 | 备注 |
|---|---|---|
| 老版 DashScope 原生接口用户 | 低 | compat-mode 是阿里官方推荐路径，参数语义一致 |
| `X-DashScope-Plugin` 头失效 | 无影响 | 新路由本就不支持此头 |
| TopP=1 原修正逻辑删除 | 无影响 | compat-mode 兼容 OpenAI 行为，允许 TopP=1.0 |
| ModelDetails 清空 | 需前端验证 | 管理后台若有依赖 `GetModelDetails()` 非空的 UI 需关注 |
| 非 qwen 模型调用 `/v1/messages` | 用户可见错误 | 按设计决策不拦截，由阿里返回模型不支持错误 |
| `RelayModeCompletions` 落到 chat/completions | 预存限制 | 阿里无独立 completions 兼容端点，与旧行为一致 |

---

## 八、相关官方文档

- [Anthropic API 兼容 - 百炼](https://help.aliyun.com/zh/model-studio/anthropic-api-messages)
- [OpenAI Chat API 参考 - 百炼](https://help.aliyun.com/zh/model-studio/qwen-api-via-openai-chat-completions)
- [深度思考模型的用法 - 百炼](https://help.aliyun.com/zh/model-studio/deep-thinking)
- [Claude Code 接入百炼](https://help.aliyun.com/zh/model-studio/claude-code)

---

## 九、Git 元信息

```
commit ba57530c
Author: ye4293
Date:   2026-04-20
Tag:    alphaas20260420-1

feat(ali): 迁移阿里通义千问渠道至 compatible-mode + 扩展 Messages/Responses 路由

7 files changed, 185 insertions(+), 381 deletions(-)
 create mode 100644 relay/channel/ali/adaptor_test.go
 delete mode 100644 relay/channel/ali/main.go
```
