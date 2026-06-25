# 审计配置迁移到 Options 表

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 将审计模块的全部环境变量配置迁移到数据库 `options` 表，与项目中 SMTP、OAuth、Stripe 等配置保持一致的管理方式。

**Architecture:** 在 `common/config/config.go` 中声明审计配置变量（以环境变量为初始默认值），通过 `InitOptionMap` / `updateOptionMap` / `loadOptionsFromDatabase` 三件套接入 options 表生命周期。审计模块的 `loadConfig()` 改为从 `config.*` 变量读取而非直接读环境变量。启动顺序调整为 `InitOptionMap()` → `audit.Start()`，确保 DB 值已加载。

**Tech Stack:** Go, GORM options 表, 现有 config 同步机制

---

## 改动范围总览

| 文件 | 改动类型 | 说明 |
|------|----------|------|
| `common/config/config.go` | 新增 | 添加 20 个审计配置变量 |
| `model/option.go` | 修改 | `InitOptionMap` 注册审计 key；`updateOptionMap` 处理审计 key 同步 |
| `common/audit/config.go` | 修改 | `loadConfig()` 从 `config.*` 读取而非 `env.*` |
| `controller/option.go` | 修改 | 扩展敏感值过滤规则；添加 `AuditEnabled` 启用验证 |
| `main.go` | 修改 | 调整启动顺序：`InitOptionMap()` 在 `audit.Start()` 之前 |

---

### Task 1: 在 config 包声明审计配置变量

**Files:**
- Modify: `common/config/config.go`

**Step 1: 添加审计配置变量**

在 `config.go` 的 `ClaudeRequestHeaders` 声明之后、`ServiceName` 之前，添加：

```go
// 审计模块配置（环境变量为初始默认值，运行时从 options 表覆盖）
var AuditEnabled = env.Bool("AUDIT_ENABLED", false)
var AuditAWSRegion = env.String("AUDIT_AWS_REGION", "")
var AuditAWSAccessKey = env.String("AUDIT_AWS_ACCESS_KEY", "")
var AuditAWSSecretKey = env.String("AUDIT_AWS_SECRET_KEY", "")
var AuditFirehoseStream = env.String("AUDIT_FIREHOSE_STREAM", "")
var AuditAthenaDatabase = env.String("AUDIT_ATHENA_DATABASE", "audit")
var AuditAthenaTable = env.String("AUDIT_ATHENA_TABLE", "request_logs")
var AuditAthenaWorkgroup = env.String("AUDIT_ATHENA_WORKGROUP", "primary")
var AuditS3OutputLocation = env.String("AUDIT_S3_OUTPUT_LOCATION", "")
var AuditS3DataLocation = env.String("AUDIT_S3_DATA_LOCATION", "")
var AuditChannelSize = env.Int("AUDIT_CHANNEL_SIZE", 2000)
var AuditMaxBufferMB = env.Int("AUDIT_MAX_BUFFER_MB", 1024)
var AuditDiskBufferDir = env.String("AUDIT_DISK_BUFFER_DIR", "./data/audit_spill")
var AuditDiskBufferMaxGB = env.Int("AUDIT_DISK_BUFFER_MAX_GB", 40)
var AuditBatchSize = env.Int("AUDIT_BATCH_SIZE", 500)
var AuditFlushIntervalSec = env.Int("AUDIT_FLUSH_INTERVAL_SEC", 10)
var AuditMaxBodyKB = env.Int("AUDIT_MAX_BODY_KB", 10240)
var AuditMaxRespKB = env.Int("AUDIT_MAX_RESP_KB", 4096)
var AuditRetentionDays = env.Int("AUDIT_RETENTION_DAYS", 0)
var AuditRedactHeaders = env.String("AUDIT_REDACT_HEADERS", "Authorization,Api-Key,X-Api-Key,Cookie,Set-Cookie")
```

**Step 2: 编译检查**

Run: `go build ./...`
Expected: PASS

**Step 3: Commit**

```bash
git add common/config/config.go
git commit -m "feat(audit): 在 config 包声明审计配置变量"
```

---

### Task 2: 注册审计 key 到 InitOptionMap 和 updateOptionMap

**Files:**
- Modify: `model/option.go`

**Step 1: 在 `InitOptionMap()` 末尾（`config.OptionMapRWMutex.Unlock()` 之前）添加审计 key 注册**

