# Runway 渠道重写实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 ezlinkai 的 Runway 集成从 `relay/controller/directvideo.go` 散落实现迁移到独立的 `relay/channel/runway/` 包，实现纯透传（body 不改，仅响应 id 加 `video-`/`image-` 前缀），补齐 `text_to_video` 路由，修复图像计费日志 10× bug，删除死代码。

**Architecture:** 新建 `relay/channel/runway/` 包，按职责拆成 9 个文件：constant / taskid / mode / status / billing / proxy / refund / result / handler。`controller.RelayRunway` 外壳（含重试）保留，内部全部委托给 `runway.Handler`。`relay/controller/directvideo.go` 中所有 Runway 相关函数删除。

**Tech Stack:** Go 1.21+、gin、gorm、testify 无（项目用 `testing` 原生 + 自写断言）。

**参照文件：**
- Spec：`docs/superpowers/specs/2026-04-25-runway-rewrite-design.md`
- 测试风格参考：`relay/channel/kling/billing_test.go`、`relay/channel/kling/util_test.go`

**前置知识（读代码的关键位置）：**
- `common/config/config.go:23` — `QuotaPerUnit = 500000`（$1 = 500000 quota）
- `model/image.go:9-29` — `Image` 结构（`Quota int64` 已存在）
- `model/video.go:68` — `GetVideoTaskById(taskId)` 返回 `*Video`
- `model/image.go:46` — `GetImageByTaskId(taskId)` 返回 `*Image`
- `model/user.go:563` — `CompensateVideoTaskQuota(userId, quota)` 补用户配额
- `model/channel.go:873` — `CompensateChannelQuota(channelId, quota)` 补渠道配额
- `model/log.go:81` — `RecordConsumeLogWithRequestID(...)` 图像消费日志
- `model/log.go:529` — `RecordVideoConsumeLog(...)` 视频消费日志
- `relay/controller/image.go:2864` — `CreateImageLog(...)` 创建 Image 表记录
- `relay/controller/video.go:819` — `CreateVideoLog(...)` 创建 Video 表记录

**每完成一个 Task 必须：`go build ./... && go vet ./...` 通过后 commit。**

---

## Task 0：DB 迁移 — Video / Image 表添加 `key_index` 列

**Files:**
- Modify: `model/video.go:11-42`（`Video` 结构）
- Modify: `model/image.go:9-29`（`Image` 结构）
- Produce: 供用户手动执行的 SQL

**背景**：Runway 任务 ID 绑定创建时用的 API key。multi-key 渠道下，查询时必须用创建时的那个 key。Video / Image 表现无 `key_index` 列。

- [ ] **Step 1：在 `Video` 结构体中添加 `KeyIndex` 字段**

定位到 `model/video.go` 中 `Video` 结构体的 `ChannelId` 字段（约第 24 行），在其后增加：

```go
KeyIndex       int     `json:"key_index" gorm:"default:0"` // 多Key渠道下创建任务使用的 key 索引
```

完整上下文（添加后）：
```go
ChannelId      int     `json:"channel_id" gorm:"index:idx_videos_channel_id"`
KeyIndex       int     `json:"key_index" gorm:"default:0"` // 多Key渠道下创建任务使用的 key 索引
UserId         int     `json:"user_id" gorm:"index:idx_videos_user_id"`
```

- [ ] **Step 2：在 `Image` 结构体中添加 `KeyIndex` 字段**

定位到 `model/image.go` 中 `Image` 结构体的 `ChannelId` 字段（约第 13 行），在其后增加：

```go
KeyIndex      int    `json:"key_index" gorm:"default:0"` // 多Key渠道下创建任务使用的 key 索引
```

完整上下文：
```go
ChannelId     int    `gorm:"index:idx_images_channel_id" json:"channel_id"`
KeyIndex      int    `json:"key_index" gorm:"default:0"` // 多Key渠道下创建任务使用的 key 索引
UserId        int    `gorm:"index:idx_images_user_id" json:"user_id"`
```

- [ ] **Step 3：编译验证**

Run: `go build ./... && go vet ./...`
Expected: 无错误。GORM 的 `AutoMigrate` 会在下次启动时自动添加列；若项目不启用自动迁移，则用 Step 4 的 SQL。

- [ ] **Step 4：输出供用户手动执行的 SQL（不自动执行）**

**STOP** — 告知用户以下 SQL，由用户决策是否执行：

```sql
ALTER TABLE videos ADD COLUMN key_index INT DEFAULT 0;
ALTER TABLE images ADD COLUMN key_index INT DEFAULT 0;
```

（若项目启用了 GORM AutoMigrate，重启服务即会自动执行，此步可跳过。）

- [ ] **Step 5：Commit**

```bash
git add model/video.go model/image.go
git commit -m "feat(runway): Video/Image 表添加 key_index 字段以支持 multi-key 查询"
```

---

## Task 1：新建 `runway` 包骨架 + 常量

**Files:**
- Create: `relay/channel/runway/constant.go`

- [ ] **Step 1：创建常量文件**

```go
// relay/channel/runway/constant.go
package runway

// Runway 官方 API 版本。升级时只改这一处。
// 官方发布说明：https://docs.dev.runwayml.com/
const APIVersion = "2024-11-06"

// HeaderVersion 是 Runway API 要求附带的版本头名称。
const HeaderVersion = "X-Runway-Version"

// RoutePrefix 是 ezlinkai 对外暴露的 Runway 代理路径前缀。
// 透传时需剥离，将 `/runway/v1/image_to_video` 映射为官方 `/v1/image_to_video`。
const RoutePrefix = "/runway"
```

- [ ] **Step 2：编译验证**

Run: `go build ./...`
Expected: 无错误

- [ ] **Step 3：Commit**

```bash
git add relay/channel/runway/constant.go
git commit -m "feat(runway): 新建 runway 包骨架与 API 常量"
```

---

## Task 2：任务 ID 前缀 helper（`taskid.go`）

**Files:**
- Create: `relay/channel/runway/taskid.go`
- Test: `relay/channel/runway/taskid_test.go`

- [ ] **Step 1：写失败测试**

