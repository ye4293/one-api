# 模型级自动禁用 + 动态优先级评分

## 背景与目标

### 痛点
1. **渠道禁用粒度过粗**：当前自动禁用是整渠道级别，某个模型不可用时（如 Gemini 429）会连带该渠道所有其他正常模型一起下线。
2. **优先级静态不可调**：`Ability.Priority` 和 `Channel.Weight` 由管理员手动配置，无法根据实时成功率、延迟、价格自动优化流量分配。

### 目标
- 特性一：模型级自动禁用 + 探活恢复——精细化故障隔离
- 特性二：动态优先级评分——基于实时指标自动优化渠道选择

两个特性可独立开启，互不依赖。

---

## 特性一：模型级自动禁用 + 探活恢复

### 1.1 数据模型变更

Ability 表新增字段：

```go
type Ability struct {
    Group     string `json:"group" gorm:"type:varchar(32);primaryKey"`
    Model     string `json:"model" gorm:"primaryKey"`
    ChannelId int    `json:"channel_id" gorm:"primaryKey;index"`
    Enabled   bool   `json:"enabled"`
    Priority  *int64 `json:"priority" gorm:"bigint;default:0;index"`

    // 新增：模型级自动禁用元数据
    AutoDisabled       bool   `json:"auto_disabled" gorm:"default:false"`
    AutoDisabledReason string `json:"auto_disabled_reason,omitempty" gorm:"type:varchar(500)"`
    AutoDisabledTime   int64  `json:"auto_disabled_time,omitempty"`
}
```

### 1.2 禁用触发流程

```
relay 请求失败
  → ShouldDisableChannel() == true
  → if config.UpstreamModelAutoDisableEnabled:
      → AutoDisableAbility(channelId, modelName, reason, statusCode)
          ├─ UPDATE abilities SET enabled=false, auto_disabled=true, ...
          │   WHERE channel_id=? AND model=? AND enabled=true
          ├─ 幂等：RowsAffected=0 时不重复通知
          ├─ 监控通知：monitor.DisableAbilityWithStatusCode(...)
          ├─ 检查：该渠道所有 Ability 是否都 disabled？
          │   └─ 是 → AutoDisableChannelById()（升级为渠道级）
          └─ 作用域：同 channel_id+model 的所有 group 一起禁
    else:
      → monitor.DisableChannelWithStatusCode(...)（现有逻辑不变）
```

### 1.3 探活恢复流程

复用现有健康巡检框架，新增场景 `probeSceneAbilityRecovery`。

与现有 `UpstreamModelHealthProbeEnabled` 互斥：

```go
if config.UpstreamModelAutoDisableEnabled {
    // 新路径：探活恢复被禁用的 Ability
    runAbilityRecoveryProbe(...)
} else if config.UpstreamModelHealthProbeEnabled {
    // 旧路径：健康巡检删模型（向后兼容）
    runHealthProbe(...)
}
```

恢复探活逻辑：

```
runAbilityRecoveryProbe(channel, budget)
  ├─ 查询该渠道所有 auto_disabled=true 的 Ability
  ├─ 冷却保护：距 auto_disabled_time 不足 FastInterval 的跳过
  ├─ 按 auto_disabled_time 升序（最早禁的优先恢复）
  ├─ 对每个待恢复模型调用 probeChannelModel()
  │   ├─ alive → RecoverAbility(): enabled=true, 清除禁用元数据, 发通知
  │   ├─ not_found / 401 → 保持禁用
  │   └─ inconclusive / timeout → 不动，下轮再试
  ├─ 共享 probeBudget（全局每轮 200 次）
  └─ 恢复后检查：渠道 status=auto_disabled 且有 enabled Ability → 重新启用渠道
```

### 1.4 多 Key 渠道

保持现有 `HandleKeyError` 逻辑不变。多 Key 渠道的错误是 Key 级别的（额度耗尽、Key 被封），走禁 Key 路径，不在 Ability 层干预。

---

## 特性二：动态优先级评分

### 2.1 数据源：Redis 滑动窗口

所有节点请求完成时写入 Redis Sorted Set：

```
Key:    ability_metrics:{channelId}:{model}
Member: {success}:{duration}:{唯一ID}
Score:  Unix 时间戳（秒）

写入: ZADD ability_metrics:73014:gpt-4o <timestamp> "1:0.83:<uuid>"
读取: ZRANGEBYSCORE ... <now-600> <now>      （最近 10 分钟）
清理: ZREMRANGEBYSCORE ... 0 <now-600>        （丢弃过期）
```

无 Redis 降级：退回默认优先级（`Ability.Priority`），不影响正常运行。

### 2.2 评分计算（仅 Master 节点）

每 5 分钟由 Master 节点执行：

