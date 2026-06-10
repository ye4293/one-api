# 全链路请求审计 → BigQuery 实现计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 在 text/chat completions 链路旁路记录 6 类全链路数据，经两级缓冲（内存→磁盘→GCS）批量 load 到 BigQuery，与主请求完全解耦、故障让路。

**Architecture:** 新增独立 `common/audit` 包承载全部复杂度（context 暂存、脱敏、内存+磁盘两级缓冲、ingest worker、uploader、BigQuery/GCS client）；新增 `middleware/audit.go` 在最外层零侵入捕获 3 类数据（原始请求头/体、客户端响应）；在 relay 流程 3 处插入"哑操作"埋点捕获另 3 类（转换后请求头/体、上游响应）。主请求对审计的唯一成本 = 一次非阻塞 channel 写。

**Tech Stack:** Go 1.23 / gin / `cloud.google.com/go/bigquery` / `cloud.google.com/go/storage` / 标准库 `io.TeeReader`、`encoding/json`、`compress/gzip`。

**设计依据:** `docs/plans/2026-06-10-audit-bigquery-design.md`（已确认）。

---

## 全局约定

- **每个 audit.SetXxx / WrapXxx 埋点函数都是"哑操作"**：`if !audit.Enabled() { return }` 必须是第一行，关闭时零开销。
- **审计绝不 panic 外泄**：所有 goroutine 入口 `defer recover()`；所有埋点函数内部不返回 error。
- **测试位置**：与被测文件同目录 `*_test.go`。
- **每步提交**：`go build ./... && go vet ./...` 通过后再 commit。
- **不引入对主链路的同步阻塞**：唯一接触点是 `audit.Submit()` 的非阻塞 channel 写。

---

## Task 1: 配置项加载（config.go）

**Files:**
- Create: `common/audit/config.go`
- Test: `common/audit/config_test.go`

**Step 1: Write the failing test**

```go
package audit

import (
	"os"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	os.Clearenv()
	cfg := loadConfig()
	if cfg.Enabled {
		t.Errorf("Enabled 默认应为 false")
	}
	if cfg.ChannelSize != 2000 {
		t.Errorf("ChannelSize 默认应为 2000, got %d", cfg.ChannelSize)
	}
	if cfg.MaxBufferMB != 1024 {
		t.Errorf("MaxBufferMB 默认应为 1024, got %d", cfg.MaxBufferMB)
	}
	if cfg.DiskBufferMaxGB != 40 {
		t.Errorf("DiskBufferMaxGB 默认应为 40, got %d", cfg.DiskBufferMaxGB)
	}
	if cfg.BatchSize != 500 {
		t.Errorf("BatchSize 默认应为 500, got %d", cfg.BatchSize)
	}
	if cfg.MaxBodyKB != 10240 {
		t.Errorf("MaxBodyKB 默认应为 10240, got %d", cfg.MaxBodyKB)
	}
	if cfg.MaxRespKB != 4096 {
		t.Errorf("MaxRespKB 默认应为 4096, got %d", cfg.MaxRespKB)
	}
}

func TestLoadConfigRedactHeadersLowercased(t *testing.T) {
	os.Clearenv()
	cfg := loadConfig()
	if _, ok := cfg.redactSet["authorization"]; !ok {
		t.Errorf("默认脱敏头应包含小写 authorization")
	}
	if _, ok := cfg.redactSet["x-api-key"]; !ok {
		t.Errorf("默认脱敏头应包含小写 x-api-key")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./common/audit/ -run TestLoadConfig -v`
Expected: FAIL（包/类型未定义）

**Step 3: Write minimal implementation**

```go
package audit

import (
	"strings"
	"time"

	"github.com/songquanpeng/one-api/common/env"
)

type config struct {
	Enabled         bool
	GCPProject      string
	BQDataset       string
	BQTable         string
	GCSBucket       string
	CredentialsFile string
	ChannelSize     int
	MaxBufferMB     int
	DiskBufferDir   string
	DiskBufferMaxGB int
	BatchSize       int
	FlushInterval   time.Duration
	MaxBodyKB       int
	MaxRespKB       int
	PartitionExpireDays int
	redactSet       map[string]struct{}
}

func loadConfig() *config {
	c := &config{
		Enabled:             env.Bool("AUDIT_ENABLED", false),
		GCPProject:          env.String("AUDIT_GCP_PROJECT", ""),
		BQDataset:           env.String("AUDIT_BQ_DATASET", "audit"),
		BQTable:             env.String("AUDIT_BQ_TABLE", "request_logs"),
		GCSBucket:           env.String("AUDIT_GCS_BUCKET", ""),
		CredentialsFile:     env.String("AUDIT_CREDENTIALS_FILE", ""),
		ChannelSize:         env.Int("AUDIT_CHANNEL_SIZE", 2000),
		MaxBufferMB:         env.Int("AUDIT_MAX_BUFFER_MB", 1024),
		DiskBufferDir:       env.String("AUDIT_DISK_BUFFER_DIR", "./data/audit_spill"),
		DiskBufferMaxGB:     env.Int("AUDIT_DISK_BUFFER_MAX_GB", 40),
		BatchSize:           env.Int("AUDIT_BATCH_SIZE", 500),
		FlushInterval:       time.Duration(env.Int("AUDIT_FLUSH_INTERVAL_SEC", 10)) * time.Second,
		MaxBodyKB:           env.Int("AUDIT_MAX_BODY_KB", 10240),
		MaxRespKB:           env.Int("AUDIT_MAX_RESP_KB", 4096),
		PartitionExpireDays: env.Int("AUDIT_PARTITION_EXPIRE_DAYS", 0),
	}
	raw := env.String("AUDIT_REDACT_HEADERS", "Authorization,Api-Key,X-Api-Key,Cookie,Set-Cookie")
	c.redactSet = make(map[string]struct{})
	for _, h := range strings.Split(raw, ",") {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			c.redactSet[h] = struct{}{}
		}
	}
	return c
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./common/audit/ -run TestLoadConfig -v`
Expected: PASS

**Step 5: Commit**

```bash
git add common/audit/config.go common/audit/config_test.go
git commit -m "feat(audit): 审计模块配置项加载与默认值"
```

---

## Task 2: AuditRecord 数据结构与脱敏（redact.go）

**Files:**
- Create: `common/audit/record.go`
- Create: `common/audit/redact.go`
- Test: `common/audit/redact_test.go`

**Step 1: Write the failing test**

```go
package audit

import (
	"net/http"
	"testing"
)

func TestRedactHeaders(t *testing.T) {
	cfg := loadConfig()
	h := http.Header{}
	h.Set("Authorization", "Bearer sk-secret")
	h.Set("Content-Type", "application/json")
	h.Set("X-Api-Key", "abc123")
	out := redactHeaders(h, cfg.redactSet)
	if out["Authorization"][0] != redactedValue {
		t.Errorf("Authorization 应被脱敏, got %v", out["Authorization"])
	}
	if out["X-Api-Key"][0] != redactedValue {
		t.Errorf("X-Api-Key 应被脱敏（大小写不敏感）")
	}
	if out["Content-Type"][0] != "application/json" {
		t.Errorf("Content-Type 不应被脱敏")
	}
}

func TestHeadersToJSON(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	s := headersToJSON(h)
	if s == "" || s[0] != '{' {
		t.Errorf("应返回 JSON 对象字符串, got %q", s)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./common/audit/ -run TestRedact -v`
