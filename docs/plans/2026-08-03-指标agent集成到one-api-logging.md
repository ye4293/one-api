# 指标 agent 集成到 one-api-logging(6 机 2 业务线汇总)

- **日期**:2026-08-03
- **涉及仓库**:`~/code/one-api-logging`(主改动)、`~/code/one-api`(清理重复)、`~/code/monitor`(无改动,确认)
- **前置**:monitor 接收端已就绪(prometheus + nginx /api/v1/write + grafana dashboard);one-api P0/P1 指标代码已完成

## 1. 背景与目标

6 台业务服务器,跑 2 个独立 one-api 业务线(各一套 one-api 实例),需汇总到 monitor 的 prometheus 统一监控。

**决策(已确认)**:
1. agent 集成到 `one-api-logging`(日志+指标统一采集 sidecar,6 台机器只 clone 一个项目)
2. 6 台机器都装主机指标(node-exporter + cAdvisor)
3. 用 `project` 维度区分 2 条业务线,`site` 维度区分 6 台机器

**为什么独立项目而非内嵌 one-api 仓库**:6 机 2 项目规模下,内嵌意味着 6 台机器要 clone 对应 one-api 仓库才能拿 agent 配置,2 个项目 2 套配置易漂移;独立项目 6 台机器统一 clone、只改 `.env`,与 one-api-logging 现有运维心智一致。

## 2. 方案设计

### 2.1 维度对齐(复用 one-api-logging 已有 .env 变量)

| 维度 | 日志(promtail) | 指标(agent) | .env 变量 |
|---|---|---|---|
| 业务线 | `APP_NAME` → Loki `job` | `external_labels.project` | **复用 APP_NAME** |
| 机器 | `SITE_IP` → Loki `instance` | `external_labels.site` | **复用 SITE_IP** |

一套 .env 同时驱动日志和指标,运维只填一次。

### 2.2 one-api-logging 改动(主)

**新增 `prometheus-agent/` 目录**:
- `prometheus.yml.template`:envsubst 模板。含 `external_labels`(site/project)+ `remote_write`(url/credentials_file/sample_age_limit:30m)+ `scrape_configs`(one-api/node/cadvisor)
- `entrypoint.sh`:`envsubst` 模板 → `/etc/prometheus/prometheus.yml`,再 `exec prometheus --agent ...`(仿 monitor nginx 的 envsubst 模式,解决 Prometheus 不展开 `${VAR}` 的痛点)
- `Dockerfile`:基于 `prom/prometheus:v3.7.3`,装 `envsubst`(或用 alpine 带 gettext),COPY 模板与 entrypoint

**`docker-compose.yml` 加 3 服务**(参照 one-api 仓库 `deploy/prometheus/docker-compose.agent.yml` 的成熟配置):
- `prometheus-agent`:`--agent` + `--storage.agent.path`,挂 `.metrics-token` 与配置
- `node-exporter`:`pid: host` + `network_mode: host` + `path.rootfs`(漏任一项指标静默错误)
- `cadvisor`:`privileged`,默认吐几千序列需 `--disable_metrics` + metric_relabel 白名单

**`.env.example` 新增**:
```
PROM_REMOTE_WRITE_URL=http://<monitor-ip>:3100/api/v1/write
PROM_BEARER_TOKEN=<与 monitor 的 PROM_BEARER_TOKEN 一致>
METRICS_TOKEN=<one-api /metrics 的 Bearer token>
ONE_API_METRICS_TARGET=<one-api metrics 地址,默认宿主机IP:9099>
# 复用 APP_NAME(业务线)、SITE_IP(机器)
```

**README 更新**:加「指标采集」章节,说明 `printf '%s' "$PROM_BEARER_TOKEN" > .metrics-token`(必须 printf 不能 echo)。

### 2.3 one-api 仓库改动(清理重复)

- `deploy/prometheus/docker-compose.agent.yml`:**删除**(agent 部署移至 one-api-logging,两边各留一份必然漂移)
- `deploy/prometheus/README.md`:更新章节,指向 one-api-logging
- **保留** `run-local.sh` + `prometheus.local.yml`(本地开发验证用,与生产 agent 推送互斥但都有价值)
- 保留 `common/metrics/`(指标导出代码,不动)

### 2.4 monitor 侧(无改动,仅确认)

- `PROM_BEARER_TOKEN` 与 one-api-logging `.env` 一致
- 规则已支持 `site` 维度全局聚合(无需改)
- grafana `one-api-metrics.json` 已 provisioning

## 3. 关键技术点(踩过的坑)

| 点 | 说明 |
|---|---|
| Prometheus 不展开 `${VAR}` | 用 entrypoint `envsubst` 生成配置,不能照抄 promtail 占位符方式 |
| token 必须 `printf` 生成 | `echo` 加换行,Prometheus 把换行也发出去,网关 401(现象极隐蔽) |
| `sample_age_limit: 30m` | 必须与 monitor `out_of_order_time_window: 30m` 成对,否则 agent 反复重试超龄样本、队列卡死 |
| `job_name` 以 `one-api` 开头 | `OneApiDown` 告警选择器 `job=~"one-api.*"`,不匹配则告警永不触发 |
| node-exporter `path.rootfs` | 漏掉则 mountpoint label 带 `/rootfs` 前缀,alerts.yml 匹配失配、告警静默 |
| `external_labels.site` 必填且唯一 | 6 台机器 SITE_IP 必须不同,漏填/重复 → 序列互相覆盖且无告警 |

## 4. 影响范围

| 项 | 评估 |
|---|---|
| one-api-logging | 新增 `prometheus-agent/` 目录,改 docker-compose / .env.example / README |
| one-api | 删 `deploy/prometheus/docker-compose.agent.yml`,改 README;指标代码不动 |
| monitor | 无改动 |
| 6 台业务机 | clone one-api-logging,填 .env,docker compose up -d |
| 数据库 | 无 |
| 回滚 | one-api-logging 删 agent 服务即可,日志链路不受影响 |

## 5. 验证方式

1. **本地单机验证**:起 one-api(开 metrics)+ one-api-logging(agent),agent remote_write 到本地 monitor,确认 monitor prometheus 收到数据且 `site`/`project` label 正确
2. **关键不变式**:`count by (site) (up{job=~"one-api.*"})` 应返回 6 个不同 site;`count by (project) (...)` 应返回 2 个 project
3. **主机指标**:node-exporter 的 `node_filesystem_*` mountpoint label 不带 `/rootfs` 前缀
4. **Grafana**:`one-api-metrics.json` 按 site/project 筛选正常
5. **单台跑通后铺开 6 台**;token 轮换时 6 台同步改 .env
6. **注意**:本地 `run-local.sh`(拉取式)验通 ≠ 生产 agent 推送通,生产端到端至少在一台业务机跑真实推送

## 6. 风险

1. **site 唯一性**(最大风险):6 台机器 .env 的 `SITE_IP` 必须互不相同。建议部署清单核对
2. **token 同步**:6 台共用 `PROM_BEARER_TOKEN`,轮换要一次性改完,否则部分机器 401
3. **本地/生产 gap**:本地拉取式跳过了 agent + remote_write + nginx + OOO 整条链路,相关 bug 本地遇不到