```go
// 审计模块配置
config.OptionMap["AuditEnabled"] = strconv.FormatBool(config.AuditEnabled)
config.OptionMap["AuditAWSRegion"] = config.AuditAWSRegion
config.OptionMap["AuditAWSAccessKey"] = ""
config.OptionMap["AuditAWSSecretKey"] = ""
config.OptionMap["AuditAWSAccessKeyConfigured"] = strconv.FormatBool(config.AuditAWSAccessKey != "")
config.OptionMap["AuditAWSSecretKeyConfigured"] = strconv.FormatBool(config.AuditAWSSecretKey != "")
config.OptionMap["AuditFirehoseStream"] = config.AuditFirehoseStream
config.OptionMap["AuditAthenaDatabase"] = config.AuditAthenaDatabase
config.OptionMap["AuditAthenaTable"] = config.AuditAthenaTable
config.OptionMap["AuditAthenaWorkgroup"] = config.AuditAthenaWorkgroup
config.OptionMap["AuditS3OutputLocation"] = config.AuditS3OutputLocation
config.OptionMap["AuditS3DataLocation"] = config.AuditS3DataLocation
config.OptionMap["AuditChannelSize"] = strconv.Itoa(config.AuditChannelSize)
config.OptionMap["AuditMaxBufferMB"] = strconv.Itoa(config.AuditMaxBufferMB)
config.OptionMap["AuditDiskBufferDir"] = config.AuditDiskBufferDir
config.OptionMap["AuditDiskBufferMaxGB"] = strconv.Itoa(config.AuditDiskBufferMaxGB)
config.OptionMap["AuditBatchSize"] = strconv.Itoa(config.AuditBatchSize)
config.OptionMap["AuditFlushIntervalSec"] = strconv.Itoa(config.AuditFlushIntervalSec)
config.OptionMap["AuditMaxBodyKB"] = strconv.Itoa(config.AuditMaxBodyKB)
config.OptionMap["AuditMaxRespKB"] = strconv.Itoa(config.AuditMaxRespKB)
config.OptionMap["AuditRetentionDays"] = strconv.Itoa(config.AuditRetentionDays)
config.OptionMap["AuditRedactHeaders"] = config.AuditRedactHeaders
```

注意：`AuditAWSAccessKey` 和 `AuditAWSSecretKey` 在 OptionMap 中设为空字符串（不暴露真实值），同时用 `Configured` 伴生 bool 供前端判断是否已配置。与 `EpayKey` 模式一致。

**Step 2: 在 `updateOptionMap()` 的 switch 中添加审计 key 的同步逻辑**

在最后一个 `case "ChannelAffinityConfig":` 块之后添加：

```go
// 审计模块配置
case "AuditAWSRegion":
    config.AuditAWSRegion = value
case "AuditAWSAccessKey":
    config.AuditAWSAccessKey = value
    config.OptionMap["AuditAWSAccessKeyConfigured"] = strconv.FormatBool(value != "")
case "AuditAWSSecretKey":
    config.AuditAWSSecretKey = value
    config.OptionMap["AuditAWSSecretKeyConfigured"] = strconv.FormatBool(value != "")
case "AuditFirehoseStream":
    config.AuditFirehoseStream = value
case "AuditAthenaDatabase":
    config.AuditAthenaDatabase = value
case "AuditAthenaTable":
    config.AuditAthenaTable = value
case "AuditAthenaWorkgroup":
    config.AuditAthenaWorkgroup = value
case "AuditS3OutputLocation":
    config.AuditS3OutputLocation = value
case "AuditS3DataLocation":
    config.AuditS3DataLocation = value
case "AuditChannelSize":
    config.AuditChannelSize, _ = strconv.Atoi(value)
case "AuditMaxBufferMB":
    config.AuditMaxBufferMB, _ = strconv.Atoi(value)
case "AuditDiskBufferDir":
    config.AuditDiskBufferDir = value
case "AuditDiskBufferMaxGB":
    config.AuditDiskBufferMaxGB, _ = strconv.Atoi(value)
case "AuditBatchSize":
    config.AuditBatchSize, _ = strconv.Atoi(value)
case "AuditFlushIntervalSec":
    config.AuditFlushIntervalSec, _ = strconv.Atoi(value)
case "AuditMaxBodyKB":
    config.AuditMaxBodyKB, _ = strconv.Atoi(value)
case "AuditMaxRespKB":
    config.AuditMaxRespKB, _ = strconv.Atoi(value)
case "AuditRetentionDays":
    config.AuditRetentionDays, _ = strconv.Atoi(value)
case "AuditRedactHeaders":
    config.AuditRedactHeaders = value
```