Expected: FAIL

**Step 3: Write minimal implementation**

`record.go`:

```go
package audit

import "time"

type AuditRecord struct {
	EventTime                time.Time
	XRequestID               string
	UserID                   int
	Username                 string
	ChannelID                int
	TokenName                string
	OriginModel              string
	ActualModel              string
	IsStream                 bool
	StatusCode               int
	DurationMS               int64
	OriginalReqHeaders       string
	OriginalReqBody          string
	ConvertedReqHeaders      string
	ConvertedReqBody         string
	ConvertedSameAsOriginal  bool
	UpstreamResponse         string
	ClientResponse           string
	TruncatedFields          []string
	DroppedNote              string
}

// Size 估算单条记录占用字节，用于内存计量。
func (r *AuditRecord) Size() int {
	return len(r.OriginalReqHeaders) + len(r.OriginalReqBody) +
		len(r.ConvertedReqHeaders) + len(r.ConvertedReqBody) +
		len(r.UpstreamResponse) + len(r.ClientResponse) + 256
}
```

`redact.go`:

```go
package audit

import (
	"encoding/json"
	"net/http"
	"strings"
)

const redactedValue = "***REDACTED***"

func redactHeaders(h http.Header, redactSet map[string]struct{}) http.Header {
	out := http.Header{}
	for k, vs := range h {
		if _, ok := redactSet[strings.ToLower(k)]; ok {
			out[k] = []string{redactedValue}
			continue
		}
		cp := make([]string, len(vs))
		copy(cp, vs)
		out[k] = cp
	}
	return out
}

func headersToJSON(h http.Header) string {
	b, err := json.Marshal(h)
	if err != nil {
		return "{}"
	}
	return string(b)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./common/audit/ -run TestRedact -v && go test ./common/audit/ -run TestHeadersToJSON -v`
Expected: PASS

**Step 5: Commit**

```bash
git add common/audit/record.go common/audit/redact.go common/audit/redact_test.go
git commit -m "feat(audit): AuditRecord 结构与请求头脱敏"
```

---

## Task 3: 截断工具（truncate.go）

**Files:**
- Create: `common/audit/truncate.go`
- Test: `common/audit/truncate_test.go`

**Step 1: Write the failing test**

```go
package audit

import (
	"strings"
	"testing"
)

func TestTruncateUnderLimit(t *testing.T) {
	s, truncated := truncate("hello", 10*1024)
	if truncated {
		t.Errorf("未超限不应截断")
	}
	if s != "hello" {
		t.Errorf("内容应原样返回")
	}
}

func TestTruncateOverLimit(t *testing.T) {
	big := strings.Repeat("a", 2048)
	s, truncated := truncate(big, 1) // 1KB 上限
	if !truncated {
		t.Errorf("超限应标记截断")
	}
	if len(s) > 1024 {
		t.Errorf("截断后长度应 <= 1024, got %d", len(s))
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./common/audit/ -run TestTruncate -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
package audit

// truncate 将 s 截断到 limitKB 千字节以内，返回截断后字符串及是否发生截断。
func truncate(s string, limitKB int) (string, bool) {
	limit := limitKB * 1024
	if len(s) <= limit {
		return s, false
	}
	return s[:limit], true
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./common/audit/ -run TestTruncate -v`
Expected: PASS

**Step 5: Commit**

```bash
git add common/audit/truncate.go common/audit/truncate_test.go
git commit -m "feat(audit): payload 截断工具"
```

---

## Task 4: AuditContext 暂存与埋点 API（context.go）

**Files:**
- Create: `common/audit/context.go`
- Test: `common/audit/context_test.go`

设计：在 `gin.Context` 里用一个 key 存 `*AuditContext`，埋点函数读写它；关闭时全部 return。

**Step 1: Write the failing test**

```go
package audit

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

func newTestCtx() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	c.Request, _ = http.NewRequest("POST", "/v1/chat/completions", nil)
	return c
}

func TestSetConvertedBodyDisabledIsNoop(t *testing.T) {
	pkgConfig = &config{Enabled: false}
	c := newTestCtx()
	SetConvertedBody(c, "{}") // 关闭时不应 panic、不应写入
	if _, ok := c.Get(ctxKey); ok {
		t.Errorf("关闭时不应在 context 写入审计数据")
	}
}

func TestSetConvertedBodyEnabled(t *testing.T) {
	pkgConfig = &config{Enabled: true, MaxBodyKB: 10240}
	c := newTestCtx()
	InitAuditContext(c)
	SetConvertedBody(c, `{"model":"gpt-4"}`)
	ac := getAuditContext(c)
	if ac == nil || ac.ConvertedReqBody != `{"model":"gpt-4"}` {
		t.Errorf("开启时应暂存转换后请求体")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./common/audit/ -run TestSetConvertedBody -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
package audit

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const ctxKey = "audit_context"

// AuditContext 暂存单次请求在 relay 流程中埋点写入的数据。
type AuditContext struct {
	ConvertedReqHeaders http.Header
	ConvertedReqBody    string
	UpstreamResponse    string
	truncatedFields     []string
}

func InitAuditContext(c *gin.Context) {
	if !Enabled() {
		return
	}
	c.Set(ctxKey, &AuditContext{})
}

func getAuditContext(c *gin.Context) *AuditContext {
	v, ok := c.Get(ctxKey)
	if !ok {
		return nil
	}
	ac, _ := v.(*AuditContext)
	return ac
}

func SetConvertedBody(c *gin.Context, body string) {
	if !Enabled() {
		return
	}
	ac := getAuditContext(c)
	if ac == nil {
		return
	}
	s, truncated := truncate(body, pkgConfig.MaxBodyKB)
	ac.ConvertedReqBody = s
	if truncated {
		ac.truncatedFields = append(ac.truncatedFields, "converted_req_body")
	}
}

func SetConvertedHeader(c *gin.Context, h http.Header) {
	if !Enabled() {
		return
	}
	ac := getAuditContext(c)
	if ac == nil {
		return
	}
	ac.ConvertedReqHeaders = h.Clone()
}

// SetMeta 暂存 relay 流程才知道的元信息，供中间件 defer 阶段组装记录。
func SetMeta(c *gin.Context, isStream bool, actualModel string) {
	if !Enabled() {
		return
	}
	c.Set("audit_is_stream", isStream)
	c.Set("audit_actual_model", actualModel)
}
```