```go
// relay/channel/runway/taskid_test.go
package runway

import "testing"

func TestEncodeTaskID(t *testing.T) {
	cases := []struct {
		kind Kind
		raw  string
		want string
	}{
		{KindVideo, "abc123", "video-abc123"},
		{KindImage, "xyz789", "image-xyz789"},
	}
	for _, tc := range cases {
		got := EncodeTaskID(tc.kind, tc.raw)
		if got != tc.want {
			t.Errorf("EncodeTaskID(%q,%q) = %q, want %q", tc.kind, tc.raw, got, tc.want)
		}
	}
}

func TestDecodeTaskID(t *testing.T) {
	cases := []struct {
		in         string
		wantKind   Kind
		wantRaw    string
		wantHasPfx bool
	}{
		{"video-abc", KindVideo, "abc", true},
		{"image-xyz", KindImage, "xyz", true},
		{"legacy123", KindVideo, "legacy123", false}, // 无前缀默认视频，向后兼容
		{"", KindVideo, "", false},
	}
	for _, tc := range cases {
		k, raw, has := DecodeTaskID(tc.in)
		if k != tc.wantKind || raw != tc.wantRaw || has != tc.wantHasPfx {
			t.Errorf("DecodeTaskID(%q) = (%q,%q,%v), want (%q,%q,%v)",
				tc.in, k, raw, has, tc.wantKind, tc.wantRaw, tc.wantHasPfx)
		}
	}
}
```

- [ ] **Step 2：运行测试确认失败**

Run: `go test ./relay/channel/runway/ -run TestEncodeTaskID -v`
Expected: FAIL / 编译错误（未定义 `Kind`、`EncodeTaskID`、`DecodeTaskID`）

- [ ] **Step 3：实现**

```go
// relay/channel/runway/taskid.go
package runway

import "strings"

// Kind 表示任务归属于图像还是视频，用于分表与计费分支。
type Kind string

const (
	KindVideo Kind = "video"
	KindImage Kind = "image"
)

// prefix 返回该 Kind 对应的任务 ID 前缀（含分隔符）。
func (k Kind) prefix() string { return string(k) + "-" }

// EncodeTaskID 给原始任务 ID 加上 Kind 前缀。
// 这是整个项目里唯一产生前缀的位置。
func EncodeTaskID(k Kind, rawID string) string {
	return k.prefix() + rawID
}

// DecodeTaskID 拆出 Kind 与原始 ID。
// 若输入不含前缀，默认视作 KindVideo 并返回 hasPrefix=false，
// 保留对历史无前缀任务 ID 的查询兼容。
func DecodeTaskID(taskID string) (kind Kind, rawID string, hasPrefix bool) {
	if strings.HasPrefix(taskID, KindImage.prefix()) {
		return KindImage, strings.TrimPrefix(taskID, KindImage.prefix()), true
	}
	if strings.HasPrefix(taskID, KindVideo.prefix()) {
		return KindVideo, strings.TrimPrefix(taskID, KindVideo.prefix()), true
	}
	return KindVideo, taskID, false
}
```

- [ ] **Step 4：运行测试确认通过**

Run: `go test ./relay/channel/runway/ -v`
Expected: PASS（两个测试全绿）

- [ ] **Step 5：Commit**

```bash
git add relay/channel/runway/taskid.go relay/channel/runway/taskid_test.go
git commit -m "feat(runway): 添加 taskid 前缀编解码 helper"
```

---

## Task 3：URL 路径 → Mode 映射（`mode.go`）

**Files:**
- Create: `relay/channel/runway/mode.go`
- Test: `relay/channel/runway/mode_test.go`

- [ ] **Step 1：写失败测试**

```go
// relay/channel/runway/mode_test.go
package runway

import "testing"

func TestModeFromPath(t *testing.T) {
	cases := []struct {
		path     string
		wantName string
		wantKind Kind
		wantOk   bool
	}{
		{"/runway/v1/text_to_video", "texttovideo", KindVideo, true},
		{"/runway/v1/image_to_video", "imagetovideo", KindVideo, true},
		{"/runway/v1/video_to_video", "videotovideo", KindVideo, true},
		{"/runway/v1/text_to_image", "texttoimage", KindImage, true},
		{"/runway/v1/character_performance", "characterperformance", KindVideo, true},
		{"/runway/v1/video_upscale", "upscalevideo", KindVideo, true},
		{"/runway/v1/unknown", "", "", false},
		{"", "", "", false},
	}
	for _, tc := range cases {
		m, ok := ModeFromPath(tc.path)
		if ok != tc.wantOk || m.Name != tc.wantName || m.Kind != tc.wantKind {
			t.Errorf("ModeFromPath(%q) = (%+v,%v), want name=%q kind=%q ok=%v",
				tc.path, m, ok, tc.wantName, tc.wantKind, tc.wantOk)
		}
	}
}
```

- [ ] **Step 2：运行测试确认失败**

Run: `go test ./relay/channel/runway/ -run TestModeFromPath -v`
Expected: FAIL（`Mode`、`ModeFromPath` 未定义）

- [ ] **Step 3：实现**

```go
// relay/channel/runway/mode.go
package runway

import "strings"

// Mode 表示一个 Runway 端点的语义。
// Kind 决定计费与 DB 分支（image/video），Name 用于日志 / Image.Mode / Video.Mode 字段。
type Mode struct {
	Name string
	Kind Kind
}

// pathToMode 是 Runway 官方路径到 Mode 的映射表。
// key 是剥离 RoutePrefix 之后的路径（即官方路径）。
var pathToMode = map[string]Mode{
	"/v1/text_to_video":         {Name: "texttovideo", Kind: KindVideo},
	"/v1/image_to_video":        {Name: "imagetovideo", Kind: KindVideo},
	"/v1/video_to_video":        {Name: "videotovideo", Kind: KindVideo},
	"/v1/text_to_image":         {Name: "texttoimage", Kind: KindImage},
	"/v1/character_performance": {Name: "characterperformance", Kind: KindVideo},
	"/v1/video_upscale":         {Name: "upscalevideo", Kind: KindVideo},
}

// StripRoutePrefix 将 ezlinkai 对外路径（如 `/runway/v1/x`）去掉代理前缀，
// 返回 Runway 官方路径（`/v1/x`）。
func StripRoutePrefix(urlPath string) string {
	return strings.TrimPrefix(urlPath, RoutePrefix)
}

// ModeFromPath 根据请求路径返回 Mode。
// 入参可带或不带 RoutePrefix，均能正确识别。
func ModeFromPath(urlPath string) (Mode, bool) {
	official := StripRoutePrefix(urlPath)
	m, ok := pathToMode[official]
	return m, ok
}
```

- [ ] **Step 4：运行测试确认通过**

Run: `go test ./relay/channel/runway/ -v`
Expected: PASS

- [ ] **Step 5：Commit**

