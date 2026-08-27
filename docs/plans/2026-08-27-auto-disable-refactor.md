# 自动禁用体系重构：解耦调度 + 僵尸退火 + 观测闭环

## Context

线上排查 channel 80919（AWS Bedrock，命中"security token invalid"关键词）为何未被自动禁用，暴露出**四层叠加问题**，并非单一 bug：

| 层 | 现象 | 根因 |
|---|---|---|
| 1. 调度耦合 | 收尾判定跑不到 | `ShouldDisableChannelByRecentUsage` 挂在 `recoverAutoDisabledModels` 尾部 (`controller/channel-test.go:718-739`)，共享 `AutomaticEnableChannelEnabled` 开关和 `recoverModelsMaxPerRound=100` 硬顶截断 |
| 2. 僵尸失控 | 18,020 个 (channel, model) auto_disabled 候选，恢复探针 top 100 永远处理不完 | 无退避、无死亡机制。恢复无望的条目（token 吊销、账号冻结、上游长期不可用）持续占位并烧上游 API 配额 |
| 3. 语义耦合 | "关自动启用" ≠ "关自动禁用" 却互相依赖 | 历史巧合把两件事捆一个 goroutine——`AutomaticEnableChannelEnabled=false` 时整渠道自动禁用**间接失效** |
| 4. 观测缺失 | 出问题前无告警 | 无 auto_disabled 队列长度、恢复成功率、僵尸比例、"应评估未评估" backlog 指标 |

**实测数据（2026-08-27）**：

- 待恢复候选（去重后）：**18,020** (channel, model) → 89,302 abilities 行（5 个 group 展开）
- 该被整渠道禁但漏禁的渠道：**720 个**
- 僵尸年龄分布：<1h 494 渠道 / 1h-1d 912 / 1-3d 891 / 3-7d 909
- 探针每 10 分钟处理 100 个候选，channel 80919 的候选排在第 15,385+ 位 → 理论 25+ 小时才可能被评估一次，实际因队列前端不断新增而**永不可达**

**目标**：

1. **P1（3-5 天）**：解耦收尾判定，让所有涉及渠道能在一个探针周期内被评估。同步跑历史数据清理脚本，把 720 个漏禁渠道分档禁掉。
2. **P2（1-2 周）**：恢复探针接入指数退避 + 7 天死亡机制，让僵尸队列长期收敛。
3. **P3（1 周）**：观测四指标接入 Prometheus + 飞书告警，避免下次积压到万级才被发现。

---

## 决策汇总

| 维度 | 选择 | 备注 |
|---|---|---|
| P1 触发方式 | 独立 goroutine，周期同 `AutoTestChannelFrequency` | 与恢复探针解耦，不受 100 上限和 `AutomaticEnableChannelEnabled` 影响 |
| P1 判定 SQL 覆盖范围 | `DISTINCT channel_id FROM abilities WHERE auto_disabled=1` | 纯 SQL 无 API 调用，成本亚秒级；不再依赖恢复候选截断结果 |
| P1 复用现有判定函数 | 是（`ShouldDisableChannelByRecentUsage` 完全复用） | 逻辑不变，只改调用点 |
| P2 退避表 | `1min → 5min → 30min → 2h → 6h → 24h` | 指数增长，24h 封顶。第 N 次失败取 `backoff[min(N-1, len-1)]` |
| P2 死亡判定 | `recovery_fail_count >= 10` 或 `auto_disabled_time` 超 7 天 | 两个条件任一命中即置 `is_dead=true` |
| P2 死亡后行为 | 不进恢复队列、不派单、不占位；仅前端"批量启用"或手动 API 可解 | 保留人工救援通路 |
| 历史数据清理 | 分三档，直连 SQL 绕过应用层 | 避免 720 条飞书告警刷屏 + 熔断计数误耗 |
| 历史清理时机 | **P1 上线之前**执行 | 避免 P1 首轮跑一次性禁 720 个 |
| P3 指标存储 | Prometheus + `common/metrics/registry.go` 复用 | 与已有 LLM 指标一致 |
| P3 告警通道 | 飞书 webhook 复用 `message.SendFeishuNotification` | 与现有渠道禁用通知同一通道 |

