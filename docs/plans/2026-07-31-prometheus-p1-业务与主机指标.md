# P1：AI 业务指标 + 主机指标

- **日期**：2026-07-31
- **分支**：`prometheus-p0`（承接 P0，未合并）
- **前置**：P0（进程/连接池 + pprof）已完成，108 条序列

## 1. 背景与目标

P0 只回答"这个进程健康吗"，回答不了"业务在发生什么"。用户提出 12 项监控需求，
其中 **7 项零代码**（主机层由 exporter 提供），5 项需要埋点：

| 需求 | 归属 | 实现 |
|---|---|---|
| CPU / 内存 / 磁盘 / IO / 网络 / Load / 文件系统 | 主机层 | node_exporter + cAdvisor，**零代码** |
| LLM 请求数、Token 数、模型耗时、模型错误率 | 业务层 | 8 处埋点，全部"加一行" |
| 各模型 TPM / RPM | 业务层 | **不建独立指标**，`rate(counter[5m])*60` 派生 |

用户已定前提：错误率**两种语义都要**；TPM/RPM **仅用于观测**（不参与限流决策，
故 `rate()` 的估算性质可接受）；`abilities` 表 **388 个 distinct model**，日常有流量十几个。

## 2. 方案设计

### 2.1 探索中推翻的三个初始判断

1. **`controller/relay.go` 有 12 个独立重试循环、30 个 `processChannelRelayError` 调用点。**
   在调用点埋点只能覆盖 12 条链路中的 1 条，且新增入口必然遗漏。
   → **埋在 `processChannelRelayError` 函数体内**，1 处覆盖 30 个调用点，新入口自动被覆盖。

2. **用户请求级指标完全不需要动 relay 层。** gin 中间件天然是"每用户请求恰好一次"，
   且能覆盖 video / midjourney / sora / flux —— 这些链路根本不经过
   `RecordConsumeLogWithOtherAndRequestID`。

3. **Prometheus 的序列不会因为"常用只有十几个"而减少。**
   `CounterVec` 的子指标一旦被 `WithLabelValues` 创建就**永不回收**，会持续出现在每次
   `/metrics` 输出里；staleness 只在 target 消失后生效。
   → 必须按 388 的理论上限设计容量并加基数守卫，不能按"日常十几个"设计。

### 2.2 新增文件

| 文件 | 内容 |
|---|---|
| `common/metrics/llm.go` | 模型维度指标（Group A）+ Observe/Inc 函数 + 桶边界 |
| `common/metrics/channel.go` | 渠道维度指标（Group B） |
| `common/metrics/classify.go` | `ClassifyReason` 9 值封闭枚举 + `CodeClass` |
| `common/metrics/cardinality.go` | 基数守卫 + `label_overflow_total` |
| `common/metrics/metrics_test.go` | 单测（含并发守卫、桶对齐） |
| `middleware/metrics.go` | `RelayMetrics()` 中间件 |
| `middleware/metrics_test.go` | 单测（流式 200 陷阱） |
| `deploy/prometheus/rules.yml` | 14 条 recording rule |
| `deploy/prometheus/docker-compose.monitoring.yml` | node_exporter + cAdvisor + Prometheus |
| `deploy/prometheus/alerts_test.yml` | promtool 规则单测 |

`common/metrics` 的 leaf 约束保持不破（只 import `strings`/`strconv`/`sync`/`prometheus`/`common/config`/`common/logger`）。

### 2.3 关键设计决定

**Group A（模型维度）与 Group B（渠道维度）严格不交叉。**
`model` 与 `channel_id` 若出现在同一指标：388 × 200 × 14(直方图序列) ≈ **110 万条序列**，
单这一个指标就能打爆 Prometheus。`model_metrics` 表能承受三元组是因为有小时聚合和唯一索引 ——
不能照搬 DB 的维度设计。

