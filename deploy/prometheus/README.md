# Prometheus 接入

one-api 的 Prometheus 指标**导出**与业务服务器侧的采集配置。实现代码在 `common/metrics/`。

分两个阶段落地：

- **P0** 进程与依赖健康度：Go runtime、DB / Redis 连接池、pprof
- **P1** AI 业务指标（模型 / 渠道维度）+ 主机指标（node_exporter / cAdvisor，零代码）

## ⚠️ 跨仓库分工（改动前必读）

指标链路横跨两个仓库，**按部署位置划分归属，不留重复**：

| 内容 | 归属仓库 | 位置 |
|---|---|---|
| 指标导出代码、`/metrics` 端点 | **本仓库** | `common/metrics/` |
| 业务服务器侧的 exporter 与 agent | **`~/code/one-api-logging`** | 与 promtail 同项目，见其 README |
| 本地开发脚手架 | **本仓库** | `run-local.sh`、`prometheus.local.yml` |
| K8s 路线（与推送架构互斥） | **本仓库** | `servicemonitor.yaml` |
| **告警规则、recording rule、接收端配置** | **`~/code/monitor`** | `monitor/prometheus/` |
| Prometheus / Grafana 服务本身 | **`~/code/monitor`** | `monitor/docker-compose.yml` |

**告警规则不在本仓库**。它们在接收端评估，所以归 monitor 仓库；本仓库刻意不保留副本 ——
两边各放一份必然漂移，而漂移的症状是"本地测过的规则上线后行为不同"，极难排查。
`run-local.sh` 从 monitor 发布的镜像里 `docker cp` 取规则，物理上无法漂移。

**代价（必须知道）**：加一个 `oneapi_llm_*` 指标要开两个 PR、两次发布，
且**有顺序依赖 —— 先发指标，后发规则**（先发规则会导致告警引用不存在的指标）。

校验规则请去 monitor 仓库：`cd ~/code/monitor && make prom-check`

---

## 一、职责边界：五条裁决规则

P1 起本套指标与 `model_metrics` 表存在**有意的重叠**（请求量、tokens、延迟）。
重叠不是缺陷，但必须有边界，否则运营会问"Prometheus 说 12,345、model-plaza 说 12,290，哪个对"。

| 问题 | 唯一答案 | 理由 |
|---|---|---|
| 账单、对客数字、财务对账 | **只用 DB `logs.quota`** | Prometheus counter 进程重启归零，`increase()` 是线性外推的估算值，永远不等于真值 |
| 分钟级告警、SLO、实时排障（< 6h） | **只用 Prometheus** | `model_metrics` 是小时粒度 + 300s 聚合延迟，物理上做不到分钟级 |
| 按 user / token / request_id / IP 下钻 | **只用 DB** | 这些维度基数无界，Prometheus 侧刻意不做成 label。`controller/log.go` 已有完整能力 |
| 历史趋势 > 15 天 | **只用 DB** | Prometheus 保留期短，且前端 model-plaza 已依赖 `model_metrics` |
| 渠道调用级（含重试）错误率 | **只有 Prometheus 有** | DB 侧 `recordFinalErrorLog` 只写一条聚合记录，重试明细在 `other` 字段的 JSON 里无法聚合查询 → 不存在争议 |

**Grafana 组织约定**：分两个 folder —— `SRE (Prometheus)` 与 `Business (MySQL)`。
面板标题加前缀 `[实时]`（Prometheus）/ `[账务]`（DB）。
**禁止在同一个面板里混放两个数据源的同类指标。**

## 二、两种错误率，绝不可混用

one-api 有两个语义完全不同的错误率：

```
用户请求级（SLO）    oneapi_llm_requests_total / oneapi_llm_request_errors_total
                     一次 API 调用计 1 次，重试不计。这是客户实际感受到的失败率。

渠道调用级（质量）    oneapi_channel_attempts_total / oneapi_channel_call_errors_total
                     含重试。RetryTimes=2 时一次用户请求最多计 3 次
                     （首次尝试在重试循环之外）。与用户体验无关。
```

一次用户请求重试 3 次全失败 → 前者记 **1** 次失败，后者记 **3** 次。
**渠道级失败率可能全红而 SLO 完全正常**（重试兜住了）—— 这不矛盾，是两个不同的问题。