---

## 方案

### P1：解耦收尾判定

#### 1.1 新增独立评估函数（`controller/channel-test.go`）

```go
// evaluateUsageBasedChannelDisable 独立执行"最近使用中的模型全部被自动禁用"的收尾判定。
//
// 与 recoverAutoDisabledModels 解耦：
//   - 不依赖 AutomaticEnableChannelEnabled（禁用是保护动作，不该被"启用"开关阉割）
//   - 不受 recoverModelsMaxPerRound 硬顶截断（判定纯 SQL 无 API 调用，成本可忽略）
//   - 不参与恢复队列的抖动 / 挤位
//
// 触发时机：主循环独立 tick，周期同 AutoTestChannelFrequency。
func evaluateUsageBasedChannelDisable() {
    if !config.IsMasterNode {
        return
    }
    if !config.AutomaticDisableChannelEnabled {
        return
    }
    channelIds, err := model.GetChannelsWithAutoDisabledAbilities()
    if err != nil {
        logger.SysError(fmt.Sprintf("evaluate usage-based disable: query channels failed: %s", err.Error()))
        return
    }
    triggered := 0
    for _, cid := range channelIds {
        should, used, disabled, jerr := model.ShouldDisableChannelByRecentUsage(cid)
        if jerr != nil {
            logger.SysError(fmt.Sprintf("channel #%d usage-based disable judge failed: %s", cid, jerr.Error()))
            continue
        }
        if !should {
            continue
        }
        logger.SysLog(fmt.Sprintf("channel #%d usage-based disable triggered: used=%d disabled=%d", cid, used, disabled))
        monitor.DisableChannelByRecentUsage(cid, used)
        triggered++
    }
    logger.SysLog(fmt.Sprintf("usage-based disable round done: candidates=%d triggered=%d", len(channelIds), triggered))
}
```

#### 1.2 新增 SQL（`model/ability.go`）

```go
// GetChannelsWithAutoDisabledAbilities 返回所有当前有 auto_disabled 模型的渠道 id（去重）。
// 用于独立收尾判定，覆盖范围完整、不受恢复候选队列上限约束。
func GetChannelsWithAutoDisabledAbilities() ([]int, error) {
    var ids []int
    err := DB.Table("abilities").
        Where("auto_disabled = ?", true).
        Distinct("channel_id").
        Pluck("channel_id", &ids).Error
    return ids, err
}
```

#### 1.3 主循环接入（`controller/channel-test.go:742` `AutomaticallyTestChannels` 或独立函数）

新增独立 goroutine，与探针主循环并行：

```go
// StartUsageBasedDisableEvaluator 独立 tick，周期同 AutoTestChannelFrequency。
// 与 AutomaticallyTestChannels 并行运行，无相互依赖。
func StartUsageBasedDisableEvaluator() {
    if !config.IsMasterNode {
        return
    }
    // 启动即跑一次（对齐 AutomaticallyTestChannels 的 startup run 语义）
    if config.AutoTestChannelFrequency > 0 {
        evaluateUsageBasedChannelDisable()
    }
    for {
        frequency := config.AutoTestChannelFrequency
        if frequency <= 0 {
            time.Sleep(time.Minute)
            continue
        }
        time.Sleep(time.Duration(frequency) * time.Minute)
        if config.AutoTestChannelFrequency <= 0 {
            continue
        }
        evaluateUsageBasedChannelDisable()
    }
}
```

在 `main.go` 主节点启动段并行拉起：

```go
go controller.AutomaticallyTestChannels()
go controller.StartUsageBasedDisableEvaluator()   // 新增
```

#### 1.4 移除原尾部收尾循环（`controller/channel-test.go:718-739`）