**两种错误率的指标名前缀彻底分开，并用 recording rule 固化公式。**
`oneapi_llm_*` = 用户请求级（SLO，重试不计）；`oneapi_channel_*` = 渠道调用级（含重试）。
一次请求重试 3 次全失败：前者记 1 次，后者记 3 次。规则：**任何除法的分子分母不得跨前缀**。
光靠文档约束不住人，所以固化在 `rules.yml`，Grafana 只引用固定名字。

**埋点在 `LogConsumeEnabled` 早退之前。**
那是可后台动态关闭的开关，而运维关掉它的场景（DB 压力大、磁盘吃紧）恰好是最需要监控的时刻。
放在后面则一关开关 tokens 曲线掉到 0，与"真的没流量"在 Grafana 上完全无法区分。
代价：`speed` 拿不到 → 不导出 speed 指标，吞吐由 PromQL 派生（顺带修掉 DB 侧
`AvgSpeed = SumSpeed/SpeedCount` 这个"平均值的平均值"的统计错误）。

**处理流式 200 陷阱。**
SSE 请求响应头写出后，中途失败时 `c.Writer.Status()` 仍是 200，只看状态码会**系统性低估错误率**。
解法：`recordFinalErrorLog` 写 `metrics.CtxRelayFailedKey`，中间件优先读它。
副产品：Prometheus 失败计数与 `logs` 表 `LogTypeError` 行数由构造保证一致。

**`Error.Type` 不能当 label。** `relay/util/common.go` 会用上游 JSON 的 `error.type` 整体覆盖它，
基数无界。所以以状态码为主键自建 9 值封闭枚举，并有单测守住封闭性。

**桶边界扩展到 600s。** 前 7 个与 `model.LatencyBoundaries` 逐值对齐（有单测守住），
后 4 个（60/120/300/600）是 Prometheus 侧独有：生产 `STREAMING_TIMEOUT=600`，
DB 侧上界 30s 意味着 40s 与 25 分钟的请求落进同一桶，P95/P99 是编的。

**`provider` 不加到 Group A。** 埋点处只有 `channelId`，反查要多一次查询；
且 provider 是 `channel_id` 的函数，不该冗余。改用 `channel_info` info 指标 + `group_left` 关联。

### 2.4 埋点清单（8 处，全部"加一行"）

| 位置 | 记录什么 |
|---|---|
| `model/log.go` `RecordConsumeLogWithOtherAndRequestID` | tokens/quota/耗时/首字延迟，**一处覆盖 6 条模态链路** |
| `model/log.go` `RecordVideoConsumeLog` | 视频模态（独立写库） |
| `middleware/distributor.go` 503 分支 | `no_channel_total` + SLO 所需 context 键 |
| `middleware/distributor.go` `SetupContextForSelectedChannel` | 渠道尝试 + `channel_info` |
| `controller/relay.go` `processChannelRelayError` 函数体内 | 渠道调用失败 |
| `controller/retry_log.go` `recordFinalErrorLog` | 用户级最终失败 + 流式失败标记 |
| `router/relay-router.go` | 挂载中间件 |
| `main.go` | `RegisterBusinessMetrics()` |

**`relay/controller/text.go` 零改动；12 个重试循环里一行不动。**

### 2.5 新增配置项

`METRICS_LLM_ENABLED`(true) / `METRICS_CHANNEL_ENABLED`(true) —— 两个独立**回滚阀门**；
`METRICS_MAX_MODEL_LABELS`(500) / `METRICS_LATENCY_BUCKETS`。全部只在启动时读。

## 3. 影响范围

| 项 | 评估 |
|---|---|
| 修改文件 | `model/log.go`(2 行埋点+import)、`middleware/distributor.go`(2 处)、`controller/relay.go`(1 处)、`controller/retry_log.go`(1 处)、`router/relay-router.go`(1 行)、`main.go`(1 行)、`common/config/config.go`(4 项)、`common/metrics/registry.go`(注册入口+包注释) |
| 新增依赖 | **无**（沿用 P0 的 client_golang） |
| 数据库 | **无 schema 变更、无新表、scrape 路径零 DB 查询** |
| 前端 | 无改动 |
| 序列数 | 实测预期 ~1780（含 P0 的 108）；理论上限 ~23000，均在安全范围 |
| 回滚 | `METRICS_LLM_ENABLED=false METRICS_CHANNEL_ENABLED=false` 即完全消失，P0 指标不受影响 |