**规则：任何除法表达式的分子分母不得跨这两组。**
落地手段是 `rules.yml` 把公式固化成 recording rule，Grafana 只引用固定名字、不手写除法：

| Recording rule | 含义 |
|---|---|
| `oneapi:slo_error_ratio:5m` | 用户请求级失败率（SLO） |
| `oneapi:slo_error_ratio_by_model:5m` | 按模型拆分的 SLO |
| `oneapi:channel_error_ratio:5m` | 渠道调用级失败率（含重试） |
| `oneapi:channel_error_ratio_by_provider:5m` | 按 provider 聚合 |
| `oneapi:rpm:5m` / `oneapi:tpm:5m` | 全局 RPM / TPM |
| `oneapi:rpm_by_model:5m` / `oneapi:tpm_by_model:5m` | **各模型 RPM / TPM** |
| `oneapi:output_tpm_by_model:5m` | 输出 TPM（容量规划关心的） |
| `oneapi:throughput_tokens_per_second:5m` | 实际吞吐 |
| `oneapi:cache_hit_ratio:5m` | 缓存命中率（直接对应成本） |
| `oneapi:llm_duration_p95:10m` / `_p99:10m` | 耗时分位数 |
| `oneapi:llm_first_token_p95:10m` | 首字延迟分位数 |

## 三、允许的偏差（不是 bug，别来提单）

| 偏差 | 允许范围 | 原因 |
|---|---|---|
| `increase(llm_requests_total[30m])` vs `SELECT count(*) FROM logs` | **< 1%** | rate/increase 的外推 + 滚动重启丢失未 scrape 的增量 |
| Prometheus P95 vs `model_metrics` P95 | **≤ 一个桶宽**；**> 30s 的场景以 Prometheus 为准** | 桶上界不同（见下）；DB 侧 `addToHistogram` 是左闭右开 `<`，Prometheus 是 `le`（`<=`） |
| xAI 内容违规请求 | **Prometheus 记 error，DB 记成功消费** | `recordXAIContentViolationCharge` 写的是 `LogTypeConsume`。用户收到 403，**Prometheus 侧才是对的**，两边差值 = 违规量 |
| 吞吐（tokens/s） | 两边不等 | DB 的 `AvgSpeed = SumSpeed/SpeedCount` 是**比值的算术平均**（平均值的平均值），数学上不等于总吞吐。`oneapi:throughput_tokens_per_second:5m` 的 sum/sum 才正确 |
| 关闭"记录消费日志"后 | DB 无新增行，Prometheus **仍在记** | 刻意设计，见下方"埋点位置" |

## 四、开关

| 环境变量 | 默认 | 说明 |
|---|---|---|
| `METRICS_ENABLED` | `false` | 总开关。关掉则 server 不启动、collector 不注册 |
| `METRICS_PORT` | `9090` | 独立监听端口，不复用主服务端口 |
| `METRICS_TOKEN` | 空 | Bearer token。**为空时只接受 loopback 请求** |
| `PPROF_ENABLED` | `false` | 是否挂载 `/debug/pprof/*` |
| `METRICS_LLM_ENABLED` | `true` | 模型维度指标。**回滚阀门**：出问题改环境变量重启即可，不用回滚二进制 |
| `METRICS_CHANNEL_ENABLED` | `true` | 渠道维度指标。渠道数量若远超预期可单独关掉 |
| `METRICS_MAX_MODEL_LABELS` | `500` | 模型名 label 基数上限，超出归并为 `__other__` |
| `METRICS_LATENCY_BUCKETS` | `0.5,1,2,3,5,10,30,60,120,300,600` | 延迟桶边界（秒），避免为改桶重新发版 |

全部只在启动时读取，**不接入 options 表的动态配置** —— 运行时改开关会让时间序列突然消失，
导致 Grafana 图断裂、`rate()` 出现假 reset。

## 五、指标清单

### P0：进程与依赖（约 108 条序列）