删除 `recoverAutoDisabledModels` 尾部 `evaluated` map 相关整段。保留恢复探针本职工作（测试单个 model 并 enable），不再兼任收尾。

删除后在函数头补一行注释：

```go
// 注意：整渠道"最近使用模型全禁"的收尾判定已移出本函数，
// 由 evaluateUsageBasedChannelDisable 独立评估。
// 参见 docs/plans/2026-08-27-auto-disable-refactor.md
```

---

### P2：恢复探针退避 + 死亡机制

#### 2.1 Schema（`model/ability.go` Ability struct）

新增 3 列，AutoMigrate 兼容：

```go
// RecoveryFailCount 恢复探针连续失败次数。成功恢复 → 归零。
RecoveryFailCount int `json:"recovery_fail_count" gorm:"default:0"`

// NextRecoveryAt 下一次允许恢复探测的 unix 秒。0 表示无退避，可立即探测。
// 恢复失败时按 ChannelRecoveryBackoff[min(fail_count-1, len-1)] 递增。
NextRecoveryAt int64 `json:"next_recovery_at" gorm:"bigint;default:0;index"`

// IsDead 判定"永久失败"标记：连续失败超阈值或 auto_disabled_time 超 7 天。
// 死亡后不再自动测试恢复，只能通过前端"模型自动禁用"手动批量启用。
IsDead bool `json:"is_dead" gorm:"default:false;index"`
```

#### 2.2 常量（`common/constants.go`）

```go
var ChannelRecoveryBackoff = []time.Duration{
    1 * time.Minute,
    5 * time.Minute,
    30 * time.Minute,
    2 * time.Hour,
    6 * time.Hour,
    24 * time.Hour,
}

const (
    AbilityRecoveryFailDeathThreshold  = 10          // 连续失败达此值 → is_dead
    AbilityRecoveryAgeDeathSeconds     = 86400 * 7   // auto_disabled_time 超此秒数 → is_dead
)
```

#### 2.3 `GetAutoDisabledAbilities` 过滤（`model/ability.go:344`）

```go
err := DB.Table("abilities a").
    Select("DISTINCT a.channel_id, a.model, MIN(a.auto_disabled_time) as auto_disabled_time").
    Joins("JOIN channels c ON c.id = a.channel_id").
    Where("a.auto_disabled = ? AND c.status != ? AND a.is_dead = ? AND a.next_recovery_at <= ?",
        true, common.ChannelStatusManuallyDisabled, false, time.Now().Unix()).
    Group("a.channel_id, a.model").
    Order("auto_disabled_time ASC").
    Scan(&items).Error
```

`recoverModelsMaxPerRound=100` **保留不变**——退避 + 死亡收敛后队列会自然缩小，100 够用。

#### 2.4 恢复失败 → 退避写入（`controller/channel-test.go:704-712` `recoverAutoDisabledModels`）

在 `testChannel` 判定后新增分支：

```go
testErr, openaiErr, _, _ := testChannel(channel, it.Model, true)
if util.ShouldEnableChannel(testErr, openaiErr) {
    if e := model.EnableModelOnChannel(it.ChannelId, it.Model); e != nil {
        logger.SysError(...)
    } else {
        recovered++
        logger.SysLog(...)
    }
} else {
    // 失败退避：fail_count +1，next_recovery_at 按退避表递增
    if e := model.MarkAbilityRecoveryFailed(it.ChannelId, it.Model); e != nil {
        logger.SysError(...)
    }
}
```

新增 `model.MarkAbilityRecoveryFailed(channelId int, modelName string)`：事务内 SELECT 当前 `recovery_fail_count`，`newCount = min(current+1, max)`，`newNext = now + backoff[min(newCount-1, len-1)]`；同时判定 `newCount >= AbilityRecoveryFailDeathThreshold` 或 `now - auto_disabled_time > AbilityRecoveryAgeDeathSeconds` 时置 `is_dead=true`。

#### 2.5 恢复成功清零（`model/ability.go` `EnableModelOnChannel` 内）

