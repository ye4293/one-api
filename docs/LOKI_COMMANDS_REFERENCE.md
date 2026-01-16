# Promtail + Loki + Grafana 常用命令参考

快速参考：日志栈的日常操作命令

---

## 📦 容器管理

### 启动/停止服务

```bash
# 启动Loki和Grafana
cd /Users/yueqingli/code/one-api/loki
docker compose -f docker-compose-logging.yml up -d

# 停止服务
docker compose -f docker-compose-logging.yml down

# 重启服务
docker compose -f docker-compose-logging.yml restart loki
docker compose -f docker-compose-logging.yml restart grafana

# 启动Promtail
cd /Users/yueqingli/code/one-api
docker compose -f docker-compose-deps.yml restart promtail
```

### 查看状态和日志

```bash
# 查看容器状态
docker ps | grep -E "(loki|grafana|promtail)"

# 查看Loki日志
docker logs loki --tail 50
docker logs loki --follow

# 查看Promtail日志
docker logs one-api-promtail --tail 50
docker logs one-api-promtail --follow

# 查看Grafana日志
docker logs grafana --tail 50
```

---

## 🔍 Loki API 查询

### 基础查询

```bash
# 查询所有标签
curl -s "http://localhost:3100/loki/api/v1/labels" | python3 -m json.tool

# 查询特定标签的值
curl -s "http://localhost:3100/loki/api/v1/label/method/values" | python3 -m json.tool
curl -s "http://localhost:3100/loki/api/v1/label/status/values" | python3 -m json.tool
curl -s "http://localhost:3100/loki/api/v1/label/level/values" | python3 -m json.tool

# 查询日志（最近的3条）
curl -s -G "http://localhost:3100/loki/api/v1/query_range" \
  --data-urlencode 'query={job="oneapi"}' \
  --data-urlencode 'limit=3' | python3 -m json.tool

# 查询HTTP访问日志
curl -s -G "http://localhost:3100/loki/api/v1/query_range" \
  --data-urlencode 'query={job="oneapi", msg="HTTP request"}' \
  --data-urlencode 'limit=5' | python3 -m json.tool
```

### 高级查询

```bash
# 查询特定路径的日志
curl -s -G "http://localhost:3100/loki/api/v1/query_range" \
  --data-urlencode 'query={job="oneapi", path="/api/status"}' \
  --data-urlencode 'limit=5' | python3 -m json.tool

# 查询404错误
curl -s -G "http://localhost:3100/loki/api/v1/query_range" \
  --data-urlencode 'query={job="oneapi", status="404"}' \
  --data-urlencode 'limit=10' | python3 -m json.tool

# 查询错误日志
curl -s -G "http://localhost:3100/loki/api/v1/query_range" \
  --data-urlencode 'query={job="oneapi", level="error"}' \
  --data-urlencode 'limit=10' | python3 -m json.tool

# 使用JSON解析查询高基数字段
curl -s -G "http://localhost:3100/loki/api/v1/query_range" \
  --data-urlencode 'query={job="oneapi"} | json | client_ip="192.168.65.1"' \
  --data-urlencode 'limit=5' | python3 -m json.tool
```

### 健康检查

```bash
# Loki健康检查
curl http://localhost:3100/ready
curl http://localhost:3100/metrics

# 查询统计信息
curl -s "http://localhost:3100/loki/api/v1/query_range" \
  --data-urlencode 'query={job="oneapi"}' | \
  python3 -c "import sys, json; d=json.load(sys.stdin); print(json.dumps(d['data']['stats'], indent=2))"
```

---

## 📊 LogQL 查询语言

### 基础筛选（使用索引标签）

```logql
# 查看所有HTTP请求
{job="oneapi", msg="HTTP request"}

# 按状态码筛选
{job="oneapi", status="404"}
{job="oneapi", status="200"}
{job="oneapi", status=~"4..|5.."}  # 所有4xx和5xx

# 按HTTP方法筛选
{job="oneapi", method="GET"}
{job="oneapi", method="POST"}

# 按日志级别筛选
{job="oneapi", level="error"}
{job="oneapi", level=~"warn|error"}

# 按路径筛选
{job="oneapi", path="/api/status"}
{job="oneapi", path=~"/api/chat.*"}
```