> 注：`Enabled()` 与 `pkgConfig` 在 Task 5 定义；本任务测试中直接给 `pkgConfig` 赋值并实现一个临时 `Enabled()`。为避免编译顺序问题，在 context.go 末尾加占位 `func Enabled() bool { return pkgConfig != nil && pkgConfig.Enabled }`，Task 5 改为从 manager 读取后删除占位。

**Step 4: Run test to verify it passes**

Run: `go test ./common/audit/ -run TestSetConvertedBody -v`
Expected: PASS

**Step 5: Commit**

```bash
git add common/audit/context.go common/audit/context_test.go
git commit -m "feat(audit): AuditContext 暂存与埋点哑操作 API"
```

---

## Task 5: 上游响应 TeeReader 包装（context.go 续）

**Files:**
- Modify: `common/audit/context.go`
- Test: `common/audit/context_test.go`

**Step 1: Write the failing test**

```go
func TestWrapUpstreamBody(t *testing.T) {
	pkgConfig = &config{Enabled: true, MaxRespKB: 4096}
	c := newTestCtx()
	InitAuditContext(c)
	resp := &http.Response{
		Body: io.NopCloser(strings.NewReader("upstream-data")),
	}
	WrapUpstreamBody(c, resp)
	// 模拟 DoResponse 照常消费 body
	consumed, _ := io.ReadAll(resp.Body)
	if string(consumed) != "upstream-data" {
		t.Errorf("包装后 body 仍应可被完整消费, got %q", consumed)
	}
	// tee 旁路应抓到同样内容
	FinalizeUpstream(c)
	ac := getAuditContext(c)
	if ac.UpstreamResponse != "upstream-data" {
		t.Errorf("tee 应抓到上游响应, got %q", ac.UpstreamResponse)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./common/audit/ -run TestWrapUpstreamBody -v`
Expected: FAIL

**Step 3: Write minimal implementation**

在 `context.go` 增加（用带上限的 buffer，避免大响应吃内存）：

```go
import (
	"bytes"
	"io"
)

type cappedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if remain := b.limit - b.buf.Len(); remain > 0 {
		if len(p) > remain {
			b.buf.Write(p[:remain])
			b.truncated = true
		} else {
			b.buf.Write(p)
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil // 永远"全部写入"，不打断 TeeReader
}

func WrapUpstreamBody(c *gin.Context, resp *http.Response) {
	if !Enabled() || resp == nil || resp.Body == nil {
		return
	}
	ac := getAuditContext(c)
	if ac == nil {
		return
	}
	cb := &cappedBuffer{limit: pkgConfig.MaxRespKB * 1024}
	c.Set("audit_upstream_buf", cb)
	resp.Body = struct {
		io.Reader
		io.Closer
	}{
		Reader: io.TeeReader(resp.Body, cb),
		Closer: resp.Body,
	}
}

func FinalizeUpstream(c *gin.Context) {
	if !Enabled() {
		return
	}
	ac := getAuditContext(c)
	if ac == nil {
		return
	}
	if v, ok := c.Get("audit_upstream_buf"); ok {
		if cb, ok := v.(*cappedBuffer); ok {
			ac.UpstreamResponse = cb.buf.String()
			if cb.truncated {
				ac.truncatedFields = append(ac.truncatedFields, "upstream_response")
			}
		}
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./common/audit/ -run TestWrapUpstreamBody -v`
Expected: PASS

**Step 5: Commit**

```bash
git add common/audit/context.go common/audit/context_test.go
git commit -m "feat(audit): 上游响应 TeeReader 带上限旁路捕获"
```

---

## Task 6: NDJSON 序列化（serialize.go）

**Files:**
- Create: `common/audit/serialize.go`
- Test: `common/audit/serialize_test.go`

**Step 1: Write the failing test**

```go
package audit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestToNDJSONLine(t *testing.T) {
	r := &AuditRecord{
		EventTime:  time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC),
		XRequestID: "req-1",
		OriginModel: "gpt-4",
	}
	line := toNDJSONLine(r)
	if !strings.HasSuffix(line, "\n") {
		t.Errorf("NDJSON 行应以换行结尾")
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &m); err != nil {
		t.Fatalf("应为合法 JSON: %v", err)
	}
	if m["x_request_id"] != "req-1" {
		t.Errorf("字段名应为 BigQuery snake_case")
	}
	if m["event_time"] == nil {
		t.Errorf("event_time 应存在（BigQuery TIMESTAMP 可解析格式）")
	}
}

func TestConvertedSameAsOriginalEmptiesBody(t *testing.T) {
	r := &AuditRecord{
		OriginalReqBody:         `{"a":1}`,
		ConvertedReqBody:        `{"a":1}`,
		ConvertedSameAsOriginal: true,
	}
	line := toNDJSONLine(r)
	var m map[string]any
	_ = json.Unmarshal([]byte(strings.TrimSpace(line)), &m)
	if m["converted_req_body"] != "" {
		t.Errorf("converted_same_as_original=true 时 converted_req_body 应为空")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./common/audit/ -run TestToNDJSON -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
package audit

import (
	"encoding/json"
	"time"
)

type bqRow struct {
	EventTime               string   `json:"event_time"`
	XRequestID              string   `json:"x_request_id"`
	UserID                  int      `json:"user_id"`
	Username                string   `json:"username"`
	ChannelID               int      `json:"channel_id"`
	TokenName               string   `json:"token_name"`
	OriginModel             string   `json:"origin_model"`
	ActualModel             string   `json:"actual_model"`
	IsStream                bool     `json:"is_stream"`
	StatusCode              int      `json:"status_code"`
	DurationMS              int64    `json:"duration_ms"`
	OriginalReqHeaders      string   `json:"original_req_headers"`
	OriginalReqBody         string   `json:"original_req_body"`
	ConvertedReqHeaders     string   `json:"converted_req_headers"`
	ConvertedReqBody        string   `json:"converted_req_body"`
	ConvertedSameAsOriginal bool     `json:"converted_same_as_original"`
	UpstreamResponse        string   `json:"upstream_response"`
	ClientResponse          string   `json:"client_response"`
	TruncatedFields         []string `json:"truncated_fields"`
	DroppedNote             string   `json:"dropped_note"`
}

func toNDJSONLine(r *AuditRecord) string {
	convBody := r.ConvertedReqBody
	if r.ConvertedSameAsOriginal {
		convBody = ""
	}
	row := bqRow{
		EventTime:               r.EventTime.UTC().Format("2006-01-02 15:04:05.000000"),
		XRequestID:              r.XRequestID,
		UserID:                  r.UserID,
		Username:                r.Username,
		ChannelID:               r.ChannelID,
		TokenName:               r.TokenName,
		OriginModel:             r.OriginModel,
		ActualModel:             r.ActualModel,
		IsStream:                r.IsStream,
		StatusCode:              r.StatusCode,
		DurationMS:              r.DurationMS,
		OriginalReqHeaders:      r.OriginalReqHeaders,
		OriginalReqBody:         r.OriginalReqBody,
		ConvertedReqHeaders:     r.ConvertedReqHeaders,
		ConvertedReqBody:        convBody,
		ConvertedSameAsOriginal: r.ConvertedSameAsOriginal,
		UpstreamResponse:        r.UpstreamResponse,
		ClientResponse:          r.ClientResponse,
		TruncatedFields:         r.TruncatedFields,
		DroppedNote:             r.DroppedNote,
	}
	b, _ := json.Marshal(row)
	return string(b) + "\n"
}

var _ = time.Now
```

