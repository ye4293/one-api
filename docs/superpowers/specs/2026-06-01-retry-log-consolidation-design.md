# 重试日志聚合设计

**日期**: 2026-06-01
**目标**: 让用户在日志列表中只看到每个请求的最终一条记录,且错误消息不含渠道信息;管理员仍可看到完整重试链路。

---

## 1. 现状

`controller/relay.go` 的 `Relay()` 主链路在每次失败时都会写一条 `LogTypeError` 日志:

- 首次失败 → `recordRetryFailureLog(attempt=0)` (line 166)
- 每次重试失败 → `recordRetryFailureLog(attempt=N)` (line 266)

后果:5 次重试后,DB 里至少有 5 条日志(全失败)或 4 失败 + 1 成功 = 5 条记录,且每条 `content` 字段都含 `"渠道=xxx(#NN)"` 前缀,普通用户透过 `/api/log/self` 也能看到。

`stripAdminInfoFromLogs` (`controller/log.go:26`) 已经会从 `other` 字段剥 `adminInfo`,但**不处理 `content` 字段**,这是渠道泄漏的根源。

其他重试链路(Image/Midjourney/Video/Runway/Sora/xAI Video)已经是"全失败写 1 条"模式,只是 content 仍带一些不必要的前缀。

## 2. 行为契约

| 场景 | 写 DB 的记录数 | 类型 | content | other.retryHistory |
|---|---|---|---|---|
| 1 次成功(无重试) | 1 | `LogTypeConsume` | 同现状 | 不写 |
| 重试 N 次后成功 | 1 | `LogTypeConsume` | 同现状 | 写,含全部尝试 |
| 全部失败 | 1 | `LogTypeError` | 仅最终 `bizErr.Error.Message`(已经过 429 等友好翻译),无渠道前缀 | 写,含全部尝试 |
| xAI 内容违规 | 1 | `LogTypeConsume` quota=25000 | 现状不变 | 写(如有重试历史) |

**普通用户**(`/api/log/self`):通过 `stripAdminInfoFromLogs` 同时剥 `adminInfo` 和 `retryHistory`,看到一条干净的最终记录,content 无渠道信息。

**管理员**(`/api/log/`):原样返回 `retryHistory`,前端"重试"列展开看完整明细。

## 3. `retryHistory` 数据结构

挂在 `Log.Other` 字段里,沿用现有 `usageDetails:{}` 同款分号格式:

```
adminInfo:[12,7,15,9,3];retryHistory:[
  {"attempt":1,"channel_id":12,"channel_name":"通义","key_index":0,"duration":12.3,"error":"upstream timeout","status":502},
  {"attempt":2,"channel_id":7,"channel_name":"deepseek","key_index":0,"duration":0.4,"error":"429 too many requests","status":429},
  {"attempt":3,"channel_id":15,"channel_name":"豆包","key_index":0,"duration":3.1,"error":"500 internal error","status":500},
  {"attempt":4,"channel_id":9,"channel_name":"groq","key_index":0,"duration":1.2,"error":"context length exceeded","status":400},
  {"attempt":5,"channel_id":3,"channel_name":"siliconflow","key_index":0,"duration":2.8,"error":"","status":200}
];usageDetails:{...}
```

**约定**:
- `attempt` 从 1 开始(首次=1)
- 数组始终包含最终那次:成功 → 最后一条 `error:""` + `status:200`;失败 → 最后一条带最终错误
- `adminInfo` 扁平 channel_id 数组保留,作为快速预览(向前兼容现有"重试"列)
- 多 Key 渠道 → `key_index` 反映实际使用的 key
- `error` 字段不脱敏,原汁原味上游报错,只给管理员看
- 仅在 attempt 总数 ≥ 2 时才写 `retryHistory`(单次成功不需要)

## 4. 后端改动

### 4.1 `controller/relay.go` `Relay()` 函数

引入聚合器:

```go
type retryAttempt struct {
    Attempt     int     `json:"attempt"`
    ChannelId   int     `json:"channel_id"`
    ChannelName string  `json:"channel_name"`
    KeyIndex    int     `json:"key_index"`
    Duration    float64 `json:"duration"`
    Error       string  `json:"error,omitempty"`
    Status      int     `json:"status"`
}
```

