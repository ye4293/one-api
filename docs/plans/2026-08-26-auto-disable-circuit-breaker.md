# 自动禁用熔断锁死：24h 内 3 次整渠道禁用 → auto_enabled=false

## Context

当前自动禁用体系有两条粒度不同的路径共存：

- **模型级**：`controller/relay.go:811` 业务请求上游报错 → `DisableModelOnChannelWithStatusCode` → `AutoDisableModelOnChannel`，只禁 `(channel, model)`。升级到整渠道禁用由恢复探针收尾判定 `ShouldDisableChannelByRecentUsage`（"最近使用中的模型全部被禁 + 抖动窗口过后"）触发 `DisableChannelByRecentUsage`。
- **整渠道**：测试超时、账单检查失败、Recraft 认证失败等非业务路径直接走 `DisableChannelSafelyWithStatusCode`。

两条路径最终都汇聚到 `model.AutoDisableChannelById`。

**痛点**：
1. 整渠道被禁后 → 恢复探针救起 → 立刻又挂 → 又救 → 反复循环。运维疲惫，上游被反复施压。
2. 缺乏"熔断上限"。持续故障的渠道（key 失效、配额耗尽、上游长期不可用）没有终止条件。

**目标**：在保留现有模型级隔离（不动它）的前提下，对**升级到整渠道自动禁用**这个事件引入熔断计数：24h 内一个渠道被整渠道自动禁用超过 3 次就锁死（`auto_enabled=false`）；前 2 次通过指数退避（15/30/60 min）降低救火频率；管理员手动启用后计数清零，但 `auto_enabled` 保持——需要显式打开才能重新参与自动救援。

---

## 决策汇总

| 维度 | 选择 | 备注 |
|---|---|---|
| 计数窗口 | 滚动 24h | 日常抖动被消化，持续故障集中撞阈值 |
| 锁死表达 | `channels.auto_enabled=false` | 复用 `recoverAutoDisabledModels` 已有的跳过分支，零改动兼容 |
| 计数存储 | MySQL `channels` 表新字段 | GORM AutoMigrate 已启用，改 struct 即可 |
| 计数触发点 | 只有升级到"整渠道自动禁用"才 +1 | 模型级禁用不计数，保留隔离价值 |
| 阈值 | 3 次 | |
| 探测退避 | 15/30/60 min（index by count-1） | 第 3 次因 `auto_enabled=false` 已跳过，60min 是防御性上限 |
| 手动启用 | 清零 count + window_start，不动 `auto_enabled` | 防止手滑救活应该拉黑的渠道 |
| 恢复探针恢复模型时清零？ | 否 | 模型级恢复不代表渠道健康，让 24h 自然过窗口 |

---

## 方案

### 1. Schema（`model/channel.go` Channel struct）

```go
AutoDisableCount        int   `json:"auto_disable_count" gorm:"default:0"`
AutoDisableWindowStart  int64 `json:"auto_disable_window_start" gorm:"bigint;default:0"`
```

GORM AutoMigrate 自动 DDL（参见 `model/main.go:117`）。新字段 default 0，历史数据启动即可用；window_start=0 保证第一次触发时（now - 0 > 86400）走"新窗口"分支。

### 2. 常量（`common/constants.go`）

```go
const (
    ChannelAutoDisableCircuitThreshold     = 3
    ChannelAutoDisableCircuitWindowSeconds = 86400
)

var ChannelAutoDisableProbeBackoff = []time.Duration{
    15 * time.Minute,
    30 * time.Minute,
    60 * time.Minute,
}
```

### 3. 计数写入点：`model/channel.go:777-826` `AutoDisableChannelById`

**这是所有整渠道自动禁用的最终收敛点**——4 处 monitor 调用最终都进这里。逻辑放这里，一处覆盖。

在原有事务内改造：

- SELECT 追加 `auto_disable_count`, `auto_disable_window_start`, `auto_enabled`。
- 在事务内 CPU 判定：
  ```go
  now := time.Now().Unix()
  newCount := current.AutoDisableCount + 1
  newWindowStart := current.AutoDisableWindowStart
  if now-current.AutoDisableWindowStart > ChannelAutoDisableCircuitWindowSeconds {
      newCount = 1
      newWindowStart = now
  }
  newAutoEnabled := current.AutoEnabled
  if newCount >= ChannelAutoDisableCircuitThreshold {
      newAutoEnabled = false
  }
  ```