| 指标 | 类型 | Labels |
|---|---|---|
| `go_goroutines` / `go_memstats_*`(22) / `go_gc_duration_seconds` / `go_sched_*`(7) | 官方 GoCollector | — |
| `process_open_fds` / `process_max_fds` / `process_resident_memory_bytes` / `process_cpu_seconds_total` | 官方 ProcessCollector | — |
| **`go_sql_wait_count_total`** | Counter | `db_name` |
| `go_sql_{max_open,open,in_use,idle}_connections` | Gauge | `db_name` |
| `go_sql_wait_duration_seconds_total` / `go_sql_max_{idle,idle_time,lifetime}_closed_total` | Counter | `db_name` |
| `oneapi_redis_pool_connections` | Gauge | `state`(total/idle) |
| `oneapi_redis_pool_{hits,misses,timeouts,stale_conns}_total` | Counter | — |

`db_name` 只在单独配置 `LOG_SQL_DSN` 时才有 `log` 值；否则 `LOG_DB == DB`，只导出 `main`
（否则两份相同数字会误导告警）。

### P1 Group A：模型维度 —— **绝不含 `channel_id`**

| 指标 | 类型 | Labels | 满足需求 |
|---|---|---|---|
| `oneapi_llm_requests_total` | Counter | `model`, `outcome` | LLM 请求数、RPM |
| `oneapi_llm_request_errors_total` | Counter | `model`, `reason`(9 值) | 模型错误率（用户级） |
| `oneapi_llm_tokens_total` | Counter | `model`, `kind`(prompt/completion/cached) | Token 数、TPM |
| `oneapi_llm_quota_total` | Counter | `model` | 实时看哪个模型在烧钱（**非账务**） |
| `oneapi_llm_request_duration_seconds` | Histogram | `model`, `stream` | 模型耗时 |
| `oneapi_llm_first_token_seconds` | Histogram | `model` | 首字延迟 |
| `oneapi_llm_no_channel_total` | Counter | `model`, `group` | 哪个模型无可用渠道 |
| `oneapi_llm_requests_by_group_total` | Counter | `group`, `outcome` | 按分组的请求量 |
| `oneapi_metrics_label_overflow_total` | Counter | — | 基数守卫被触发次数 |

### P1 Group B：渠道维度 —— **绝不含 `model`**

| 指标 | 类型 | Labels |
|---|---|---|
| `oneapi_channel_attempts_total` | Counter | `channel_id` |
| `oneapi_channel_call_errors_total` | Counter | `channel_id`, `reason` |
| `oneapi_channel_info` | Gauge(=1) | `channel_id`, `provider`, `channel_type` |

**为什么 `model` 与 `channel_id` 必须分在两组**：`abilities` 表有约 388 个 distinct model，
渠道数量级在百，两者相乘再乘延迟直方图的 14 条序列 ≈ **110 万条时间序列**，
单这一个指标就能打爆 Prometheus。`model_metrics` 表能承受 `(model, provider, channel_id)`
三元组是因为它有小时聚合和唯一索引 —— **不能照搬 DB 的维度设计到 Prometheus**。
需要"某模型在某渠道上的表现"请查 `model_metrics` 表。

`provider` 用 info 指标模式关联，不冗余到各渠道指标的 label 上：

```promql
sum by (provider) (
  rate(oneapi_channel_call_errors_total[5m])
  * on (channel_id) group_left(provider)
    max by (channel_id, provider) (oneapi_channel_info)
)
```
`max by` 不是可选的：多副本时每个 Pod 都暴露相同的 `channel_info`，
直接 `group_left` 会因 many-to-many 报错。

### 错误原因枚举（封闭集合，9 值）

`no_channel`(503) / `rate_limited`(429) / `upstream_5xx`(≥500) / `content_filtered`(403+违规关键词)
/ `auth_failed`(401,403) / `param_invalid`(422) / `bad_request`(400) / `other_4xx` / `unknown`

**为什么不直接用 `relay/model.Error` 的 `Type` 字段**：`relay/util/common.go` 会用上游 JSON 里的
`error.type` **整体覆盖**它，取值完全由上游决定 → 基数无界。`Error.Code` 是 `any` 类型同理。
所以只能以状态码为主键自建映射（`common/metrics/classify.go`），并有单测守住"封闭集合"这个前提。

