# 模型监控系统 (Model Metrics Monitoring)

> 最后更新: 2026-03-27
> 状态: 已实现，已通过代码审查

## 一、功能概述

为模型广场中的每个模型增加性能监控详情页，类似 OpenRouter 的模型监控。

**核心原则：**
- 对外（客户）：按 provider 维度展示聚合数据，绝不暴露 channel ID 等系统机密
- 对内（管理员）：可下钻到 channel 级别的详细数据
- 高可用、可扩展、高性能

**展示的监控指标：**
- 成功率、平均延迟、平均速度 (TPS)、RPM/TPM
- P50/P95/P99 延迟百分位、TTFT（首 Token 延迟）
- 延迟/速度/成功率趋势图、Token 用量柱状图
- 定价详情、用户等级折扣
- 管理员渠道明细（成功率、延迟、速度、请求量）

---

## 二、架构总览

```
请求处理 → 写 Log + 更新直方图(内存,O(1))
                    │
                    ▼
         聚合 Worker (每5分钟)
         ├── 从 logs 表 GROUP BY 聚合基础指标
         ├── 从内存累加器取直方图快照（深拷贝）
         ├── UPSERT → model_metrics 表（channel级 + provider级）
         ├── 清理过期数据
         └── 刷新内存缓存 + Redis缓存
                    │
                    ▼
         API 层（3个端点）
         ├── /metrics/all       → 内存缓存（全模型迷你摘要）
         ├── /metrics/detail    → 内存缓存（单模型详情 + 管理员channel明细）
         └── /metrics/timeseries → 1h:查logs | 24h:内存缓存 | 7d/30d:查DB+Redis
                    │
                    ▼
         前端模型详情页 /model-plaza/[model]
```

---

## 三、文件清单

### 后端新建文件

| 文件 | 用途 | 关键导出 |
|------|------|---------|
| `model/model_metrics.go` | 表结构、直方图工具、DB 查询 | `ModelMetrics`, `HistogramBuckets`, `AggregateLogsForHour()`, `UpsertModelMetrics()`, `EstimatePercentile()` |
| `model/model_metrics_aggregator.go` | 后台聚合 Worker（自动重启） | `StartModelMetricsAggregator()` |
| `model/model_metrics_cache.go` | 内存缓存 + Redis + 时间序列查询 | `RefreshMetricsCache()`, `GetCachedAllModelMini()`, `GetCachedModelSummary()`, `GetCachedModel24hSeries()`, `GetModelTimeSeriesDaily()`, `GetModelTimeSeries1h()` |
| `model/model_metrics_histogram.go` | 实时直方图累加器（零DB开销） | `RecordMetricsHistogram()`, `SnapshotHistogramsForHour()`, `ClearHistogramAccumulator()` |
| `controller/model_metrics.go` | 3 个 API 端点 | `GetAllModelMetricsMini()`, `GetModelMetricsDetail()`, `GetModelMetricsTimeSeries()` |

### 后端修改文件

| 文件 | 修改内容 |
|------|----------|
| `middleware/auth.go` | 新增 `TryUserAuth()` 可选认证中间件 |
| `router/api-router.go` | 注册 3 个 metrics 路由 |
| `common/config/config.go` | 新增 4 个配置变量 |
| `model/option.go` | 注册 `ModelMetricsEnabled` 在线开关 |
| `model/log.go` | 在 `RecordConsumeLogWithOtherAndRequestID` 末尾调用 `RecordMetricsHistogram()` |
| `model/main.go` | `AutoMigrate(&ModelMetrics{})` |
| `main.go` | 启动聚合 Worker goroutine |

### 前端新建文件（ezlinkai-web-next）

| 文件 | 用途 |
|------|------|
| `lib/types/model-metrics.ts` | TypeScript 类型定义 |
| `app/model-plaza/[model]/page.tsx` | 详情页路由入口 |
| `sections/model-plaza/model-detail-view.tsx` | 详情页主组件（数据获取 + 布局） |
| `sections/model-plaza/components/metric-card.tsx` | 指标卡片 |
| `sections/model-plaza/components/metrics-chart.tsx` | 通用图表（Area/Bar） |
| `sections/model-plaza/components/time-range-selector.tsx` | 时间范围切换 |
| `sections/model-plaza/components/status-badge.tsx` | 健康状态圆点 |
| `sections/model-plaza/components/channel-detail-table.tsx` | 管理员渠道明细表 |
| `app/api/model-plaza/metrics/*/route.ts` | 3 个 API 代理路由 |