**Step 4: Run test to verify it passes**

Run: `go test ./common/audit/ -run TestToNDJSON -v && go test ./common/audit/ -run TestConvertedSame -v`
Expected: PASS

**Step 5: Commit**

```bash
git add common/audit/serialize.go common/audit/serialize_test.go
git commit -m "feat(audit): AuditRecord → BigQuery NDJSON 序列化"
```

---

## Task 7: 磁盘缓冲（spill.go）

**Files:**
- Create: `common/audit/spill.go`
- Test: `common/audit/spill_test.go`

职责：把一批 NDJSON 字节 gzip 写入 `DiskBufferDir` 下唯一文件名；扫描已有 spill 文件；统计目录用量；删除文件。

**Step 1: Write the failing test**

```go
package audit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSpillWriteAndScan(t *testing.T) {
	dir := t.TempDir()
	s := &spillStore{dir: dir, maxBytes: 100 * 1024 * 1024}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path, err := s.write([]byte(`{"x":1}` + "\n"))
	if err != nil {
		t.Fatalf("write 失败: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("文件应写入 spill 目录")
	}
	files, _ := s.scan()
	if len(files) != 1 {
		t.Errorf("scan 应找到 1 个 spill 文件, got %d", len(files))
	}
}

func TestSpillRejectWhenFull(t *testing.T) {
	dir := t.TempDir()
	s := &spillStore{dir: dir, maxBytes: 1} // 1 字节上限
	_, err := s.write([]byte("aaaa"))
	if err == nil {
		t.Errorf("磁盘满应返回错误（触发丢弃+计数）")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./common/audit/ -run TestSpill -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
package audit

import (
	"compress/gzip"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"time"
)

var errDiskFull = errors.New("audit: disk spill buffer full")

type spillStore struct {
	dir      string
	maxBytes int64
	seq      int64
}

func (s *spillStore) usage() int64 {
	var total int64
	files, _ := s.scan()
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil {
			total += fi.Size()
		}
	}
	return total
}

func (s *spillStore) write(ndjson []byte) (string, error) {
	if s.usage() >= s.maxBytes {
		return "", errDiskFull
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("audit-%d-%d.ndjson.gz", time.Now().UnixNano(), atomic.AddInt64(&s.seq, 1))
	path := filepath.Join(s.dir, name)
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gw := gzip.NewWriter(f)
	if _, err := gw.Write(ndjson); err != nil {
		gw.Close()
		return "", err
	}
	if err := gw.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func (s *spillStore) scan() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".gz" {
			files = append(files, filepath.Join(s.dir, e.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./common/audit/ -run TestSpill -v`
Expected: PASS

**Step 5: Commit**

```bash
git add common/audit/spill.go common/audit/spill_test.go
git commit -m "feat(audit): 磁盘 spill 缓冲（gzip 写入/扫描/容量限制）"
```

---

## Task 8: GCS + BigQuery 客户端封装（bqclient.go）

**Files:**
- Create: `common/audit/bqclient.go`
- Test: `common/audit/bqclient_test.go`（仅测试可构造 + 接口契约，不连真实 GCP）

依赖：`go get cloud.google.com/go/bigquery cloud.google.com/go/storage`。

**Step 1: Write the failing test**

```go
package audit

import "testing"

func TestBigQuerySchemaHasAllColumns(t *testing.T) {
	schema := buildBQSchema()
	want := []string{
		"event_time", "x_request_id", "user_id", "username", "channel_id",
		"token_name", "origin_model", "actual_model", "is_stream", "status_code",
		"duration_ms", "original_req_headers", "original_req_body",
		"converted_req_headers", "converted_req_body", "converted_same_as_original",
		"upstream_response", "client_response", "truncated_fields", "dropped_note",
	}
	got := map[string]bool{}
	for _, f := range schema {
		got[f.Name] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("BigQuery schema 缺少列 %s", w)
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./common/audit/ -run TestBigQuerySchema -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
package audit

import (
	"context"
	"fmt"

	"cloud.google.com/go/bigquery"
	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

type gcpClient struct {
	cfg *config
	bq  *bigquery.Client
	gcs *storage.Client
}

func buildBQSchema() bigquery.Schema {
	str := bigquery.StringFieldType
	return bigquery.Schema{
		{Name: "event_time", Type: bigquery.TimestampFieldType},
		{Name: "x_request_id", Type: str},
		{Name: "user_id", Type: bigquery.IntegerFieldType},
		{Name: "username", Type: str},
		{Name: "channel_id", Type: bigquery.IntegerFieldType},
		{Name: "token_name", Type: str},
		{Name: "origin_model", Type: str},
		{Name: "actual_model", Type: str},
		{Name: "is_stream", Type: bigquery.BooleanFieldType},
		{Name: "status_code", Type: bigquery.IntegerFieldType},
		{Name: "duration_ms", Type: bigquery.IntegerFieldType},
		{Name: "original_req_headers", Type: str},
		{Name: "original_req_body", Type: str},
		{Name: "converted_req_headers", Type: str},
		{Name: "converted_req_body", Type: str},
		{Name: "converted_same_as_original", Type: bigquery.BooleanFieldType},
		{Name: "upstream_response", Type: str},
		{Name: "client_response", Type: str},
		{Name: "truncated_fields", Type: str, Repeated: true},
		{Name: "dropped_note", Type: str},
	}
}

func newGCPClient(ctx context.Context, cfg *config) (*gcpClient, error) {
	var opts []option.ClientOption
	if cfg.CredentialsFile != "" {
		opts = append(opts, option.WithCredentialsFile(cfg.CredentialsFile))
	}
	bq, err := bigquery.NewClient(ctx, cfg.GCPProject, opts...)
	if err != nil {
		return nil, fmt.Errorf("bigquery client: %w", err)
	}
	gcs, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("gcs client: %w", err)
	}
	return &gcpClient{cfg: cfg, bq: bq, gcs: gcs}, nil
}

// ensureTable 幂等建表（按 event_time 天分区，可选过期）。
func (g *gcpClient) ensureTable(ctx context.Context) error {
	ds := g.bq.Dataset(g.cfg.BQDataset)
	if _, err := ds.Metadata(ctx); err != nil {
		if e := ds.Create(ctx, &bigquery.DatasetMetadata{}); e != nil {
			return fmt.Errorf("create dataset: %w", e)
		}
	}
	tbl := ds.Table(g.cfg.BQTable)
	if _, err := tbl.Metadata(ctx); err == nil {
		return nil // 已存在
	}
	tp := &bigquery.TimePartitioning{Field: "event_time", Type: bigquery.DayPartitioningType}
	if g.cfg.PartitionExpireDays > 0 {
		tp.Expiration = time.Duration(g.cfg.PartitionExpireDays) * 24 * time.Hour
	}
	return tbl.Create(ctx, &bigquery.TableMetadata{
		Schema:           buildBQSchema(),
		TimePartitioning: tp,
	})
}

// uploadAndLoad 上传 gzip NDJSON 到 GCS 后提交 load job。
func (g *gcpClient) uploadAndLoad(ctx context.Context, objectName string, gzData []byte) error {
	obj := g.gcs.Bucket(g.cfg.GCSBucket).Object(objectName)
	w := obj.NewWriter(ctx)
	if _, err := w.Write(gzData); err != nil {
		w.Close()
		return fmt.Errorf("gcs write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("gcs close: %w", err)
	}
	gcsRef := bigquery.NewGCSReference(fmt.Sprintf("gs://%s/%s", g.cfg.GCSBucket, objectName))
	gcsRef.SourceFormat = bigquery.JSON
	gcsRef.Compression = bigquery.Gzip
	loader := g.bq.Dataset(g.cfg.BQDataset).Table(g.cfg.BQTable).LoaderFrom(gcsRef)
	loader.WriteDisposition = bigquery.WriteAppend
	job, err := loader.Run(ctx)
	if err != nil {
		return fmt.Errorf("load job run: %w", err)
	}
	status, err := job.Wait(ctx)
	if err != nil {
		return fmt.Errorf("load job wait: %w", err)
	}
	if err := status.Err(); err != nil {
		return fmt.Errorf("load job failed: %w", err)
	}
	// 加载成功后删除中转对象
	_ = obj.Delete(ctx)
	return nil
}
```