`Updates` 追加：

```go
"recovery_fail_count": 0,
"next_recovery_at":    0,
"is_dead":             false,
```

#### 2.6 前端手动救援入口保持

前端"模型自动禁用"批量启用调用现有 `EnableModelOnChannel`，天然清零。无需前端改动。

---

### P3：观测四指标

#### 3.1 指标定义（`common/metrics/registry.go` 追加）

```go
// 待恢复队列长度（按渠道类型分维度）
AbilityAutoDisabledTotal = promauto.NewGaugeVec(...)

// 恢复探针本轮成功率
ChannelRecoveryRoundSuccessRate = promauto.NewGauge(...)

// 僵尸占比（is_dead=true / total auto_disabled）
ZombieAbilityRatio = promauto.NewGauge(...)

// P1 独立评估器一轮内应评估但触发失败的渠道数
UsageDisableEvaluationBacklog = promauto.NewGauge(...)
```

#### 3.2 采集时机

- P1 `evaluateUsageBasedChannelDisable` 结尾 → 采样 `UsageDisableEvaluationBacklog`
- P2 `recoverAutoDisabledModels` 结尾 → 采样其余 3 项
- 独立采样 goroutine 每 5 分钟兜底刷新 `AbilityAutoDisabledTotal` / `ZombieAbilityRatio`（防主循环卡死时指标僵化）

#### 3.3 告警规则（本文档只列阈值，实际配置在 Prometheus alertmanager）

| 指标 | 阈值 | 严重级别 |
|---|---|---|
| `ability_auto_disabled_total` (总和) | > 5000 持续 30 min | warning |
| `channel_recovery_round_success_rate` | < 5% 持续 1h | warning |
| `zombie_ability_ratio` | > 30% | info |
| `usage_disable_evaluation_backlog` | > 100 持续 15 min | critical |

---

### 历史数据处理脚本（P1 上线之前执行）

**风险控制原则**：
- 全部使用 UPDATE，不使用 DROP/TRUNCATE，符合 CLAUDE.md 数据库禁令
- 分档处理，避免 720 条飞书告警刷屏
- 老僵尸（>3d）不消耗熔断计数额度——历史清算不该影响未来的熔断阈值

#### Step 1：诊断（只读）

```sql
-- 1.1 类型分布——识别批量事件
SELECT c.type,
       COUNT(*) AS ch_count,
       MIN(FROM_UNIXTIME(a.min_dt)) AS oldest_disable
FROM (
    <Step 1.2 SELECT 完整贴入 as tmp_zombie_channels>
) tc
JOIN channels c ON c.id = tc.id
JOIN (SELECT channel_id, MIN(auto_disabled_time) AS min_dt
      FROM abilities WHERE auto_disabled = 1 GROUP BY channel_id) a
  ON a.channel_id = c.id
GROUP BY c.type
ORDER BY ch_count DESC;

-- 1.2 找该被禁但漏禁的渠道
SELECT c.id, c.name, c.type,
       COUNT(DISTINCT a.model) AS disabled_models,
       used.used_models
FROM channels c
JOIN abilities a ON a.channel_id = c.id AND a.auto_disabled = 1
JOIN (
    SELECT channel_id, COUNT(DISTINCT model_name) AS used_models
    FROM model_metrics
    WHERE hour_timestamp >= UNIX_TIMESTAMP() - 86400
      AND total_requests > 0
    GROUP BY channel_id
) used ON used.channel_id = c.id
WHERE c.status = 1
  AND c.auto_disabled = 1
  AND c.auto_enabled = 1
  AND (c.multi_key_info IS NULL
       OR JSON_EXTRACT(c.multi_key_info, '$.is_multi_key') = false)
GROUP BY c.id
HAVING disabled_models >= used_models;

-- 1.3 批次识别（同一批创建的渠道系列，可能有救）
SELECT SUBSTRING_INDEX(name, '-', 3) AS batch_prefix,
       COUNT(*) AS ch_count,
       GROUP_CONCAT(DISTINCT type) AS types
FROM channels
WHERE id IN (<Step 1.2 结果的 id 列表>)
GROUP BY batch_prefix
HAVING ch_count >= 5
ORDER BY ch_count DESC;
```