同时，`AuditEnabled` 需要在 `strings.HasSuffix(key, "Enabled")` 分支的 switch 中加一个 case：

```go
case "AuditEnabled":
    config.AuditEnabled = boolValue
```

**Step 3: 编译检查**

Run: `go build ./...`
Expected: PASS

**Step 4: Commit**

```bash
git add model/option.go
git commit -m "feat(audit): 将审计 key 注册到 InitOptionMap 和 updateOptionMap"
```

---

### Task 3: 修改审计模块 loadConfig 从 config 包读取

**Files:**
- Modify: `common/audit/config.go`

**Step 1: 修改 `loadConfig()` 函数**

将所有 `env.*` 调用替换为从 `config.*` 变量读取。需要新增 import `config` 包，删除 `env` 包的 import。

```go
package audit

import (
	"regexp"
	"strings"
	"time"

	"github.com/songquanpeng/one-api/common/config"
)

var reIdentifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,127}$`)

type auditConfig struct {
	Enabled         bool
	AWSRegion       string
	AWSAccessKey    string
	AWSSecretKey    string
	FirehoseStream  string
	AthenaDatabase  string
	AthenaTable     string
	AthenaWorkgroup string
	S3OutputLocation string
	S3DataLocation  string
	ChannelSize     int
	MaxBufferMB     int
	DiskBufferDir   string
	DiskBufferMaxGB int
	BatchSize       int
	FlushInterval   time.Duration
	MaxBodyKB       int
	MaxRespKB       int
	RetentionDays   int
	redactSet       map[string]struct{}
}

func loadConfig() *auditConfig {
	c := &auditConfig{
		Enabled:         config.AuditEnabled,
		AWSRegion:       config.AuditAWSRegion,
		AWSAccessKey:    config.AuditAWSAccessKey,
		AWSSecretKey:    config.AuditAWSSecretKey,
		FirehoseStream:  config.AuditFirehoseStream,
		AthenaDatabase:  config.AuditAthenaDatabase,
		AthenaTable:     config.AuditAthenaTable,
		AthenaWorkgroup: config.AuditAthenaWorkgroup,
		S3OutputLocation: config.AuditS3OutputLocation,
		S3DataLocation:  config.AuditS3DataLocation,
		ChannelSize:     config.AuditChannelSize,
		MaxBufferMB:     config.AuditMaxBufferMB,
		DiskBufferDir:   config.AuditDiskBufferDir,
		DiskBufferMaxGB: config.AuditDiskBufferMaxGB,
		BatchSize:       config.AuditBatchSize,
		FlushInterval:   time.Duration(config.AuditFlushIntervalSec) * time.Second,
		MaxBodyKB:       config.AuditMaxBodyKB,
		MaxRespKB:       config.AuditMaxRespKB,
		RetentionDays:   config.AuditRetentionDays,
	}
	c.redactSet = make(map[string]struct{})
	for _, h := range strings.Split(config.AuditRedactHeaders, ",") {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			c.redactSet[h] = struct{}{}
		}
	}
	if c.Enabled {
		if !reIdentifier.MatchString(c.AthenaDatabase) {
			c.Enabled = false
		}
		if !reIdentifier.MatchString(c.AthenaTable) {
			c.Enabled = false
		}
	}
	return c
}
```

注意：结构体名从 `config` 改为 `auditConfig`，避免与导入的 `config` 包名冲突。

**Step 2: 更新 audit 包内所有引用 `*config` 类型的地方**

audit 包内部其他文件（manager.go、worker.go、query.go、compaction.go 等）引用了 `*config` 类型，需要全部替换为 `*auditConfig`。

用 grep 确认引用点：
```bash
grep -rn '\*config' common/audit/ --include='*.go'
```

逐文件替换 `*config` → `*auditConfig`（仅限类型引用，不影响 import 的 config 包）。

**Step 3: 编译检查**

Run: `go build ./...`
Expected: PASS

**Step 4: Commit**

```bash
git add common/audit/
git commit -m "refactor(audit): loadConfig 从 config 包变量读取而非环境变量"
```

---

### Task 4: 修复敏感值过滤规则

**Files:**
- Modify: `controller/option.go`

**Step 1: 扩展 GetOptions 中的敏感值过滤**

将：
```go
if strings.HasSuffix(k, "Token") || strings.HasSuffix(k, "Secret") {
    continue
}
```

改为：
```go
if strings.HasSuffix(k, "Token") || strings.HasSuffix(k, "Secret") ||
    strings.HasSuffix(k, "SecretKey") || strings.HasSuffix(k, "AccessKey") {
    continue
}
```

这同时修复了 `CfFileSecretKey`、`CfImageSecretKey` 泄露的既有 bug。

**Step 2: 添加 AuditEnabled 启用校验（可选）**

在 `validateOptionUpdate` 中添加：
```go
case "AuditEnabled":
    if option.Value == "true" && (config.AuditAWSAccessKey == "" || config.AuditAWSSecretKey == "" || config.AuditAWSRegion == "" || config.AuditFirehoseStream == "") {
        return "无法启用审计模块，请先填入 AWS 凭证、Region 和 Firehose Stream 名称！"
    }