### JSON字段查询（高基数字段）

```logql
# 按客户端IP筛选
{job="oneapi"} | json | client_ip="192.168.65.1"

# 按请求ID查找
{job="oneapi"} | json | request_id="2026011615440372452154843554952"

# 查询慢请求
{job="oneapi"} | json | latency_ms > 100
{job="oneapi"} | json | latency_ms > 1000

# 组合查询
{job="oneapi", status="404"} | json | path =~ ".*api.*"
{job="oneapi", method="POST"} | json | latency_ms > 100
```

### 聚合统计

```logql
# 每分钟请求数
sum(rate({job="oneapi", msg="HTTP request"} [1m]))

# 按状态码统计
sum by (status) (rate({job="oneapi", msg="HTTP request"} [1m]))

# 按HTTP方法统计
sum by (method) (rate({job="oneapi", msg="HTTP request"} [1m]))

# 按路径统计
sum by (path) (rate({job="oneapi", msg="HTTP request"} [1m]))

# P95响应时间
quantile_over_time(0.95, {job="oneapi"} | json | unwrap latency_ms [5m])

# 平均响应时间
avg_over_time({job="oneapi"} | json | unwrap latency_ms [5m])

# 错误率（百分比）
(sum(rate({job="oneapi", status=~"4..|5.."} [5m])) /
 sum(rate({job="oneapi", msg="HTTP request"} [5m]))) * 100
```

---

## 🎨 Grafana 操作

### 访问和登录

```bash
# Grafana URL
http://localhost:3200

# 默认登录
用户名: admin
密码: admin
```

### API操作

```bash
# 查询数据源列表
curl -s -u admin:admin "http://localhost:3200/api/datasources" | python3 -m json.tool

# 测试数据源连接
curl -s -u admin:admin \
  "http://localhost:3200/api/datasources/uid/<UID>/health" | python3 -m json.tool

# 查询仪表板列表
curl -s -u admin:admin "http://localhost:3200/api/search" | python3 -m json.tool

# 查询组织信息
curl -s -u admin:admin "http://localhost:3200/api/org" | python3 -m json.tool
```

---

## 🗑️ 数据清理

### 查看磁盘使用

```bash
# 查看Loki数据大小
du -sh /Users/yueqingli/code/one-api/loki/loki-data
du -sh /Users/yueqingli/code/one-api/loki/loki-data/*

# 查看应用日志大小
du -sh /Users/yueqingli/code/one-api/logs
ls -lh /Users/yueqingli/code/one-api/logs/*.log
```

### 修改保留期限

```bash
# 编辑配置
vim /Users/yueqingli/code/one-api/loki/loki-config.yaml

# 修改保留期限
# retention_period: 168h  # 7天
# retention_period: 72h   # 3天
# retention_period: 24h   # 1天

# 重启Loki应用配置
cd /Users/yueqingli/code/one-api/loki
docker compose -f docker-compose-logging.yml restart loki
```

### 清理应用日志文件

```bash
# 删除7天前的日志
find /Users/yueqingli/code/one-api/logs -name "*.log" -mtime +7 -delete

# 删除3天前的日志
find /Users/yueqingli/code/one-api/logs -name "*.log" -mtime +3 -delete

# 只保留今天的日志
TODAY=$(date +%Y%m%d)
find /Users/yueqingli/code/one-api/logs -name "*.log" ! -name "*${TODAY}*" -delete
```

### 完全重置Loki

```bash
# ⚠️ 警告：这会删除所有历史数据！
cd /Users/yueqingli/code/one-api/loki
docker compose -f docker-compose-logging.yml stop loki
rm -rf loki-data/*
mkdir -p loki-data/{chunks,wal,index,index-cache,compactor,rules}
docker compose -f docker-compose-logging.yml start loki
```

---

## 🔧 故障排查

### 检查服务状态

```bash
# 检查所有容器
docker ps -a | grep -E "(loki|grafana|promtail)"

# 检查网络连接
docker network inspect loki_logging
docker network inspect one-api_one-api-network

# 测试容器间连通性
docker exec one-api-promtail ping -c 2 loki
docker exec one-api-promtail wget -O- http://loki:3100/ready
```

### 查看配置