#### Step 2：分档批量禁用

```sql
-- 档 1：老僵尸 (>3d)——直接禁用，不消耗熔断额度
UPDATE channels
SET status = 3,
    auto_disabled_time = UNIX_TIMESTAMP()
WHERE id IN (<Step 1.2 结果>)
  AND status = 1
  AND id IN (
    SELECT channel_id FROM abilities
    WHERE auto_disabled = 1
    GROUP BY channel_id
    HAVING MIN(auto_disabled_time) < UNIX_TIMESTAMP() - 86400 * 3
  );

-- 档 2：中期 (1h-3d)——加熔断计数（符合"最近发生"语义）
UPDATE channels
SET status = 3,
    auto_disabled_time = UNIX_TIMESTAMP(),
    auto_disable_count = auto_disable_count + 1
WHERE id IN (<Step 1.2 结果>)
  AND status = 1
  AND id IN (
    SELECT channel_id FROM abilities
    WHERE auto_disabled = 1
    GROUP BY channel_id
    HAVING MIN(auto_disabled_time) BETWEEN UNIX_TIMESTAMP() - 86400 * 3
                                       AND UNIX_TIMESTAMP() - 3600
  );

-- 档 3：<1h 的不动，让 P1 上线后自然处理（在抖动窗口内）
```

**执行后必须做**：

1. 重启所有 master 节点，刷新 channel cache（避免选渠道热路径继续用老数据）
2. 到飞书群发一条运维公告说明本次为历史清算，避免值班误判
3. 验证 SQL 参见 Step 4

#### Step 3（P2 上线后再做）：清理死亡候选

P2 死亡机制上线后，`abilities.is_dead=true` 会自然过滤出恢复队列。**不需要额外脚本**，等待 P2 逻辑推进即可。

如需加速：

```sql
UPDATE abilities
SET is_dead = 1
WHERE auto_disabled = 1
  AND auto_disabled_time > 0
  AND UNIX_TIMESTAMP() - auto_disabled_time > 86400 * 7;
```

#### Step 4：验证

```sql
-- 4.1 待恢复队列应显著缩小
SELECT COUNT(*) FROM (
    SELECT channel_id, model FROM abilities WHERE auto_disabled = 1 GROUP BY channel_id, model
) t;

-- 4.2 漏禁渠道数应归零（重跑 Step 1.2）
```

---

## 影响范围

### 改动文件

| 文件 | P1 | P2 | P3 |
|---|---|---|---|
| `controller/channel-test.go` | 新增 `evaluateUsageBasedChannelDisable` + `StartUsageBasedDisableEvaluator`；移除 718-739 收尾循环 | 恢复失败分支加 `MarkAbilityRecoveryFailed` 调用 | 采集点 |
| `model/ability.go` | 新增 `GetChannelsWithAutoDisabledAbilities` | struct 加 3 字段；`GetAutoDisabledAbilities` 加过滤；`EnableModelOnChannel` 清零；新增 `MarkAbilityRecoveryFailed` | — |
| `common/constants.go` | — | 新增 `ChannelRecoveryBackoff` + 2 死亡阈值 | — |
| `common/metrics/registry.go` | — | — | 新增 4 指标 |
| `main.go` | 新增 `go controller.StartUsageBasedDisableEvaluator()` | — | 采样 goroutine 拉起 |
| 新增测试 | `evaluate_usage_disable_test.go` | `ability_recovery_backoff_test.go` | — |

### Schema 变更

**P1**：无。
**P2**：`abilities` 表新增 3 列，GORM AutoMigrate 兼容（参见 `model/main.go:117`），default 值保证历史数据启动即用（`is_dead=false`, `next_recovery_at=0`, `recovery_fail_count=0`）。

