# StreamStatus 移植实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 new-api 的 `StreamStatus` 机制完整移植到 ezlinkai，使每条流式请求日志的 `Other` 字段包含 `streamStatus` 段，记录结束原因（`done`/`timeout`/`client_gone` 等）和过程软错误。

**Architecture:** 在 `relay/util` 包新增 `StreamStatus` 结构体（与 `RelayMeta` 同包，无循环引用）；`StreamScannerHandler` 在各退出点调用 `SetEndReason`/`RecordError`；`postConsumeQuota` 末尾将 `StreamStatus` 序列化追加到 `Log.Other` 的 `streamStatus:{}` 段。

**Tech Stack:** Go 标准库（`sync`, `encoding/json`, `time`）、gin、现有 `relay/util` 包

---

## 文件清单

| 文件 | 动作 |
|---|---|
| `relay/util/stream_status.go` | **新建** — `StreamStatus` 结构体、`StreamEndReason` 枚举、`AppendStreamStatusOther` |
| `relay/util/stream_status_test.go` | **新建** — 单元测试 |
| `relay/util/relay_meta.go` | **修改** — 加 `StreamStatus *StreamStatus` 字段 |
| `relay/helper/stream_scanner.go` | **修改** — 初始化 StreamStatus；各退出点设 EndReason/RecordError |
| `relay/controller/helper.go` | **修改** — `postConsumeQuota` 末尾追加 `AppendStreamStatusOther` |

---

## Task 1: 新建 `stream_status.go` — 核心结构体

**Files:**
- Create: `relay/util/stream_status.go`

- [ ] **Step 1: 创建文件**

完整写入以下内容：

```go
package util

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

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

const maxStreamErrorEntries = 20

type StreamErrorEntry struct {
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

type StreamStatus struct {
	EndReason StreamEndReason
	EndError  error
	endOnce   sync.Once

	mu         sync.Mutex
	Errors     []StreamErrorEntry
	ErrorCount int
}

func NewStreamStatus() *StreamStatus {
	return &StreamStatus{}
}

func (s *StreamStatus) SetEndReason(reason StreamEndReason, err error) {
	if s == nil {
		return
	}
	s.endOnce.Do(func() {
		s.EndReason = reason
		s.EndError = err
	})
}

func (s *StreamStatus) RecordError(msg string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ErrorCount++
	if len(s.Errors) < maxStreamErrorEntries {
		s.Errors = append(s.Errors, StreamErrorEntry{
			Message:   msg,
			Timestamp: time.Now(),
		})
	}
}

func (s *StreamStatus) HasErrors() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ErrorCount > 0
}

func (s *StreamStatus) TotalErrorCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ErrorCount
}

// IsNormalEnd 判断流是否正常结束。done/eof/handler_stop 视为正常。
func (s *StreamStatus) IsNormalEnd() bool {
	if s == nil {
		return true
	}
	return s.EndReason == StreamEndReasonDone ||
		s.EndReason == StreamEndReasonEOF ||
		s.EndReason == StreamEndReasonHandlerStop
}

func (s *StreamStatus) Summary() string {
	if s == nil {
		return "StreamStatus<nil>"
	}
	b := &strings.Builder{}
	fmt.Fprintf(b, "reason=%s", s.EndReason)
	if s.EndError != nil {
		fmt.Fprintf(b, " end_error=%q", s.EndError.Error())
	}
	s.mu.Lock()
	if s.ErrorCount > 0 {
		fmt.Fprintf(b, " soft_errors=%d", s.ErrorCount)
	}
	s.mu.Unlock()
	return b.String()
}

// AppendStreamStatusOther 将 StreamStatus 序列化为 streamStatus:{...} 段，
// 以 ; 分隔追加到 otherInfo。StreamStatus 为 nil 时直接返回原 otherInfo。
func AppendStreamStatusOther(otherInfo string, ss *StreamStatus) string {
	if ss == nil {
		return otherInfo
	}

	status := "ok"
	if !ss.IsNormalEnd() || ss.HasErrors() {
		status = "error"
	}

	type streamStatusJSON struct {
		Status     string   `json:"status"`
		EndReason  string   `json:"end_reason"`
		EndError   string   `json:"end_error,omitempty"`
		ErrorCount int      `json:"error_count,omitempty"`
		Errors     []string `json:"errors,omitempty"`
	}

	data := streamStatusJSON{
		Status:    status,
		EndReason: string(ss.EndReason),
	}
	if ss.EndError != nil {
		data.EndError = ss.EndError.Error()
	}
	if ss.ErrorCount > 0 {
		data.ErrorCount = ss.ErrorCount
		ss.mu.Lock()
		msgs := make([]string, 0, len(ss.Errors))
		for _, e := range ss.Errors {
			msgs = append(msgs, e.Message)
		}
		ss.mu.Unlock()
		data.Errors = msgs
	}

	b, err := json.Marshal(data)
	if err != nil {
		return otherInfo
	}
	seg := "streamStatus:" + string(b)
	if otherInfo == "" {
		return seg
	}
	return otherInfo + ";" + seg
}
```