**没有独立的 `timeout` 分类** —— `RELAY_TIMEOUT` / `STREAMING_TIMEOUT` 超时在上游被包成 502/504，
到分类函数已无法区分，统一归入 `upstream_5xx`。想识别超时只能看延迟直方图最后一桶。

### 桶边界

```
延迟   0.5, 1, 2, 3, 5, 10, 30, 60, 120, 300, 600  （秒）
首字   0.2, 0.5, 1, 2, 3, 5, 10, 20               （秒）
```

前 7 个与 `model.LatencyBoundaries`（`model/model_metrics.go`）**逐值对齐**，
改动任一侧都必须同步改另一侧。因 `common/metrics` 是 leaf package 不能 import `model`，
只能复制 —— 有单测守住对齐关系。

后 4 个是 Prometheus 侧独有：生产 `RELAY_TIMEOUT=1800`、`STREAMING_TIMEOUT=600`，
长流式请求是常态。**DB 侧上界只到 30s，意味着一个 40s 的请求和一个 25 分钟的请求落进同一桶，
`histogram_quantile` 在最后一桶内做线性插值 → P95/P99 是编的。**

## 六、埋点位置（8 处，全部"加一行"）

| 位置 | 记录什么 |
|---|---|
| `model/log.go` `RecordConsumeLogWithOtherAndRequestID` | tokens / quota / 耗时 / 首字延迟。**一处覆盖 text/claude/gemini/audio/image/response 六条链路** |
| `model/log.go` `RecordVideoConsumeLog` | 视频模态（独立写库，不经过上面那个函数） |
| `middleware/distributor.go` 503 分支 | `no_channel_total` + SLO 所需的两个 context 键 |
| `middleware/distributor.go` `SetupContextForSelectedChannel` | 渠道调用尝试 + `channel_info` |
| `controller/relay.go` `processChannelRelayError` **函数体内** | 渠道调用失败 |
| `controller/retry_log.go` `recordFinalErrorLog` | 用户级最终失败 + 流式失败标记 |
| `router/relay-router.go` | 挂载 `middleware.RelayMetrics()` |
| `middleware/metrics.go`（新增） | 用户请求级计数 |

`relay/controller/text.go` **零改动**；12 个重试循环里一行不动。

### 三个必须知道的设计决定

**(1) 埋点在 `LogConsumeEnabled` 早退之前。**
那是 `model/option.go` 可后台动态关闭的开关，而运维关掉它的场景（DB 压力大、磁盘吃紧）
恰好是最需要监控的时刻。放在后面的话，一关开关 tokens 曲线就掉到 0，
与"真的没流量"在 Grafana 上**完全无法区分** —— 这是最坏的一类监控故障。
代价：`speed` 在早退之后才计算，拿不到 → 不导出 speed 指标，吞吐由 PromQL 派生（那也更准确）。

**(2) 渠道失败埋在 `processChannelRelayError` 函数体内，不是各调用点。**
`controller/relay.go` 有 **12 个独立重试循环、30 个 `processChannelRelayError` 调用点**。
在调用点埋只能覆盖 12 条链路中的 1 条，且新增 relay 入口必然遗漏。
埋在函数体内 1 处覆盖全部，且新入口自动被覆盖。

**(3) 流式 200 陷阱。**
SSE 请求一旦响应头写出，中途失败时 `c.Writer.Status()` 仍是 200
（`c.JSON` 只打一条 "headers already written" 警告）。中间件只看状态码会**系统性低估错误率**
—— 而流式恰好是主要流量形态。解法是 `recordFinalErrorLog` 写 `metrics.CtxRelayFailedKey`，
中间件优先读它。副产品：Prometheus 的失败计数与 `logs` 表 `LogTypeError` 行数
由构造保证一致（同一函数体内），对账不需解释。

### 隐式契约（最脆弱的一环，无法用代码强制）

`oneapi_channel_attempts_total` 依赖"每次渠道尝试都经过 `SetupContextForSelectedChannel`"
（目前 13 个调用点全部经过）。将来若有人新增 relay 入口而直接 `GetChannelById` + 自行拼装
context，分母会漏计而分子照常增长 → **渠道错误率虚高甚至超过 100%**。
改动该函数或新增 relay 入口时请一并检查。

## 七、已知缺口