### 前端影响

- 无强制改动。新字段默认值不影响现有前端渲染。
- **可选**：后续在渠道详情页显示 `auto_disable_count`（P0 熔断已加，本次可补） / `is_dead` 标记。开单独 issue 跟进，不阻塞本重构。

### 兼容性

- **Multi-key 渠道**：`monitor.DisableChannelByRecentUsage → disableChannelInternalWithStatusCode` 已前置退出 (`monitor/channel.go:163-166`)，P1 触发时天然跳过，无需额外处理。
- **`AutoDisabled=false` 渠道**：`disableChannelInternalWithStatusCode` 已前置退出，同理。
- **`AutomaticDisableChannelEnabled=false`（全局关）**：P1 `evaluateUsageBasedChannelDisable` 入口第一行 return，与现状一致。
- **`AutoTestChannelFrequency=0`**：P1 主循环内层 sleep 1 min 后 continue，不做无效评估。
- **恢复探针原有 24h 熔断退避**（`controller/channel-test.go:685-694`）：**保留**，与 P2 的 `next_recovery_at` 无冲突（前者作用于渠道级，后者作用于模型级）。

### 与已有 plan 的关系

- 复用 `docs/plans/2026-08-21-channel-disable-by-recent-usage.md` 的判定函数 `ShouldDisableChannelByRecentUsage`，只改调用点。
- 复用 `docs/plans/2026-08-26-auto-disable-circuit-breaker.md` 的 `AutoDisableCount` 熔断字段，P1 触发时自然沿用其计数逻辑。
- 与 `docs/plans/2026-08-20-model-scope-auto-disable.md` 的模型级隔离价值**保留不冲突**——P1 只加强"整渠道升级判定"这一步，不改模型级禁用本身。

---

## 验证

### P1

**编译与静态检查**

```bash
go build ./... && go vet ./...
```

**单元测试**（新增 `controller/evaluate_usage_disable_test.go`）

参考 `model/channel_disable_by_usage_test.go` 表驱动模式，覆盖：

| 场景 | 输入 | 预期 |
|---|---|---|
| A `AutomaticDisableChannelEnabled=false` | 调用评估 | 直接 return，无 DB 查询 |
| B 非 master 节点 | 调用评估 | 直接 return |
| C 单渠道 should=true | mock 单渠道满足条件 | `DisableChannelByRecentUsage` 被调用 1 次 |
| D 多渠道混合 | 5 渠道，3 触发 2 不触发 | triggered=3，日志正确 |
| E 判定错误容错 | 单个 `ShouldDisableChannelByRecentUsage` 报错 | 跳过继续处理其他渠道 |

**端到端手工验证**

```bash
# 前置：本地 sqlite，构造 3 个渠道
# ch1: 4 model 全禁，最近 24h 用过 4 个 → 应触发
# ch2: 4 model 禁 2 个，用过 4 个     → 不触发
# ch3: 手动禁用 (status=2)             → 跳过
```

预期：一个周期内 ch1 status 变 3，ch2/ch3 不变。

### P2

**单元测试**（新增 `model/ability_recovery_backoff_test.go`）

| 场景 | 输入 | 预期 |
|---|---|---|
| A 首次失败 | fail_count=0 | fail_count=1, next_recovery_at=now+1min |
| B 第 5 次失败 | fail_count=4 | fail_count=5, next_recovery_at=now+6h |
| C 死亡（次数触发） | fail_count=9 | fail_count=10, is_dead=true |
| D 死亡（年龄触发） | fail_count=1, auto_disabled_time=now-8d | is_dead=true |
| E 成功清零 | is_dead=true, fail_count=5 | is_dead=false, fail_count=0, next_recovery_at=0 |
| F 退避期内跳过 | next_recovery_at=now+5min | `GetAutoDisabledAbilities` 结果不包含 |

**手工验证**

sqlite 起服务，插入 `next_recovery_at=now-1min` → 一轮内被处理；改 `next_recovery_at=now+5min` → 跳过；连续失败 10 次后 → is_dead=true 且从队列消失。