### 前端修改文件

| 文件 | 修改内容 |
|------|----------|
| `sections/model-plaza/model-plaza-view.tsx` | 卡片加健康徽章 + 延迟/速度 + 点击跳转 |
| `locales/zh.ts` | 新增 `modelDetail` i18n 段 |
| `locales/en.ts` | 新增 `modelDetail` i18n 段 |

---

## 四、数据库表 `model_metrics`

```sql
-- 存储在 LOG_DB（与 logs 表同库）
-- 小时粒度预聚合

字段:
  model_name        VARCHAR(200)  -- 模型名
  provider          VARCHAR(100)  -- 供应商（来自 logs 表，可为空）
  channel_id        INT           -- 0=provider级汇总, >0=channel级明细
  hour_timestamp    BIGINT        -- UTC 小时起始时间戳
  total_requests    BIGINT        -- 请求总数
  success_requests  BIGINT        -- 成功请求数（type=2）
  error_requests    BIGINT        -- 错误请求数（type=5）
  stream_requests   BIGINT        -- 流式请求数
  total_tokens      BIGINT        -- 总 token 数
  prompt_tokens     BIGINT
  completion_tokens BIGINT
  cached_tokens     BIGINT
  total_quota       BIGINT        -- 总 quota 消耗
  sum_duration      FLOAT         -- 延迟累加值（读时除以 total_requests 算均值）
  sum_speed         FLOAT         -- 速度累加值
  speed_count       BIGINT        -- 有效速度记录数
  sum_first_word    FLOAT         -- TTFT 累加值
  first_word_count  BIGINT        -- 有效 TTFT 记录数
  latency_buckets   TEXT          -- 延迟直方图 JSON
  speed_buckets     TEXT          -- 速度直方图 JSON

索引:
  idx_mm_upsert (UNIQUE):     (model_name, provider, channel_id, hour_timestamp) → UPSERT
  idx_mm_model_channel_hour:  (model_name, channel_id, hour_timestamp) → 单模型查询
  idx_mm_channel_hour:        (channel_id, hour_timestamp) → 全模型聚合
  idx_mm_hour:                (hour_timestamp) → 过期清理
```

---

## 五、API 端点

### `GET /api/model-plaza/metrics/all`（公开）
返回所有模型迷你摘要，用于列表页健康徽章。
```json
{ "success": true, "data": { "gpt-4o": { "success_rate": 0.985, "avg_latency": 2.3, "avg_speed": 45.2, "total_requests_24h": 15234, "status": "healthy" } } }
```

### `GET /api/model-plaza/metrics/detail?model_name=xxx`（公开 + 管理员增强）
- 中间件: `TryUserAuth()`
- 公开: current, period_24h, pricing
- 管理员额外: channels 数组（各 channel 的独立指标）
- 当 summary 为空时也返回 pricing 和 channels（不 early return）

### `GET /api/model-plaza/metrics/timeseries?model_name=xxx&period=24h`（公开）
- `1h`: 实时查 logs 表，5分钟粒度，12个点
- `24h`: 内存缓存，小时粒度，25个点（无数据时返回零值点）
- `7d`: 查 model_metrics 表，天粒度，Redis 缓存 30min
- `30d`: 查 model_metrics 表，天粒度，Redis 缓存 60min

---

## 六、配置项

| 环境变量 | 默认值 | 说明 |
|----------|--------|------|
| `MODEL_METRICS_ENABLED` | `true` | 总开关（支持在线切换） |
| `MODEL_METRICS_AGGREGATION_INTERVAL` | `300` | 聚合间隔（秒） |
| `MODEL_METRICS_RETENTION_DAYS` | `30` | 数据保留天数 |
| `MODEL_METRICS_BACKFILL_DAYS` | `7` | 首次启动回填天数 |

---

## 七、对 logs 表的依赖