```bash
git add relay/channel/runway/mode.go relay/channel/runway/mode_test.go
git commit -m "feat(runway): URL 路径驱动的 Mode 判定"
```

---

## Task 4：Runway 状态映射（`status.go`）

**Files:**
- Create: `relay/channel/runway/status.go`
- Test: `relay/channel/runway/status_test.go`

- [ ] **Step 1：写失败测试**

```go
// relay/channel/runway/status_test.go
package runway

import "testing"

func TestMapStatus(t *testing.T) {
	cases := map[string]string{
		"PENDING":    "pending",
		"RUNNING":    "running",
		"SUCCEEDED":  "succeeded",
		"FAILED":     "failed",
		"CANCELLED":  "cancelled",
		"THROTTLED":  "throttled",
		"WHATEVER":   "WHATEVER", // 未知状态透传
		"":           "",
	}
	for in, want := range cases {
		if got := MapStatus(in); got != want {
			t.Errorf("MapStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsTerminalFailed(t *testing.T) {
	cases := map[string]bool{
		"failed":    true,
		"cancelled": true,
		"succeeded": false,
		"running":   false,
		"pending":   false,
		"":          false,
	}
	for in, want := range cases {
		if got := IsTerminalFailed(in); got != want {
			t.Errorf("IsTerminalFailed(%q) = %v, want %v", in, got, want)
		}
	}
}
```

- [ ] **Step 2：确认失败**

Run: `go test ./relay/channel/runway/ -v`
Expected: FAIL

- [ ] **Step 3：实现**

```go
// relay/channel/runway/status.go
package runway

// MapStatus 将 Runway 官方状态枚举映射到 ezlinkai DB 内部状态。
// 未知状态原样透传，便于新状态出现时不至于丢信息。
func MapStatus(runwayStatus string) string {
	switch runwayStatus {
	case "PENDING":
		return "pending"
	case "RUNNING":
		return "running"
	case "SUCCEEDED":
		return "succeeded"
	case "FAILED":
		return "failed"
	case "CANCELLED":
		return "cancelled"
	case "THROTTLED":
		return "throttled"
	default:
		return runwayStatus
	}
}

// IsTerminalFailed 判断 DB 状态是否为"失败终态"（用于幂等退款判定）。
func IsTerminalFailed(dbStatus string) bool {
	return dbStatus == "failed" || dbStatus == "cancelled"
}
```

- [ ] **Step 4：确认通过**

Run: `go test ./relay/channel/runway/ -v`
Expected: PASS（3 个测试全绿）

- [ ] **Step 5：Commit**

```bash
git add relay/channel/runway/status.go relay/channel/runway/status_test.go
git commit -m "feat(runway): 添加状态映射 helper"
```

---

## Task 5：计费计算（`billing.go`，含 bug 修复）

**Files:**
- Create: `relay/channel/runway/billing.go`
- Test: `relay/channel/runway/billing_test.go`

**重要：** 原图像日志 `float64(quota)/5000000` 是 bug（视频是 `/500000`，二者差 10 倍）。新实现统一除以 `config.QuotaPerUnit`（= 500000）。

- [ ] **Step 1：写失败测试**

```go
// relay/channel/runway/billing_test.go
package runway

import "testing"

func TestVideoCreditRate(t *testing.T) {
	cases := map[string]float64{
		"gen4_turbo":  5,
		"gen3a_turbo": 5,
		"act_two":     5,
		"gen4_aleph":  15,
		"upscale_v1":  2,
		"unknown":     5, // 默认
	}
	for model, want := range cases {
		if got := videoCreditRate(model); got != want {
			t.Errorf("videoCreditRate(%q) = %v, want %v", model, got, want)
		}
	}
}

func TestComputeVideoQuota(t *testing.T) {
	// gen4_turbo, 10s = 5 credits/s × 10s × $0.01/credit = $0.50
	// $0.50 × 500000 quota/$ = 250000 quota
	got := ComputeVideoQuota("gen4_turbo", 10)
	if got != 250000 {
		t.Errorf("ComputeVideoQuota(gen4_turbo,10) = %d, want 250000", got)
	}

	// gen4_aleph, 5s = 15 × 5 × 0.01 × 500000 = 375000
	got = ComputeVideoQuota("gen4_aleph", 5)
	if got != 375000 {
		t.Errorf("ComputeVideoQuota(gen4_aleph,5) = %d, want 375000", got)
	}

	// upscale_v1, 10s = 2 × 10 × 0.01 × 500000 = 100000
	got = ComputeVideoQuota("upscale_v1", 10)
	if got != 100000 {
		t.Errorf("ComputeVideoQuota(upscale_v1,10) = %d, want 100000", got)
	}
}

func TestComputeImageQuota(t *testing.T) {
	// 720p (ratio<1500000像素) → 5 credits = $0.05 = 25000 quota
	got := ComputeImageQuota("1280:720")
	if got != 25000 {
		t.Errorf("ComputeImageQuota(720p) = %d, want 25000", got)
	}
	// 1080p (≥1500000像素) → 8 credits = $0.08 = 40000 quota
	got = ComputeImageQuota("1920:1080")
	if got != 40000 {
		t.Errorf("ComputeImageQuota(1080p) = %d, want 40000", got)
	}
	// 缺省/非法 → 按 720p
	got = ComputeImageQuota("")
	if got != 25000 {
		t.Errorf("ComputeImageQuota(empty) = %d, want 25000", got)
	}
}

func TestExtractDurationSeconds(t *testing.T) {
	cases := []struct {
		body string
		want float64
	}{
		{`{"duration":5}`, 5},
		{`{"duration":10.0}`, 10},
		{`{"duration":"8"}`, 8},
		{`{}`, 10}, // 默认 10s
		{`not-json`, 10},
	}
	for _, tc := range cases {
		if got := extractDurationSeconds([]byte(tc.body)); got != tc.want {
			t.Errorf("extractDurationSeconds(%q) = %v, want %v", tc.body, got, tc.want)
		}
	}
}

func TestExtractModelName(t *testing.T) {
	if got := extractModelName([]byte(`{"model":"gen4_aleph"}`)); got != "gen4_aleph" {
		t.Errorf("extractModelName = %q", got)
	}
	if got := extractModelName([]byte(`{}`)); got != "" {
		t.Errorf("extractModelName empty = %q, want empty", got)
	}
}

func TestExtractRatio(t *testing.T) {
	if got := extractRatio([]byte(`{"ratio":"1920:1080"}`)); got != "1920:1080" {
		t.Errorf("extractRatio = %q", got)
	}
}
```