- 循环中:每次尝试后 append 一条到 `[]retryAttempt`(成功也 append)
- **删除** line 166 和 line 266 的 `recordRetryFailureLog` 调用
- 循环结束:
  - **成功路径**:把 `retryHistory` JSON 通过 `c.Set("retry_history_json", ...)` 传给下游的消费日志写入点,由其拼接到 `otherInfo`
  - **失败路径**:新函数 `recordFinalErrorLog()` 写 1 条 `LogTypeError`,content 仅 = `bizErr.Error.Message`,`other` 含 `retryHistory`
- 删除 `recordRetryFailureLog` 函数(确认无其他调用方)

### 4.2 消费日志写入点接收 `retry_history_json`

`relay/controller/helper.go` 等地方在调用 `RecordConsumeLogWithOtherAndRequestID` 前,从 `c.Get("retry_history_json")` 取出并 append 到 `otherInfo`(类似现有 `adminInfo` 的写法)。

### 4.3 其他重试链路的 content 清理

精简 `recordFailedRequestLog` / `recordMidjourneyFailedLog` / `recordRunwayFailedLog` / `recordXaiVideoFailedLog` 的 content 模板:

- 移除 `"请求失败 [%s]: "` 等前缀
- content 仅保留原始错误消息

(可选)给这些函数也加上 `retryHistory` 字段——但优先级低,本期可只做 `Relay()` 主链路,其他链路保持现状。

### 4.4 `controller/log.go` 脱敏扩展

在 `stripAdminInfoFromLogs` 中:
- 现有 `adminInfoRegex` 剥 `adminInfo`
- **新增** `retryHistoryRegex` 剥 `retryHistory:[...]`(注意 `]` 不能用 `[^\]]*` 因为 array 里有嵌套对象,要做括号配对)
- fast-path 关键字从 `"admin"` 拓宽到 `strings.Contains(other, "admin") || strings.Contains(other, "retryHistory")`
- JSON 格式也对应处理 `retry_history` / `retryHistory` 字段

## 5. 前端改动 (`ezlinkai-web-next/sections/log/tables/columns.tsx`)

### 5.1 `extractJsonFromSemicolonFormat` 拓展支持数组

现在只处理 `{` 开头的对象。需要扩展也能处理 `[` 开头的数组(`[` 计数到 `]`)。

### 5.2 解析 `retryHistory`

新增 `parseRetryHistory(row)` 函数,返回 `RetryAttempt[]` 或 `null`。

### 5.3 "重试"列展开视图

- 列表里仍显示 `1->2->3` 形式的简略链路(逻辑不变)
- 鼠标 hover / 点击展开 popover 时:
  - 如果 `retryHistory` 存在 → 渲染表格,每行显示 attempt / 渠道 / 耗时 / 错误 / 状态码
  - 否则降级到现有 `adminInfo` 扁平视图(向前兼容旧数据)

## 6. 范围与兼容性

- **范围**:本期只改 `Relay()`(文本/chat/embeddings/audio/flux 主链路)的写入聚合 + 前端展示。Image/Midjourney/Video/Runway/Sora/xAI Video 等链路只清理 content 前缀,**不强制**追加 `retryHistory`(若时间允许可在同一 PR 补)。
- **存量数据兼容**:DB 里已有的 N 条/请求的旧日志保持原样,不做迁移。前端解析时:
  - 新格式(`retryHistory` 存在)→ 用新展示
  - 旧格式(`adminInfo` 存在但 `retryHistory` 不存在)→ 退回扁平 channel ID 展示
- **后端兼容**:`adminInfo` 字段保留写,确保前端旧解析路径不破坏。

## 7. 验收

1. 触发一次 5 次重试全失败:
   - DB 只多 1 条 `LogTypeError`
   - 普通用户 API 返回的 `content` 是纯错误消息(无 "渠道=xxx" 前缀)
   - 普通用户 API 返回的 `other` 不含 `retryHistory` 和 `adminInfo`
   - 管理员 API 返回的 `other` 含 `retryHistory`,前端展开能看到 5 次明细
2. 触发一次 3 次重试后成功:
   - DB 只多 1 条 `LogTypeConsume`(quota 正常扣)
   - 管理员能看到完整 3 次明细,最后一条 `status:200, error:""`
3. 单次成功(无重试):行为完全不变
4. xAI 内容违规:扣费 + 写一条记录,行为不变
5. `go build ./... && go vet ./...` 通过