| 缺口 | 影响 | 是否处理 |
|---|---|---|
| 首字延迟只由 openai / gemini / anthropic / xai 四个适配器上报，且仅流式 | 其余模型 `first_token_seconds` 无数据 | 不为补全去改各适配器 |
| `recordFinalErrorLog` 只覆盖 Relay / Gemini / Claude / Response 四条链路 | video / midjourney / ocr / flux / runway / sora 的最终失败只能靠状态码兜底 | 可接受（这些以异步任务提交为主，非流式，状态码准确） |
| 视频模态不记延迟直方图 | 无视频的 P95 | 刻意为之：那里的 duration 是提交耗时而非生成耗时，混入会污染 P95 |
| `classify.go` 的违规关键词与 `relay.go` 的 `isXAIContentViolation` 重复 | 需人工同步 | 不改 relay.go（它参与重试决策，改动影响线上转发行为） |

## 八、主机指标（零代码）

7 项需求全部由 exporter 提供，one-api 本体不改一行 —— 应用进程看不到宿主机的块设备、
网卡和磁盘剩余空间，硬塞进去只能读到容器视角的假象。

**docker-compose**：见 `~/code/one-api-logging`（含 node_exporter + cAdvisor + prometheus-agent，与 promtail 同项目）
**K8s**：装 kube-prometheus-stack（内置 node-exporter DaemonSet + kube-state-metrics + cAdvisor）

```bash
helm install kps prometheus-community/kube-prometheus-stack -n monitoring \
  --set prometheus.prometheusSpec.serviceMonitorSelectorNilUsesHelmValues=false \
  --set prometheus.prometheusSpec.ruleSelectorNilUsesHelmValues=false
```

⚠️ `servicemonitor.yaml` 里 `release: kube-prometheus-stack` 是**占位值**，装完 chart
必须核对成实际 release 名 —— label 不匹配 = ServiceMonitor 被静默忽略，
是最常见的接入失败原因。

### 7 项需求的 PromQL

```promql
# ① CPU 使用率
100 - (avg by (instance) (rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)

# ② 内存（宿主机）—— 用 MemAvailable 不要用 MemFree（后者不含可回收 cache，永远看着很低）
(1 - node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes) * 100

# ③ 磁盘：预测比静态阈值有用得多
100 * (1 - node_filesystem_avail_bytes{fstype!~"tmpfs|overlay"} / node_filesystem_size_bytes{fstype!~"tmpfs|overlay"})
predict_linear(node_filesystem_avail_bytes{mountpoint="/"}[6h], 4*24*3600) < 0

# ④ IO：利用率 + 饱和度（后者 >1 说明在排队）
rate(node_disk_io_time_seconds_total[5m])
rate(node_disk_io_time_weighted_seconds_total[5m])

# ⑤ 网络（必须排除虚拟网卡，否则容器流量被重复计算）
rate(node_network_receive_bytes_total{device!~"lo|veth.*|docker.*|br-.*|cni.*"}[5m]) * 8
node_nf_conntrack_entries / node_nf_conntrack_entries_limit   # 网关重点：打满 = 连不上上游

# ⑥ Load（必须按核数归一，绝对值跨机型无意义）
node_load5 / count by (instance) (node_cpu_seconds_total{mode="idle"})

# ⑦ 文件系统
node_filesystem_files_free / node_filesystem_files   # inode 余量
node_filesystem_readonly == 1                        # 只读 = 磁盘故障前兆
```

### 容器环境的两个坑

**坑 a：容器内存绝不能看 `node_memory_*`。**
宿主机 128G、Pod limit 4G 时，容器 3.9G 濒临 OOMKill 而宿主机指标显示内存使用率 5%，
一切正常。必须用：

```promql
container_memory_working_set_bytes / container_spec_memory_limit_bytes > 0.9
```

用 `working_set` 而非 `usage` 的理由：**`working_set` 正是 kubelet 判定 OOMKill 的那个值**
（`usage` 含可回收的 page cache，会虚高）。
P0 的 `process_resident_memory_bytes` 是进程 RSS，可与它对照 —— 差值 ≈ 容器内 page cache。

