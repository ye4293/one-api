# 渠道级自动禁用改按「最近使用模型集」判定

- 日期：2026-08-21
- 分支：`feat/model-level-disable-dynamic-priority`（或新起 `feat/channel-disable-by-recent-usage`）
- 关联计划：
  - `docs/plans/2026-08-20-model-scope-auto-disable.md`（现行模型级自动禁用）
  - `docs/plans/2026-08-20-unified-recovery.md`（现行统一恢复链路）

## 背景与目标

### 现状
`model/ability.go` 的 `AutoDisableModelOnChannel` 在禁完单模型后同步统计：

```go
Where("channel_id = ? AND enabled = ?", channelId, true).Count(&remaining)
channelDisabled = remaining == 0
```

分母是「渠道 abilities 表中所有 enabled=true 的行」——本质是「渠道配置的全部模型（跨 group 展平）」。`monitor.DisableChannelWithStatusCode` 拿到 `channelDisabled=true` 才禁整个渠道。

### 痛点
渠道一般会配几十个模型，但实际有流量的往往只是其中一小部分。当高频模型（几个）都被自动禁用时，剩下几十个低频/未使用模型仍是 enabled=true，`remaining > 0` 永远达不到禁渠道条件——**用户观感是「渠道已经不能用了，但系统还在继续把请求分派过去」**。

### 目标
把「是否禁渠道」的判定分母从「配置模型总数」改为「**最近 1 天内被真实调用过的模型集**」，只要这个集合里的模型**全部**被自动禁用，就禁整个渠道。

**分母定义要点**：
- 数据源：`model_metrics` 表（按小时聚合，有 `idx_mm_channel_hour` 复合索引）
- 时间窗：最近 1 天（低频模型误伤可接受，业务已确认）
- 过滤：`total_requests > 0`

**分子定义要点**：
- 该 (channel, model) 在 `abilities` 中 `auto_disabled=true`
- **抖动窗口**：`auto_disabled_time` 距今 ≥ `2 × AutoTestChannelFrequency`（分钟）——给恢复探针至少 2 个完整周期尝试

**触发时机**：不再在「禁模型」同步链路里判定；改由「统一恢复链路」`recoverAutoDisabledModels()` 每轮探测跑完之后，对本轮涉及的渠道各调用一次判定。

## 方案设计

### 涉及文件

| 文件 | 改动 |
|---|---|
| `model/ability.go` | 移除 `AutoDisableModelOnChannel` 内的 `remaining==0` 判定；返回值语义变为「本次是否有更新」，不再驱动禁渠道 |
| `model/ability.go`（或新文件 `model/channel_disable_by_usage.go`） | 新增 `ShouldDisableChannelByRecentUsage(channelId int) (should bool, usedModels int, disabledModels int, err error)` |
| `monitor/channel.go` | `DisableChannelWithStatusCode` 里去掉「拿 channelDisabled 就禁渠道」的分支；模型级失败只做模型级禁用 |
| `controller/channel-test.go` | `recoverAutoDisabledModels()` 结尾追加：对本轮涉及的 `channel_id` 去重后逐个调用 `ShouldDisableChannelByRecentUsage`，命中则调用统一的「禁渠道 + 发通知」函数 |
| `monitor/channel.go`（或从 `disableChannelInternalWithStatusCode` 里抽公用函数） | 暴露一个「因『最近使用模型全禁』而禁渠道 + 发告警」的入口，供恢复链路调用 |
| `model/ability_test.go` | 更新 `TestAutoDisableModelOnChannel` 的期望值（channelDisabled 不再由该函数返回） |
| （新增）测试文件 | 覆盖 `ShouldDisableChannelByRecentUsage` 的 SQL 逻辑 |

### 判定函数核心 SQL

```sql
SELECT
  COUNT(DISTINCT mm.model_name)                                        AS used_models,
  COUNT(DISTINCT CASE
    WHEN a.auto_disabled = 1 AND a.auto_disabled_time <= ?
    THEN mm.model_name END)                                            AS disabled_models
FROM model_metrics mm
LEFT JOIN abilities a
       ON a.channel_id = mm.channel_id
      AND a.model = mm.model_name
WHERE mm.channel_id = ?
  AND mm.hour_timestamp >= ?
  AND mm.total_requests > 0;
```

- 参数 1：`disableCutoff = now - 2*AutoTestChannelFrequency*60`（抖动窗口）
- 参数 2：`channelId`
- 参数 3：`windowStart = now - 86400`（1 天窗口）

