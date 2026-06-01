# 重试日志聚合改造

**日期**: 2026-06-01
**Tag**: `alphaas-06012023`
**主提交**: `b7b3c4ed feat(log): 重试日志按请求聚合,普通用户脱敏渠道信息`
**设计文档**: [docs/superpowers/specs/2026-06-01-retry-log-consolidation-design.md](./superpowers/specs/2026-06-01-retry-log-consolidation-design.md)

---

## 1. 背景

旧实现里,`Relay` 主链路每次重试失败都写一条 `LogTypeError` 日志:

- 首次失败 → 1 条
- 每次重试失败 → 又 1 条
- 重试成功 → 之前失败的 N 条全留 + 1 条成功消费日志

**5 次重试 = 5 条日志**(全失败)或 **4 条失败 + 1 条成功 = 5 条**(重试成功)。

更糟的是 `content` 字段硬编码了渠道名/ID:

```
首次调用失败 [RequestID: xxx]: 渠道=通义(#12), 耗时=12.3s, 原因=...
```

`stripAdminInfoFromLogs` 只剥 `other.adminInfo`,**不处理 content 字段**——普通用户透过 `/api/log/self` 就能看到全部渠道信息。

## 2. 目标

| 视角 | 列表行数 | 看到的字段 | 重试详情 |
|---|---|---|---|
| 普通用户 | **1 条**(最终) | 时间/模型/quota/耗时/纯错误消息 | ❌ 看不到 |
| 管理员/root | **同样 1 条** | 同上 + 渠道 ID | ✅ 点击"重试"列展开,看全部 N 次明细 |

## 3. 改动文件清单

### 后端 Go

| 文件 | 类型 | 说明 |
|---|---|---|
| `relay/util/retry_log.go` | 新增 | `RetryAttempt` 结构、`PublishFailedRetryHistory`、`AppendRetryHistoryOther` |
| `controller/retry_log.go` | 新增 | `recordFinalErrorLog`(聚合后的失败日志写入) |
| `controller/relay.go` | 修改 | 4 条主链路(`Relay`/`RelayGemini`/`RelayClaude`/`RelayResponse`)重构 |
| `controller/log.go` | 修改 | `stripAdminInfoFromLogs` 同时剥 `adminInfo` + `retryHistory` |
| `relay/controller/helper.go` | 修改 | text/chat 消费日志写入点接入 `AppendRetryHistoryOther` |
| `relay/controller/claude.go` | 修改 | 同上,且 `go recordClaudeConsumption(... c.Copy() ...)` |
| `relay/controller/gemini.go` | 修改 | 同上 |
| `relay/controller/opeai_response.go` | 修改 | 同上 |
| `relay/controller/text.go` | 修改 | `go postConsumeQuota(... c.Copy() ...)` |
| `relay/controller/audio.go` | 修改 | 4 个 `go func()` 用 `c.Copy()`,otherInfo 改 `audioUsageDetails:{...}` |
| `relay/controller/image.go` | 修改 | 4 个消费日志写入点接入 `AppendRetryHistoryOther` |

### 前端 (TypeScript / Next.js)

| 文件 | 类型 | 说明 |
|---|---|---|
| `ezlinkai-web-next/sections/log/tables/columns.tsx` | 修改 | `extractJsonFromSemicolonFormat` 支持数组、新增 `parseRetryHistory`、重试列 UX 重写(Tooltip → Popover + 颜色徽章 + timeline) |

### 文档

| 文件 | 类型 | 说明 |
|---|---|---|
| `docs/superpowers/specs/2026-06-01-retry-log-consolidation-design.md` | 新增 | 设计文档 |
| `docs/retry-log-consolidation-2026-06-01.md` | 新增 | 本文件 |

---

## 4. 核心改动详解

### 4.1 后端聚合层(`relay/util/retry_log.go`)