**坑 b：Load Average 在容器里不隔离。**
`/proc/loadavg` 不受 cgroup namespace 隔离，容器读到的是**整台宿主机**的 load。
8 核节点跑 20 个 Pod，每个都报 load=15，实际每个只用 0.2 核 ——
**这个指标在容器里给不出任何 Pod 级信息**。Pod 级"CPU 不够"的真信号是 CFS 限流：

```promql
rate(container_cpu_cfs_throttled_periods_total[5m]) / rate(container_cpu_cfs_periods_total[5m]) > 0.05
```

它与 P0 的 `go_sched_latencies_seconds` 是同一现象的两面（CFS 按住进程 → goroutine 调度延迟飙升
→ 表现为"服务整体变慢但看不出哪里慢"）。两条告警同时响 = **CPU limit 设小了，不是代码问题**。

**compose 下的补充坑**：没设 limit 时 `container_spec_memory_limit_bytes` 返回 0 或宿主机总量，
所有比值分母失效。表达式已用 `clamp_min(..., 1)` 兜底，但仍应给 one-api 加 `deploy.resources.limits`。

## 九、告警（规则在 monitor 仓库）

告警与 recording 规则**不在本仓库** —— 它们在接收端评估，归 `~/code/monitor/prometheus/`：

| 文件 | 内容 |
|---|---|
| `monitor/prometheus/alerts.yml` | 34 条告警（依赖 / runtime / 可用性 / 主机 / 容器 / 业务 / 自监控） |
| `monitor/prometheus/rules.yml` | 15 条 recording rule（两种错误率、RPM/TPM、分位数、agent 存活） |
| `monitor/prometheus/alerts_test.yml` | promtool 单测，锁住 4 类"语法合法但静默失效"的 bug |

校验：`cd ~/code/monitor && make prom-check`

**当前不发送任何告警通知**（monitor 侧有意为之）：告警只以 `ALERTS{alertstate="firing"}`
呈现在 Grafana「指标告警总览」面板，没有人会被主动通知。详见
`~/code/monitor/CLAUDE.md` 的「当前不发送任何告警通知」一节。

迁移时对本仓库原有的 31 条规则做了 5 处适配（都是 remote_write 架构导致的）：
severity 归一到 `critical/high/medium/low`、补 `category`、新增 `MetricsAgentGone`
（`up == 0` 在 agent 挂掉时永远静默）、`container` label 改 `name`（原生 cAdvisor 没有
`container`）、全局聚合加 `site` 维度。


## 十、生产是推送式，不是拉取式

生产环境的指标不由监控服务器拉取，而是**业务服务器上的 agent 推送**：

```
业务服务器                              监控服务器（monitor 仓库）
one-api :9090/metrics  ──┐
node_exporter :9100    ──┼─▶ prometheus-agent ──remote_write──▶ nginx :3100
cadvisor :8080         ──┘                                        /api/v1/write
                                                                       ▼
                                                                  Prometheus
```

理由：与现有 Loki 日志链路同构，业务服务器只需**出站**连接，不必开入站端口改安全组。

部署在 `~/code/one-api-logging`（与 promtail 同项目），其中三个强制项漏一个都会出问题
（`external_labels.site`、job 名以 `one-api` 开头、token 用 `printf` 生成）——
详见该项目的 `entrypoint.sh` 与 `~/code/monitor/README.md` 的「指标接入契约」。

**推送式丢失的能力**：监控服务器的 `/targets` 页面是空的，排查"某台业务服务器的
exporter 配错了没"必须 ssh 到那台机器看 `docker logs prometheus-agent`。

## 十一、本地启动

### 最快路径

```bash
# 终端 1：起 one-api（METRICS_PORT 用 9099，避开 Prometheus 自己的 9090）
METRICS_ENABLED=true METRICS_PORT=9099 METRICS_TOKEN=devtoken go run .

# 终端 2：起 Prometheus
./deploy/prometheus/run-local.sh
```

然后开 <http://localhost:19090/targets> 确认 `one-api` 是 **UP**。

脚本做的事：探测 one-api 是否就绪 → 探测容器能用哪个地址访问宿主机 →
生成临时配置（含真实 token）→ 起容器 → 等 target UP。

### 为什么需要这个脚本（两个坑，实测踩过）