- [ ] **Step 2: 编译确认无错误**

```bash
go build ./relay/util/...
```

期望：无输出（无错误）。

- [ ] **Step 3: Commit**

```bash
git add relay/util/stream_status.go
git commit -m "feat(stream): add StreamStatus struct and AppendStreamStatusOther"
```

---

## Task 2: 单元测试 `stream_status_test.go`

**Files:**
- Create: `relay/util/stream_status_test.go`

- [ ] **Step 1: 创建测试文件**

```go
package util

import (
	"sync"
	"testing"
)

func TestSetEndReason_FirstWins(t *testing.T) {
	s := NewStreamStatus()
	s.SetEndReason(StreamEndReasonDone, nil)
	s.SetEndReason(StreamEndReasonTimeout, nil)
	if s.EndReason != StreamEndReasonDone {
		t.Fatalf("expected done, got %s", s.EndReason)
	}
}

func TestSetEndReason_Idempotent(t *testing.T) {
	s := NewStreamStatus()
	s.SetEndReason(StreamEndReasonDone, nil)
	s.SetEndReason(StreamEndReasonDone, nil) // should not panic
}

func TestRecordError_Limit(t *testing.T) {
	s := NewStreamStatus()
	for i := 0; i < 25; i++ {
		s.RecordError("err")
	}
	if s.ErrorCount != 25 {
		t.Fatalf("expected ErrorCount=25, got %d", s.ErrorCount)
	}
	if len(s.Errors) != maxStreamErrorEntries {
		t.Fatalf("expected len(Errors)=%d, got %d", maxStreamErrorEntries, len(s.Errors))
	}
}

func TestHasErrors(t *testing.T) {
	s := NewStreamStatus()
	if s.HasErrors() {
		t.Fatal("expected no errors initially")
	}
	s.RecordError("oops")
	if !s.HasErrors() {
		t.Fatal("expected HasErrors after RecordError")
	}
}

func TestIsNormalEnd(t *testing.T) {
	cases := []struct {
		reason StreamEndReason
		want   bool
	}{
		{StreamEndReasonDone, true},
		{StreamEndReasonEOF, true},
		{StreamEndReasonHandlerStop, true},
		{StreamEndReasonTimeout, false},
		{StreamEndReasonClientGone, false},
		{StreamEndReasonScannerErr, false},
		{StreamEndReasonPanic, false},
		{StreamEndReasonPingFail, false},
	}
	for _, tc := range cases {
		s := NewStreamStatus()
		s.SetEndReason(tc.reason, nil)
		if got := s.IsNormalEnd(); got != tc.want {
			t.Errorf("IsNormalEnd(%s) = %v, want %v", tc.reason, got, tc.want)
		}
	}
}

func TestNilSafe(t *testing.T) {
	var s *StreamStatus
	s.SetEndReason(StreamEndReasonDone, nil) // should not panic
	s.RecordError("msg")                     // should not panic
	_ = s.HasErrors()
	_ = s.TotalErrorCount()
	_ = s.IsNormalEnd()
	_ = s.Summary()
}

func TestConcurrent(t *testing.T) {
	s := NewStreamStatus()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			s.SetEndReason(StreamEndReasonDone, nil)
		}()
		go func() {
			defer wg.Done()
			s.RecordError("concurrent error")
		}()
	}
	wg.Wait()
	if s.EndReason != StreamEndReasonDone {
		t.Fatalf("expected done after concurrent sets, got %s", s.EndReason)
	}
}

func TestAppendStreamStatusOther_NilReturnsOriginal(t *testing.T) {
	result := AppendStreamStatusOther("existing", nil)
	if result != "existing" {
		t.Fatalf("expected 'existing', got %q", result)
	}
}

func TestAppendStreamStatusOther_NormalEnd(t *testing.T) {
	s := NewStreamStatus()
	s.SetEndReason(StreamEndReasonDone, nil)
	result := AppendStreamStatusOther("", s)
	want := `streamStatus:{"status":"ok","end_reason":"done"}`
	if result != want {
		t.Fatalf("expected %q, got %q", want, result)
	}
}

func TestAppendStreamStatusOther_ClientGone(t *testing.T) {
	s := NewStreamStatus()
	s.SetEndReason(StreamEndReasonClientGone, fmt.Errorf("context canceled"))
	result := AppendStreamStatusOther("billingDetails:{}", s)
	if !strings.Contains(result, "client_gone") {
		t.Fatalf("expected client_gone in %q", result)
	}
	if !strings.Contains(result, "billingDetails:{}") {
		t.Fatalf("expected original prefix preserved in %q", result)
	}
}
```