```go
type RetryAttempt struct {
    Attempt     int     `json:"attempt"`
    ChannelId   int     `json:"channel_id"`
    ChannelName string  `json:"channel_name"`
    KeyIndex    int     `json:"key_index"`
    Duration    float64 `json:"duration"`
    Error       string  `json:"error,omitempty"`
    Status      int     `json:"status"`
}

// PublishFailedRetryHistory: 失败循环里每次 append 后,把 JSON 写进 ctx
// AppendRetryHistoryOther: 成功消费日志写入前调用,把 ctx 里失败历史 + 最终成功条目拼到 other
```

### 4.2 `Relay()` 主链路改造(`controller/relay.go`)

```diff
- // 每次失败都写一条 LogTypeError
- recordRetryFailureLog(ctx, userId, channel.Id, ..., bizErr.Error.Message, ...)

+ // 改为 append 到聚合栈
+ retryAttempts = append(retryAttempts, util.RetryAttempt{...})
+ util.PublishFailedRetryHistory(c, retryAttempts)

  // 循环结束后,统一写一条:
+ if !isXAIContentViolation(bizErr.StatusCode, bizErr.Error.Message) {
+     recordFinalErrorLog(ctx, c, bizErr, retryAttempts, channelHistory, ...)
+ }
```

`recordFinalErrorLog` 的 `content` 仅为 `bizErr.Error.Message`(纯上游错误,无渠道前缀);`other` 字段含 `adminInfo:[ids];retryHistory:[{...}];affinityTag`。

### 4.3 普通用户脱敏(`controller/log.go`)

```diff
  func stripAdminInfoFromLogs(logs []*model.Log) {
      for _, log := range logs {
-         if !strings.Contains(log.Other, "admin") { continue }
+         if !strings.Contains(log.Other, "admin") &&
+            !strings.Contains(log.Other, "retryHistory") { continue }
          ...
          cleaned := adminInfoRegex.ReplaceAllString(other, "")
+         cleaned = stripRetryHistorySegment(cleaned)  // 方括号配对剥 retryHistory:[...]
      }
  }
```

新增 `stripRetryHistorySegment` 是手写的 `[`/`]` 配对扫描(因为 `retryHistory` 是 JSON 对象数组,简单 regex 会被嵌套 `}` 击穿)。

### 4.4 前端重试列 UX(`columns.tsx`)

旧:`12->...(5)` 文本 + Tooltip(hover 才显示,移动端无效)

新:**颜色徽章 + Popover**

```
┌─ 重试明细                共 5 次 · 累计 19.8s
├ (1) 通义       #12       [502]  12.30s
│     upstream timeout
├ (2) deepseek   #7        [429]   0.40s
│     429 too many requests
├ (3) 豆包       #15       [500]   3.10s
│     500 internal error
├ (4) groq       #9        [400]   1.20s
│     context length exceeded
└ (5) siliconflow #3       [200]   2.80s   ← 最后一行有浅绿/浅红背景
      ✓ 成功
```

- 单元格:`↻ 5 ✓` 绿(最终成功) / `↻ 5 ✗` 红(最终失败)
- 点击触发 Popover,可滚动、可复制
- 单次成功(`attemptCount <= 1`)显示 `-`
- 按钮加 `onClick={(e) => e.stopPropagation()}` 避免触发 TableRow 的展开

---

## 5. Code Review 发现与修复

经过 **两轮** code review,发现并修复了 8 个高/中危问题。

### 第一轮修复(merge 前)

| # | 文件 | 问题 | 修复 |
|---|---|---|---|
| 1 | `controller/retry_log.go` | `duration` 硬编码 0.0,SQL `WHERE duration > N` 漏失败 | 累加 `attempts[i].Duration` 之和 |
| 2 | `controller/relay.go` affinity skip | 用 `recordFailedRequestLog` 写 `LogTypeConsume` 而不是 `LogTypeError`,缺 retryHistory | 替换为 `recordFinalErrorLog` |
| 3 | `controller/relay.go` xAI mid-loop | xAI 重试中途违规丢失前 N-1 次明细 | `recordXAIContentViolationCharge` 接收 `retryAttempts` 嵌入 retryHistory |
| 4 | `relay/controller/{audio,image}.go` | 这些消费日志写入点没接 `AppendRetryHistoryOther`,图片/音频重试成功丢失 retry 明细 | 加上调用(audio.go × 4,image.go × 4) |