- [ ] **Step 2：确认失败**

Run: `go test ./relay/channel/runway/ -run TestVideoCreditRate -v`
Expected: FAIL（函数未定义）

- [ ] **Step 3：实现**

```go
// relay/channel/runway/billing.go
package runway

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/songquanpeng/one-api/common/config"
)

// Runway 官方定价：1 credit = $0.01
const creditToUSD = 0.01

// 图像分辨率阈值：像素 ≥ 1.5M 视为 1080p 档，否则 720p 档。
// 参考现有 calculateImageCredits 实现。
const imageHighResPixelThreshold = 1_500_000

// 图像 credits（对应 Runway gen4_image 定价）
const (
	imageCredits720p  = 5
	imageCredits1080p = 8
)

// videoCreditRate 返回视频模型的 credits/s 费率。
// 未知模型按 5 c/s 保底（gen3a_turbo/gen4_turbo/act_two 的常见值）。
func videoCreditRate(model string) float64 {
	switch model {
	case "gen4_aleph":
		return 15
	case "upscale_v1":
		return 2
	case "gen4_turbo", "gen3a_turbo", "act_two":
		return 5
	default:
		return 5
	}
}

// ComputeVideoQuota 计算视频任务的 quota（1 quota = 1/QuotaPerUnit 美元）。
// 公式：creditsPerSec × durationSec × $/credit × quota/$
func ComputeVideoQuota(model string, durationSec float64) int64 {
	usd := videoCreditRate(model) * durationSec * creditToUSD
	return int64(usd * config.QuotaPerUnit)
}

// ComputeImageQuota 根据 ratio 字段计算图像 quota。
// 解析 "宽:高"；像素数 ≥ 阈值算 1080p 档，否则 720p 档。
func ComputeImageQuota(ratio string) int64 {
	credits := imageCredits720p
	var w, h int
	if _, err := fmt.Sscanf(ratio, "%d:%d", &w, &h); err == nil {
		if w*h >= imageHighResPixelThreshold {
			credits = imageCredits1080p
		}
	}
	usd := float64(credits) * creditToUSD
	return int64(usd * config.QuotaPerUnit)
}

// QuotaToUSD 把 quota 转为美元（用于日志展示）。
// 修复了原代码 `quota/5000000`（图像）与 `quota/500000`（视频）不一致的 10× bug。
func QuotaToUSD(quota int64) float64 {
	return float64(quota) / config.QuotaPerUnit
}

// ---- body 解析 helper（只读，不修改原 body）----

func extractModelName(body []byte) string {
	var m struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &m)
	return m.Model
}

func extractRatio(body []byte) string {
	var m struct {
		Ratio string `json:"ratio"`
	}
	_ = json.Unmarshal(body, &m)
	return m.Ratio
}

// extractDurationSeconds 解析 body 里的 duration 字段，容忍 number / string。
// 缺省或解析失败时返回 10（Runway 默认时长）。
func extractDurationSeconds(body []byte) float64 {
	var m struct {
		Duration any `json:"duration"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return 10
	}
	switch v := m.Duration.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return 10
}

// ComputeQuota 根据 mode + 请求 body 计算应扣 quota。
// 图像按 ratio，视频按 model × duration。
func ComputeQuota(mode Mode, body []byte) int64 {
	if mode.Kind == KindImage {
		return ComputeImageQuota(extractRatio(body))
	}
	return ComputeVideoQuota(extractModelName(body), extractDurationSeconds(body))
}
```

- [ ] **Step 4：确认通过**

Run: `go test ./relay/channel/runway/ -v`
Expected: PASS（所有测试全绿）

- [ ] **Step 5：编译验证**

Run: `go build ./... && go vet ./...`
Expected: 无错误

- [ ] **Step 6：Commit**

```bash
git add relay/channel/runway/billing.go relay/channel/runway/billing_test.go
git commit -m "feat(runway): 统一计费计算并修复图像日志 10× bug"
```

---

## Task 6：HTTP 透传层（`proxy.go`）

**Files:**
- Create: `relay/channel/runway/proxy.go`

本层唯一职责：发 HTTP 请求。不解析 body、不改写响应体。

- [ ] **Step 1：实现**

```go
// relay/channel/runway/proxy.go
package runway

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/util"
)

// UpstreamResult 是上游 Runway 响应的原始快照。
type UpstreamResult struct {
	Status int
	Header http.Header
	Body   []byte
}

// Proxy 把客户请求原样转发给 Runway。
// - URL：meta.BaseURL + StripRoutePrefix(c.Request.URL.Path)
// - key：优先用 middleware.Distribute 已选好的 actual_key（支持 multi-key 轮询），
//   single-key 渠道下 actual_key 即 channel.Key
// - 注入 X-Runway-Version
// - body 参数即客户原始请求体，传什么发什么
//
// 返回上游响应的 status / header / body；网络错误返回 err。
// 不写入 c.Writer，由 caller 决定如何回写。
func Proxy(c *gin.Context, meta *util.RelayMeta, body []byte) (*UpstreamResult, error) {
	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return nil, fmt.Errorf("获取渠道信息失败: %w", err)
	}

	// 优先用 middleware 选好的真实 key（multi-key 正确性来源）
	actualKey := c.GetString("actual_key")
	if actualKey == "" {
		actualKey = channel.Key
	}
	if actualKey == "" {
		return nil, fmt.Errorf("渠道密钥为空")
	}

	fullURL := meta.BaseURL + StripRoutePrefix(c.Request.URL.Path)

	req, err := http.NewRequestWithContext(c.Request.Context(),
		c.Request.Method, fullURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 拷贝关键请求头
	if ct := c.Request.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if accept := c.Request.Header.Get("Accept"); accept != "" {
		req.Header.Set("Accept", accept)
	}
	req.Header.Set("Authorization", "Bearer "+actualKey)
	req.Header.Set(HeaderVersion, APIVersion)

	resp, err := util.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	return &UpstreamResult{
		Status: resp.StatusCode,
		Header: resp.Header,
		Body:   respBody,
	}, nil
}

// WriteUpstream 把 UpstreamResult 原样写回客户端。
// 跳过 Content-Length（因 body 可能已被 caller 改写为加前缀后的版本）。
func WriteUpstream(c *gin.Context, up *UpstreamResult) {
	for k, vs := range up.Header {
		if isHopHeader(k) {
			continue
		}
		for _, v := range vs {
			c.Writer.Header().Add(k, v)
		}
	}
	ct := up.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}
	c.Data(up.Status, ct, up.Body)
}