**坑 1：`host.docker.internal` 不一定可用。**
在部分 Docker Desktop 配置下它解析成 `198.18.0.10` 但**完全不可达**（curl 超时）。
更坑的是 **busybox 的 `nc -z` 会假阳性报"可达"**，用它排查会得到完全错误的结论。
脚本会自动回落到宿主机 LAN IP（用 `ipconfig getifaddr en0`）。

正确的连通性验证方式：
```bash
docker run --rm curlimages/curl:latest -sv --max-time 5 http://host.docker.internal:9099/metrics
# 看 "Trying <ip>:9099... " 后面是 Connected 还是 timed out
```

**坑 2：Prometheus 不展开配置文件里的 `${ENV_VAR}`。**
写 `credentials: '${METRICS_TOKEN}'` 并设置该环境变量，Prometheus 会把**这 17 个字符原样**
当 token 发出去 → 401。现象极其隐蔽：配置看着完全正确，`promtool check config` 也 SUCCESS，
只有 target DOWN。

实测对照（同一个 endpoint，同时配两个 job）：

| 写法 | 结果 |
|---|---|
| `credentials: '${METRICS_TOKEN}'` + `-e METRICS_TOKEN=devtoken` | `health=down`，`server returned HTTP status 401 Unauthorized` |
| `credentials: 'devtoken'` | `health=up` |

所以生产配置（`prometheus.yml`）用 `credentials_file` 挂文件：
```bash
printf '%s' "$METRICS_TOKEN" > deploy/prometheus/.metrics-token
```
**必须用 `printf` 不能用 `echo`** —— `echo` 会加换行，Prometheus 把换行也发出去，同样 401。
该文件已在 `.gitignore` 里。

### 完整 stack（含 node_exporter + cAdvisor + Grafana）

业务服务器侧的 agent + exporter 部署已移至 `~/code/one-api-logging`（与 promtail 同项目），
见该项目的 README。本地验证指标导出用上面的 `run-local.sh`（只起一个拉取式 Prometheus）。

Grafana 在 `~/code/monitor` 跑着，Prometheus datasource 指向 `http://prometheus:9090`。

### 手动起（不想用脚本时）

```bash
HOST_ADDR=$(ipconfig getifaddr en0)      # macOS；Linux 用 docker0 网关或 --network host
mkdir -p /tmp/promlocal && cp deploy/prometheus/{alerts,rules}.yml /tmp/promlocal/
cat > /tmp/promlocal/prometheus.yml <<EOF
global: { scrape_interval: 5s, evaluation_interval: 5s }
rule_files: ['/etc/prometheus/alerts.yml', '/etc/prometheus/rules.yml']
scrape_configs:
  - job_name: one-api
    static_configs: [{ targets: ['${HOST_ADDR}:9099'] }]
    authorization: { type: Bearer, credentials: 'devtoken' }
EOF
docker run -d --name one-api-prom-local -p 19090:9090 \
  -v /tmp/promlocal:/etc/prometheus:ro prom/prometheus:v3.7.3 \
  --config.file=/etc/prometheus/prometheus.yml
```

停止：`docker rm -f one-api-prom-local`

### 起来之后先看这几个

```
http://localhost:19090/targets    # target 必须 UP
http://localhost:19090/alerts     # 31 条告警是否都加载了
```

PromQL 冒烟测试（在 <http://localhost:19090/graph> 里跑）：
```promql
go_sql_max_open_connections            # P0：连接池上限
go_goroutines                          # P0：goroutine 数
oneapi_llm_requests_total              # P1：LLM 请求数
oneapi:rpm_by_model:5m                 # P1：各模型 RPM（recording rule）
oneapi:slo_error_ratio:5m              # P1：用户请求级失败率
```

**注意**：recording rule 的 group 是 `interval: 30s`，且 `rate()` 需要窗口内有
≥2 个采样点。刚启动时查会显示"无数据"，**这不是配置错误** ——
持续打点流量并等一个评估周期（约 30–60s）再看。

### 没有 Docker 的话

```bash
brew install prometheus
prometheus --config.file=deploy/prometheus/prometheus.local.yml \
           --web.listen-address=:19090
```
原生跑完全绕开容器网络，`prometheus.local.yml` 里把 target 改成 `127.0.0.1:9099` 即可。

## 十二、验证

