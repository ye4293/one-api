# Loki + Grafana 日志栈部署指南

## 📋 概述

使用 `docker-compose-logging.yml` 快速部署 Loki + Grafana 日志栈，用于接收和可视化 one-api 的日志。

## 🏗️ 架构

```
one-api 容器
  ↓ 生成 JSON 日志
Promtail 容器
  ↓ HTTP Push (localhost:3100)
Loki 容器（本指南部署）
  ↓ LogQL 查询
Grafana 容器（本指南部署）
```

## 🚀 快速启动

### 1. 配置环境变量

```bash
# 复制示例文件
cp .env.logging.example .env.logging

# 编辑配置
vim .env.logging
```

**推荐配置**：
```bash
# .env.logging
GF_ADMIN_USER=admin
GF_ADMIN_PASSWORD=your-secure-password-here
```

### 2. 启动 Loki + Grafana

```bash
# 启动日志栈
docker-compose -f docker-compose-logging.yml --env-file .env.logging up -d

# 查看服务状态
docker-compose -f docker-compose-logging.yml ps

# 查看 Loki 日志
docker-compose -f docker-compose-logging.yml logs -f loki

# 查看 Grafana 日志
docker-compose -f docker-compose-logging.yml logs -f grafana
```

### 3. 验证服务

```bash
# 检查 Loki 健康状态
curl http://localhost:3100/ready

# 预期输出: ready

# 检查 Loki 标签（如果 Promtail 已开始推送）
curl http://localhost:3100/loki/api/v1/labels

# 访问 Grafana
open http://localhost:3200
# 或
curl http://localhost:3200/api/health
```

## 🔐 访问 Grafana

### 登录

1. 浏览器打开：`http://localhost:3200`
2. 使用配置的账号密码登录：
   - 用户名：`admin`（或 `.env.logging` 中配置的）
   - 密码：`admin`（或 `.env.logging` 中配置的）

### 验证 Loki 数据源

Grafana 已自动配置 Loki 数据源，验证方法：

1. 左侧菜单 → **Configuration** → **Data Sources**
2. 应该看到 **Loki** 数据源，状态为绿色勾选

### 查看预置 Dashboard

1. 左侧菜单 → **Dashboards** → **Browse**
2. 找到 **One-API Logs** Dashboard
3. 点击进入，可以看到：
   - 错误日志流
   - 错误率图表
   - 按级别统计图表
   - 可筛选的所有日志

## 🔍 使用 Grafana Explore 查询日志

### 基础查询

1. 左侧菜单 → **Explore**
2. 确保数据源选择 **Loki**
3. 尝试以下查询：

```logql
# 查看所有日志
{service="one-api"}

# 只看错误日志
{service="one-api", stream="error"}

# 按级别筛选
{service="one-api", level="error"}

# 按 HTTP 方法筛选
{service="one-api", method="POST"}
```

### 高级查询

```logql
# 按 request_id 追踪请求
{service="one-api"} | json | request_id="具体的ID"

# 筛选特定路径
{service="one-api"} | json | path="/v1/chat/completions"

# 筛选 5xx 错误
{service="one-api"} | json | status >= 500

# 筛选延迟超过 1 秒的请求
{service="one-api"} | json | latency_ms > 1000
```

### 日志统计查询

```logql
# 错误率（每秒错误数）
sum(rate({service="one-api", stream="error"}[5m]))

# 按状态码统计
sum by (status) (count_over_time({service="one-api"} | json [5m]))

# 按接口统计请求量
sum by (path, method) (count_over_time({service="one-api"} | json [5m]))
```

## 📂 目录结构

部署后会创建以下目录：

```
one-api/
├── docker-compose-logging.yml
├── loki-config.yaml
├── .env.logging
├── loki-data/                    # Loki 数据持久化
│   ├── chunks/                   # 日志数据块
│   ├── index/                    # 索引数据
│   ├── boltdb-cache/            # BoltDB 缓存
│   ├── wal/                      # Write-Ahead Log
│   └── compactor/                # 压缩工作目录
├── grafana-data/                 # Grafana 数据持久化
│   ├── grafana.db               # Grafana 配置数据库
│   ├── plugins/                  # 插件
│   └── ...
└── grafana/
    └── provisioning/
        ├── datasources/
        │   └── loki.yaml         # 自动配置 Loki 数据源
        └── dashboards/
            ├── default.yaml      # Dashboard 配置
            └── one-api-logs.json # 预置 Dashboard
```

## 🔄 与 Promtail 对接

### 确认 Promtail 配置

确保 `promtail-config.yaml` 中的 Loki URL 正确：

```yaml
clients:
  - url: ${LOKI_URL:-http://localhost:3100/loki/api/v1/push}
```

或者在 `.env` 中设置：
```bash
LOKI_URL=http://localhost:3100/loki/api/v1/push
```

### 启动顺序

```bash
# 1. 启动 Loki + Grafana
docker-compose -f docker-compose-logging.yml up -d

# 2. 等待服务就绪（约 10 秒）
docker-compose -f docker-compose-logging.yml ps

# 3. 启动 one-api + Promtail
docker-compose -f docker-compose-deps.yml up -d

# 4. 验证 Promtail 连接
docker-compose -f docker-compose-deps.yml logs promtail | grep "POST"
# 应该看到推送日志的请求
```

## 📊 配置说明

### Loki 配置（loki-config.yaml）

| 配置项 | 值 | 说明 |
|-------|-----|------|
| **端口** | 3100 | HTTP API 端口 |
| **存储** | BoltDB + Filesystem | 本地文件系统存储 |
| **数据保留** | 14 天 | 自动删除 14 天前的日志 |
| **压缩** | 禁用 | 不压缩日志数据 |
| **索引周期** | 24 小时 | 每天创建新索引 |
| **WAL** | 启用 | Write-Ahead Log，防止数据丢失 |