注意：需要在测试文件顶部加 import：

```go
import (
	"fmt"
	"strings"
	"sync"
	"testing"
)
```

完整文件头部：

```go
package util

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)
```

- [ ] **Step 2: 运行测试，确认全部通过**

```bash
go test ./relay/util/... -run TestSet -v
go test ./relay/util/... -run TestRecord -v
go test ./relay/util/... -run TestHas -v
go test ./relay/util/... -run TestIsNormal -v
go test ./relay/util/... -run TestNil -v
go test ./relay/util/... -run TestConcurrent -race -v
go test ./relay/util/... -run TestAppend -v
```

每条期望：`PASS`

- [ ] **Step 3: Commit**

```bash
git add relay/util/stream_status_test.go
git commit -m "test(stream): add StreamStatus unit tests"
```

---

## Task 3: 修改 `relay_meta.go` — 加字段

**Files:**
- Modify: `relay/util/relay_meta.go`

- [ ] **Step 1: 在 `RelayMeta` struct 末尾加字段**

在 `relay/util/relay_meta.go` 的 `RelayMeta` struct 中，找到最后一个字段（目前是 `UserChannelRatio float64`），在其后加一行：

```go
	// StreamStatus 记录流式响应的结束原因和过程错误，非流式请求为 nil
	StreamStatus *StreamStatus
```

修改后 struct 末尾形如：

```go
	// 当前用户对当前渠道类型的额外折扣倍率，默认 1.0
	UserChannelRatio float64
	// StreamStatus 记录流式响应的结束原因和过程错误，非流式请求为 nil
	StreamStatus *StreamStatus
}
```

- [ ] **Step 2: 编译确认**

```bash
go build ./relay/util/...
```

期望：无输出。

- [ ] **Step 3: Commit**

```bash
git add relay/util/relay_meta.go
git commit -m "feat(stream): add StreamStatus field to RelayMeta"
```

---

## Task 4: 修改 `stream_scanner.go` — 接入 StreamStatus

**Files:**
- Modify: `relay/helper/stream_scanner.go`

此任务修改点较多，逐一列出。**整个 Task 完成后一次性 commit。**

`relay/helper/stream_scanner.go` 已经 import 了 `relay/util`（`info *util.RelayMeta`），无需新增 import。

- [ ] **Step 1: 函数开头，初始化 StreamStatus**

找到：
```go
func StreamScannerHandler(c *gin.Context, resp *http.Response, info *util.RelayMeta, dataHandler func(data string) bool) {

	if resp == nil || dataHandler == nil {
		return
	}
	println("ping interval seconds:")
```

替换为：
```go
func StreamScannerHandler(c *gin.Context, resp *http.Response, info *util.RelayMeta, dataHandler func(data string) bool) {

	if resp == nil || dataHandler == nil || info == nil {
		return
	}
	info.StreamStatus = util.NewStreamStatus()
```

（同时删除那行遗留的 `println("ping interval seconds:")` 调试输出）

- [ ] **Step 2: scanner goroutine — panic recover 加 RecordError + SetEndReason**

找到（scanner goroutine defer 里的 panic recover）：
```go
		defer func() {
			wg.Done()
			if r := recover(); r != nil {
				logger.Error(c, fmt.Sprintf("scanner goroutine panic: %v", r))
			}
			common.SafeSendBool(stopChan, true)
```

替换为：
```go
		defer func() {
			wg.Done()
			if r := recover(); r != nil {
				msg := fmt.Sprintf("scanner goroutine panic: %v", r)
				logger.Error(c, msg)
				info.StreamStatus.RecordError(msg)
				info.StreamStatus.SetEndReason(util.StreamEndReasonPanic, fmt.Errorf("%v", r))
			}
			common.SafeSendBool(stopChan, true)
```

- [ ] **Step 3: scanner goroutine — dataHandler 返回 false 设 handler_stop**

找到：
```go
				case success := <-done:
					if !success {
						return
					}
```

替换为：
```go
				case success := <-done:
					if !success {
						info.StreamStatus.SetEndReason(util.StreamEndReasonHandlerStop, nil)
						return
					}
```

- [ ] **Step 4: scanner goroutine — dataHandler 超时设 handler_stop + RecordError**

