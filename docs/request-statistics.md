# 分钟报表配置

所有节点默认提供成功率、模型统计和失败明细查询，沿用已有登录与用户数据权限。
各节点连接相同的日志数据库：优先使用 `LOG_SQL_DSN`，未配置时使用 `SQL_DSN`。
报表需要 MySQL 8 或支持当前 SQL 的兼容数据库。

只在负责聚合的主节点设置：

```env
NODE_TYPE=master
REQUEST_STATS_WORKER_ENABLED=true
```

其他节点不需要任何 `REQUEST_STATS_*` 配置。主节点不配置 Worker 开关时也只提供查询；
非主节点即使设置 `REQUEST_STATS_WORKER_ENABLED=true`，也不会启动聚合或创建 Worker 连接池。
多个主节点的聚合、重算和清理仍通过同一数据库连接上的 `GET_LOCK` 互斥。

`REQUEST_STATS_ENABLED`、`REQUEST_STATS_SOURCE_RETAINED_FROM`、
`REQUEST_STATS_COVERAGE_VERIFIED`、`REQUEST_STATS_COVERAGE_START` 均不再读取，旧配置可以移除。
修改 Docker Compose 中的环境变量后，需要重新创建容器才能生效。

## 数据库与统计范围

`request_stats_minute` 不进入 AutoMigrate，部署前需在日志数据库中准备表结构：

- 新建表使用 [完整建表 SQL](plans/2026-10-19-request-minute-statistics-schema.sql)。
- 旧表按缺失字段依次应用 [username 升级](plans/2026-10-20-request-minute-statistics-username.sql)、
  [max_log_id 升级](plans/2026-10-21-request-minute-statistics-max-log-id.sql)
  和 [用量与金额升级](plans/2026-10-22-request-minute-statistics-quota.sql)，遵循各文件的升级说明。

分钟表的 `used_quota` 为同一统计分组内 `logs.quota` 的整数汇总，源值为 NULL 时按 0，负数参与求和。
`used_quota_usd` 固定等于 `used_quota / 500000`，使用 `DECIMAL(31,6)` 保存六位小数，不经过浮点数。
例如 `used_quota=500003` 对应 `used_quota_usd=1.000006`。
新生成的空分钟及数据异常零占位写入 `0` 和 `0.000000`；旧记录两个字段为 NULL，表示尚未回填。
需要历史用量时，使用仍保留的源日志分段重算已完成分钟，不会仅通过执行升级 SQL 自动补齐历史。

空汇总表首次启动时，Worker 读取当前最大日志 ID，从下一完整分钟开始，正常约两分钟后生成首批汇总。
不自动补历史；已有汇总从最新分钟重算并继续推进。查询覆盖和汇总保留范围根据已有汇总记录及 30 天窗口确定。
缺表、缺字段、连接失败和 SQL 超时仍作为错误返回，不会作为零流量发布。

统计指标只表示已汇总的消费和错误日志。失败明细读取当前仍存在的源日志，并保留现有统计覆盖、
查询时长和分页限制；源日志删除后，明细可能少于此前保存的汇总，取消配置参数不保证源日志永久完整。
重算使用当时仍存在的日志，已删除日志的原始计数无法恢复。

## 启动检查

Worker 启动时打印 `request statistics worker started`；未启动时，日志明确指出非主节点或未开启 Worker 开关。
启动日志只证明任务已启动，实际进度可通过 `request_stats_minute` 的 `MAX(bucket_start)` 检查。
每轮聚合失败记录 `request statistics worker stopped: ...`，下一轮继续重试。
