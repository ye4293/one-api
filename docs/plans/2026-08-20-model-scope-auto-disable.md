# 模型级自动禁用（Model-Scope Auto-Disable）

## 背景与目标

现状：请求失败命中禁用条件（`ShouldDisableChannel`：401、权限类错误、`AutoDisableKeywords` 关键词）时，
`AutoDisableChannelById` 会把**整个渠道** `status=auto_disabled`、该渠道**全部 abilities `enabled=false`**。
后果：某个模型出问题（如上游临时不提供某模型、单模型限流）会连累同渠道其他正常模型全部不可用。

目标：
1. 自动禁用**只禁"该渠道上的该模型"**（abilities 中 `channel_id + model` 的所有 group 行），渠道保持启用，其他模型照常路由。
2. 当某渠道**所有模型都被禁用**时，才禁用整个渠道（沿用现有 `AutoDisableChannelById`）。
3. 提供**模型级自动恢复**：定时测试被禁模型，成功则重新启用该模型。
4. 仅作用于**单 Key 渠道**；多 Key 渠道维持现有 key 级禁用逻辑，不做模型级禁用。

## 关键设计决策（已与使用者确认）

- **恢复机制**：持久落库禁用 + 模型级恢复测试（非内存冷却）。
- **多 Key 渠道**：暂不纳入，只处理单 Key 渠道。
- **UI 手动编辑渠道**：`UpdateAbilities` 走 DELETE+INSERT 重建，会清空模型级禁用状态——视为"管理员主动干预"，可接受，不额外保留。
- **渠道整体恢复/手动启用**：乐观清零该渠道所有模型级禁用标记（`auto_disabled=false`），若仍有坏模型，正常失败路径会再次将其禁用。
- **通知策略**：模型级禁用只记日志（避免告警风暴，频次更高）；渠道级（全模型禁用触发）沿用现有完整通知。
- **灰度开关**：新增 `config.ModelScopeAutoDisableEnabled`（默认 `true`）。关闭时回退到旧的整渠道禁用行为，便于线上快速回滚。

## Schema 变更（abilities 表，GORM AutoMigrate 自动 ADD COLUMN）

在 `model.Ability` 结构体新增两列（均为非主键列，AutoMigrate 会 `ALTER TABLE ADD COLUMN`，符合数据库禁令）：

```go
// 模型级自动禁用标记：区分“因渠道禁用而 enabled=false” vs “因该模型自身故障被自动禁用”
AutoDisabled     bool  `json:"auto_disabled" gorm:"default:false;index"`
AutoDisabledTime int64 `json:"auto_disabled_time" gorm:"default:0"`
```

**不变式（invariant）**：`enabled = (channel.status == enabled) AND (ability.auto_disabled == false)`

存量行 AutoMigrate 后 `auto_disabled=false`、`auto_disabled_time=0`，语义等价于旧行为，无需数据回填。

## 状态转移表

渠道启用状态下，单条 ability：

| 状态 | enabled | auto_disabled |
|---|---|---|
| 正常 | true | false |
| 模型级自动禁用 | false | true |

渠道级转移：
- **模型级禁用触发**：目标模型所有 group 行 → `enabled=false, auto_disabled=true, auto_disabled_time=now`。随后统计该渠道剩余 `enabled=true` 行数，若为 0 → 调 `AutoDisableChannelById` 禁渠道。
- **渠道自动/手动禁用**：全部行 `enabled=false`（`auto_disabled` 不动）。
- **渠道启用/恢复**：全部行 `enabled=true, auto_disabled=false`（乐观清零）。
- **模型级恢复**：目标模型行 `enabled=true, auto_disabled=false`（仅当渠道 `status=enabled`）。

## 改动文件与核心逻辑

### 1. `common/config/config.go`
- 新增 `var ModelScopeAutoDisableEnabled = true`。

### 2. `model/ability.go`
- `Ability` 结构体加 `AutoDisabled`、`AutoDisabledTime` 两列。
- 新增 `AutoDisableModelOnChannel(channelId int, modelName, reason string) (channelDisabled bool, err error)`：
  - 复用 `getChannelStatusLock(channelId)` 保证"禁模型 + 统计剩余"原子。
  - 事务内：①`UPDATE abilities SET enabled=false, auto_disabled=true, auto_disabled_time=? WHERE channel_id=? AND model=?`；
    ②`COUNT abilities WHERE channel_id=? AND enabled=true`，为 0 则 `channelDisabled=true`（由调用方决定是否禁渠道，避免锁重入）。