找到：
```go
				case <-time.After(10 * time.Second):
					logger.Error(c, "data handler timeout")
					return
```

替换为：
```go
				case <-time.After(10 * time.Second):
					logger.Error(c, "data handler timeout")
					info.StreamStatus.RecordError("data handler timeout")
					info.StreamStatus.SetEndReason(util.StreamEndReasonHandlerStop, fmt.Errorf("data handler timeout"))
					return
```

- [ ] **Step 5: scanner goroutine — 收到 [DONE] 设 done**

找到：
```go
			} else {
				// done, 处理完成标志，直接退出停止读取剩余数据防止出错
				logger.Info(c, "received [DONE], stopping scanner")
				return
			}
```

替换为：
```go
			} else {
				// done, 处理完成标志，直接退出停止读取剩余数据防止出错
				logger.Info(c, "received [DONE], stopping scanner")
				info.StreamStatus.SetEndReason(util.StreamEndReasonDone, nil)
				return
			}
```

- [ ] **Step 6: scanner goroutine — scanner.Err() 处设 scanner_error / eof**

找到：
```go
		if err := scanner.Err(); err != nil {
			if err != io.EOF && !strings.Contains(err.Error(), "use of closed network connection") {
				logger.Error(c, "scanner error: "+err.Error())
			}
		}
```

替换为：
```go
		if err := scanner.Err(); err != nil {
			if err != io.EOF && !strings.Contains(err.Error(), "use of closed network connection") {
				logger.Error(c, "scanner error: "+err.Error())
				info.StreamStatus.RecordError("scanner error: " + err.Error())
				info.StreamStatus.SetEndReason(util.StreamEndReasonScannerErr, err)
			} else {
				info.StreamStatus.SetEndReason(util.StreamEndReasonEOF, nil)
			}
		} else {
			// scanner.Scan() 正常返回 false（无错误），表示上游 EOF 但未收到 [DONE]
			info.StreamStatus.SetEndReason(util.StreamEndReasonEOF, nil)
		}
```

- [ ] **Step 7: ping goroutine — panic recover 加 RecordError + SetEndReason**

找到（ping goroutine defer 里的 panic recover）：
```go
			defer func() {
				wg.Done()
				if r := recover(); r != nil {
					logger.Error(c, fmt.Sprintf("ping goroutine panic: %v", r))
					common.SafeSendBool(stopChan, true)
				}
```

替换为：
```go
			defer func() {
				wg.Done()
				if r := recover(); r != nil {
					msg := fmt.Sprintf("ping goroutine panic: %v", r)
					logger.Error(c, msg)
					info.StreamStatus.RecordError(msg)
					info.StreamStatus.SetEndReason(util.StreamEndReasonPanic, fmt.Errorf("%v", r))
					common.SafeSendBool(stopChan, true)
				}
```

- [ ] **Step 8: ping goroutine — ping 发送超时设 ping_fail**

找到：
```go
					case <-time.After(10 * time.Second):
						logger.Error(c, "ping data send timeout")
						return
```

替换为：
```go
					case <-time.After(10 * time.Second):
						logger.Error(c, "ping data send timeout")
						info.StreamStatus.SetEndReason(util.StreamEndReasonPingFail, fmt.Errorf("ping send timeout"))
						return
```

- [ ] **Step 9: ping goroutine — pingTimeout（30min）设 ping_fail**

找到（pingTimeout.C 触发时）：
```go
				case <-pingTimeout.C:
					logger.Error(c, "ping goroutine max duration reached")
					return
```

替换为：
```go
				case <-pingTimeout.C:
					logger.Error(c, "ping goroutine max duration reached")
					info.StreamStatus.SetEndReason(util.StreamEndReasonPingFail, fmt.Errorf("ping goroutine max duration reached"))
					return
```

- [ ] **Step 10: 主 select — 设 timeout / client_gone，并补日志改善**

找到：
```go
	// 主循环等待完成或超时
	select {
	case <-ticker.C:
		// 超时处理逻辑
		logger.Error(c, "streaming timeout")
	case <-stopChan:
		// 正常结束
		logger.Info(c, "streaming finished")
	case <-c.Request.Context().Done():
		// 客户端断开连接
		logger.Info(c, "client disconnected")
	}
```