### Grafana 配置

| 配置项 | 值 | 说明 |
|-------|-----|------|
| **端口** | 3200 | Web UI 端口 |
| **管理员用户** | 环境变量 | 通过 `.env.logging` 配置 |
| **数据源** | 自动配置 | 启动时自动添加 Loki |
| **Dashboard** | 预置 | One-API Logs Dashboard |

## 🛠️ 常用命令

### 服务管理

```bash
# 启动
docker-compose -f docker-compose-logging.yml up -d

# 停止
docker-compose -f docker-compose-logging.yml stop

# 重启
docker-compose -f docker-compose-logging.yml restart

# 查看状态
docker-compose -f docker-compose-logging.yml ps

# 查看日志
docker-compose -f docker-compose-logging.yml logs -f

# 停止并删除容器（保留数据）
docker-compose -f docker-compose-logging.yml down

# 停止并删除所有（包括数据，危险！）
docker-compose -f docker-compose-logging.yml down -v
```

### 数据管理

```bash
# 查看数据目录大小
du -sh loki-data grafana-data

# 备份数据
tar -czf loki-backup-$(date +%Y%m%d).tar.gz loki-data/
tar -czf grafana-backup-$(date +%Y%m%d).tar.gz grafana-data/

# 清理旧数据（Loki 会自动清理，但可手动触发）
docker-compose -f docker-compose-logging.yml exec loki \
  wget --post-data='' http://localhost:3100/flush
```

## 🐛 故障排查

### Loki 启动失败

**症状**：`docker-compose ps` 显示 Loki 不断重启

**解决方案**：
```bash
# 查看详细日志
docker-compose -f docker-compose-logging.yml logs loki

# 检查配置文件语法
docker-compose -f docker-compose-logging.yml exec loki \
  /usr/bin/loki -config.file=/etc/loki/config.yaml -verify-config

# 检查目录权限
ls -la loki-data/
chmod 755 loki-data/
```

### Grafana 无法连接 Loki

**症状**：Grafana 中查询不到日志，提示连接错误

**解决方案**：
```bash
# 检查网络连通性
docker-compose -f docker-compose-logging.yml exec grafana \
  wget -qO- http://loki:3100/ready

# 检查 Loki 是否运行
docker-compose -f docker-compose-logging.yml ps loki

# 重启 Grafana
docker-compose -f docker-compose-logging.yml restart grafana
```

### Promtail 无法推送日志

**症状**：Promtail 日志显示连接被拒绝

**解决方案**：
```bash
# 检查 Loki 是否可访问
curl http://localhost:3100/ready

# 检查 Promtail 的 LOKI_URL 配置
docker-compose -f docker-compose-deps.yml exec promtail \
  cat /etc/promtail/config.yaml | grep url

# 确保使用正确的地址（宿主机访问用 localhost）
# 如果 Promtail 和 Loki 在同一 Docker 网络，用服务名 loki:3100
```

### 磁盘空间不足

**症状**：Loki 停止接收日志，日志显示磁盘满

**解决方案**：
```bash
# 检查磁盘使用
df -h
du -sh loki-data/*

# 手动清理旧数据（调整保留期）
# 编辑 loki-config.yaml
vim loki-config.yaml
# 修改 retention_period 为更短时间（如 7 天）
# 重启 Loki
docker-compose -f docker-compose-logging.yml restart loki
```

### Dashboard 不显示数据

**症状**：Dashboard 打开了但看不到日志

**解决方案**：
```bash
# 1. 检查 Loki 数据源配置
# Grafana → Configuration → Data Sources → Loki → Save & test

# 2. 确认有日志数据
curl 'http://localhost:3100/loki/api/v1/labels'

# 3. 在 Explore 中手动查询
# {service="one-api"}

# 4. 检查时间范围（Dashboard 右上角）
```

## 📈 性能调优

### Loki 性能优化

如果日志量很大（>1GB/天），可以调整以下配置：

```yaml
# loki-config.yaml
limits_config:
  ingestion_rate_mb: 50          # 增加摄入速率限制
  ingestion_burst_size_mb: 100   # 增加突发大小

querier:
  max_concurrent: 50              # 增加并发查询数

chunk_store_config:
  chunk_cache_config:
    embedded_cache:
      enabled: true
      max_size_mb: 500            # 增加缓存大小
```

### Grafana 性能优化

```yaml
# docker-compose-logging.yml
environment:
  - GF_DATABASE_WAL=true          # 启用 WAL 提升写入性能
  - GF_LOG_LEVEL=warn             # 减少日志输出
```

## 🔒 安全建议

1. **修改默认密码**：必须修改 Grafana 管理员密码
2. **限制访问**：生产环境建议配置反向代理和 HTTPS
3. **网络隔离**：使用 Docker 网络隔离，不暴露不必要的端口
4. **定期备份**：定期备份 `loki-data` 和 `grafana-data`
5. **监控磁盘**：设置磁盘空间告警

## 📚 相关文档

- [Loki 官方文档](https://grafana.com/docs/loki/latest/)
- [Grafana 官方文档](https://grafana.com/docs/grafana/latest/)
- [LogQL 查询语言](https://grafana.com/docs/loki/latest/logql/)
- [Promtail 配置](PROMTAIL_SETUP.md)

---

**快速命令备忘**：
```bash
# 启动日志栈
docker-compose -f docker-compose-logging.yml up -d

# 访问 Grafana
open http://localhost:3200

# 查看 Loki 状态
curl http://localhost:3100/ready

# 查看日志
docker-compose -f docker-compose-logging.yml logs -f
```
