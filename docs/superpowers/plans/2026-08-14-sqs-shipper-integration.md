# one-api 接入 AWS SQS（billship 消费日志异步投递）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 每条消费日志（普通 + 视频）写入 `logs` 表成功后，把该行 JSON 通过 billship SDK 非阻塞异步投递到 SQS，绝不阻塞原业务流程。

**Architecture:** 新增薄适配层 `common/shipper`（不 import model，避免 import 环），封装外部 SDK `billship` 的 `Init/Ship/Shutdown`；在 `model/log.go` 两处消费日志入口的 `LOG_DB.Create` 成功后调用 `shipper.Ship`；`main.go` 启动时 `Init`、`defer Shutdown`。

**Tech Stack:** Go、私有 module `github.com/changshiaos/charge/server/shipper v0.1.0`、`aws-sdk-go-v2/service/sqs`、Docker BuildKit secret。

## Global Constraints

- **不阻塞原业务**：`billship.Ship` 本身即非阻塞（buffer 满即丢弃 + 失败日志，永不阻塞/panic），**直接同步调用**，禁止再包 `go func()`。
- **投递范围仅两处**：`RecordConsumeLogWithOtherAndRequestID`、`RecordVideoConsumeLog`。不碰探针/错误/系统日志。
- **无 DB schema 变更**。禁用（默认）时对现有行为零影响。
- **生产依赖约束**：固定引用 `github.com/changshiaos/charge/server/shipper v0.1.0`，不使用本地 `replace`；Docker CI 通过只授予 charge `Contents: Read-only` 的 `CHARGE_REPO_READ_TOKEN` BuildKit secret 下载，凭证不进入镜像层。
- `SourceType` 固定 `"one-api"`；`SiteID` 取环境变量固定值（多站点部署靠各实例的 `BILL_SHIP_SITE_ID` 区分，同一镜像通用，无需重新打包）。
- 每条 `Ship` 的 `Body` 用 `json.Marshal(log)` **新分配**，满足 billship "Ship 后不得修改 Body" 契约。
- 提交前必跑 `go build ./... && go vet ./...`。

## 文件结构

- `go.mod` / `go.sum`：修改 —— 固定引用已发布的 `v0.1.0`，不使用 `replace`。
- `common/config/config.go`：修改 —— 末尾加 5 个 `BillShip*` 配置项。
- `common/shipper/shipper.go`：新建 —— 适配层 `Init/Ship/Shutdown`。
- `common/shipper/shipper_test.go`：新建 —— 单测。
- `model/log.go`：修改 —— 两处埋点 + import。
- `main.go`：修改 —— `shipper.Init()` + `defer shipper.Shutdown(ctx)`。
- `docs/CHANGELOG.md`：修改 —— 变更记录。

---

### Task 1: 引入 billship 依赖 + 配置项 + 适配层 `common/shipper`

**Files:**
- Modify: `go.mod`（加 require + replace）、`go.sum`（tidy 生成）
- Modify: `common/config/config.go:275`（`InitialRootToken` 之后追加配置项）
- Create: `common/shipper/shipper.go`
- Test: `common/shipper/shipper_test.go`

**Interfaces:**
- Produces:
  - `func shipper.Init()` —— 读 config 初始化 billship 单例；禁用/失败降级不 crash。
  - `func shipper.Ship(logID int64, createdAt int64, modelName string, body []byte)` —— 转发到 `billship.Ship`；未 Init 时安全 no-op。
  - `func shipper.Shutdown(ctx context.Context)` —— 转发 `billship.Shutdown`。
  - config 变量：`config.BillShipEnabled bool`、`config.BillShipQueueURL string`、`config.BillShipRegion string`、`config.BillShipSiteID string`、`config.BillShipLogFailedBody bool`。

- [ ] **Step 1: 加生产依赖**

Run:
```bash
cd /Users/changshiao/work/project/one-api
go mod edit -require=github.com/changshiaos/charge/server/shipper@v0.1.0
```

- [ ] **Step 2: 追加配置项**

在 `common/config/config.go` 末尾 `getHostname()` 函数**之前**追加：
```go
// —— 计费投递（billship / SQS）——
// SiteID 为每个部署实例的固定值：多站点用同一镜像、各自设 BILL_SHIP_SITE_ID 区分，无需重新打包。
var BillShipEnabled = env.Bool("BILL_SHIP_ENABLED", false)
var BillShipQueueURL = env.String("BILL_SHIP_QUEUE_URL", "")
var BillShipRegion = env.String("BILL_SHIP_REGION", "")
var BillShipSiteID = env.String("BILL_SHIP_SITE_ID", "")
var BillShipLogFailedBody = env.Bool("BILL_SHIP_LOG_FAILED_BODY", false)
var BillShipBufferSize = env.Int("BILL_SHIP_BUFFER_SIZE", 10000)
var BillShipBatchSize = env.Int("BILL_SHIP_BATCH_SIZE", 10)
var BillShipBatchWaitMS = env.Int("BILL_SHIP_BATCH_WAIT_MS", 200)
var BillShipSendConcurrency = env.Int("BILL_SHIP_SEND_CONCURRENCY", 8)
var BillShipSendTimeoutSeconds = env.Int("BILL_SHIP_SEND_TIMEOUT_SECONDS", 3)
var BillShipMaxRetries = env.Int("BILL_SHIP_MAX_RETRIES", 3)
```