// isHopHeader 列出需跳过的响应头（由 Gin 自行计算）。
func isHopHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Content-Length", "Transfer-Encoding":
		return true
	}
	return false
}
```

- [ ] **Step 2：编译验证**

Run: `go build ./...`
Expected: 无错误

- [ ] **Step 3：Commit**

```bash
git add relay/channel/runway/proxy.go
git commit -m "feat(runway): 添加 HTTP 透传层"
```

---

## Task 7：创建请求组合层（`handler.go`）

**Files:**
- Create: `relay/channel/runway/handler.go`

组合：Proxy → 计费 → 写日志 → 改写响应 id。

- [ ] **Step 1：实现**

```go
// relay/channel/runway/handler.go
package runway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	relaycontroller "github.com/songquanpeng/one-api/relay/controller"
	"github.com/songquanpeng/one-api/relay/util"
)

// Handler 处理 Runway 创建类请求（text_to_video / image_to_video / ... / video_upscale）。
// 由 controller.RelayRunway 外壳调用（外壳负责重试）。
//
// 流程：
// 1. 从 URL 判 mode（text_to_image → 图像分支；其它 → 视频分支）
// 2. 读 body 原样透传
// 3. 上游 200 时：编码任务 ID、计费、写日志；改写响应 body 的 id 字段
// 4. 其它状态原样透传
func Handler(c *gin.Context, meta *util.RelayMeta) {
	ctx := c.Request.Context()

	mode, ok := ModeFromPath(c.Request.URL.Path)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown runway endpoint: " + c.Request.URL.Path})
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取请求体失败: " + err.Error()})
		return
	}

	upstream, err := Proxy(c, meta, body)
	if err != nil {
		logger.Errorf(ctx, "runway.Handler proxy error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if upstream.Status == http.StatusOK {
		rawID, okID := extractResponseID(upstream.Body)
		if okID {
			taskID := EncodeTaskID(mode.Kind, rawID)
			upstream.Body = rewriteResponseID(upstream.Body, taskID)

			quota := ComputeQuota(mode, body)
			keyIndex := c.GetInt("key_index") // middleware 设置
			if err := bill(c, meta, mode, quota, body, taskID, keyIndex); err != nil {
				logger.Errorf(ctx, "runway.Handler bill error: %v", err)
			}
		} else {
			logger.Errorf(ctx, "runway.Handler: 200 response missing `id`: %s", string(upstream.Body))
		}
	}

	WriteUpstream(c, upstream)
}

// extractResponseID 从 Runway JSON 响应里读 id 字段。
func extractResponseID(body []byte) (string, bool) {
	var m struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &m); err != nil || m.ID == "" {
		return "", false
	}
	return m.ID, true
}

// rewriteResponseID 把响应体中的 id 替换为带前缀版本。
// 失败时返回原 body（不破坏透传）。
func rewriteResponseID(body []byte, newID string) []byte {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	m["id"] = newID
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}

// bill 统一扣费 + 日志 + DB 记录 + 记录 keyIndex（用于未来查询时定位到正确的 key）。
// 失败时只记录错误不抛，避免破坏客户响应（扣费失败不影响客户拿到 task ID）。
func bill(c *gin.Context, meta *util.RelayMeta, mode Mode, quota int64, body []byte, taskID string, keyIndex int) error {
	if quota == 0 {
		return nil
	}
	ctx := context.Background()

	// 1. 扣 token 配额
	if err := dbmodel.PostConsumeTokenQuota(meta.TokenId, quota); err != nil {
		return fmt.Errorf("扣 token 配额失败: %w", err)
	}
	_ = dbmodel.CacheUpdateUserQuota(ctx, meta.UserId)

	tokenName := c.GetString("token_name")
	referer := c.Request.Header.Get("HTTP-Referer")
	title := c.Request.Header.Get("X-Title")
	requestID := c.GetString("X-Request-ID")

	// 2. 写消费日志 + 更新用户/渠道累计
	if mode.Kind == KindImage {
		logContent := fmt.Sprintf(
			"Runway Image Generation  model: %s, mode: %s, total cost: $%.6f",
			meta.OriginModelName, mode.Name, QuotaToUSD(quota))
		dbmodel.RecordConsumeLogWithRequestID(ctx, meta.UserId, meta.ChannelId,
			0, 0, meta.OriginModelName, tokenName, quota, logContent,
			0, title, referer, false, 0, requestID)
	} else {
		duration := extractDurationSeconds(body)
		logContent := fmt.Sprintf(
			"Runway Video Generation   model: %s, mode: %s, duration: %.0f, total cost: $%.6f",
			meta.OriginModelName, mode.Name, duration, QuotaToUSD(quota))
		dbmodel.RecordVideoConsumeLog(ctx, meta.UserId, meta.ChannelId,
			0, 0, meta.OriginModelName, tokenName, quota, logContent,
			0, title, referer, taskID)
	}
	dbmodel.UpdateUserUsedQuotaAndRequestCount(meta.UserId, quota)
	dbmodel.UpdateChannelUsedQuota(meta.ChannelId, quota)

	// 3. 写 Image / Video 表，再补写 key_index
	if mode.Kind == KindImage {
		if err := relaycontroller.CreateImageLog("runway", taskID, meta, "success", "", mode.Name, 1, quota); err != nil {
			return err
		}
		if err := dbmodel.DB.Model(&dbmodel.Image{}).
			Where("task_id = ?", taskID).Update("key_index", keyIndex).Error; err != nil {
			logger.Errorf(ctx, "runway.bill 写入图像 key_index 失败 taskId=%s: %v", taskID, err)
		}
		return nil
	}
	duration := fmt.Sprintf("%.0f", extractDurationSeconds(body))
	if err := relaycontroller.CreateVideoLog("runway", taskID, meta, mode.Name, duration, mode.Name, taskID, quota, 0, "", ""); err != nil {
		return err
	}
	if err := dbmodel.DB.Model(&dbmodel.Video{}).
		Where("task_id = ?", taskID).Update("key_index", keyIndex).Error; err != nil {
		logger.Errorf(ctx, "runway.bill 写入视频 key_index 失败 taskId=%s: %v", taskID, err)
	}
	return nil
}
```

- [ ] **Step 2：编译验证**

Run: `go build ./... && go vet ./...`
Expected: 无错误

- [ ] **Step 3：Commit**

```bash
git add relay/channel/runway/handler.go
git commit -m "feat(runway): 添加创建请求组合层 Handler"
```

---

## Task 8：查询请求 + 退款（`result.go` + `refund.go`）

**Files:**
- Create: `relay/channel/runway/refund.go`
- Create: `relay/channel/runway/result.go`

**简化**：`Image.Quota` 字段已存在（`model/image.go:25`），退款直接读 `image.Quota`，不再 `LIKE '%provider%'` 模糊查询。

- [ ] **Step 1：实现 refund.go**

```go
// relay/channel/runway/refund.go
package runway