```

**Step 3: 编译检查**

Run: `go build ./...`
Expected: PASS

**Step 4: Commit**

```bash
git add controller/option.go
git commit -m "fix(security): 扩展敏感值过滤规则，覆盖 SecretKey/AccessKey 后缀"
```

---

### Task 5: 调整启动顺序

**Files:**
- Modify: `main.go`

**Step 1: 将 `audit.Start(context.Background())` 移到 `model.InitOptionMap()` 之后**

原代码（第 126-130 行）：
```go
// 启动审计模块
audit.Start(context.Background())
defer audit.Shutdown()

// Initialize options
model.InitOptionMap()
```

改为：
```go
// Initialize options（必须在 audit.Start 之前，审计配置从 options 表读取）
model.InitOptionMap()

// 启动审计模块（依赖 options 表中的配置）
audit.Start(context.Background())
defer audit.Shutdown()
```

**Step 2: 编译检查**

Run: `go build ./... && go vet ./...`
Expected: PASS

**Step 3: Commit**

```bash
git add main.go
git commit -m "refactor(audit): 调整启动顺序，InitOptionMap 在 audit.Start 之前"
```

---

### Task 6: 验证与测试

**Step 1: 完整编译 + 静态分析**

```bash
go build ./... && go vet ./...
```

**Step 2: 运行现有测试**

```bash
go test ./common/audit/... -v -count=1
```

**Step 3: 功能验证清单**

- [ ] 不设任何环境变量 → 审计模块关闭（`Enabled()` = false）
- [ ] 通过 admin API `PUT /api/option` 设置 `AuditEnabled=true` + 所有必填项 → 重启后审计生效
- [ ] `GET /api/option` 返回中不包含 `AuditAWSAccessKey`、`AuditAWSSecretKey` 的真实值
- [ ] 返回中包含 `AuditAWSAccessKeyConfigured`、`AuditAWSSecretKeyConfigured` 布尔值
- [ ] 环境变量设置 `AUDIT_AWS_REGION=us-east-1`，DB 中设置 `AuditAWSRegion=us-west-2` → 生效值为 `us-west-2`（DB 优先）
- [ ] 多节点场景：节点 A 通过 API 更新配置，节点 B 的 SyncOptions 周期后 config 变量同步（注意：审计模块需重启才真正生效，config 变量的更新仅为下次启动做准备）

---

## 设计决策记录

### 为什么不做运行时热重载？

审计模块启动时创建 AWS client、Glue 资源、goroutine 和 channel。运行时更换配置需要优雅关闭并重建所有资源，复杂度极高且容易丢数据。当前方案：**配置通过 DB 管理，但改配置后需重启生效**，与 Stripe、SMTP 等模块的行为一致。

### 为什么 AWS 凭证也存 DB？

用户明确要求。项目中已有 Stripe API Secret、SMTP Token 等敏感凭证存 DB 的先例。通过 `GetOptions` 的后缀过滤 + 伴生 `Configured` bool 确保 API 不泄露真实值。

### 环境变量的角色变化

环境变量从"唯一配置来源"降级为"初始种子值"。首次启动（DB 中无记录）时使用环境变量的值；管理员通过 UI 保存后，DB 值在后续启动中优先生效。这保持了容器化部署的兼容性。