> 别忘了在文件顶部 import `"time"`。

**Step 4: Run test to verify it passes**

Run: `go build ./common/audit/ && go test ./common/audit/ -run TestBigQuerySchema -v`
Expected: PASS

**Step 5: Commit**

```bash
git add common/audit/bqclient.go common/audit/bqclient_test.go go.mod go.sum
git commit -m "feat(audit): GCS+BigQuery 客户端、幂等建表、GCS load job"
```

---

## Task 9: ingest worker + uploader + manager（worker.go / manager.go）

**Files:**
- Create: `common/audit/manager.go`
- Create: `common/audit/worker.go`
- Test: `common/audit/worker_test.go`

`manager.go` 持有包级单例：`pkgConfig`、`recordChan`、`dropped` 计数、`spillStore`、`gcpClient`，并提供 `Enabled()`、`Submit()`、`Start()`、`Shutdown()`。

**Step 1: Write the failing test**（不连 GCP，注入假 dispatcher 验证缓冲/丢弃逻辑）

```go
package audit

import (
	"sync"
	"testing"
	"time"
)

func TestSubmitNonBlockingDropsWhenChanFull(t *testing.T) {
	resetForTest()
	pkgConfig = &config{Enabled: true, ChannelSize: 1}
	recordChan = make(chan *AuditRecord, 1)
	recordChan <- &AuditRecord{} // 占满
	Submit(&AuditRecord{})        // 不应阻塞
	if Dropped() != 1 {
		t.Errorf("channel 满时应丢弃并计数, dropped=%d", Dropped())
	}
}

func TestIngestFlushOnBatchSize(t *testing.T) {
	resetForTest()
	pkgConfig = &config{Enabled: true, ChannelSize: 10, BatchSize: 2, FlushInterval: time.Hour, MaxBufferMB: 1024}
	var mu sync.Mutex
	var dispatched [][]*AuditRecord
	testDispatch = func(batch []*AuditRecord) {
		mu.Lock()
		dispatched = append(dispatched, batch)
		mu.Unlock()
	}
	recordChan = make(chan *AuditRecord, 10)
	done := make(chan struct{})
	go func() { ingestLoop(); close(done) }()
	Submit(&AuditRecord{XRequestID: "a"})
	Submit(&AuditRecord{XRequestID: "b"}) // 达到 BatchSize=2 → flush
	time.Sleep(50 * time.Millisecond)
	close(recordChan)
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(dispatched) == 0 || len(dispatched[0]) != 2 {
		t.Errorf("应在 batch 满 2 条时 flush, got %v", dispatched)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./common/audit/ -run "TestSubmit|TestIngest" -v`
Expected: FAIL

**Step 3: Write minimal implementation**

`manager.go`:

```go
package audit

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/songquanpeng/one-api/common/logger"
)

var (
	pkgConfig  *config
	recordChan chan *AuditRecord
	dropped    int64
	spill      *spillStore
	gcp        *gcpClient
	startOnce  sync.Once

	// 测试注入点
	testDispatch func(batch []*AuditRecord)
)

func Enabled() bool { return pkgConfig != nil && pkgConfig.Enabled }

func Dropped() int64 { return atomic.LoadInt64(&dropped) }

func Submit(r *AuditRecord) {
	if !Enabled() || recordChan == nil {
		return
	}
	select {
	case recordChan <- r:
	default:
		atomic.AddInt64(&dropped, 1)
	}
}

// Start 在 main 中调用：加载配置、校验 GCP、建表、启动 worker。
// 任何初始化失败都降级为关闭，绝不阻断主服务。
func Start(ctx context.Context) {
	startOnce.Do(func() {
		cfg := loadConfig()
		if !cfg.Enabled {
			pkgConfig = cfg
			return
		}
		if cfg.GCPProject == "" || cfg.GCSBucket == "" {
			logger.SysError("audit: 缺少 GCP 配置，自动降级为关闭")
			cfg.Enabled = false
			pkgConfig = cfg
			return
		}
		client, err := newGCPClient(ctx, cfg)
		if err != nil {
			logger.SysError("audit: 初始化 GCP 客户端失败，降级为关闭: " + err.Error())
			cfg.Enabled = false
			pkgConfig = cfg
			return
		}
		if err := client.ensureTable(ctx); err != nil {
			logger.SysError("audit: 建表失败，降级为关闭: " + err.Error())
			cfg.Enabled = false
			pkgConfig = cfg
			return
		}
		pkgConfig = cfg
		gcp = client
		spill = &spillStore{dir: cfg.DiskBufferDir, maxBytes: int64(cfg.DiskBufferMaxGB) * 1024 * 1024 * 1024}
		recordChan = make(chan *AuditRecord, cfg.ChannelSize)
		go ingestLoop()
		go uploaderLoop()
		logger.SysLog("audit: 审计模块已启动")
	})
}

func resetForTest() {
	pkgConfig = nil
	recordChan = nil
	dropped = 0
	spill = nil
	gcp = nil
	testDispatch = nil
}
```

`worker.go`:

```go
package audit

import (
	"compress/gzip"
	"bytes"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/songquanpeng/one-api/common/logger"
)

func ingestLoop() {
	defer func() { recover() }()
	var batch []*AuditRecord
	var memBytes int
	ticker := time.NewTicker(pkgConfig.FlushInterval)
	defer ticker.Stop()
	flush := func() {
		if len(batch) == 0 {
			return
		}
		dispatch(batch)
		batch = nil
		memBytes = 0
	}
	for {
		select {
		case r, ok := <-recordChan:
			if !ok {
				flush()
				return
			}
			batch = append(batch, r)
			memBytes += r.Size()
			if len(batch) >= pkgConfig.BatchSize || memBytes >= pkgConfig.MaxBufferMB*1024*1024 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func dispatch(batch []*AuditRecord) {
	if testDispatch != nil {
		testDispatch(batch)
		return
	}
	var buf bytes.Buffer
	for _, r := range batch {
		buf.WriteString(toNDJSONLine(r))
	}
	// 内存够：直接走内存→GCS；否则落盘
	if buf.Len() < pkgConfig.MaxBufferMB*1024*1024 {
		gz := gzipBytes(buf.Bytes())
		obj := fmt.Sprintf("audit/%s/%d.ndjson.gz", time.Now().UTC().Format("2006/01/02"), time.Now().UnixNano())
		if err := gcp.uploadAndLoad(context.Background(), obj, gz); err != nil {
			logger.SysError("audit: 内存直传失败，转落盘: " + err.Error())
			spillBatch(buf.Bytes())
		}
		return
	}
	spillBatch(buf.Bytes())
}

func spillBatch(ndjson []byte) {
	if _, err := spill.write(ndjson); err != nil {
		// 磁盘也满 → 丢弃 + 计数
		dropN := int64(1)
		atomicAddDropped(dropN)
		logger.SysError("audit: 磁盘缓冲已满，丢弃批次: " + err.Error())
	}
}

func gzipBytes(b []byte) []byte {
	var out bytes.Buffer
	gw := gzip.NewWriter(&out)
	_, _ = gw.Write(b)
	_ = gw.Close()
	return out.Bytes()
}

func uploaderLoop() {
	defer func() { recover() }()
	ticker := time.NewTicker(pkgConfig.FlushInterval)
	defer ticker.Stop()
	for range ticker.C {
		files, _ := spill.scan()
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			// spill 文件已是 gzip；解开成 NDJSON 再按统一对象名上传
			obj := fmt.Sprintf("audit/%s/spill-%d.ndjson.gz", time.Now().UTC().Format("2006/01/02"), time.Now().UnixNano())
			if err := gcp.uploadAndLoad(context.Background(), obj, data); err != nil {
				logger.SysError("audit: spill 文件上传失败，保留待重试: " + err.Error())
				continue
			}
			_ = os.Remove(f)
		}
	}
}
```

> `atomicAddDropped` 用 `atomic.AddInt64(&dropped, n)` 实现，放在 manager.go。注意 spill 文件本身已 gzip，`uploadAndLoad` 里 `gcsRef.Compression = bigquery.Gzip` 已声明 gzip，可直接传。

**Step 4: Run test to verify it passes**

Run: `go test ./common/audit/ -run "TestSubmit|TestIngest" -v`
Expected: PASS

**Step 5: Commit**

```bash
git add common/audit/manager.go common/audit/worker.go common/audit/worker_test.go
git commit -m "feat(audit): ingest worker、两级缓冲调度、uploader、manager 单例"
```

---

## Task 10: 优雅关停（manager.go 续）

**Files:**
- Modify: `common/audit/manager.go`
- Test: `common/audit/worker_test.go`

**Step 1: Write the failing test**

```go
func TestShutdownFlushesRemaining(t *testing.T) {
	resetForTest()
	pkgConfig = &config{Enabled: true, ChannelSize: 10, BatchSize: 1000, FlushInterval: time.Hour, MaxBufferMB: 1024}
	var got int
	testDispatch = func(batch []*AuditRecord) { got += len(batch) }
	recordChan = make(chan *AuditRecord, 10)
	go ingestLoop()
	Submit(&AuditRecord{})
	Submit(&AuditRecord{})
	Shutdown() // 关闭 channel，ingestLoop 收尾 flush 残余
	time.Sleep(50 * time.Millisecond)
	if got != 2 {
		t.Errorf("关停应 flush 残余 2 条, got %d", got)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./common/audit/ -run TestShutdown -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
func Shutdown() {
	if !Enabled() || recordChan == nil {
		return
	}
	close(recordChan) // ingestLoop 收到 !ok 后 flush 残余并退出
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./common/audit/ -run TestShutdown -v`
Expected: PASS

**Step 5: Commit**

```bash
git add common/audit/manager.go common/audit/worker_test.go
git commit -m "feat(audit): 优雅关停 flush 残余批次"
```

---

## Task 11: 审计中间件（middleware/audit.go）

**Files:**
- Create: `middleware/audit.go`
- Test: `middleware/audit_test.go`

职责：开启时 `InitAuditContext`；读原始请求头（脱敏）、原始请求体（`common.GetRequestBody`）；包装 `c.Writer` tee 客户端响应；`defer` 组装 `AuditRecord` 并 `Submit`。关闭时直接 `c.Next()`。

**Step 1: Write the failing test**

```go
package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/audit"
)

func TestAuditMiddlewareDisabledPassthrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Audit())
	r.POST("/x", func(c *gin.Context) { c.String(200, "ok") })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/x", strings.NewReader("body"))
	r.ServeHTTP(w, req)
	if w.Body.String() != "ok" {
		t.Errorf("关闭时中间件应完全透传, got %q", w.Body.String())
	}
	_ = audit.Enabled()
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./middleware/ -run TestAuditMiddleware -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
package middleware

import (
	"bytes"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/audit"
	"github.com/songquanpeng/one-api/common/logger"
)

type auditRespWriter struct {
	gin.ResponseWriter
	buf   bytes.Buffer
	limit int
	trunc bool
}

func (w *auditRespWriter) Write(b []byte) (int, error) {
	if remain := w.limit - w.buf.Len(); remain > 0 {
		if len(b) > remain {
			w.buf.Write(b[:remain])
			w.trunc = true
		} else {
			w.buf.Write(b)
		}
	}
	return w.ResponseWriter.Write(b) // 照常写给客户端
}

func Audit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !audit.Enabled() {
			c.Next()
			return
		}
		start := time.Now()
		audit.InitAuditContext(c)
		origBody, _ := common.GetRequestBody(c)
		arw := &auditRespWriter{ResponseWriter: c.Writer, limit: audit.MaxRespBytes()}
		c.Writer = arw
		origHeaders := c.Request.Header.Clone()

		defer func() {
			if r := recover(); r != nil {
				logger.SysError("audit middleware recover")
			}
			audit.FinalizeUpstream(c)
			audit.BuildAndSubmit(c, audit.FinalizeInput{
				Start:          start,
				OrigHeaders:    origHeaders,
				OrigBody:       origBody,
				ClientResponse: arw.buf.String(),
				ClientTrunc:    arw.trunc,
				StatusCode:     arw.Status(),
			})
		}()
		c.Next()
	}
}
```