替换为：
```go
	// 主循环等待完成或超时
	select {
	case <-ticker.C:
		info.StreamStatus.SetEndReason(util.StreamEndReasonTimeout, nil)
		logger.Error(c, "streaming timeout")
	case <-stopChan:
		// EndReason 已由触发该 stopChan 的 goroutine 设置
		if info.StreamStatus.IsNormalEnd() && !info.StreamStatus.HasErrors() {
			logger.Info(c, fmt.Sprintf("stream ended: %s", info.StreamStatus.Summary()))
		} else {
			logger.Warn(c, fmt.Sprintf("stream ended with issues: %s", info.StreamStatus.Summary()))
		}
	case <-c.Request.Context().Done():
		info.StreamStatus.SetEndReason(util.StreamEndReasonClientGone, c.Request.Context().Err())
		logger.Info(c, "client disconnected")
	}
```

注意：`logger.Warn` 需要确认 ezlinkai logger 包有此函数。检查：

```bash
grep -n "^func Warn\b" C:/Users/brows/Desktop/ezlinkai/common/logger/*.go
```

若不存在，将 `logger.Warn` 改为 `logger.Error`。

- [ ] **Step 11: 编译确认**

```bash
go build ./relay/helper/...
```

期望：无输出。

- [ ] **Step 12: Commit**

```bash
git add relay/helper/stream_scanner.go
git commit -m "feat(stream): wire StreamStatus in StreamScannerHandler"
```

---

## Task 5: 修改 `relay/controller/helper.go` — 写入 Other 字段

**Files:**
- Modify: `relay/controller/helper.go`

- [ ] **Step 1: 在 `postConsumeQuota` 中追加 streamStatus 段**

找到（`AppendRetryHistoryOther` 调用行）：
```go
		// 把重试历史（如有）也拼进 other，供管理员展开查看
		otherInfo = util.AppendRetryHistoryOther(c, otherInfo, duration)
		// 获取 X-Request-ID
		xRequestID := c.GetString("X-Request-ID")
```

在 `AppendRetryHistoryOther` 行后、`xRequestID` 行前插入：

```go
		// 把流式结束状态（如有）拼进 other
		otherInfo = util.AppendStreamStatusOther(otherInfo, meta.StreamStatus)
```

修改后该区域形如：

```go
		// 把重试历史（如有）也拼进 other，供管理员展开查看
		otherInfo = util.AppendRetryHistoryOther(c, otherInfo, duration)
		// 把流式结束状态（如有）拼进 other
		otherInfo = util.AppendStreamStatusOther(otherInfo, meta.StreamStatus)
		// 获取 X-Request-ID
		xRequestID := c.GetString("X-Request-ID")
```

- [ ] **Step 2: 编译确认**

```bash
go build ./relay/controller/...
```

期望：无输出。

- [ ] **Step 3: 全量编译 + vet**

```bash
go build ./... && go vet ./...
```

期望：无任何输出。

- [ ] **Step 4: 跑全量测试**

```bash
go test ./...
```

期望：所有测试通过，`stream_status_test.go` 中的测试全部 PASS。

- [ ] **Step 5: Commit**

```bash
git add relay/controller/helper.go
git commit -m "feat(stream): append streamStatus to Log.Other in postConsumeQuota"
```

---

## Task 6: 更新 CHANGELOG

**Files:**
- Modify: `docs/CHANGELOG.md`

- [ ] **Step 1: 在 CHANGELOG.md 顶部追加记录**

```markdown
## 2026-06-25

### feat(stream): 移植 StreamStatus 机制，持久化流式结束原因
- **分支**: `main`
- **类型**: 新功能
- **涉及文件**:
  - `relay/util/stream_status.go`（新建）
  - `relay/util/stream_status_test.go`（新建）
  - `relay/util/relay_meta.go`
  - `relay/helper/stream_scanner.go`
  - `relay/controller/helper.go`
- **说明**: 从 new-api 完整移植 StreamStatus 机制。流式请求的 logs.Other 字段现在包含
  `streamStatus:{status, end_reason, end_error, errors}` 段，支持 done/timeout/client_gone/
  scanner_error/handler_stop/eof/panic/ping_fail 共 8 种结束原因。
- **关联计划**: `docs/superpowers/plans/2026-06-25-stream-status-port.md`
```

- [ ] **Step 2: Commit**

```bash
git add docs/CHANGELOG.md
git commit -m "docs: update CHANGELOG for StreamStatus port"
```

---

## 验收清单

- [ ] `go build ./... && go vet ./...` 无输出
- [ ] `go test ./relay/util/... -race` 全部 PASS
- [ ] 查询 `logs` 表，流式请求的 `Other` 字段包含 `streamStatus:` 段
- [ ] 客户端断开场景：`end_reason` 为 `client_gone`
- [ ] 正常完成场景：`end_reason` 为 `done`，`status` 为 `ok`
- [ ] 非流式请求的 `Other` 字段不含 `streamStatus:` 段
