# 引入 Prometheus（P0：进程与依赖健康度 + pprof）

- **日期**：2026-07-31
- **分支**：`prometheus-p0`
- **范围**：P0，零业务代码侵入

## 1. 背景与目标

项目已有两套可观测能力，但都不覆盖**进程内部状态**：

| 已有 | 实现 | 覆盖 |
|---|---|---|
| 业务指标 | `model_metrics` 表（小时预聚合）+ 内存直方图 | 模型/渠道请求量、p50/p95/p99、首字延迟、tokens/s |
| 日志栈 | 结构化 JSON 三流 + request-id 透传 + Promtail→Loki→Grafana | 错误与慢请求的单条排查 |

经代码核实的真实盲区（本次要补的就是这三块）：

1. **进程内部状态无时序数据。** `main.go` 的 `monitorGoroutines()` 每 30s 打一行字符串日志，
   无法配告警、无趋势、无历史。而 `common/gopool.go:19` 的 `relayGoPool` 容量是
   `math.MaxInt32`（无界池），goroutine 泄漏是结构性隐患。**且项目没有 pprof，泄漏了也定位不了。**
2. **DB / Redis 连接池完全盲区。** 生产 `SQL_MAX_OPEN_CONNS=300` 跨公网连阿里云 RDS，
   Redis 是 AWS serverless ElastiCache（有 scale 抖动）。`sql.DBStats.WaitCount`
   这类"连接池已排队"的信号只存在于进程内存，DB 里没有、日志里也没有。
3. **没有趋势告警。** 现有告警只有"渠道被禁用"这类离散事件的飞书通知。

**目标**：一个受保护的 `/metrics` + `/debug/pprof/*` 端点（独立端口），一组首批告警规则。

## 2. 方案设计

### 新增 leaf 包 `common/metrics/`

- `registry.go` — 私有 `prometheus.Registry`（**不用 `DefaultRegisterer`**，避免第三方库
  偷偷注册）；注册官方 GoCollector（额外开启 `/sched/*` 运行时指标）与 ProcessCollector
- `collectors.go` — `RegisterDB(name, *sql.DB)` 复用**官方** `collectors.NewDBStatsCollector`；
  `RegisterRedis(func() *redis.PoolStats)` 是自实现的 collector（go-redis v8 无官方实现）
- `server.go` — 独立 `http.Server` + 独立 `http.ServeMux`；Bearer token 常量时间比较；
  pprof 五个 handler **显式注册**（不用 `import _ "net/http/pprof"`，那会污染 `DefaultServeMux`）

**硬约束**：本包只 import `common/config`、`common/env`、`common/logger`，
禁止 import `model` / `monitor` / `middleware` / `controller`。需要读 DB/Redis 的地方
由 `main.go` 注入。原因：未来若在 `model/log.go` 埋点，而 `model` 已被全项目依赖，
反向 import 会成环。

### 关键设计决策

**复用官方 DBStatsCollector 而非自写。** 计划初稿打算自己实现 `oneapi_db_*`，
后来发现 `client_golang` 已提供 `collectors.NewDBStatsCollector`，覆盖全部所需字段且
在 `Collect()` 时才调 `db.Stats()`。改用官方实现后指标名为 `go_sql_*`（带 `db_name` label）。

**不复用 `middleware.RootAuth()` 鉴权 `/metrics`。** 它走 `model.ValidateAccessToken`，
每次 scrape 打一次 DB；更致命的是 **DB 挂了 `/metrics` 会一起挂**——而那正是最需要看
`go_sql_wait_count_total` 的时刻。监控端点绝不能依赖被监控对象的依赖。

**独立端口而非挂 gin 路由。** 避免经过 CORS → `GlobalAPIRateLimit`（打 Redis，消耗限流配额）
→ `SetUpLogger`（污染 access log）→ dashboard 组的 gzip。同时让端口不对外暴露成为第一道防线。

**`LOG_DB == DB` 必须判等。** `LOG_SQL_DSN` 未设置时（生产即此配置）`LOG_DB` 就是 `DB`，
不判等会导出 `db_name="main"` 与 `db_name="log"` 两份完全相同的数字，误导告警。

**移除 `monitorGoroutines()` 里的 `runtime.ReadMemStats`。** 那是 stop-the-world 操作，
每 30s 一次纯属浪费；GoCollector 基于 `runtime/metrics`，开销低得多。
按需查看仍可走 `/api/monitor/health`（未改动）。

### 配置项（`common/config/config.go`）

`METRICS_ENABLED`(false) / `METRICS_PORT`(9090) / `METRICS_TOKEN`(空→只允许 loopback) /
`PPROF_ENABLED`(false)。**故意不接入 options 表动态配置**：指标注册在启动时完成，
运行时改开关会让序列突然消失，导致图断裂、`rate()` 假 reset。

### 明确不做