```
定时器触发
  → SCAN 所有 ability_metrics:* 的 key
  → 按 model 分组，每个 model 内对所有渠道做相对归一化
  → 计算 score = W_success × S_success + W_latency × S_latency + W_price × S_price
  → 批量写 DB:
      INSERT INTO abilities (channel_id, model, dynamic_priority)
      VALUES (...), (...)
      ON DUPLICATE KEY UPDATE dynamic_priority = VALUES(dynamic_priority)
```

### 2.3 归一化方式

同一 model 下所有渠道做相对排名，归一化到 0~100：

| 因子 | 归一化公式 | 含义 |
|---|---|---|
| 成功率 | `successRate × 100` | 直接映射，越高越好 |
| 响应时长 | `100 × (1 - (自己-最快) / (最慢-最快))` | 越快分越高 |
| 价格 | `100 × (1 - (自己-最低) / (最高-最低))` | 越便宜分越高 |

边界处理：
- 只有一个渠道：latency 和 price 都得 100
- 所有渠道延迟/价格相同：都得 100
- 无数据的渠道：不参与归一化，使用默认优先级

### 2.4 选渠道热路径集成

```go
if config.DynamicPriorityEnabled {
    // 新逻辑：按 dynamic_priority DESC 排序
    // 取分数在 top × 0.90 以上的作为同档（10% 阈值）
    // 同档内按 weight 加权随机
} else {
    // 现有逻辑：MAX(priority) 分组 → weight 加权随机
}
```

### 2.5 价格数据来源

Channel 结构体新增：

```go
type Channel struct {
    // ... 现有字段
    UnitPrice float64 `json:"unit_price" gorm:"type:decimal(10,6);default:0"`
}
```

在渠道创建/编辑时设置收购价，后续可随时调整。

---

## 配置项汇总

| 配置项 | 默认值 | 说明 |
|---|---|---|
| `UpstreamModelAutoDisableEnabled` | false | 模型级禁用总开关 |
| `DynamicPriorityEnabled` | false | 动态优先级总开关 |
| `DynamicPriorityWeightSuccess` | 50 | 成功率权重（0-100） |
| `DynamicPriorityWeightLatency` | 30 | 延迟权重（0-100） |
| `DynamicPriorityWeightPrice` | 20 | 价格权重（0-100） |
| `DynamicPriorityCalcIntervalMinutes` | 5 | 评分计算周期（分钟） |
| `DynamicPriorityTopThreshold` | 10 | 同档阈值（%） |

---

## 改动文件清单

| 文件 | 改动 |
|---|---|
| `model/ability.go` | 结构体新增字段；`AutoDisableAbility()`、`RecoverAbility()`、`GetAutoDisabledAbilities()`、`BatchUpdateDynamicPriority()` |
| `model/channel.go` | 新增 `UnitPrice` 字段 |
| `model/cache.go` | `CacheGetRandomSatisfiedChannel` 加 `DynamicPriorityEnabled` 分支 |
| `controller/relay.go` | 错误处理分支加模型级禁用判定 |
| `controller/channel_upstream_update.go` | 加 `runAbilityRecoveryProbe`，与 `runHealthProbe` 互斥 |
| `controller/dynamic_priority.go` | 新文件：定时计算器、Redis 读取、归一化、批量写 DB |
| `common/config/config.go` | 新增全部配置项 |
| `common/metrics/ability_window.go` | 新文件：请求完成时 ZADD 写 Redis |
| `model/option.go` | 注册新配置项到 OptionMap |
| `model/main.go` | AutoMigrate 新字段 |
| `monitor/` | 新增 `DisableAbilityWithStatusCode()`、`RecoverAbilityNotify()` |

---

## 不变的部分

- 渠道创建/编辑 API 和 UI（除新增 UnitPrice 字段外）
- 多 Key 逻辑（HandleKeyError）
- 手动禁用/启用渠道
- `ShouldDisableChannel` 判定规则本身
- 现有 Prometheus 指标体系

---

## 两个特性的交互

- 同时开启时：模型级禁用优先——被禁的 Ability（`enabled=false`）不参与动态评分和选渠道
- 动态优先级开启时，管理员手动设的 `Priority` 字段作为无数据时的默认分数
- 探活恢复的渠道重新参与动态评分，初始使用默认优先级，积累数据后自动调整

---

## 验证方式

1. **模型级禁用**：配置一个测试渠道含多个模型，对其中一个模型发送会触发禁用的请求，验证只有该模型的 Ability 被禁，其他模型不受影响
2. **升级判定**：禁用同一渠道的所有模型，验证渠道自动升级为 channel 级禁用
3. **探活恢复**：禁用后恢复上游服务，验证探活定时器自动恢复 Ability
4. **动态优先级**：配置多个渠道同一模型，制造不同成功率/延迟，验证 5 分钟后 dynamic_priority 更新且选渠道偏向高分渠道
5. **开关互斥**：验证 `UpstreamModelAutoDisableEnabled` 和 `UpstreamModelHealthProbeEnabled` 不会同时生效