| 场景 | 查 logs? | 频率 | 查询范围 |
|------|---------|------|---------|
| 聚合 Worker 基础指标 | 是 | 每5分钟 | 当前小时 GROUP BY |
| 直方图 P50/P95/P99 | **否** | - | 内存增量累加 |
| 1h 实时时间序列 | 是 | 用户触发 | 单模型 + 60分钟 |
| 24h/7d/30d | 否 | - | 读 model_metrics |

**不会修改 logs 表，不会添加 logs 索引。**

---

## 八、高可用设计

| 机制 | 说明 |
|------|------|
| Worker 崩溃自动重启 | `StartModelMetricsAggregator` 外层无限循环 + 内层 panic recover，30s 后重启 |
| 每次聚合独立 recover | `safeRun()` 包裹每个操作，单次 panic 不影响后续 |
| 内存缓存始终可用 | 不依赖 Redis，Redis 仅作加速 |
| 直方图内存自动清理 | 超过 2 小时的旧数据自动删除，防止内存泄漏 |
| 功能在线开关 | `ModelMetricsEnabled` 支持运行时切换 |
| 24h 空数据保底 | 返回零值时间点序列，保证图表框架渲染 |

---

## 九、已知限制和后续优化

### 已知限制
1. **Provider 字段为空**: logs 表中 provider 未在写入时填充，当前所有 metrics 的 provider="" 。不影响功能，但 provider 维度的区分依赖 channel 信息补充
2. **缓存全量刷新**: `RefreshMetricsCache` 每次查询完整 24h 数据。高流量场景可优化为增量更新
3. **直方图首次回填无数据**: 内存直方图在服务重启后丢失历史数据，回填的小时只有基础指标无 P50/P95/P99

### 可优化方向
1. **增量缓存刷新**: 只查 `updated_at > lastRefresh` 的行
2. **Provider 填充**: 在 relay 层传递 provider 信息到 log 写入
3. **预计算日级聚合**: 在 model_metrics 表增加 day 粒度行，减少 7d/30d 查询计算量
4. **直方图持久化**: 将直方图写入 model_metrics 表后，重启可恢复 P50/P95/P99 历史
5. **WebSocket 推送**: 替代前端 5 分钟轮询，实现实时更新

---

## 十、本地验证

```bash
# 后端编译检查
cd ezlinkai && go build ./... && go vet ./...

# 前端 lint 检查
cd ezlinkai-web-next && npx next lint

# 功能验证
# 1. 启动后端，观察日志: "model metrics aggregator: starting" + "backfill completed"
# 2. 等待 5 分钟让 Worker 运行一轮
# 3. 访问 /model-plaza，点击模型卡片进入详情页
# 4. 切换 1H/24H/7D/30D，验证图表渲染
# 5. 用管理员登录，验证底部出现「渠道明细」卡片
# 6. API 直接测试:
curl http://localhost:3000/api/model-plaza/metrics/all
curl http://localhost:3000/api/model-plaza/metrics/detail?model_name=gpt-4o
curl http://localhost:3000/api/model-plaza/metrics/timeseries?model_name=gpt-4o&period=24h
```

---

## 十一、文件依赖关系

```
model/log.go
  └─→ model/model_metrics_histogram.go (RecordMetricsHistogram)
        └─→ model/model_metrics.go (HistogramBuckets, addToHistogram)

main.go
  └─→ model/model_metrics_aggregator.go (StartModelMetricsAggregator)
        ├─→ model/model_metrics.go (AggregateLogsForHour, UpsertModelMetrics)
        ├─→ model/model_metrics_histogram.go (SnapshotHistogramsForHour)
        └─→ model/model_metrics_cache.go (RefreshMetricsCache)
              └─→ model/model_metrics.go (GetModelMetricsRange, EstimatePercentile)

controller/model_metrics.go
  ├─→ model/model_metrics_cache.go (GetCached*, GetModelTimeSeries*)
  └─→ controller/model_plaza.go (getModelInfoFromChannels, buildPriceMap)

middleware/auth.go (TryUserAuth)
  └─→ model/user.go (ValidateAccessToken)

前端:
model-detail-view.tsx
  ├─→ components/metric-card.tsx
  ├─→ components/metrics-chart.tsx
  ├─→ components/status-badge.tsx
  ├─→ components/time-range-selector.tsx
  └─→ components/channel-detail-table.tsx
```