| 候选 | 理由 |
|---|---|
| relay / model / channel 维度指标 | 与 `model_metrics` 重叠，会产生"两套数字对不上"的治理成本 |
| 渠道启禁状态 gauge | K8s 多副本下是"全局真相"，各 Pod 内存 cache 不同步会导出矛盾值 |
| quota / 费用 counter | counter 重启归零，`increase()` 是估算值，不可对账。`logs.quota` 是唯一真相源 |
| user_id / token_id / request_id 维度 | 基数无界。这类下钻走 `controller/log.go` 的 DB 查询 |
| OpenTelemetry / 分布式追踪 | 单进程 + request-id 全链路日志已够用，10 倍复杂度换不到 2 倍信息 |

## 3. 影响范围

| 项 | 评估 |
|---|---|
| 业务逻辑 | **零侵入**。未改动 `relay/`、`controller/`、`model/`、`middleware/` 任何文件 |
| 改动文件 | 新增 `common/metrics/{registry,collectors,server}.go`、`deploy/prometheus/*`；修改 `main.go`、`common/config/config.go`、`go.mod`、`go.sum` |
| 新增依赖 | `prometheus/client_golang v1.24.1`（间接引入 `client_model`、`prometheus/common`、`procfs`、`beorn7/perks`、`klauspost/compress` 等）。**顺带升级了 5 个 `golang.org/x/*` 包**（crypto/net/sync/sys/text），已验证编译通过 |
| 数据库 | **无 schema 变更、无新表、scrape 路径零 DB 查询** |
| 前端 | 无改动 |
| 序列数 | 约 110 条，全部固定小基数，无基数爆炸风险 |
| 镜像构建 | `Dockerfile` 的 `go mod download` 在 `COPY . .` 之前，加依赖后该层缓存失效，首次构建变慢（一次性） |
| 回滚 | `METRICS_ENABLED=false` 即完全消失 |

**风险**：
1. pprof 采集时开销（`goroutine?debug=2` 在 5000+ goroutine 时会 dump 几十 MB 并短暂 STW；
   `profile?seconds=30` 期间 CPU +~5%）。按需触发，默认关闭。
2. `/debug/pprof/heap` 可能含 API key 明文。必须有 token + 端口不对外。
3. Prometheus 本体的持续运维成本（部署、保留期、告警规则维护）。Grafana 已有，边际成本低。

## 4. 验证方式

已在本地完成的验证（`go build ./...` 与改动范围内 `go vet` 均通过；
`go vet ./...` 另有 `relay/controller/image.go`、`controller/relay.go` 的
non-constant format string 报错，属**既有问题**，与本次改动无关）：

| 验证项 | 结果 |
|---|---|
| 无 token / 错误 token 访问 `/metrics` | 均 **401** |
| 正确 token | **200** |
| `db_name` 值 | 只有 `main`，无 `log`（`LOG_DB==DB` 判等生效） |
| Redis 禁用时 | 无任何 `oneapi_redis_*` 序列 |
| runtime / process 指标 | `go_goroutines`、`go_sched_latencies_seconds`、`process_open_fds`、`process_max_fds`、`process_resident_memory_bytes` 均存在 |
| **主端口 `/debug/pprof/` 与 `/metrics`** | 均 **404**，证明 `http.DefaultServeMux` 未被污染 |
| metrics 端口 pprof | 带 token **200**，无 token **401**，goroutine dump 正常 |
| **DB collector 专项**（`SQL_MAX_OPEN_CONNS=1` + 60 并发登录） | `go_sql_wait_count_total` **0 → 120**、`wait_duration_seconds_total` **0 → 1.29s**，证明是 `Collect()` 时实时取快照 |
| **Redis collector 专项**（60 并发登录） | `pool_hits_total` **1 → 354**、`pool_connections` **1 → 8**；Gauge/Counter 类型声明正确 |
| 序列总数 | 108 条 |

**上线后**：
- Prometheus Targets 页面 target `UP`，scrape duration <100ms
- 连续观察 24h `go_goroutines` 与 `process_resident_memory_bytes` 是否单调上升
  （这是引入 pprof 后第一次能回答的问题）
- 人为调小 `SQL_MAX_OPEN_CONNS` 触发一次告警，确认通知链路可达

## 5. 后续（本次不做）

- **HTTP RED 指标**：`middleware/logger.go` 会跳过 `status<400 且延迟<5000ms` 的请求，
  Loki 里没有正常请求记录，**当前算不出 QPS 和 5xx 比例**。一个 gin 中间件即可补上，
  必须注册在 `gin.Recovery()` **之前**（defer 是 LIFO，否则 panic 全被记成 2xx）。
- **渠道质量与调度**：增强现有 `model_metrics`，不要用 Prometheus 造第二套。
- **凭据泄露**：`docker-compose.yml` 已被 git 跟踪且含明文 RDS/Redis 生产凭据，
  `.gitignore` 未覆盖。本次未处理（用户决定暂缓），但 `METRICS_TOKEN` 不应写入该文件。
