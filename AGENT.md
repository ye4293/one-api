# one-api Agent 协作说明

## 上游 Usage / Prompt Cache 结论

适用 OpenAI Responses API，以及字段语义一致的 OpenAI 兼容上游。**不得记录或提交 API Key**；验证请求只使用环境变量中的密钥。

### 字段含义

| API 形态 | 输入总量 | 缓存读取 | 缓存写入 | 输出总量 |
|---|---|---|---|---|
| Responses | `usage.input_tokens` | `usage.input_tokens_details.cached_tokens` | `usage.input_tokens_details.cache_write_tokens` | `usage.output_tokens` |
| Chat Completions | `usage.prompt_tokens` | `usage.prompt_tokens_details.cached_tokens` | `usage.prompt_tokens_details.cache_write_tokens` | `usage.completion_tokens` |

- **输入总量 `P`**：上游统计的全部输入 token。
- **缓存读 `R` / `cached_tokens`**：已命中既有 prompt cache 的输入 token，按缓存读取单价计费。
- **缓存写 `W` / `cache_write_tokens`**：本次创建或扩展 prompt cache 的输入 token，按缓存写入单价计费。
- **普通输入 `N`**：既未读取也未写入缓存的剩余输入 token，即 `N = P - R - W`。
- **输出 `C`**：模型生成 token；它不属于输入分桶，独立按输出价格计费。

### 分桶关系与计费公式

当上游 Usage/定价文档确认 `R`、`W` 是输入总量的互斥分桶时：

```text
P = N + R + W
N = P - R - W

input_cost  = N × input_price
cache_read  = R × cached_input_price
cache_write = W × cached_write_price
output_cost = C × output_price
total_cost  = input_cost + cache_read + cache_write + output_cost
```

每一笔响应必须校验：`P >= 0`、`R >= 0`、`W >= 0`、`R + W <= P`。若 `R + W > P`，不可继续按上述公式计费；应保存脱敏 usage、渠道、模型和 request ID 并核查上游口径。不要仅将负的 `N` 静默钳为零，否则会掩盖上游字段变更。

### 结论

对支持这些字段的上游，`input_tokens` / `prompt_tokens` 已包含缓存读取和缓存写入部分；`cached_tokens` 与 `cache_write_tokens` 是输入总量内的不同计费分桶。实现普通输入计费时必须同时扣除两者。

若以后同一响应同时返回 `R > 0` 和 `W > 0`，仍须以 `R + W <= P` 作为每请求的硬校验；不满足时应核查上游字段口径，而不是继续按缓存分项计费。

实测佐证（`gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-5.6-luna` 结果一致）：

| 场景 | `P=input_tokens` | `R=cached_tokens` | `W=cache_write_tokens` | `N=P-R-W` |
|---|---:|---:|---:|---:|
| 首次请求（建缓存） | 2717 | 0 | 2714 | 3 |
| 相同前缀再次请求（读缓存） | 2717 | 2714 | 0 | 3 |

### 项目实现对应位置

- Chat 计费：`relay/controller/helper.go` 的 `postConsumeQuota`。
- Responses 计费：`relay/controller/opeai_response.go` 的 `CalculateOpenaiResponseQuotaByRatio`。
- 统一 Usage 字段：`relay/model/misc.go`、`relay/channel/openai/model.go`。