**风险**：
1. **双指标系统不一致**（最大风险，是治理问题不是技术问题）—— 由五条裁决规则 +
   偏差容忍度 + recording rules 固化处置。代码写对但没人知道该看哪个 = 白做。
2. **`channel_attempts_total` 的隐式契约**（最脆弱一环，无法用代码强制）——
   依赖"每次渠道尝试都经过 `SetupContextForSelectedChannel`"。新增 relay 入口若绕过它，
   分母漏计而分子照涨 → 错误率虚高甚至 >100%。已在函数注释与 README 记账。
3. **已知缺口**：首字延迟仅 4 个适配器上报；`recordFinalErrorLog` 只覆盖 4 条链路
   （video/midjourney/ocr/flux/runway/sora 靠状态码兜底）；视频不记延迟直方图（刻意）。
4. **cAdvisor 序列爆炸** —— `metric_relabel_configs keep` 白名单是必须的，不是优化。
5. **`node_exporter` 仅 Linux** —— 主机指标验证需在 Linux 上做。

## 4. 验证方式

### 已完成

| 项 | 结果 |
|---|---|
| `go build ./...` | 通过 |
| `go vet`（改动范围） | 通过 |
| `go test ./common/metrics/... ./middleware/... -race` | 通过 |
| `promtool check rules` | 31 条告警 + 14 条 recording rule 全部合法 |
| `promtool test rules` | SUCCESS |
| **V7** 503 无渠道路径 | `no_channel_total{model,group}`=1、`llm_requests_total{outcome="error"}`=1、`requests_by_group_total`=1 |
| **V8** 基数守卫（上限 50，并发打 200 个随机模型名） | distinct model label 精确停在 **51**（50+`__other__`），`label_overflow_total`=302 |
| **V16** 回滚阀门 | 关掉两个开关后 `oneapi_llm_*`/`oneapi_channel_*` 序列数 = **0**，P0 的 `go_sql_*`(9)、`oneapi_redis_*`(6) 完好，总数回到 108 |

**单测抓到的真实缺陷（已修）**：
- 基数守卫原用"先 `Load` 检查再 `Add`"，两步之间有窗口，50 goroutine 并发下上限 100
  冲到 **113**。改为用 `LoadOrStore` + `Add` 返回值做原子闸门（超限则回退），硬上限成立。
- 503 请求原本完全不进 `llm_requests_total`（distributor 在 `c.Set("model")` 之前就 abort，
  且不走 `recordFinalErrorLog`，中间件两个判据都不满足）。后果是"某模型丢光全部渠道"
  在 SLO 错误率上显示为零影响。已补 `CtxModelKey` 兜底键。

### 未验证（需 mock 上游，本地渠道指向真实外部 API 不能压）

V1 token 精确性、V2 独立于 `LogConsumeEnabled`、V3/V4 两种错误率的 1:3:1 不变式、
V5 流式中途失败、V6 长请求落桶、V9 视频缺口、V10 与 DB 对账、V12–V14 主机/容器指标。

其中 V5 的判定逻辑已由 `middleware/metrics_test.go` 的单测覆盖；
V3/V4 的埋点位置由代码审查 + `processChannelRelayError` 单点汇聚保证。
完整验证需要一个指向本地 mock 上游的测试渠道，或在 staging 环境做。

## 5. 后续

- **全站 HTTP RED**：`/api/*`（管理后台）与 web 仍无请求量指标。加一个 gin 中间件即可，
  注意必须注册在 `gin.Recovery()` **之前**（defer 是 LIFO，否则 panic 全被记成 2xx）。
- **凭据泄露**：`docker-compose.yml` 已被 git 跟踪且含明文 RDS/Redis 生产凭据，
  `.gitignore` 未覆盖。本次未处理（用户决定暂缓），但 `METRICS_TOKEN` 不应写入该文件。