- [ ] **Step 3: 写适配层 `common/shipper/shipper.go`**

```go
// Package shipper 是 one-api 侧对 billship 计费投递 SDK（SQS 生产者）的适配层。
// 隔离外部 SDK：本包不 import model，故 model → common/shipper → billship 无 import 环。
package shipper

import (
	"context"
	"fmt"

	billship "github.com/changshiaos/charge/server/shipper"

	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
)

// sourceType 标识消息来源，供计费侧区分网关类型。
const sourceType = "one-api"

// Init 按配置初始化 billship 单例。未启用时直接返回（Ship 后续为安全 no-op）。
// init 失败仅告警降级、绝不 crash 启动（对齐 audit 容错模式）。
func Init() {
	if !config.BillShipEnabled {
		logger.SysLog("bill shipper disabled")
		return
	}
	err := billship.Init(billship.Config{
		QueueURL:      config.BillShipQueueURL,
		Region:        config.BillShipRegion,
		SiteID:        config.BillShipSiteID,
		SourceType:    sourceType,
		Enabled:       true,
		LogFailedBody: config.BillShipLogFailedBody,
		Logger: func(level, msg string, kv ...any) {
			logger.SysError(fmt.Sprintf("[billship] %s %s %v", level, msg, kv))
		},
	})
	if err != nil {
		logger.SysError("bill shipper init failed, shipping disabled: " + err.Error())
		return
	}
	logger.SysLog("bill shipper initialized, site=" + config.BillShipSiteID)
}

// Ship 把一条 logs 行非阻塞投递到 SQS。未 Init / 禁用时为安全 no-op。
// 契约：调用后不得再修改 body（billship 异步持有该 slice）。调用方每次传入新分配的 body。
func Ship(logID int64, createdAt int64, modelName string, body []byte) {
	billship.Ship(billship.Record{
		SiteID:     config.BillShipSiteID,
		Model:      modelName,
		SourceType: sourceType,
		LogID:      logID,
		CreatedAt:  createdAt,
		Body:       body,
	})
}

// Shutdown 优雅停机，排空在途消息。未 Init 时返回 nil。
func Shutdown(ctx context.Context) {
	if err := billship.Shutdown(ctx); err != nil {
		logger.SysError("bill shipper shutdown: " + err.Error())
	}
}
```

- [ ] **Step 4: tidy 拉齐依赖并编译**

Run:
```bash
go mod tidy && go build ./...
```
Expected: exit 0，无报错（`go.sum` 补全 sqs 等间接依赖）。

- [ ] **Step 5: 写单测 `common/shipper/shipper_test.go`**

```go
package shipper

import (
	"context"
	"testing"
	"time"
)

// 未 Init（禁用）时，Ship 必须是安全 no-op，绝不 panic —— 这是"不影响原业务"的底线。
func TestShipNoopWhenNotInitialized(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Ship panicked when not initialized: %v", r)
		}
	}()
	Ship(1, time.Now().Unix(), "gpt-4o", []byte(`{"id":1,"model_name":"gpt-4o"}`))
}

// 未 Init 时 Shutdown 也应安全返回。
func TestShutdownNoopWhenNotInitialized(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	Shutdown(ctx) // 不 panic 即通过
}
```

- [ ] **Step 6: 跑测试**

Run:
```bash
go test ./common/shipper/... -v
```
Expected: PASS（`TestShipNoopWhenNotInitialized`、`TestShutdownNoopWhenNotInitialized`）。

- [ ] **Step 7: vet + commit**

```bash
go vet ./... && git add go.mod go.sum common/config/config.go common/shipper/
git commit -m "feat(billship): 新增 SQS 投递适配层 common/shipper 与配置项"
```

---

### Task 2: 在 model/log.go 两处消费日志入口埋点

**Files:**
- Modify: `model/log.go`（import 块；`RecordConsumeLogWithOtherAndRequestID` 的 Create 处 :174；`RecordVideoConsumeLog` 的 Create 处 :688）

**Interfaces:**
- Consumes: `shipper.Ship(logID int64, createdAt int64, modelName string, body []byte)`（Task 1）。

- [ ] **Step 1: 加 import**

`model/log.go` 顶部 import 块：标准库区加 `"encoding/json"`，one-api 区加 `"github.com/songquanpeng/one-api/common/shipper"`。改后：
```go
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/common/metrics"
	"github.com/songquanpeng/one-api/common/shipper"

	"gorm.io/gorm"
)
```

- [ ] **Step 2: 普通消费日志埋点**