> 本任务需要在 `common/audit` 暴露 `MaxRespBytes()`、`FinalizeInput`、`BuildAndSubmit`——在 Task 12 实现并补测。本步先让中间件 disabled 路径编译通过即可（`audit.Enabled()` 已存在）。若编译缺符号，先在 audit 包加最小空实现占位，Task 12 填充。

**Step 4: Run test to verify it passes**

Run: `go test ./middleware/ -run TestAuditMiddleware -v`
Expected: PASS

**Step 5: Commit**

```bash
git add middleware/audit.go middleware/audit_test.go
git commit -m "feat(audit): 审计中间件，tee 客户端响应、disabled 透传"
```

---

## Task 12: 记录组装（assemble.go）

**Files:**
- Create: `common/audit/assemble.go`
- Test: `common/audit/assemble_test.go`

把中间件捕获的原始数据 + AuditContext 暂存的转换数据 + gin.Context 里的业务字段（user_id、channel_id 等）合成 `AuditRecord`，做脱敏、截断、`converted_same_as_original` 比对，最后 `Submit`。

**Step 1: Write the failing test**

```go
package audit

import (
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestBuildAndSubmitAssembles(t *testing.T) {
	resetForTest()
	pkgConfig = loadConfigEnabledForTest()
	recordChan = make(chan *AuditRecord, 10)
	c := newTestCtxAssemble()
	InitAuditContext(c)
	SetConvertedBody(c, `{"model":"gpt-4"}`)
	h := http.Header{}
	h.Set("Authorization", "Bearer up-key")
	SetConvertedHeader(c, h)

	in := FinalizeInput{
		Start:          time.Now().Add(-100 * time.Millisecond),
		OrigHeaders:    func() http.Header { hh := http.Header{}; hh.Set("Authorization", "Bearer client-key"); return hh }(),
		OrigBody:       []byte(`{"model":"gpt-4"}`),
		ClientResponse: "data: hi",
		StatusCode:     200,
	}
	BuildAndSubmit(c, in)

	r := <-recordChan
	if r.UserID != 7 || r.ChannelID != 3 {
		t.Errorf("业务字段应从 context 提取")
	}
	if !contains(r.OriginalReqHeaders, redactedValue) {
		t.Errorf("原始请求头中的 Authorization 应脱敏")
	}
	if !contains(r.ConvertedReqHeaders, redactedValue) {
		t.Errorf("转换后请求头中的 Authorization 应脱敏")
	}
	if !r.ConvertedSameAsOriginal {
		t.Errorf("转换体与原始体逐字节相同应置标记")
	}
	if r.DurationMS <= 0 {
		t.Errorf("应计算耗时")
	}
}
```

（`loadConfigEnabledForTest`、`newTestCtxAssemble`、`contains` 为测试辅助，放测试文件。）

**Step 2: Run test to verify it fails**

Run: `go test ./common/audit/ -run TestBuildAndSubmit -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
package audit

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/logger"
)

type FinalizeInput struct {
	Start          time.Time
	OrigHeaders    http.Header
	OrigBody       []byte
	ClientResponse string
	ClientTrunc    bool
	StatusCode     int
}

func MaxRespBytes() int {
	if pkgConfig == nil {
		return 4096 * 1024
	}
	return pkgConfig.MaxRespKB * 1024
}

func BuildAndSubmit(c *gin.Context, in FinalizeInput) {
	if !Enabled() {
		return
	}
	ac := getAuditContext(c)
	if ac == nil {
		return
	}

	origBody, origTrunc := truncate(string(in.OrigBody), pkgConfig.MaxBodyKB)
	clientResp := in.ClientResponse
	truncFields := append([]string{}, ac.truncatedFields...)
	if origTrunc {
		truncFields = append(truncFields, "original_req_body")
	}
	if in.ClientTrunc {
		truncFields = append(truncFields, "client_response")
	}

	convHeaders := ""
	if ac.ConvertedReqHeaders != nil {
		convHeaders = headersToJSON(redactHeaders(ac.ConvertedReqHeaders, pkgConfig.redactSet))
	}
	origHeaders := ""
	if in.OrigHeaders != nil {
		origHeaders = headersToJSON(redactHeaders(in.OrigHeaders, pkgConfig.redactSet))
	}

	sameAsOrig := ac.ConvertedReqBody != "" && ac.ConvertedReqBody == origBody

	r := &AuditRecord{
		EventTime:               in.Start,
		XRequestID:              c.GetString("X-Request-ID"),
		UserID:                  c.GetInt("id"),
		Username:                c.GetString("username"),
		ChannelID:               c.GetInt("channel_id"),
		TokenName:               c.GetString("token_name"),
		OriginModel:             c.GetString("original_model"),
		ActualModel:             actualModelFromCtx(c),
		IsStream:                c.GetBool("audit_is_stream"),
		StatusCode:              in.StatusCode,
		DurationMS:              time.Since(in.Start).Milliseconds(),
		OriginalReqHeaders:      origHeaders,
		OriginalReqBody:         origBody,
		ConvertedReqHeaders:     convHeaders,
		ConvertedReqBody:        ac.ConvertedReqBody,
		ConvertedSameAsOriginal: sameAsOrig,
		UpstreamResponse:        ac.UpstreamResponse,
		ClientResponse:          clientResp,
		TruncatedFields:         truncFields,
	}
	Submit(r)
	_ = logger.RequestIdKey
}

func actualModelFromCtx(c *gin.Context) string {
	if v := c.GetString("audit_actual_model"); v != "" {
		return v
	}
	return c.GetString("original_model")
}
```

> 删除 Task 11 中提到的占位空实现（如有）。`is_stream` 与 `actual_model` 的 context key 以实际 relay 流程为准，若 relay 未写入则用 Accept 头/original_model 兜底。

**Step 4: Run test to verify it passes**

Run: `go build ./... && go test ./common/audit/ -run TestBuildAndSubmit -v`
Expected: PASS

**Step 5: Commit**

```bash
git add common/audit/assemble.go common/audit/assemble_test.go middleware/audit.go
git commit -m "feat(audit): 记录组装、脱敏、截断、转换体去重"
```

---

## Task 13: relay 流程 3 处埋点接入

**Files:**
- Modify: `relay/controller/text.go:94-97`（拿到 requestBody 后）
- Modify: `relay/channel/common.go:41`（SetupRequestHeader 之后）、`common.go:48`（DoRequest 返回 resp 之后）

**Step 1: 写集成验证（手动 + 编译）**

本任务无单元测试（依赖真实 gin 流程），靠 `go build` + 后续端到端验证。先确认埋点不破坏现有逻辑。

**Step 2: 修改 text.go**

紧接 `meta.IsStream = textRequest.Stream`（line 37）之后，写入中间件 `defer` 阶段要用的两个 context key（中间件在 relay 之后执行，拿不到 `meta`，必须显式 `c.Set`）：