### P3

**手工验证**：起本地服务，`curl localhost:3000/metrics | grep ability_auto_disabled_total`，值 > 0 且随手工插入 auto_disabled 记录变化。

### 历史数据清理

执行前后必对比 Step 4 的两条 SQL。720 → 应变 0（<1h 那档除外）。

---

## 风险与回滚

### P1

**风险**：独立 goroutine 与恢复探针并发触发 `DisableChannelByRecentUsage`，可能双写 `channels.auto_disable_count`。
**缓解**：`AutoDisableChannelById` 内部有事务与 `WHERE status != auto_disabled` 幂等保护（`model/channel.go:820`），双写会被降级为单写。
**回滚**：注释 `main.go` 里的 `go StartUsageBasedDisableEvaluator()`，重启即恢复原行为。

### P2

**风险**：`next_recovery_at` 索引在 abilities 大表上可能拖慢查询。
**缓解**：`abilities` 表本项目实测规模 <100k 行，索引开销可接受；如实测有性能问题可去掉 index tag 保留列语义。
**回滚**：schema 加列可保留（无害），代码层回滚 `GetAutoDisabledAbilities` 过滤条件 + 恢复失败分支即可。

### 历史数据清理

**风险**：误禁正在正常服务但恰好近期没流量的渠道。
**缓解**：Step 1.2 已加 `used_models` 判定，`used=0` 不进列表；且 SQL 有 `HAVING disabled_models >= used_models` 保护。
**回滚**：清理前建议备份 `channels` 表的 `id, status, auto_disabled_time, auto_disable_count` 4 列到临时表：

```sql
CREATE TABLE tmp_channels_backup_20260827 AS
SELECT id, status, auto_disabled_time, auto_disable_count FROM channels;
```

需要回滚时：

```sql
UPDATE channels c
JOIN tmp_channels_backup_20260827 b ON b.id = c.id
SET c.status = b.status,
    c.auto_disabled_time = b.auto_disabled_time,
    c.auto_disable_count = b.auto_disable_count
WHERE c.id IN (<需要回滚的 id 列表>);
```

---

## 上线顺序

1. **T+0（今天）**：手工禁 channel 80919，解决用户原始问题：`UPDATE channels SET status=3, auto_disabled_time=UNIX_TIMESTAMP() WHERE id=80919`
2. **T+1**：执行 Step 1 诊断 SQL，导出 720 渠道类型分布 + 批次分布 → 与运营 / 上游对齐是否有批量救援机会
3. **T+2**：执行 Step 2 分档批量禁用（先备份、非高峰时段、重启节点刷缓存）
4. **T+3~5**：P1 代码 PR → 审 → 灰度 → 上线
5. **T+7~14**：P2 代码 PR → 审 → 上线 → 观察僵尸队列收敛曲线
6. **T+15~21**：P3 观测接入 → 告警联调

---

## 待确认

以下决策点建议 review 时明确：

1. **P2 死亡阈值 `10 次连续失败 / 7 天`** 是否合适？可能对某些临时性上游故障过于激进。
2. **P2 `next_recovery_at` 加索引** 是否必要？根据你线上 abilities 表规模评估。
3. **P3 告警接入飞书群 vs 独立值班群**？现有渠道禁用通知已在同群，可能刷屏。
4. **前端渠道详情页展示 `is_dead`** 是否本次一起做？影响运维体验但增加改动量。

---

## CLAUDE.md 强制

- 每个 P 上线后写 `docs/CHANGELOG.md` 一段（分支、类型、文件、说明、关联本计划）
- P2 涉及 `abilities` 表新增列，AutoMigrate 兼容，不需要手动 SQL；如需手动 DDL，走 `ALTER TABLE` 且非主键列
- 历史数据清理脚本执行前必须备份，符合 "遇到数据库 schema 迁移问题，只允许分析 / ALTER / 告知用户手动执行" 原则
