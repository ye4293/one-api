# 恢复链路统一 · 方案 A（纯测试恢复 + 人工兜底）

## 背景

上一次特性（`2026-08-20-model-scope-auto-disable.md`）落地了模型级自动禁用，但**恢复链路**仍是老的渠道级"测一个洗全部"，与模型级禁用语义不自洽：
1. 渠道级恢复 `testChannels("auto_disabled")` 测通一个模型即 `EnableChannel`，触发 `UpdateChannelStatusById` 的乐观清零——把该渠道**所有** `auto_disabled=true` 的模型一并 `enabled=true`、清 `auto_disabled`。
2. 后果：**永久坏的模型**（如某渠道天生不提供该模型、401/权限类）会被反复洗白 → 又被真实用户请求打挂 → 又模型级禁用。disable/enable 抖动，用户请求当探针。
3. 特别是 Gemini 系限流会命中 `err.Code == "Some resource has been exhausted"` 触发模型级禁用；而 image 模型无法用 `/v1/chat/completions` 探测——洗白后马上再挂。

## 目标（用户已确认的方向）

- **纯测试恢复**：只有真实测试通过才恢复。不引入冷却兜底。
- **合并恢复管道**：删除渠道级"测一个洗全部"，改为模型级恢复统一处理。同时接管 `status=auto_disabled` 的渠道恢复。
- **补人工兜底入口**：不可自动探测的模型（image/embedding/video 等）依赖人工救火，故本次一并加**批量启用被禁模型**的后端 API + 前端按钮。
- **恢复限流**：加上限 + 按 `auto_disabled_time` 排序，避免上量后一轮几百次真实付费请求。

## 核心语义变更

**新语义**：渠道 `status=enabled` 且模型 `auto_disabled=false` 才代表该 ability 可用。恢复只发生在**单个 (channel, model) 粒度**，不再有"整渠道洗白"。

**状态转移**：

| 触发 | 变化 |
|---|---|
| 模型级禁用 | 该 (channel, model) 所有 group 行：`enabled=false, auto_disabled=true, auto_disabled_time=now`。若渠道剩 0 个 enabled ability → 顺带渠道级禁用（沿用） |
| 手动/自动禁用整渠道 | `status=auto_disabled/manually_disabled`，所有行 `enabled=false`，`auto_disabled` 不动（沿用） |
| **手动/自动启用整渠道** | `status=enabled`，只对 `auto_disabled=false` 的行 `enabled=true`。**`auto_disabled=true` 的行保持 `enabled=false`。这是本次的关键变更**（旧版本会乐观清零） |
| 模型级恢复（测试通过） | 该 (channel, model) 行：`enabled=true, auto_disabled=false, time=0`。**若渠道 `status=auto_disabled`，同时把 status 提升回 enabled**。这是本次新增 |
| 人工批量启用被禁模型 | 与模型级恢复相同的写入，绕过测试 |

## 详细改动

### 1. `model/channel.go` — 去掉乐观清零

`UpdateChannelStatusById`（`:721` 附近）和 `BatchUpdateChannelStatus` 在 `status==enabled` 分支：

```diff
-  UPDATE abilities SET enabled=true, auto_disabled=false, auto_disabled_time=0 WHERE channel_id=?
+  UPDATE abilities SET enabled=true WHERE channel_id=? AND auto_disabled=false
```

启用后：`auto_disabled=true` 的行保持 `enabled=false`，等待模型级恢复或人工干预。渠道禁用分支不变（`enabled=false`、保留 `auto_disabled`）。

### 2. `model/ability.go` — 模型级恢复顺带提升渠道 status

`EnableModelOnChannel` 改为事务：先 UPDATE ability，再检查渠道当前状态；若 `status=auto_disabled` 则一并 `status=enabled`。

```go
// EnableModelOnChannel 模型级恢复：清模型禁用标记；若渠道处于 auto_disabled 则一并恢复渠道 status。
// 手动禁用（manually_disabled）的渠道不主动提升 status——尊重人工决策。
func EnableModelOnChannel(channelId int, modelName string) error {
  lock := getChannelStatusLock(channelId)  // 复用现有锁
  lock.Lock()
  defer lock.Unlock()

  return DB.Transaction(func(tx *gorm.DB) error {
    if err := tx.Model(&Ability{}).
        Where("channel_id = ? AND model = ?", channelId, modelName).
        Updates(map[string]interface{}{"enabled": true, "auto_disabled": false, "auto_disabled_time": 0}).Error; err != nil {
      return err
    }
    // 条件 UPDATE：仅 auto_disabled 状态才提升，manually_disabled 不动
    return tx.Model(&Channel{}).
        Where("id = ? AND status = ?", channelId, common.ChannelStatusAutoDisabled).
        Update("status", common.ChannelStatusEnabled).Error
  })
}
```

