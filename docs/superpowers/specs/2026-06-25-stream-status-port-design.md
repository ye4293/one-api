# 设计文档：移植 StreamStatus 机制

**日期**：2026-06-25  
**分支**：待创建  
**状态**：待实施

---

## 背景与目标

当前 ezlinkai 对流式请求（SSE）的结束原因没有持久化记录。客户端断开、超时、scanner 报错等事件只写运行时日志，`logs` 表里无法区分"正常完成"和"中途断开"。

目标：从 new-api 完整移植 `StreamStatus` 机制，使每条流式请求日志的 `Other` 字段都包含 `streamStatus` 段，记录结束原因、是否有软错误及错误详情。

---

## 架构

### 新建文件

**`relay/util/stream_status.go`**

定义 `StreamEndReason`（string 类型枚举）和 `StreamStatus` 结构体：

```go
type StreamEndReason string

const (
    StreamEndReasonNone        StreamEndReason = ""
    StreamEndReasonDone        StreamEndReason = "done"
    StreamEndReasonTimeout     StreamEndReason = "timeout"
    StreamEndReasonClientGone  StreamEndReason = "client_gone"
    StreamEndReasonScannerErr  StreamEndReason = "scanner_error"
    StreamEndReasonHandlerStop StreamEndReason = "handler_stop"
    StreamEndReasonEOF         StreamEndReason = "eof"
    StreamEndReasonPanic       StreamEndReason = "panic"
    StreamEndReasonPingFail    StreamEndReason = "ping_fail"
)

type StreamErrorEntry struct {
    Message   string
    Timestamp time.Time
}

type StreamStatus struct {
    EndReason  StreamEndReason
    EndError   error
    endOnce    sync.Once
    mu         sync.Mutex
    Errors     []StreamErrorEntry
    ErrorCount int
}
```

方法：
- `NewStreamStatus() *StreamStatus`
- `SetEndReason(reason, err)` — `sync.Once` 保护，先到先得
- `RecordError(msg)` — 最多保存 20 条，超出只计数
- `HasErrors() bool`
- `TotalErrorCount() int`
- `IsNormalEnd() bool` — `done` / `eof` / `handler_stop` 视为正常
- `Summary() string` — 日志用

**`relay/util/stream_status_test.go`**

覆盖：SetEndReason 幂等性、RecordError 上限、IsNormalEnd 各分支、并发安全。

---

### 修改文件

**`relay/util/relay_meta.go`**

`RelayMeta` struct 末尾加一个字段：

```go
// StreamStatus 记录流式响应的结束原因和过程错误，非流式请求为 nil
StreamStatus *StreamStatus
```

---

**`relay/helper/stream_scanner.go`**

函数开头初始化：
```go
info.StreamStatus = util.NewStreamStatus()
```

各退出点设置 EndReason（对应现有的 logger 调用旁边补上）：

| 退出点 | EndReason |
|---|---|
| 收到 `[DONE]` | `done` |
| `dataHandler` 返回 false | `handler_stop` |
| `dataHandler` 超时（10s）| `handler_stop` + RecordError |
| `scanner.Err()` 非 nil | `scanner_error` + RecordError |
| scanner goroutine panic | `panic` + RecordError |
| ping 发送超时 | `ping_fail` |
| 主 select ticker 超时 | `timeout` |
| 主 select `client ctx.Done()` | `client_gone` |
| 主 select stopChan | EndReason 已由触发方设置，无需重复 |

上游 EOF 无 `[DONE]`（scanner 正常结束但未收到 DONE）：在 scanner goroutine 退出时，若 EndReason 仍为空，设为 `eof`。

---

**`relay/controller/helper.go`**

在 `postConsumeQuota` 的 `otherInfo` 拼装区末尾（`AppendRetryHistoryOther` 之后）追加：

```go
otherInfo = util.AppendStreamStatusOther(otherInfo, meta.StreamStatus)
```

同时在 `relay/util/` 新增辅助函数（可放在 `stream_status.go` 末尾）：

```go
// AppendStreamStatusOther 将 StreamStatus 序列化为 streamStatus:{...} 段追加到 otherInfo。
// StreamStatus 为 nil 或无数据时直接返回原 otherInfo。
func AppendStreamStatusOther(otherInfo string, ss *StreamStatus) string
```

序列化格式与现有 `billingDetails`/`retryHistory` 段保持一致：`streamStatus:{...}`，`;` 分隔。

---

## 数据流

```
StreamScannerHandler
  └─ 初始化 info.StreamStatus = NewStreamStatus()
     ├─ scanner goroutine → SetEndReason / RecordError
     ├─ ping goroutine → SetEndReason("ping_fail")
     └─ 主 select → SetEndReason(timeout / client_gone)

流结束 → postConsumeQuota（goroutine）
  └─ AppendStreamStatusOther(otherInfo, meta.StreamStatus)
       └─ Log.Other 追加 streamStatus 段
```

**`Log.Other` 最终格式示例（客户端断开）：**
```
channelHistory:{...};billingDetails:{...};retryHistory:[...];streamStatus:{"status":"error","end_reason":"client_gone","end_error":"context canceled"}
```

**`Log.Other` 最终格式示例（正常完成）：**
```
channelHistory:{...};billingDetails:{...};streamStatus:{"status":"ok","end_reason":"done"}
```

**非流式请求**：`meta.StreamStatus` 为 nil，`AppendStreamStatusOther` 直接返回原 `otherInfo`，无任何影响。

---

## 影响范围

- 不影响非流式请求路径
- 不影响现有 `billingDetails`、`retryHistory` 字段
- `Log.Other` 新增 `streamStatus` 段，仅追加，不破坏现有解析
- 不涉及数据库 schema 变更（`Other` 是已有 string 字段）

---

## 验证方式

1. `go build ./...` 无编译错误
2. `go vet ./...` 无静态分析问题
3. 单元测试 `relay/util/stream_status_test.go` 全部通过
4. 手动触发客户端断开场景：查询 `logs` 表，`Other` 字段包含 `"end_reason":"client_gone"`
5. 正常完成的流式请求：`Other` 字段包含 `"end_reason":"done"`
6. 超时场景（可临时将超时改短）：`Other` 字段包含 `"end_reason":"timeout"`
