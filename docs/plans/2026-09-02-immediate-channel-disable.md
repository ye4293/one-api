# 即刻触发整渠道自动禁用（1h 窗口 + 去抖动 + 可配置）

## Context（背景与目标）

**问题**：一批 OpenAI 渠道账户已欠费（`You have no credits remaining`），但真实流量长期不把它们整渠道下线，必须靠手动 `test-channels` 巡检才发现。

**根因**（已排查确认）：真实流量的自动禁用是「被动 + 模型级」——`relay.go` 里 `ShouldDisableChannel` 开着（`AutomaticDisableChannelEnabled=true`），命中规则时走 `monitor.DisableModelOnChannelWithStatusCode`（`ModelScopeAutoDisableEnabled=true`），**只禁「该渠道 + 被请求的那个模型」这一行 ability**。整渠道禁用由 `model.ShouldDisableChannelByRecentUsage` 判定「最近使用的模型全部被禁」后触发，但当前该判定：
1. 窗口硬编码 **24 小时**，分母过大，账户欠费的渠道要等一天内用过的模型全挂才禁；
2. 触发时机是**恢复探针尾部周期判定**（`recoverAutoDisabledModels` 收尾，随 `AutoTestChannelFrequency=10min`），非即时；
3. 有 **stabilizeCutoff 抖动保护**（被禁模型须稳定 ≥20min 才计入），刚禁的模型不算数。

**目标**（用户已拍板，方案 C）：
- 窗口 24h → **可配置（默认 60 分钟）**；
- 触发时机 → **某模型被自动禁用的那一刻即刻判定**；
- **彻底去掉抖动保护**（用户明确接受瞬时抖动误禁风险，安全网是恢复探针逐模型救回）。

**目标分支**：`dynamic-priority`（当前分支，HEAD fe61bf7）。核心判定函数 `model/channel_disable_by_usage.go` 两分支一致；触发接入点在本分支为 `recoverAutoDisabledModels` 尾部（`controller/channel-test.go` 729-738）。

---

## 关键事实（探查结论）

- `processChannelRelayError` 全部以 `go ...` **异步协程**调用（`controller/relay.go` 多处）→ 模型级禁用不在用户请求响应路径上，**加同步判定 SQL 不拖慢用户请求**。
- `model_metrics` **非实时**：由 `StartModelMetricsAggregator`（`main.go:231`）每 `ModelMetricsAggregationInterval=300s`（**5 分钟**）从 `logs` 表按小时聚合。→ 即刻判定时刚失败的模型可能还没进 `used` 集合，**必须显式把当前 triggerModel 并入 used**。
- `DisableModelOnChannelWithStatusCode`（`monitor/channel.go:125`）先 `model.AutoDisableModelOnChannel`（写 abilities `auto_disabled=1, time≈now`），是即刻判定的接入点。
- 现有整型可配置项参照：`AutoTestChannelFrequency`（`config.go:142` → `option.go:38` OptionMap → `option.go:510` case）。`option.go` 有现成 `setPositiveIntOption`（带校验，`<=0` 保持原值）。
- 判定逻辑**无现成测试文件**（需新建）。
- 前端在独立仓库 `~/code/ezlinkai-web`（main 分支）：运营设置页 `sections/setting/view/settingPage.tsx`（含 option PUT）、类型 `lib/types/operationalSettings.ts`、文案 `locales/zh.ts`。

---

## 方案设计

### 1. 新增可配置项 `ChannelUsageWindowMinutes`（默认 60）

- **`common/config/config.go`**（142 行 `AutoTestChannelFrequency` 旁）新增：
  ```go
  var ChannelUsageWindowMinutes = 60 // 「最近使用模型」窗口（分钟），整渠道自动禁用判定用
  ```
- **`model/option.go`**：
  - OptionMap 初始化（38 行附近）：`config.OptionMap["ChannelUsageWindowMinutes"] = strconv.Itoa(config.ChannelUsageWindowMinutes)`
  - `updateOptionMap` case：`case "ChannelUsageWindowMinutes": setPositiveIntOption(&config.ChannelUsageWindowMinutes, value)`

### 2. 改造核心判定 `model/channel_disable_by_usage.go`

抽内部函数，两个公开入口复用；**窗口用配置、去掉抖动、支持并入额外模型、渠道已非 enabled 时短路**：