```bash
# 编译与单测
go build ./... && go test ./common/metrics/... ./middleware/... -race

# 规则语法与逻辑
docker run --rm -v "$PWD/deploy/prometheus:/rules:ro" --entrypoint promtool \
  prom/prometheus:v3.7.3 check rules /rules/alerts.yml /rules/rules.yml
docker run --rm -v "$PWD/deploy/prometheus:/rules:ro" -w /rules --entrypoint promtool \
  prom/prometheus:v3.7.3 test rules /rules/alerts_test.yml

# 鉴权与隔离
curl -s -o /dev/null -w '%{http_code}\n' localhost:9090/metrics            # 401
curl -s -H 'Authorization: Bearer <token>' localhost:9090/metrics | head   # 200
curl -s -o /dev/null -w '%{http_code}\n' localhost:3000/debug/pprof/       # 404（DefaultServeMux 未被污染）

# 基数自检：某个 metric 的序列数若持续爬升不收敛，说明 label 打错了
curl -s -H 'Authorization: Bearer <token>' localhost:9090/metrics \
  | grep -v '^#' | awk -F'{' '{print $1}' | sort | uniq -c | sort -rn | head
```

### 两个"证明机制真的生效"的压测

**DB collector 的实时性**（证明是 `Collect()` 时取快照而非注册时定格）：
```bash
SQL_MAX_OPEN_CONNS=1 METRICS_ENABLED=true METRICS_TOKEN=dev ./one-api
for i in $(seq 1 60); do curl -s -o /dev/null -X POST localhost:3000/api/user/login \
  -H 'Content-Type: application/json' -d '{"username":"x","password":"x"}' & done; wait
```
期望 `go_sql_wait_count_total` 从 0 明显增长（实测 60 并发下涨到 120）。恒为 0 说明接错了。

**基数守卫的硬上限**：
```bash
METRICS_MAX_MODEL_LABELS=50 METRICS_ENABLED=true METRICS_TOKEN=dev ./one-api
# 并发发 200 个随机 model 名的请求（全部走 503 无渠道路径）
curl -s -H 'Authorization: Bearer dev' localhost:9090/metrics \
  | grep '^oneapi_llm_no_channel_total' | sed -E 's/.*model="([^"]*)".*/\1/' | sort -u | wc -l
```
期望精确停在 **51**（50 上限 + `__other__`），且 `oneapi_metrics_label_overflow_total > 0`。

### 与 DB 对账（上线后做一次，结果归档）

```promql
increase(oneapi_llm_requests_total{outcome="success"}[30m])
```
对比 `SELECT count(*) FROM logs WHERE type=2 AND created_at BETWEEN ...`，偏差应 < 1%。
超出则检查是否有链路漏埋点。

## 十三、pprof

默认关闭。需要时 `PPROF_ENABLED=true` 滚动重启，用完关掉。

```bash
kubectl port-forward pod/one-api-xxx 9090:9090
curl -s -H 'Authorization: Bearer <token>' 'localhost:9090/debug/pprof/goroutine?debug=1' > g.txt
curl -s -H 'Authorization: Bearer <token>' localhost:9090/debug/pprof/heap > heap.pprof
go tool pprof -http=: heap.pprof
```

- `goroutine?debug=2` 在 5000+ goroutine 时会 dump 几十 MB 并短暂 STW
- `profile?seconds=30` 期间 CPU 约 +5%
- **`/debug/pprof/heap` 可能包含 API key 明文**，导出的文件不要外发

## 十四、后续（尚未实现）

- **全站 HTTP RED 指标**：目前 `middleware/logger.go` 会跳过 `status<400 且延迟<5000ms` 的请求，
  所以 Loki 里没有正常请求记录。P1 的 `oneapi_llm_requests_total` 覆盖了 relay 流量，
  但 `/api/*`（管理后台）与 web 仍无请求量指标。补法是再加一个 gin 中间件，
  注意必须注册在 `gin.Recovery()` **之前**（defer 是 LIFO，否则 panic 全被记成 2xx）。
- **多副本聚合**：`counter` 重启归零 → 滚动更新期间曲线有小凹陷，属正常。
  所有 gauge 需 `avg/max by (pod)` 而非直接取值。