- 新增 `GetAutoDisabledAbilities() ([]struct{ChannelId int; Model string}, ...)`：
  `SELECT DISTINCT channel_id, model FROM abilities WHERE auto_disabled=true`（供恢复扫描；只取渠道 `status=enabled` 的，JOIN channels 过滤）。
- 新增 `EnableModelOnChannel(channelId int, modelName string) error`：
  `UPDATE abilities SET enabled=true, auto_disabled=false WHERE channel_id=? AND model=?`（仅在渠道 enabled 时调用）。
- **重写 `CheckDataConsistency`**：调和到新不变式，识别三类不一致：
  - channel enabled 且 auto_disabled=false 但 enabled=0 → 置 1
  - channel enabled 且 auto_disabled=true 但 enabled=1 → 置 0
  - channel !enabled 但 enabled=1 → 置 0
  （MySQL/PG 用 UPDATE JOIN，SQLite 用子查询，沿用现有双分支写法。）
- `SyncChannelAbilities`：同样按新不变式设置 enabled（考虑 auto_disabled）。

### 3. `model/channel.go`
- `UpdateChannelStatusById`（:721 处）：
  - `status==enabled` 时：`UPDATE abilities SET enabled=true, auto_disabled=false WHERE channel_id=?`（乐观清零）。
  - 否则：`UPDATE abilities SET enabled=false WHERE channel_id=?`（auto_disabled 不动）。
- 批量状态更新（:859 处 `UpdateChannelStatusByIds`）同上处理。
- `AutoDisableChannelById` 内 `UPDATE abilities enabled=false` 保持不变（全模型已禁时禁渠道，auto_disabled 保留即可）。

### 4. `monitor/channel.go`
- 新增 `DisableModelOnChannelWithStatusCode(channelId int, channelName, modelName, reason string, statusCode int)`：
  - 校验渠道存在、`AutoDisabled` 开、非多 Key。
  - 调 `model.AutoDisableModelOnChannel`；若返回 `channelDisabled=true` → 调现有 `disableChannelInternalWithStatusCode` 禁整渠道并走完整通知；否则只 `logger.SysLog` 记模型级禁用。

### 5. `controller/relay.go`（:795 单 Key 分支）
- 命中 `ShouldDisableChannel` 且 `channel.AutoDisabled` 时：
  - `config.ModelScopeAutoDisableEnabled` 为 true → 调 `monitor.DisableModelOnChannelWithStatusCode`（模型级）。
  - 为 false → 保持旧的 `monitor.DisableChannelWithStatusCode`（整渠道，回滚路径）。
- 多 Key 分支（`processMultiKeyChannelError`）**不改**。

### 6. `controller/channel-test.go`
- 新增 `recoverAutoDisabledModels()`：
  - `model.GetAutoDisabledAbilities()` 拿到 (channelId, model) 列表（仅 enabled 渠道）。
  - 逐个 `testChannel(channel, model, true)`，成功且 `ShouldEnableChannel` → `model.EnableModelOnChannel` + 日志。
  - 受 `AutomaticEnableChannelEnabled` / `channel.AutoEnabled` 约束，与现有渠道恢复一致。
- 在 `AutomaticallyTestChannels` 的周期循环里，渠道级恢复之后追加一次模型级恢复扫描（复用同一 frequency）。

## 影响范围

- **选渠道热路径** `GetRandomSatisfiedChannel` **不受影响**（仍按 `enabled=true` 过滤，模型级禁用天然生效）。
- **数据迁移**：仅 AutoMigrate 加两列，无回填，存量语义不变。
- **前端**：模型视图页可后续增加"模型级禁用"状态展示（本次不做，仅后端）。
- **回滚**：`ModelScopeAutoDisableEnabled=false` 即回旧行为；已被模型级禁用的行在渠道下次整体启用时被清零。

## 验证方式

1. `go build ./... && go vet ./...` 通过。
2. 单测（新增 `model/ability_test.go` 用例）：
   - 禁用单模型后，该渠道其他模型仍 `enabled=true`，渠道 `status=enabled`。
   - 禁用渠道最后一个模型后，`channelDisabled=true` 且渠道被禁。
   - `CheckDataConsistency` 不会把 `auto_disabled=true` 的行错误恢复。
   - `EnableModelOnChannel` 正确清标记。
3. 手动/集成：构造命中关键词的失败请求，确认只有该模型 ability 被禁；等一个恢复周期确认模型自动恢复。

## 待确认

- `ModelScopeAutoDisableEnabled` 默认值定 `true`（直接启用新行为）是否符合预期？若希望灰度，可先默认 `false`。