```go
// 内部：windowSeconds 窗口；extraUsed 强制并入的模型（兜底 metrics 聚合滞后）
func shouldDisableByRecentUsage(channelId int, extraUsed []string) (should bool, used, disabled int, err error) {
    now := time.Now().Unix()
    windowStart := now - int64(config.ChannelUsageWindowMinutes)*60

    // used = model_metrics 近窗口 total_requests>0 ∪ extraUsed（去重）
    // disabled = used ∩ abilities(auto_disabled=1)   —— 去掉 auto_disabled_time <= stabilizeCutoff 条件
    // should = used>0 && used==disabled
}

// 即刻入口（monitor 模型级禁用后同步调）：并入 triggerModel
func ShouldDisableChannelByRecentUsageImmediate(channelId int, triggerModel string) (bool, int, int, error) {
    return shouldDisableByRecentUsage(channelId, []string{triggerModel})
}

// 周期兜底入口（恢复探针尾部）：不并入额外模型
func ShouldDisableChannelByRecentUsage(channelId int) (bool, int, int, error) {
    return shouldDisableByRecentUsage(channelId, nil)
}
```

- 删除 `channelDisableStabilizeProbeCycles` / `channelDisableStabilizeFloorSeconds` / `channelDisableDefaultProbeFreqMinutes` / `stabilizeCutoff` 计算（去抖动后不再需要）。
- 删除 `channelDisableUsageWindowSeconds` 常量，改用 `config.ChannelUsageWindowMinutes`。
- **幂等短路**：函数开头查 `channel.Status`，若已非 enabled 直接 `should=false`，避免即刻触发与 5 分钟后周期兜底重复禁用/重复通知。

### 3. 即刻触发接入 `monitor/channel.go`

`DisableModelOnChannelWithStatusCode`（125-152）在 `AutoDisableModelOnChannel` 成功后追加：

```go
if should, used, _, jerr := model.ShouldDisableChannelByRecentUsageImmediate(channelId, modelName); jerr != nil {
    logger.SysError(...)
} else if should {
    logger.SysLog(fmt.Sprintf("channel #%d immediate usage-based disable: used=%d", channelId, used))
    DisableChannelByRecentUsage(channelId, used)
}
```
（此函数已在异步协程内，同步两条 SQL 无阻塞风险。）

### 4. 周期兜底保留

`controller/channel-test.go` 729-738 的恢复探针尾部判定**不改调用**，其调用的 `ShouldDisableChannelByRecentUsage` 自动获得新窗口+去抖动行为。作用：即刻触发因 metrics 滞后漏判时，5 分钟聚合补齐后由周期判定兜底。

### 5. 前端（`~/code/ezlinkai-web`，main 分支）

在 `settingPage.tsx` 参照 `AutoTestChannelFrequency` 加整型输入项「渠道最近使用窗口（分钟）」；`lib/types/operationalSettings.ts` 加字段；`locales/zh.ts` 加文案。走既有 option PUT 链路，无需新接口。

---

## 影响范围

- **行为变化**：整渠道自动禁用变快、变激进（窗口小 10 倍 + 即时 + 无抖动缓冲）。符合目标。
- **风险**：上游瞬时抖动（热模型集体 429/超时）可能瞬间连锁禁整渠道。**安全网** = 恢复探针 `recoverAutoDisabledModels` 逐模型探测救回；建议保持 `AutoTestChannelFrequency` 合理（当前 10min）。
- **无数据库 schema 变更**（仅 options 表新增一行 KV，走既有 option 机制，非 DDL）。
- **多 Key 渠道**：`DisableChannelByRecentUsage` 已跳过，语义不变。
- 后端与前端为**两个独立仓库**，分别提交。

---

## 验证方式

1. `go build ./... && go vet ./...`（提交前必跑）。
2. **新增单测** `model/channel_disable_by_usage_test.go`：
   - 窗口取 `config.ChannelUsageWindowMinutes`（改配置后窗口变化）；
   - 去抖动（刚禁 `time≈now` 的模型计入 disabled）；
   - `triggerModel` 并入 used（mock model_metrics 不含该模型时仍判定成立）；
   - 渠道已非 enabled 时短路返回 false。
3. **端到端**（预发/本地造数）：一渠道声明多模型，逐个把「近 1h 用过的模型」禁掉，验证最后一个被禁的瞬间整渠道即刻 `status=3`；且 5 分钟内不因周期兜底重复发通知。
4. 前端改窗口 → PUT → `GET /api/option/` 确认 `ChannelUsageWindowMinutes` 生效，判定窗口随之变化。
5. 按项目规范更新 `docs/CHANGELOG.md`，并将本 plan 落到 `docs/plans/2026-09-02-immediate-channel-disable.md`。