```go
meta.IsStream = textRequest.Stream
audit.SetMeta(c, meta.IsStream, meta.ActualModelName) // 新增：供中间件 defer 组装记录
```

> `audit.SetMeta` 也是哑操作（disabled 直接 return），内部 `c.Set("audit_is_stream", ...)`、`c.Set("audit_actual_model", ...)`。注意 `meta.ActualModelName` 此时可能尚未经模型映射更新——更准确的做法是在 line 51 `meta.ActualModelName = textRequest.Model` 之后再调用。**实现时放在 line 51 之后**。

在 `requestBody = bytes.NewBuffer(jsonStr)` 与 `requestBody = c.Request.Body` 两个分支之后、`requestStartTime := time.Now()` 之前，统一捕获转换后请求体。注意 `requestBody` 是 `io.Reader`，已序列化分支用 `jsonStr`，透传分支用原始 body 字符串：

```go
// 在 shouldResetRequestBody 分支里：
if shouldResetRequestBody {
	jsonStr, err := json.Marshal(convertedRequest)
	if err != nil {
		return openai.ErrorWrapper(err, "json_marshal_failed", http.StatusInternalServerError)
	}
	audit.SetConvertedBody(c, string(jsonStr)) // 新增
	requestBody = bytes.NewBuffer(jsonStr)
} else {
	if raw, e := common.GetRequestBody(c); e == nil {
		audit.SetConvertedBody(c, string(raw)) // 透传：转换体==原始体
	}
	requestBody = c.Request.Body
}
```

非 OpenAI 分支同理，在 `requestBody = bytes.NewBuffer(jsonData)` 前加 `audit.SetConvertedBody(c, string(jsonData))`。

**Step 3: 修改 common.go**

`DoRequestHelper` 内，`a.SetupRequestHeader` 成功后：

```go
err = a.SetupRequestHeader(c, req, meta)
if err != nil {
	return nil, fmt.Errorf("setup request header failed: %w", err)
}
ApplyHeadersOverride(req, meta)
audit.SetConvertedHeader(c, req.Header) // 新增：覆盖后才是最终发往上游的头

resp, err := DoRequest(c, req, meta)
if err != nil {
	return nil, fmt.Errorf("do request failed: %w", err)
}
audit.WrapUpstreamBody(c, resp) // 新增：tee 上游响应
return resp, nil
```

**Step 4: 编译验证**

Run: `go build ./... && go vet ./...`
Expected: 无错误

**Step 5: Commit**

```bash
git add relay/controller/text.go relay/channel/common.go
git commit -m "feat(audit): relay 流程 3 处埋点（转换头/体、上游响应）"
```

---

## Task 14: main.go 启动 + 路由注册 + 关停接入

**Files:**
- Modify: `main.go`（启动 `audit.Start`、`defer audit.Shutdown`）
- Modify: `router/relay-router.go`（在 relayV1Router 链注册 `middleware.Audit()`）

**Step 1: 修改 main.go**

在 Redis 初始化之后、HTTP server 之前：

```go
// 启动审计模块（关闭时为空操作，初始化失败自动降级）
audit.Start(context.Background())
defer audit.Shutdown()
```

import `"github.com/songquanpeng/one-api/common/audit"`。

**Step 2: 修改 router/relay-router.go**

仅给 text/chat completions 链路加中间件。最稳妥：在 `relayV1Router.Use(middleware.RelayPanicRecover(), middleware.TokenAuth(), middleware.Distribute())` 这一行追加 `middleware.Audit()`：

```go
relayV1Router.Use(middleware.RelayPanicRecover(), middleware.TokenAuth(), middleware.Distribute(), middleware.Audit())
```

> 一期只关心 text/chat，但该 group 也挂了其它端点；因 `Audit()` 在 disabled 时零开销、enabled 时也只对走通的请求记录，可接受。若需严格限定，仅在 `/completions`、`/chat/completions` 两条路由单独挂 `middleware.Audit()`。**采用后者更贴合"一期仅 text/chat"边界**：

```go
relayV1Router.POST("/completions", middleware.Audit(), controller.Relay)
relayV1Router.POST("/chat/completions", middleware.Audit(), controller.Relay)
```

**Step 3: 编译验证**

Run: `go build ./... && go vet ./...`
Expected: 无错误

**Step 4: 关闭态回归**

Run: `AUDIT_ENABLED=false go test ./... 2>&1 | tail -20`
Expected: 既有测试全过，无新行为

**Step 5: Commit**

```bash
git add main.go router/relay-router.go
git commit -m "feat(audit): main 启动/关停接入、text-chat 路由注册审计中间件"
```

---

## Task 15: 端到端验证 + CHANGELOG

**Files:**
- Modify: `docs/CHANGELOG.md`

**Step 1: 关闭态验证**

```bash
go build ./... && go vet ./... && go test ./...
```
确认 `AUDIT_ENABLED` 未设时：worker 未启动、埋点零开销、既有行为不变。

**Step 2: 开启态验证（需测试 GCP 项目）**

设置环境变量后启动，发起流式与非流式 chat/completions 请求，确认：
- BigQuery `audit.request_logs` 出现 6 类字段完整记录
- `Authorization`/`X-Api-Key` 已脱敏为 `***REDACTED***`
- `x_request_id` 可与 `model.Log` 关联
- 流式响应 `client_response` 为拼接的 SSE 分块

**Step 3: 故障隔离验证**

人为让 GCS 不可达（错误 bucket），确认：
- 主请求延迟/成功率无变化
- 数据落 `./data/audit_spill`，恢复后 uploader 续传
- channel 打满时 `Dropped()` 计数增长、有告警日志

**Step 4: 截断验证**

构造 >10MB 请求体 / >4MB 响应，确认截断且 `truncated_fields` 标记正确。

**Step 5: 更新 CHANGELOG 并提交**

```bash
# 在 docs/CHANGELOG.md 顶部加入 2026-06-10 feat(audit) 记录
git add docs/CHANGELOG.md
git commit -m "docs: 记录审计→BigQuery 功能变更"
```

---

## 实现顺序与依赖

```
T1 config → T2 record/redact → T3 truncate → T4 context → T5 tee
   → T6 serialize → T7 spill → T8 bqclient → T9 worker/manager
   → T10 shutdown → T11 middleware → T12 assemble → T13 埋点
   → T14 main/router → T15 端到端
```

T1–T10 是纯 `common/audit` 内部、可独立 TDD；T11–T14 才接触业务文件，且全部是 disabled 零开销的哑操作接入。

## 风险与回滚

- **回滚**：删除 `middleware.Audit()` 注册 + `audit.Start` 调用即彻底停用；`common/audit` 包留存不影响编译。
- **依赖体积**：GCP SDK 较大，首次 `go mod tidy` 后确认 `go build` 时间可接受。
- **成本**：分区不过期，需在上线后纳入存储成本监控（见设计文档 §1 提醒）。
