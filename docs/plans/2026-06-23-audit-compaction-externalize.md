# Audit Compaction 外部化：从进程内定时器改为单机调度

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 把 Iceberg compaction 从 audit 模块的内部 goroutine 剥离，改为复用 `ENABLE_VIDEO_TASK_POLLER` 开关，保证只在一台机器上执行。

**Architecture:** 删除 `compaction.go` 中的 `compactionLoop`，在 `main.go` 中新增独立的 compaction 定时任务，用 `isVideoTaskPollerEnabled()` 守卫（与其他 poller 一致的单机保证模式）。`runCompaction` 提升为 `RunCompaction` 公开函数供外部调用。

**Tech Stack:** Go, AWS Athena (OPTIMIZE SQL), 现有 `ENABLE_VIDEO_TASK_POLLER` 环境变量

---

## 变更范围

| 文件 | 动作 | 说明 |
|------|------|------|
| `common/audit/compaction.go` | 修改 | 删除 `compactionLoop`，导出 `RunCompaction` |
| `common/audit/manager.go` | 修改 | 删除 `compactionLoop` 启动逻辑 |
| `common/audit/config.go` | 修改 | 删除 `CompactionEnabled` 字段 |
| `main.go` | 修改 | 新增 compaction 定时调度，受 `ENABLE_VIDEO_TASK_POLLER` 守卫 |

---

### Task 1: 导出 RunCompaction 并删除 compactionLoop

**Files:**
- Modify: `common/audit/compaction.go`

**Step 1: 重写 compaction.go**

删除 `compactionLoop`（内部定时器），将 `runCompaction` 改名为 `RunCompaction` 并导出。删除 `compactionLoop` 里的 `recover/ticker` 逻辑——外部调度负责频率和异常恢复。

```go
package audit

import (
	"context"
	"fmt"

	"github.com/songquanpeng/one-api/common/logger"
)

// RunCompaction 执行一次 Iceberg BIN_PACK compaction。
// 由外部定时调度调用，不再内置定时器。
func RunCompaction(ctx context.Context) {
	if awsClient == nil {
		return
	}
	tableRef := fmt.Sprintf(`"%s"."%s"`, pkgConfig.AthenaDatabase, pkgConfig.AthenaTable)
	sql := fmt.Sprintf("OPTIMIZE %s REWRITE DATA USING BIN_PACK", tableRef)

	_, err := awsClient.executeQuery(ctx, sql)
	if err != nil {
		logger.SysError("audit: compaction failed: " + err.Error())
		return
	}
	logger.SysLog("audit: compaction completed")
}
```

**Step 2: 编译验证**

Run: `cd /Users/yueqingli/code/one-api && go build ./...`
Expected: 编译失败——`manager.go` 仍引用 `compactionLoop`

---

### Task 2: 从 manager.go 删除 compactionLoop 启动逻辑

**Files:**
- Modify: `common/audit/manager.go:84-87`

**Step 1: 删除 compaction 相关代码**

将 `manager.go` 中这段删除：

```go
		if cfg.CompactionEnabled {
			go compactionLoop(bgCtx)
		}
```

**Step 2: 编译验证**

Run: `cd /Users/yueqingli/code/one-api && go build ./...`
Expected: 编译失败——`config.go` 中 `CompactionEnabled` 未使用（或可能不报错因为 struct 字段不强制使用）

---

### Task 3: 从 config.go 删除 CompactionEnabled

**Files:**
- Modify: `common/audit/config.go`

**Step 1: 删除 struct 字段和赋值**

从 `config` struct 中删除 `CompactionEnabled bool`，从 `loadConfig` 中删除 `CompactionEnabled: env.Bool("AUDIT_COMPACTION_ENABLED", false),`。

**Step 2: 编译验证**

Run: `cd /Users/yueqingli/code/one-api && go build ./... && go vet ./...`
Expected: PASS

**Step 3: Commit**

```bash
git add common/audit/compaction.go common/audit/manager.go common/audit/config.go
git commit -m "refactor(audit): 导出 RunCompaction 并删除内部定时器

将 compaction 从 audit 模块内部 goroutine 剥离，
为外部调度做准备。"
```

---

### Task 4: 在 main.go 新增外部 compaction 调度

**Files:**
- Modify: `main.go`

**Step 1: 提取公共的 poller 开关判断**

项目中 `isAliWanTaskPollerEnabled` / `isDoubaoVideoPollerEnabled` / `isFluxReconcilerEnabled` 等全都是读 `ENABLE_VIDEO_TASK_POLLER` 环境变量，逻辑完全一样。在 `main.go` 中直接内联判断即可，不需要额外引入依赖：

在 `main.go` 现有的 poller 启动区域之后（约 L192 `StartXaiVideoTaskPoller` 之后），添加：

```go
	// 启动 Iceberg 审计表 compaction 定时任务（复用 poller 开关保证单机执行）
	if isVideoTaskPollerEnabled() {
		common.SafeGoroutine(func() {
			startAuditCompaction(context.Background())
		})
	}
```

在 `main.go` 底部（`monitorGoroutines` 函数附近）添加两个辅助函数：

```go
func isVideoTaskPollerEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("ENABLE_VIDEO_TASK_POLLER")))
	return v == "true" || v == "1"
}

func startAuditCompaction(ctx context.Context) {
	if !audit.Enabled() {
		return
	}
	const interval = 24 * time.Hour
	logger.SysLog(fmt.Sprintf("audit: compaction scheduler started, interval=%v", interval))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			audit.RunCompaction(ctx)
		}
	}
}
```

**Step 2: 确保 import 包含 `strings` 和 `os`**

检查 `main.go` 的 import，如果缺少 `strings` 和 `os` 则补上。

**Step 3: 编译验证**

Run: `cd /Users/yueqingli/code/one-api && go build ./... && go vet ./...`
Expected: PASS

**Step 4: Commit**

```bash
git add main.go
git commit -m "feat(audit): compaction 改为外部调度，受 ENABLE_VIDEO_TASK_POLLER 守卫

保证 compaction 只在开启 poller 的单台机器上执行，
避免多实例重复触发和应用重启导致的计时漂移。"
```

---

### Task 5: 更新 CHANGELOG

**Files:**
- Modify: `docs/CHANGELOG.md`

添加记录：

```markdown
## 2026-06-23

### refactor(audit): compaction 从进程内定时器改为外部调度
- **分支**: `AthenaQuery`
- **类型**: 重构
- **涉及文件**: `common/audit/compaction.go`, `common/audit/manager.go`, `common/audit/config.go`, `main.go`
- **说明**: 将 Iceberg BIN_PACK compaction 从 audit 模块内部 goroutine 剥离，改为在 main.go 中由 `ENABLE_VIDEO_TASK_POLLER` 环境变量守卫的独立定时任务。保证多实例部署时只在一台机器上执行，消除 `AUDIT_COMPACTION_ENABLED` 配置项。
- **关联计划**: `docs/plans/2026-06-23-audit-compaction-externalize.md`
```