import (
	"context"

	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
)

// Refund 根据 Kind 退还失败任务的配额给用户和渠道。
// quota 必须来自 DB（Image.Quota / Video.Quota），防止前端伪造。
func Refund(ctx context.Context, kind Kind, taskID string, userID, channelID int, quota int64) {
	if quota <= 0 {
		logger.Infof(ctx, "runway.Refund: taskId=%s quota=0，跳过", taskID)
		return
	}
	if err := dbmodel.CompensateVideoTaskQuota(userID, quota); err != nil {
		logger.Errorf(ctx, "runway.Refund 补用户配额失败 taskId=%s: %v", taskID, err)
		return
	}
	if err := dbmodel.CompensateChannelQuota(channelID, quota); err != nil {
		logger.Errorf(ctx, "runway.Refund 补渠道配额失败 taskId=%s: %v", taskID, err)
		return
	}
	logger.Infof(ctx, "runway.Refund 成功 kind=%s taskId=%s userId=%d channelId=%d quota=%d",
		kind, taskID, userID, channelID, quota)
}
```

- [ ] **Step 2：实现 result.go**

```go
// relay/channel/runway/result.go
package runway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/util"
)

// HandleResult 处理 GET /runway/v1/tasks/:taskId 查询。
// 流程：
// 1. 解码前缀 → 找到对应 DB 记录与渠道
// 2. 向 Runway 官方发起查询
// 3. 同步状态到 DB；若转为失败终态且此前非失败，退款
// 4. 把响应 id 改回带前缀的形式后透传
func HandleResult(c *gin.Context, taskID string) {
	ctx := c.Request.Context()

	kind, rawID, _ := DecodeTaskID(taskID)
	channel, keyIndex, err := lookupChannelByTaskID(kind, taskID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// 按 keyIndex 取创建时用的那个 key（multi-key 渠道关键）
	key, err := channel.GetKeyByIndex(keyIndex)
	if err != nil || key == "" {
		// 兼容：旧数据 keyIndex=0 + single-key 渠道 → channel.Key
		key = channel.Key
	}

	upstreamURL := fmt.Sprintf("%s/v1/tasks/%s", channel.GetBaseURL(), rawID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建请求失败: " + err.Error()})
		return
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set(HeaderVersion, APIVersion)
	req.Header.Set("Accept", "application/json")

	resp, err := util.HTTPClient.Do(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "请求失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取响应失败: " + err.Error()})
		return
	}

	// 解析并同步 DB（即使非 200 也解析，可能有 failureCode 信息）
	var upstream map[string]any
	if jsonErr := json.Unmarshal(body, &upstream); jsonErr == nil {
		syncTaskStatus(ctx, kind, taskID, upstream)
	} else {
		logger.Errorf(ctx, "runway.HandleResult: 响应 JSON 解析失败: %v body=%s", jsonErr, string(body))
	}

	// 200 时把上游 id 改回带前缀的形式
	if resp.StatusCode == http.StatusOK {
		if id, ok := upstream["id"].(string); ok && id == rawID {
			upstream["id"] = taskID
			if rewrite, err := json.Marshal(upstream); err == nil {
				body = rewrite
			}
		}
	}

	// 透传响应头（跳过 Content-Length）
	for k, vs := range resp.Header {
		if isHopHeader(k) {
			continue
		}
		for _, v := range vs {
			c.Writer.Header().Add(k, v)
		}
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}
	c.Data(resp.StatusCode, ct, body)
}

// lookupChannelByTaskID 根据 Kind 去对应表查渠道与创建时使用的 keyIndex。
func lookupChannelByTaskID(kind Kind, taskID string) (*dbmodel.Channel, int, error) {
	var channelID, keyIndex int
	switch kind {
	case KindImage:
		task, err := dbmodel.GetImageByTaskId(taskID)
		if err != nil {
			return nil, 0, fmt.Errorf("图像任务不存在: %w", err)
		}
		channelID = task.ChannelId
		keyIndex = task.KeyIndex
	default:
		task, err := dbmodel.GetVideoTaskById(taskID)
		if err != nil {
			return nil, 0, fmt.Errorf("视频任务不存在: %w", err)
		}
		channelID = task.ChannelId
		keyIndex = task.KeyIndex
	}
	channel, err := dbmodel.GetChannelById(channelID, true)
	return channel, keyIndex, err
}

// syncTaskStatus 根据上游响应更新 DB 并在状态转为失败终态时退款（幂等）。
func syncTaskStatus(ctx context.Context, kind Kind, taskID string, upstream map[string]any) {
	status, _ := upstream["status"].(string)
	failure, _ := upstream["failure"].(string)
	failureCode, _ := upstream["failureCode"].(string)
	dbStatus := MapStatus(status)

	if kind == KindImage {
		task, err := dbmodel.GetImageByTaskId(taskID)
		if err != nil {
			logger.Errorf(ctx, "syncTaskStatus 取图像任务失败: %v", err)
			return
		}
		oldStatus := task.Status
		task.Status = dbStatus
		task.FailReason = chooseFailReason(status, failure, failureCode, task.FailReason)
		if status == "SUCCEEDED" {
			task.StoreUrl = firstOutputURL(upstream)
		}
		needRefund := !IsTerminalFailed(oldStatus) && IsTerminalFailed(dbStatus)
		if err := dbmodel.DB.Model(&dbmodel.Image{}).
			Where("task_id = ?", taskID).Updates(task).Error; err != nil {
			logger.Errorf(ctx, "更新图像任务状态失败: %v", err)
			return
		}
		if needRefund {
			Refund(ctx, kind, taskID, task.UserId, task.ChannelId, task.Quota)
		}
		return
	}

	// 视频
	task, err := dbmodel.GetVideoTaskById(taskID)
	if err != nil {
		logger.Errorf(ctx, "syncTaskStatus 取视频任务失败: %v", err)
		return
	}
	oldStatus := task.Status
	task.Status = dbStatus
	task.FailReason = chooseFailReason(status, failure, failureCode, task.FailReason)
	if status == "SUCCEEDED" {
		task.StoreUrl = firstOutputURL(upstream)
	}
	task.TotalDuration = time.Now().Unix() - task.CreatedAt
	needRefund := !IsTerminalFailed(oldStatus) && IsTerminalFailed(dbStatus)
	if err := task.Update(); err != nil {
		logger.Errorf(ctx, "更新视频任务状态失败: %v", err)
		return
	}
	if needRefund {
		Refund(ctx, kind, taskID, task.UserId, task.ChannelId, task.Quota)
	}
}