**判定条件**：`used_models > 0 AND used_models == disabled_models`

**代价估算**：`model_metrics` 走 `idx_mm_channel_hour` 复合索引扫 ~24 行 × 模型数，abilities 走主键 JOIN，单次亚毫秒 ~ 个位毫秒。

### 常量/配置

**硬编码**（不做成配置项）：
- 时间窗 1 天
- 抖动窗口倍数 `2 × AutoTestChannelFrequency`

理由：现阶段还没有生产数据支撑更细的调参需求，先用简单默认值，需要时再抽配置。

### 触发时机与并发

在 `recoverAutoDisabledModels()` 收尾处（`channel-test.go:702` log 之后）：

1. 从本轮 `items` 收集 `channel_id` 去重集合
2. 对每个渠道调用 `ShouldDisableChannelByRecentUsage`
3. 命中则调 `AutoDisableChannelById + 发通知`（复用现有函数或抽公用入口）

**并发安全**：判定与禁渠道走 `getChannelStatusLock(channelId)` 同一把锁，与 `AutoDisableModelOnChannel` 串行，避免竞态。

**去重**：`items` 里同一 channel 可能出现多次（多模型）。用 map 去重后调用。

## 影响范围

### 用户可见行为变化
- 模型全部失效到渠道被禁之间会**多出 10 ± 分钟延迟**（等 2 个探针周期）。这是刻意设计的抖动缓冲。
- 相应地，「配了 60 个模型只用 5 个」这类渠道，从此能正确禁掉。

### 通知与告警
- 现在的告警是在 `disableChannelInternalWithStatusCode` 里发的，含 statusCode 与失败模型信息。
- 新路径下禁渠道的时机与失败请求解耦，需要构造一个通知内容（reason=「最近使用的 N 个模型全部自动禁用」），可以复用现有通知函数的骨架，statusCode 传 0。

### 数据迁移
无需 schema 变更。

### 兼容性
- `AutoDisableModelOnChannel` 的返回值签名变化——需要 grep 所有调用点确认没有除 monitor 之外的依赖。已确认只在 `monitor/channel.go:143` 和测试里被调用。
- 测试用例 `TestAutoDisableModelOnChannel` 里对 `channelDisabled` 的断言需要更新。

## 验证方式

### 单元测试
1. `ShouldDisableChannelByRecentUsage` 覆盖场景：
   - 无 model_metrics 记录 → `used_models=0`，返回 `should=false`（不禁空转渠道）
   - 有流量但没被禁 → `should=false`
   - 有流量全被禁但都在抖动窗口内 → `should=false`
   - 有流量全被禁且超过抖动窗口 → `should=true`
   - 部分被禁 → `should=false`
2. `AutoDisableModelOnChannel` 更新后：确认不再返回 `channelDisabled=true`，或返回但语义调整。

### 手动验证
1. 本地起 sqlite / mysql，构造一个渠道 configure 30 个模型
2. 只对 3 个模型 mock 流量（写 model_metrics 记录）
3. 触发 3 个模型自动禁用
4. 等待 > `2 × AutoTestChannelFrequency` 分钟
5. 手动跑一次 `recoverAutoDisabledModels()`（或等 tick）
6. 断言渠道被禁 + 通知发出

### 集成回归
- 确认恢复链路仍能把「因该逻辑被禁的渠道」拉回来（`GetAutoDisabledAbilities` 覆盖 status=auto_disabled，探针测通任一模型 → `EnableModelOnChannel` 提升状态）
- 手动禁用渠道（`manually_disabled`）不受影响
- 多 key 渠道现在就被 `MultiKeyInfo.IsMultiKey` 挡在外面，不进入新判定路径——保持

### 提交前校验
```bash
go build ./... && go vet ./...
go test ./model/... ./monitor/... ./controller/...
```

## 分步实施顺序

1. 新增 `ShouldDisableChannelByRecentUsage` + 单测
2. 抽公用「禁渠道 + 通知」函数（如需要）
3. 修改 `AutoDisableModelOnChannel` 移除 `remaining==0` 判定
4. 修改 `monitor.DisableChannelWithStatusCode` 移除 channelDisabled 分支
5. 修改 `recoverAutoDisabledModels()` 追加渠道级判定
6. 更新 `ability_test.go`
7. 手动验证 + 通/单测
8. 更新 `docs/CHANGELOG.md`