### 第二轮修复(push 前)

| # | 文件 | 问题 | 修复 |
|---|---|---|---|
| 1 | `relay/controller/{audio,claude,gemini,opeai_response,text}.go` | 8 处 goroutine 调用 `AppendRetryHistoryOther` 读 `gin.Context`,gin 用 sync.Pool 回收 ctx,有 race | 所有 goroutine 用 `c.Copy()` 隔离 |
| 2 | `relay/controller/audio.go` | otherInfo 是裸 JSON 字面量 `{...}`,被拼 `;retryHistory:[...]` 后既非 JSON 又非分号格式,前端解析失败丢失 audio 计费明细 | otherInfo 改为 `audioUsageDetails:{...}` 分号格式 |
| 3 | `columns.tsx` | Popover 按钮无 `stopPropagation`,点击同时触发 TableRow 的 `toggleExpanded` | 加 `onClick={(e) => e.stopPropagation()}` |
| 4 | `controller/relay.go` affinity skip | xAI 违规 + skip_retry_on_failure 双发日志(charge log + error log) | affinity skip 块加 `!isXAIContentViolation` 守卫 |

---

## 6. 已知遗留 trade-off

Code review 还发现以下问题,**未修**——属于聚合设计的本质 trade-off 或低优先级:

| 现象 | 后果 | 修复成本 |
|---|---|---|
| `Log.ChannelId` 列只存最后一次的渠道 | SQL `WHERE channel_id=X AND type=Error` 漏中间失败 | 需加 `failed_channels` 索引列(schema migration) |
| `RetryAttempt.Status` 硬编码 200 | 上游返回 201/202 时管理员看到的是 200 | 需读 `c.Writer.Status()` |
| 429 友好文案覆盖 retryAttempts 最后一条 Error | 管理员看 retryHistory 无法分辨真正限流的上游 | 让 friendly 文案只改 bizErr 不改 attempts |
| Midjourney 上游 Description+Result 都空时,DB content 列空白 | 分诊 SQL `WHERE content LIKE '%Midjourney%'` 命中不到 | 兜底 content 填 `HTTP <code>` 或子系统名 |
| `SearchAllLogs` LIKE 搜不到旧关键词(`请求失败`/`第N次重试失败`) | 运维既有搜索习惯失效 | 文档化,或扩展搜索到 `other`/`x_request_id` |
| `PublishFailedRetryHistory` 每次循环 marshal 一次 → O(n²) | RetryTimes=9 时 ~90 条 marshal | 直接把 slice 存 ctx,只在 success 时 marshal |
| `RetryAttempt.Error` 有 `omitempty` | 成功条目 error 字段缺失,前端非可选链调用会 NPE | 移除 omitempty,或前端用 `?.` |
| 数据 race:`bizErr.Error.Message` 在 `processChannelRelayError` goroutine 启动后被主 goroutine 改写(429 友好文案) | `go test -race` 必然 flag,生产可能撕裂字符串 | launch goroutine 前先 snapshot |

---

## 7. 验证

```bash
# 后端
go build ./...     # 通过
go vet ./...       # 通过

# 前端(ezlinkai-web-next)
npx tsc --noEmit -p tsconfig.json   # 通过
```

行为验证:
- 触发 5 次重试全失败 → DB 只多 1 条 `LogTypeError`,普通用户 `/api/log/self` 看到的 content 是纯错误消息(无渠道前缀),`other` 不含 `retryHistory`/`adminInfo`
- 触发 3 次重试后成功 → DB 只多 1 条 `LogTypeConsume`,管理员前端"重试"列展开能看到 3 次明细
- 单次成功 → 行为完全不变,重试列显示 `-`

## 8. 发版

```
分支         旧 HEAD       新 HEAD       状态
main         c9145a19  →   b7b3c4ed     已 push
ins          7d139a96  →   b7b3c4ed     已 push(跳过 97 个 main 历史 commit)
feat/retry-log-consolidation             b7b3c4ed(保留)
tag          alphaas-06012023            指向 b7b3c4ed
```

如 CI/CD 按 tag 触发部署,`alphaas-06012023` 即本次 release。