`RecordConsumeLogWithOtherAndRequestID` 中，把原 Create 块（`model/log.go:174-177`）：
```go
	err := LOG_DB.Create(log).Error
	if err != nil {
		logger.Error(ctx, "failed to record log: "+err.Error())
	}
```
改为：
```go
	err := LOG_DB.Create(log).Error
	if err != nil {
		logger.Error(ctx, "failed to record log: "+err.Error())
	} else if body, mErr := json.Marshal(log); mErr == nil {
		// 写库成功后把该行 JSON 非阻塞投递到 SQS（billship 内部攒批异步发，不阻塞热路径）
		shipper.Ship(int64(log.Id), log.CreatedAt, log.ModelName, body)
	}
```

- [ ] **Step 3: 视频消费日志埋点**

`RecordVideoConsumeLog` 中，把原 Create 块（`model/log.go:688-691`）：
```go
	err := LOG_DB.Create(log).Error
	if err != nil {
		logger.Error(ctx, "failed to record video log: "+err.Error())
	}
```
改为：
```go
	err := LOG_DB.Create(log).Error
	if err != nil {
		logger.Error(ctx, "failed to record video log: "+err.Error())
	} else if body, mErr := json.Marshal(log); mErr == nil {
		shipper.Ship(int64(log.Id), log.CreatedAt, log.ModelName, body)
	}
```

- [ ] **Step 4: 编译验证（含 import 环检查）**

Run:
```bash
go build ./... && go vet ./...
```
Expected: exit 0。若报 import cycle，说明 `common/logger` 或 `common/config` 反向依赖 model —— 立即停止排查（预期不会）。

- [ ] **Step 5: commit**

```bash
git add model/log.go
git commit -m "feat(billship): 消费/视频日志写库成功后投递到 SQS"
```

---

### Task 3: main.go 接线（启动 Init + 停机 Shutdown）

**Files:**
- Modify: `main.go`（import 块加 shipper；`defer audit.Shutdown()` 之后 :153 插入）

**Interfaces:**
- Consumes: `shipper.Init()`、`shipper.Shutdown(ctx)`（Task 1）。`context`、`time` 已在 main.go 导入。

- [ ] **Step 1: 加 import**

`main.go` import 块 one-api 区（`"github.com/songquanpeng/one-api/common/metrics"` 一行下方）加：
```go
	"github.com/songquanpeng/one-api/common/shipper"
```

- [ ] **Step 2: 插入 Init + defer Shutdown**

在 `main.go:153` 的 `defer audit.Shutdown()` 之后插入：
```go
	// 启动计费投递（billship → SQS）；未启用/初始化失败自动降级，不影响业务
	shipper.Init()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shipper.Shutdown(ctx)
	}()
```

- [ ] **Step 3: 编译验证**

Run:
```bash
go build ./... && go vet ./...
```
Expected: exit 0。

- [ ] **Step 4: commit**

```bash
git add main.go
git commit -m "feat(billship): main 启动初始化投递、停机优雅排空"
```

---

### Task 4: 更新 CHANGELOG 并整体验证

**Files:**
- Modify: `docs/CHANGELOG.md`（顶部追加当日记录）

- [ ] **Step 1: 追加 CHANGELOG 记录**

在 `docs/CHANGELOG.md` 顶部（保持倒序）追加：
```markdown
## 2026-08-14

### feat(billship): 消费日志异步投递到 AWS SQS
- **分支**: `<当前分支名>`
- **类型**: 新功能
- **涉及文件**: `go.mod`、`common/config/config.go`、`common/shipper/shipper.go`(新)、`common/shipper/shipper_test.go`(新)、`model/log.go`、`main.go`
- **说明**: 新增 `common/shipper` 适配层封装 billship SDK；消费/视频日志写库成功后把该行 JSON 非阻塞投递到 SQS。默认关闭（`BILL_SHIP_ENABLED`），多站点靠 `BILL_SHIP_SITE_ID` 区分。生产构建固定引用 `github.com/changshiaos/charge/server/shipper v0.1.0`，由 BuildKit secret 提供 charge 仓库只读凭证。
- **关联计划**: `docs/plans/2026-08-14-sqs-shipper-integration.md`、`docs/superpowers/plans/2026-08-14-sqs-shipper-integration.md`
```

- [ ] **Step 2: 整体构建 + 测试 + vet**

Run:
```bash
go build ./... && go vet ./... && go test ./common/shipper/...
```
Expected: 全部 exit 0 / PASS。

- [ ] **Step 3: commit**

```bash
git add docs/CHANGELOG.md
git commit -m "docs(billship): CHANGELOG 记录 SQS 投递接入"
```

---

## 发布收尾

- shipper module 已发布并完成真实路径替换；后续升级只修改 `go.mod` 版本并重新执行 `go mod tidy`。
- Docker CI 使用 `CHARGE_REPO_READ_TOKEN` BuildKit secret 拉取依赖，生产者仓库自身的 `GITHUB_TOKEN` 不用于跨仓访问。
- 手工联调：LocalStack / 真实 SQS，发一次请求确认队列收到消息（属性 `site_id`/`model`/`source_type` 正确、Body 为 logs 行 JSON），业务响应不受影响。