`GetAutoDisabledAbilities` 放宽：不再要求 `c.status = enabled`，改为 `c.status != manually_disabled`——手动禁用的渠道属于运维决策，不主动自动测试；`auto_disabled` 的渠道允许由模型级恢复接管。

### 3. `controller/channel-test.go` — 删除"渠道级测通洗全部"，收敛到模型级恢复

**删除** `testChannels("auto_disabled")` 走到 `EnableChannel` 那一支的逻辑。保留渠道测试相关的响应时间/失败检测，但**不再自动 enable 整渠道**——把这项能力完全交给 `recoverAutoDisabledModels`。

`recoverAutoDisabledModels` 增强：
- **上限**：每轮最多处理 `min(len(items), 100)` 个 (channel, model)——避免上量后单轮跑爆。
- **排序**：`GetAutoDisabledAbilities` 加 `ORDER BY auto_disabled_time ASC` —— 优先恢复最久的（也是最可能已经自愈的）。
- **快速跳过不可探测模型**：调用 `isUnsupportedTestModel(model)` / `isUnsupportedTestChannel(channelType)` **在发请求前**过滤，不浪费 API 调用。这类模型只能靠人工入口恢复（用户已知情）。
- **保留 `RequestInterval` sleep**：避免瞬间打爆上游。

### 4. 新增后端：批量启用被禁模型 API

**路由**：`POST /api/channel/model_channel_enable`（Admin 权限）

**请求体**：
```json
{ "items": [{ "channel_id": 123, "model": "gemini-3.1-flash-image" }, ...] }
```

**逻辑**：遍历 items，对每项调 `EnableModelOnChannel`（复用上面的事务函数，天然处理 status 提升）。返回 `{ success, affected, failed: [...] }`。

**放在**：`controller/dynamic_priority_view.go`（已有类似的 `UpdateModelChannelPriority`，聚焦模型视图相关 API）。

**路由注册**：`router/api-router.go` 加一行。

### 5. 前端 `ezlinkai-web` — 批量启用入口

- `sections/model/model-channels-table.tsx`：DataTable 加**多选列**（复用 `@tanstack/react-table` 的 selection），选中若干行后表头显示"批量启用（N）"按钮。
- 按钮点击 → 调 `/api/channel/model_channel_enable` → toast 结果 → 刷新列表。
- 只对"模型自动禁用"行启用多选可见性——普通启用/手动禁用行不需要这个动作（防止误操作）。
- 已有的 `status_filter=4`（模型自动禁用）配合此功能：筛选出被禁模型 → 全选 → 一键启用。

## 影响范围

- **不动**：`AutoDisableModelOnChannel`（禁用侧不变）、`CheckDataConsistency` 的新不变式判定（本身正确）、动态优先级计算（无关）。
- **删除**：`testChannels` scope="auto_disabled" 的自动启用整渠道分支——这是**行为破坏性变更**。老的运维习惯（等系统自愈整渠道）会失效，需要在 CHANGELOG 明写。
- **数据迁移**：无，纯逻辑变更。
- **回滚**：`ModelScopeAutoDisableEnabled=false` 仍能回退到旧的整渠道禁用行为，但**渠道级恢复删了就是删了**——回滚要靠 revert commit。这是接受方案 A 的代价。

## 验证方式

1. `go build ./... && go vet ./...` 通过。
2. 新增单测（`model/ability_test.go`）：
   - `EnableModelOnChannel` 在 `channel.status=auto_disabled` 时同时把 status 提升为 enabled。
   - `EnableModelOnChannel` 在 `channel.status=manually_disabled` 时**不**提升 status。
   - `UpdateChannelStatusById(id, enabled)` 不再清 `auto_disabled=true` 行的 enabled=false。
3. 手动/集成：
   - 构造一个渠道两模型，两模型都禁 → 渠道自动禁用 → 触发 `recoverAutoDisabledModels` → 一个模型测通 → 该模型 enabled、渠道 status=enabled、另一个模型仍 disabled。
   - 前端筛 `status_filter=4` → 选几行 → 批量启用 → 检查列表刷新与后端持久化。
   - 不可探测模型（image）测试跳过：日志有 skip 记录，不消耗 API 配额。

## 运维承诺（用户已确认接受）

- **image/embedding/video 等不可 chat 探测的模型**被模型级禁用后**不会自动恢复**，只能通过前端「模型自动禁用」筛选 + 批量启用手工救火。
- Gemini image 模型偶发限流会触发禁用（`Resource has been exhausted` 命中判定），恢复需人工。
- 建议 SRE 建立**周期性巡检**：每天扫一次 `status_filter=4` 结果，评估是否需要批量恢复。