- `updates` map 增加 `auto_disable_count`、`auto_disable_window_start`；`auto_enabled` 若发生变化则走**独立** `Select("auto_enabled").Updates(...)`（复用 `model/channel.go:596-600` 已有模式，避开 bool false 零值坑）。

**幂等性**：复用现状的 `WHERE id=? AND status != ChannelStatusAutoDisabled`，只有 RowsAffected>0 才算"首次禁用"、才 +1。跨进程并发天然幂等。

### 4. 探针退避：`controller/channel-test.go:665-688` `recoverAutoDisabledModels`

在 `!channel.AutoEnabled` 检查之后、`isUnsupportedTestChannel` 检查之前插入：

```go
if channel.AutoDisableCount > 0 && channel.AutoDisabledTime != nil {
    idx := channel.AutoDisableCount - 1
    if idx >= len(common.ChannelAutoDisableProbeBackoff) {
        idx = len(common.ChannelAutoDisableProbeBackoff) - 1
    }
    minWait := int64(common.ChannelAutoDisableProbeBackoff[idx].Seconds())
    if time.Now().Unix()-*channel.AutoDisabledTime < minWait {
        skipped++
        continue
    }
}
```

复用现有 `AutoDisabledTime` 字段，无需新字段。

### 5. 手动启用清零：`model/channel.go:696` `UpdateChannelStatusById` + 854 行 `BatchUpdateChannelStatus`

当 `status == ChannelStatusEnabled`：

```go
updates["auto_disable_count"] = 0
updates["auto_disable_window_start"] = 0
// auto_enabled 保持——锁死后必须显式打开才能重新参与自动救援
```

---

## 影响范围

**改动文件**：
- `model/channel.go`：struct 加 2 字段；`AutoDisableChannelById` 加计数写入；`UpdateChannelStatusById` + `BatchUpdateChannelStatus` 加手动启用清零。
- `controller/channel-test.go`：`recoverAutoDisabledModels` 加退避跳过分支。
- `common/constants.go`：新增 2 常量 + 1 退避表。
- `model/channel_auto_disable_count_test.go`（新建）：单元测试。

**不改动**：
- `monitor/channel.go` 所有 Disable* 函数——它们已收敛到 `AutoDisableChannelById`。
- `controller/relay.go` 模型级禁用路径——保留隔离价值。
- 前端 UI——新字段默认 0，UI 不显示不影响运行。后续可补 issue 加显示。

**兼容性**：
- Multi-key 渠道：`AutoDisableChannelById` 内部前置退出（`IsMultiKey=true` 不进 update 分支），计数天然不触发。
- `AutoDisabled=false` 渠道：同样前置退出。
- 老数据：新字段 default 0，无迁移风险。

---

## 验证

### 编译与静态检查
```bash
go build ./... && go vet ./...
```

### 单元测试（新增 `model/channel_auto_disable_count_test.go`）
参考 `model/channel_disable_by_usage_test.go` 表驱动模式，覆盖：

| 场景 | 输入 | 预期 |
|---|---|---|
| A 首次触发 | count=0, window_start=0 | count=1, window_start=now |
| B 达阈锁死 | count=2, window_start=now-1h | count=3, auto_enabled=false |
| C 窗口过期 | count=3, window_start=now-25h | count=1, window_start=now |
| D 手动启用清零 | UpdateChannelStatusById(1) | count=0, window_start=0, auto_enabled 保持 |
| E AutoDisabled=false | 触发禁用 | count 保持 0（前置退出）|
| F Multi-key | 触发禁用 | count 保持 0（前置退出）|

### 探针退避手工验证
起 sqlite，插入 `AutoDisableCount=1, AutoDisabledTime=now-5min` → `recoverAutoDisabledModels` 日志显示 skipped 且不发真实请求；改 `AutoDisabledTime=now-16min` → 进入探测分支。

### 端到端手工验证
```bash
# 前置：起本地服务，创建 key 故意写错的渠道
for i in 1 2 3 4; do
    curl -X PUT localhost:3000/api/channel/... -d '{"status":1}'   # 手动启用
    sleep 2
    curl localhost:3000/v1/chat/completions ...                    # 触发上游 401
    sleep $((5*60))                                                # 等 recent-usage 判定
done
```
预期：第 3 次触发后 `auto_enabled=false`；第 4 次手动启用后 count=0 但 auto_enabled 仍 false（管理员需在 UI 显式打开）。

### CLAUDE.md 强制
- commit 后写 `docs/CHANGELOG.md` 一段（分支、类型、文件、说明、关联本计划）。
