# 恢复探针改造：DESC 排序 + 并发全量 + 僵尸退避

**日期**: 2026-08-27
**分支**: `auto-disable-refactor`

## 背景与目标

线上排查 jsy 站点渠道 62395（`openai-az-anger`，Azure OpenAI 号池，one-api 侧单 key 入口）
的 gpt-5.5 反复被自动禁用、且从不自动恢复，只能人工「禁用→启用」救回。Grafana/Loki 日志坐实根因：

1. **禁用侧**：号池换 key 间隙上游返回 401 `Access denied due to invalid subscription key`，
   `controller/relay.go:807` 的 `ShouldDisableChannel` 判定 401 命中规则 → 立即模型级禁用。
   判定本身正确（401 该禁），但号池的 401 是秒级瞬时、会自愈——属用错场景。

2. **恢复侧（致命）**：恢复探针每轮日志为 `candidates=877 processed=100 recovered=0`：
   - `recoverModelsMaxPerRound=100` 硬顶，每轮只处理 100 个
   - 排序 `auto_disabled_time ASC`（优先最老）→ 前 100 永远是测不通的老僵尸，recovered=0
   - 僵尸不自愈也不消失，每轮继续占满前 100
   - 62395 的 gpt-5.5 最近才被禁（time 最新），永远排在 877 队尾，**从未被探测到**
   - 死锁：僵尸永占前排 → 恢复预算被吸干 → 新被误禁的活跃渠道饿死

**目标**：让恢复探针不再被僵尸堵死，新被误禁的渠道能在一轮内被探测并自动救回；一轮跑完积压。

## 方案设计（4 处改动）

### 1. 排序 ASC → DESC — `model/ability.go` `GetAutoDisabledAbilities`
`Order("auto_disabled_time ASC")` → `DESC`。优先探测最近被禁的（最可能瞬时误禁、已自愈）。

### 2. 僵尸退避（新增） — `controller/channel-test.go`
进程内退避表 `recoverBackoff map[string]*recoverBackoffEntry` + `recoverBackoffMu`，
曲线 `recoverModelProbeBackoff = {5m,15m,30m,1h,3h,6h}`：
- 探测失败 → `failCount++`，`nextProbeAt = now + backoff[min(failCount-1, len-1)]`
- 探测成功 → 清除条目
- 退避期内的候选跳过（计 skipped），不发请求
- 每轮收尾 `recoverBackoffPrune(liveKeys)` 清理已不在候选集合的残留（防内存泄漏）
- 仅主节点跑，进程内一致；重启丢失可接受

### 3. 并发化 + 取消硬顶 — `controller/channel-test.go` `recoverAutoDisabledModels`
串行 for → worker pool：
- 并发度 `config.RecoverConcurrency`（默认 16，下限保护 >=1）
- jobs channel + N worker + `sync.WaitGroup`；计数用 `atomic.AddInt64`
- 每个 job 独立 `model.GetChannelById(id, true)`，不共享 `*Channel` 指针（并发安全：
  `GetAdaptor` 每次新建无状态、`testChannel` 内部全局部对象、`EnableModelOnChannel` 有 per-channel 锁）
- 保留既有前置检查：`AutoEnabled`、24h 熔断退避、`isUnsupportedTestChannel/Model`；新增僵尸退避检查
- `recoverModelsMaxPerRound` 100 → 2000（纯兜底防 abilities 异常膨胀；退避后真实候选远低于此）
- `RequestInterval` sleep 移到 per-worker（默认 0 无影响）

### 4. 新增配置 — `common/config/config.go`
`var RecoverConcurrency = env.Int("RECOVER_CONCURRENCY", 16)`

## 影响范围

- 只改恢复链路，不动禁用判定（`MatchesDisableRule`/`ShouldDisableChannel`）。禁用侧号池 401
  敏感问题本次不处理（后续可对 401 加抖动窗口）。
- `AutomaticallyTestChannels` tick + `recoverModelsRunning` 锁不变；`evaluateUsageBasedChannelDisable` 不受影响。
- 上游 QPS：16 路并发，候选分散多上游 + 退避压低总量。
- 无数据库 schema 变更（退避表纯内存）。

## 验证方式

- `go build ./... && go vet ./...` 通过
- `go test ./controller/ ./model/ -count=1` 通过；`go test -race ./controller/ -run RecoverBackoff` 通过
  - `model/ability_test.go`：`TestGetAutoDisabledAbilities_OrdersByTimeDESC`（DESC 断言）
  - `controller/channel_recover_backoff_test.go`：退避递增/成功清除/prune 清理
- 线上部署后查 Loki：`model recovery round done` 的 candidates 应骤降、recovered>0；
  `model recovery ... 62395 ... re-enabled` 应出现；观察 62395 gpt-5.5 被 401 误禁后能否 1~2 轮内自动恢复。