```bash
# 查看Loki配置
docker exec loki cat /etc/loki/config.yaml

# 查看Promtail配置
docker exec one-api-promtail cat /etc/promtail/config.yaml

# 查看Grafana数据源配置
docker exec grafana cat /etc/grafana/provisioning/datasources/loki.yaml
```

### 查看日志文件

```bash
# 查看应用日志
docker exec one-api tail -50 /app/logs/oneapi-$(date +%Y%m%d).log

# 查看最新的HTTP访问日志
docker exec one-api tail -20 /app/logs/oneapi-$(date +%Y%m%d).log | grep "HTTP request"

# 查看错误日志
docker exec one-api tail -50 /app/logs/oneapi-error-$(date +%Y%m%d).log
```

### Promtail问题排查

```bash
# 检查Promtail是否在读取文件
docker logs one-api-promtail 2>&1 | grep "tail routine"

# 检查Promtail推送错误
docker logs one-api-promtail 2>&1 | grep -E "error|retry"

# 检查Promtail positions文件
docker exec one-api-promtail cat /tmp/positions.yaml
```

---

## 📝 配置文件路径

| 文件 | 路径 |
|------|------|
| Loki配置 | `/Users/yueqingli/code/one-api/loki/loki-config.yaml` |
| Promtail配置 | `/Users/yueqingli/code/one-api/promtail-config.yaml` |
| Docker Compose (Loki) | `/Users/yueqingli/code/one-api/loki/docker-compose-logging.yml` |
| Docker Compose (Promtail) | `/Users/yueqingli/code/one-api/docker-compose-deps.yml` |
| Grafana数据源 | `/Users/yueqingli/code/one-api/grafana/provisioning/datasources/loki.yaml` |
| Loki数据目录 | `/Users/yueqingli/code/one-api/loki/loki-data/` |
| 应用日志目录 | `/Users/yueqingli/code/one-api/logs/` |

---

## 🚀 常用场景

### 场景1：查看最近的错误

```bash
# 查看Loki中的错误日志
curl -s -G "http://localhost:3100/loki/api/v1/query_range" \
  --data-urlencode 'query={job="oneapi", level="error"}' \
  --data-urlencode 'limit=10' | python3 -m json.tool
```

在Grafana中：
```logql
{job="oneapi", level="error"}
```

### 场景2：分析特定API的性能

```bash
# 查询/api/status的慢请求
curl -s -G "http://localhost:3100/loki/api/v1/query_range" \
  --data-urlencode 'query={job="oneapi", path="/api/status"} | json | latency_ms > 100' \
  --data-urlencode 'limit=10' | python3 -m json.tool
```

在Grafana中：
```logql
{job="oneapi", path="/api/status"} | json | latency_ms > 100
```

### 场景3：查找特定请求

```bash
# 通过request_id查找
curl -s -G "http://localhost:3100/loki/api/v1/query_range" \
  --data-urlencode 'query={job="oneapi"} | json | request_id="2026011615440372452154843554952"' \
  --data-urlencode 'limit=1' | python3 -m json.tool
```

### 场景4：监控API健康

在Grafana中创建Dashboard，使用以下查询：

```logql
# 总请求数（每分钟）
sum(rate({job="oneapi", msg="HTTP request"} [1m]))

# 错误率
(sum(rate({job="oneapi", status=~"4..|5.."} [5m])) /
 sum(rate({job="oneapi", msg="HTTP request"} [5m]))) * 100

# P95响应时间
quantile_over_time(0.95, {job="oneapi"} | json | unwrap latency_ms [5m])

# 按状态码分组的请求数
sum by (status) (rate({job="oneapi", msg="HTTP request"} [1m]))
```

---

## 📚 相关文档

- [HTTP_ACCESS_LOG_IMPLEMENTATION.md](../loki/HTTP_ACCESS_LOG_IMPLEMENTATION.md) - 完整实现报告
- [GIN_ACCESS_LOG_SETUP.md](../loki/GIN_ACCESS_LOG_SETUP.md) - 设置指南
- [TROUBLESHOOTING.md](../loki/TROUBLESHOOTING.md) - 故障排查指南
- [LogQL官方文档](https://grafana.com/docs/loki/latest/query/)

---

**最后更新**: 2026-01-16