func chooseFailReason(status, failure, failureCode, current string) string {
	if status != "FAILED" {
		return ""
	}
	if failure != "" {
		return failure
	}
	if failureCode != "" {
		return failureCode
	}
	return current
}

// firstOutputURL 取 Runway 响应里 output[0] 作为结果 URL。
func firstOutputURL(upstream map[string]any) string {
	out, ok := upstream["output"].([]any)
	if !ok || len(out) == 0 {
		return ""
	}
	url, _ := out[0].(string)
	return url
}
```

**注意**：`result.go` 顶部需 `import "context"`，与 `gin-gonic/gin` 并列。

- [ ] **Step 3：编译验证**

Run: `go build ./... && go vet ./...`
Expected: 无错误

- [ ] **Step 4：Commit**

```bash
git add relay/channel/runway/result.go relay/channel/runway/refund.go
git commit -m "feat(runway): 添加任务查询、DB 同步与幂等退款"
```

---

## Task 9：接入外壳 `controller.RelayRunway` / `RelayRunwayResult`

**Files:**
- Modify: `controller/relay.go` — 改写 `tryRunwayRequest` 调用新 Handler；`RelayRunwayResult` 调用新 HandleResult

- [ ] **Step 1：改 `tryRunwayRequest`（约在 `controller/relay.go:1936` 附近）**

找到：
```go
controller.DirectRelayRunway(c, meta)
```
（两处：`tryRunwayRequest` 函数体内 + `writeLastFailureResponse` 函数体内）

**替换为：**
```go
runway.Handler(c, meta)
```

文件顶部 `import` 块新增：
```go
"github.com/songquanpeng/one-api/relay/channel/runway"
```

- [ ] **Step 2：改 `RelayRunwayResult`（约在 `controller/relay.go:2016`）**

原函数：
```go
func RelayRunwayResult(c *gin.Context) {
	taskId := c.Param("taskId")
	if taskId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "taskId is required"})
		return
	}
	controller.GetRunwayResult(c, taskId)
}
```

**改为：**
```go
func RelayRunwayResult(c *gin.Context) {
	taskId := c.Param("taskId")
	if taskId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "taskId is required"})
		return
	}
	runway.HandleResult(c, taskId)
}
```

- [ ] **Step 3：清理 import（删除 `relay/controller` 如不再被此文件其它地方使用）**

Run: `go build ./...`
如果报 "imported and not used: relay/controller"，从 import 中删除该行；如果仍被其它函数使用则保留。

- [ ] **Step 4：编译验证**

Run: `go build ./... && go vet ./...`
Expected: 无错误

- [ ] **Step 5：Commit**

```bash
git add controller/relay.go
git commit -m "refactor(runway): 外壳接入新 runway 包 Handler"
```

---

## Task 10：新增 `text_to_video` 路由

**Files:**
- Modify: `router/relay-router.go:210`（Runway 路由组内）

- [ ] **Step 1：定位到 Runway 路由组**

搜索 `runwayRouter.POST("/image_to_video"` 找到 Runway 路由组（约 208-215 行）。

- [ ] **Step 2：在组内新增一行**

现状：
```go
runwayRouter.POST("/image_to_video", controller.RelayRunway)
runwayRouter.POST("/video_to_video", controller.RelayRunway)
runwayRouter.POST("/text_to_image", controller.RelayRunway)
runwayRouter.POST("/video_upscale", controller.RelayRunway)
runwayRouter.POST("/character_performance", controller.RelayRunway)
```

**改为（首行新增 `text_to_video`）：**
```go
runwayRouter.POST("/text_to_video", controller.RelayRunway)
runwayRouter.POST("/image_to_video", controller.RelayRunway)
runwayRouter.POST("/video_to_video", controller.RelayRunway)
runwayRouter.POST("/text_to_image", controller.RelayRunway)
runwayRouter.POST("/video_upscale", controller.RelayRunway)
runwayRouter.POST("/character_performance", controller.RelayRunway)
```

- [ ] **Step 3：编译验证**

Run: `go build ./... && go vet ./...`
Expected: 无错误

- [ ] **Step 4：Commit**

```bash
git add router/relay-router.go
git commit -m "feat(runway): 补齐 text_to_video 路由"
```

---

## Task 11：删除旧 Runway 实现（directvideo.go 里的 Runway 函数）

**Files:**
- Modify: `relay/controller/directvideo.go` — 删除以下函数

目标删除函数（在 `relay/controller/directvideo.go` 内）：

| 函数 | 行位置（以当前文件为准） |
|---|---|
| `DirectRelayRunway` | ~42 |
| `determineVideoMode` | ~210 |
| `extractDurationFromRequest` | ~246 |
| `calculateRunwayQuota` | ~260 |
| `calculateImageCredits` | ~299 |
| `getDurationSeconds` | ~327 |
| `GetRunwayResult` | ~348 |
| `updateTaskStatus` | ~474 |
| `mapRunwayStatusToDbStatus` | ~585 |
| `compensateRunwayImageTask` | ~610 |
| `calculateDefaultImageQuota` | ~657 |
| `compensateWithQuota` | ~668 |
| `compensateRunwayVideoTask` | ~687 |
| `handleRunwayImageBilling` | ~724 |
| `handleRunwayVideoBilling` | ~758 |

- [ ] **Step 1：grep 确认删除目标不被外部调用**

Run: `grep -rn "DirectRelayRunway\|GetRunwayResult\|compensateRunwayImageTask\|compensateRunwayVideoTask\|compensateWithQuota\|handleRunwayImageBilling\|handleRunwayVideoBilling\|determineVideoMode\|calculateRunwayQuota\|updateTaskStatus\|mapRunwayStatusToDbStatus\|calculateDefaultImageQuota" --include="*.go" .`

Expected: 仅 `relay/controller/directvideo.go` 内部调用 + `controller/relay.go` 对 `DirectRelayRunway` / `GetRunwayResult` 的调用（已在 Task 9 替换为 runway 包）。

**若发现外部（非 directvideo.go / 非 controller/relay.go）调用任何一个函数，STOP：**
- 记录调用点
- 评估是否该函数实际服务于非 Runway 场景（如 `compensateWithQuota` 若被 Sora 调用）
- 报给用户决策

- [ ] **Step 2：逐个删除函数**

用 Edit 工具按函数为单位删除（不要整块替换以防误伤上下文注释）。每删一个跑一次 `go build ./...` 防止语法崩。

- [ ] **Step 3：清理孤儿 import**

`go build ./...` 若报 "imported and not used"，删除 `relay/controller/directvideo.go` 里不再使用的 import（如 `strings`、`time`、`context` 等——保留仍被 Sora / 其它代码使用的）。

- [ ] **Step 4：编译与 vet**

Run: `go build ./... && go vet ./...`
Expected: 无错误

- [ ] **Step 5：Commit**

```bash
git add relay/controller/directvideo.go
git commit -m "refactor(runway): 删除 directvideo.go 中的 Runway 实现（已迁移到 runway 包）"
```

---

## Task 12：删除 `relay/channel/runway/` 内的旧文件

**Files:**
- Delete: `relay/channel/runway/video_adaptor.go`
- Delete: `relay/channel/runway/model.go`

**前置验证**：确认 `VideoAdaptor` 类型、`VideoGenerationRequest`、`VideoResponse`、`VideoFinalResponse` 不被除本文件外的任何地方引用。

- [ ] **Step 1：grep 确认**

Run: `grep -rn "runway\.VideoAdaptor\|runway\.VideoGenerationRequest\|runway\.VideoResponse\|runway\.VideoFinalResponse\|runway\.PromptImage\|runway\.ModelDetails\|mapTaskStatusRunway" --include="*.go" .`

Expected: 仅 `relay/channel/runway/video_adaptor.go` / `model.go` 内部引用。

**若有外部引用，STOP 并报给用户。**

- [ ] **Step 2：删除**

```bash
rm relay/channel/runway/video_adaptor.go
rm relay/channel/runway/model.go
```

- [ ] **Step 3：编译与 vet**

Run: `go build ./... && go vet ./...`
Expected: 无错误

- [ ] **Step 4：Commit**

```bash
git add -A relay/channel/runway/
git commit -m "refactor(runway): 删除 video_adaptor.go / model.go 死代码"
```

---

## Task 13：运行全量单元测试 + 冒烟清单

- [ ] **Step 1：全量单元测试**

Run: `go test ./...`
Expected: 现有测试 + 新增 runway 包测试全部 PASS（或与 main 分支一致的既有失败，不引入新失败）

- [ ] **Step 2：`go vet`**

Run: `go vet ./...`
Expected: 无新 warning

- [ ] **Step 3：冒烟测试清单（手动，需可用 Runway 渠道 key）**

对每个端点发一次请求，确认：
- 响应 body 格式与官方一致，仅 `id` 字段带 `video-`/`image-` 前缀
- DB 的 `videos` / `images` 表有对应记录
- 配额正确扣除

| 端点 | curl 示例 | 预期 |
|---|---|---|
| `POST /runway/v1/text_to_video` | `{"model":"gen4_turbo","promptText":"a cat","duration":5}` | 200，id 以 `video-` 开头 |
| `POST /runway/v1/image_to_video` | `{"model":"gen4_turbo","promptImage":"https://...","duration":5}` | 200，id 以 `video-` 开头 |
| `POST /runway/v1/video_to_video` | `{"model":"gen4_aleph","videoUri":"https://...","duration":5}` | 200，id 以 `video-` 开头 |
| `POST /runway/v1/text_to_image` | `{"model":"gen4_image","promptText":"sunset","ratio":"1920:1080"}` | 200，id 以 `image-` 开头 |
| `POST /runway/v1/character_performance` | 参考官方 | 200，id 以 `video-` 开头 |
| `POST /runway/v1/video_upscale` | 参考官方 | 200，id 以 `video-` 开头 |
| `GET /runway/v1/tasks/video-<id>` | 从上面任一创建返回拿 ID | 200，id 字段保留前缀 |

**额外 multi-key 场景验证（若有 multi-key 的 Runway 渠道）：**
- 用一个带多个 key 的 Runway 渠道重复发 3-5 次 `image_to_video`，确认每次 DB `videos.key_index` 值反映当次被选中的 key
- 对每个任务 ID 单独查询，全部返回 200（证明查询用的是创建时那个 key）

- [ ] **Step 4：最终整体 commit（如仍有 uncommitted）**

```bash
git status
# 若干净则跳过
# 若有文件变更，按模块分批 commit
```

---

## 附录 A：文件职责速查表

| 文件 | 职责 | 对外导出 |
|---|---|---|
| `constant.go` | API 版本、路由前缀常量 | `APIVersion`、`HeaderVersion`、`RoutePrefix` |
| `taskid.go` | 任务 ID 前缀编解码 | `Kind`、`KindVideo`、`KindImage`、`EncodeTaskID`、`DecodeTaskID` |
| `mode.go` | URL 路径 → Mode 映射 | `Mode`、`ModeFromPath`、`StripRoutePrefix` |
| `status.go` | 上游状态 → DB 状态映射 | `MapStatus`、`IsTerminalFailed` |
| `billing.go` | 计费金额计算 | `ComputeQuota`、`ComputeVideoQuota`、`ComputeImageQuota`、`QuotaToUSD` |
| `proxy.go` | HTTP 透传 | `Proxy`、`WriteUpstream`、`UpstreamResult` |
| `refund.go` | 失败任务退款 | `Refund` |
| `result.go` | 查询 + DB 同步 | `HandleResult` |
| `handler.go` | 创建请求组合层 | `Handler` |

## 附录 B：对外兼容性清单

| 维度 | 保证 |
|---|---|
| URL 路径 | `/runway/v1/*` 全保留 + 新增 `text_to_video` |
| HTTP 方法 | 不变 |
| 请求体 | 纯透传，不做字段校验/补全 |
| 响应体 | 上游响应 body 原样返回，**仅 `id` 字段改为 `video-`/`image-` 前缀版本** |
| 任务 ID 前缀 | `video-` / `image-` 保留 |
| 旧任务查询 | `DecodeTaskID` 容忍无前缀（默认视频） |
| 重试行为 | 不变（外壳不改） |
| DB schema | 零变更 |
