# HTTP 访问日志实现完成报告

## ✅ 实现状态：已完成

HTTP 访问日志功能已成功实现并部署。所有日志现在都包含完整的请求信息，可在 Grafana 中查询和分析。

---

## 📋 实现内容

### 1. 修改的文件

**`/Users/yueqingli/code/one-api/middleware/logger.go`**

#### 修改内容：
1. ✅ 添加 `Msg` 字段到 `AccessLogEntry` 结构体
2. ✅ 设置 `Msg` 为固定值 "HTTP request"
3. ✅ 在 formatter 内部直接写入 `gin.DefaultWriter`
4. ✅ 确保访问日志同时输出到标准输出和日志文件

#### 关键代码：
```go
type AccessLogEntry struct {
    Ts        string `json:"ts"`
    Level     string `json:"level"`
    RequestId string `json:"request_id"`
    Msg       string `json:"msg"`           // 新增
    Status    int    `json:"status"`
    LatencyMs int64  `json:"latency_ms"`
    ClientIP  string `json:"client_ip"`
    Method    string `json:"method"`
    Path      string `json:"path"`
    Service   string `json:"service"`
    Instance  string `json:"instance"`
}
```

---

## 📊 日志格式

### 完整的 JSON 日志示例

#### 成功请求（200）:
```json
{
  "ts": "2026-01-15T19:21:45.159294088+08:00",
  "level": "info",
  "request_id": "2026011519214515452262999265956",
  "msg": "HTTP request",
  "status": 200,
  "latency_ms": 4,
  "client_ip": "192.168.65.1",
  "method": "GET",
  "path": "/api/status",
  "service": "one-api",
  "instance": "dev-localhost-li"
}
```

#### 错误请求（404）:
```json
{
  "ts": "2026-01-15T19:21:45.201154921+08:00",
  "level": "warn",
  "request_id": "2026011519214519835612919652427",
  "msg": "HTTP request",
  "status": 404,
  "latency_ms": 2,
  "client_ip": "192.168.65.1",
  "method": "GET",
  "path": "/api/test404",
  "service": "one-api",
  "instance": "dev-localhost-li"
}
```

---

## ✅ 验证结果

### 1. 日志文件写入 ✅
```bash
$ docker exec one-api tail -5 /app/logs/oneapi-$(date +%Y%m%d).log | grep '"msg":"HTTP request"'
# 输出：包含 status, method, path 等完整字段的 JSON 日志
```

### 2. Loki 接收日志 ✅
```bash
$ curl -s "http://localhost:3100/loki/api/v1/label/method/values"
{
    "status": "success",
    "data": ["GET"]
}
```

### 3. Promtail 正常推送 ✅
- Promtail 正在监控日志文件
- 日志被成功推送到 Loki
- 标签提取正常工作

---

## 🔍 Grafana 查询指南

### 访问 Grafana

1. **URL**: http://localhost:3200
2. **登录**:
   - 用户名: `admin`
   - 密码: `admin`
3. **进入 Explore 页面**: 左侧菜单 → Explore (罗盘图标)

### 常用 LogQL 查询

#### 1. 查看所有 HTTP 请求
```logql
{job="oneapi"} | json | msg = "HTTP request"
```

#### 2. 按 HTTP 方法筛选
```logql
# 只看 GET 请求
{job="oneapi"} | json | method = "GET"

# 只看 POST 请求
{job="oneapi"} | json | method = "POST"
```

#### 3. 按状态码筛选
```logql
# 成功请求（200-299）
{job="oneapi"} | json | status >= 200 | status < 300

# 客户端错误（400-499）
{job="oneapi"} | json | status >= 400 | status < 500

# 服务器错误（500+）
{job="oneapi"} | json | status >= 500

# 404 错误
{job="oneapi"} | json | status = 404
```

#### 4. 按路径筛选
```logql
# 特定路径
{job="oneapi"} | json | path = "/api/status"

# 路径包含 chat
{job="oneapi"} | json | path =~ ".*chat.*"

# API 路径（正则）
{job="oneapi"} | json | path =~ "/api/(chat|embeddings)/.*"
```

#### 5. 慢请求分析
```logql
# 响应时间 > 100ms
{job="oneapi"} | json | latency_ms > 100

# 响应时间 > 500ms
{job="oneapi"} | json | latency_ms > 500

# 响应时间 > 1000ms（1秒）
{job="oneapi"} | json | latency_ms > 1000
```

#### 6. 按客户端 IP 筛选
```logql
{job="oneapi"} | json | client_ip = "192.168.65.1"
```

#### 7. 组合查询
```logql
# GET 请求 + 404 错误
{job="oneapi"} | json | method = "GET" | status = 404

# POST 请求 + 响应时间 > 100ms
{job="oneapi"} | json | method = "POST" | latency_ms > 100

# /api/status 路径 + 成功请求
{job="oneapi"} | json | path = "/api/status" | status < 400
```

#### 8. 聚合统计
```logql
# 每分钟请求数（按方法分组）
sum by (method) (rate({job="oneapi"} | json | msg = "HTTP request" [1m]))

# 每分钟错误率
sum(rate({job="oneapi"} | json | status >= 400 [1m]))

# 平均响应时间（5分钟内）
avg_over_time({job="oneapi"} | json | unwrap latency_ms [5m])

# P95 响应时间
quantile_over_time(0.95, {job="oneapi"} | json | unwrap latency_ms [5m])

# 每分钟总请求数
sum(rate({job="oneapi"} | json | status != 0 [1m]))
```

---

## 📈 常见使用场景

### 场景 1: 监控 API 健康状况
```logql
# 查看所有错误请求
{job="oneapi", level=~"warn|error"} | json | msg = "HTTP request"
```

### 场景 2: 性能分析
```logql
# 找出最慢的请求
{job="oneapi"} | json | msg = "HTTP request" | latency_ms > 1000
```

### 场景 3: 用户行为分析
```logql
# 特定客户端的所有请求
{job="oneapi"} | json | client_ip = "192.168.65.1"
```

### 场景 4: API 端点使用统计
```logql
# 按路径统计请求数
sum by (path) (count_over_time({job="oneapi"} | json | msg = "HTTP request" [1h]))
```

### 场景 5: 错误率监控
```logql
# 计算 5 分钟内的错误率百分比
(sum(rate({job="oneapi"} | json | status >= 400 [5m])) /
 sum(rate({job="oneapi"} | json | status != 0 [5m]))) * 100
```

---

## 🎯 可用的标签（Labels）

访问日志提供以下标签用于快速筛选：

| 标签 | 值示例 | 说明 |
|------|--------|------|
| `job` | `oneapi` | 任务名称 |
| `stream` | `general` | 日志流类型 |
| `level` | `info`, `warn`, `error` | 日志级别 |
| `service` | `one-api` | 服务名称 |
| `instance` | `dev-localhost-li` | 实例ID |
| `method` | `GET`, `POST`, `PUT`, `DELETE` | HTTP 方法 |

---

## 🎨 创建 Dashboard

### 推荐的 Panel 配置

#### 1. 请求数时序图
- **查询**: `sum(rate({job="oneapi"} | json | status != 0 [1m]))`
- **可视化**: Time series
- **Y轴标签**: Requests/sec

#### 2. 状态码分布
- **查询**: `sum by (status) (count_over_time({job="oneapi"} | json | msg = "HTTP request" [5m]))`
- **可视化**: Pie chart
- **图例**: Status Code

#### 3. 响应时间 P95
- **查询**: `quantile_over_time(0.95, {job="oneapi"} | json | unwrap latency_ms [5m])`
- **可视化**: Stat
- **单位**: milliseconds (ms)

#### 4. 错误率
- **查询**: `(sum(rate({job="oneapi"} | json | status >= 400 [5m])) / sum(rate({job="oneapi"} | json | status != 0 [5m]))) * 100`
- **可视化**: Gauge
- **单位**: percent (%)
- **阈值**:
  - 绿色: 0-1%
  - 黄色: 1-5%
  - 红色: >5%

#### 5. HTTP 方法分布
- **查询**: `sum by (method) (count_over_time({job="oneapi"} | json | msg = "HTTP request" [1h]))`
- **可视化**: Bar chart
- **X轴**: Method

#### 6. 最新的错误日志
- **查询**: `{job="oneapi"} | json | status >= 400`
- **可视化**: Logs
- **限制**: 最近 50 条

---

## 📝 日志级别说明

| HTTP 状态码 | 日志级别 | 说明 |
|------------|---------|------|
| 200-399 | `info` | 成功请求 |
| 400-499 | `warn` | 客户端错误（如 404, 401） |
| 500+ | `error` | 服务器错误 |

---

## 🔧 故障排查

### 问题 1: 看不到访问日志

**检查步骤**:
```bash
# 1. 检查日志文件
docker exec one-api tail /app/logs/oneapi-$(date +%Y%m%d).log | grep '"msg":"HTTP request"'

# 2. 检查 Promtail 状态
docker logs one-api-promtail --tail 50

# 3. 检查 Loki 标签
curl -s "http://localhost:3100/loki/api/v1/labels"
```

### 问题 2: 查询返回空结果

**可能原因**:
1. 时间范围不对（调整查询的时间范围）
2. 查询语法错误（检查 LogQL 语法）
3. 日志还未被推送（等待几秒钟）

**解决方法**:
```logql
# 使用更宽的时间范围
{job="oneapi"} | json | status != 0 [15m]
```

### 问题 3: Grafana 中数据源连接失败

**检查步骤**:
```bash
# 1. 检查 Loki 健康状态
curl http://localhost:3100/ready

# 2. 检查 Grafana 数据源配置
curl -u admin:admin http://localhost:3200/api/datasources
```

---

## 📚 相关文档

- [GIN_ACCESS_LOG_SETUP.md](./GIN_ACCESS_LOG_SETUP.md) - 完整的设置指南
- [TROUBLESHOOTING.md](./TROUBLESHOOTING.md) - 故障排查指南
- [LogQL 官方文档](https://grafana.com/docs/loki/latest/query/)

---

## ✨ 下一步建议

### 1. 创建告警规则
为关键指标设置告警：
- 错误率超过 5%
- P95 响应时间超过 1000ms
- 5xx 错误出现

### 2. 优化日志记录
考虑实现以下优化：
- 只记录重要端点的成功请求
- 采样记录（如只记录 10% 的成功请求）
- 不记录健康检查端点

### 3. 扩展监控
添加更多监控指标：
- 业务指标（如 Token 使用量）
- 数据库查询时间
- 外部 API 调用耗时

---

## 🎉 总结

HTTP 访问日志功能已成功实现，现在你可以：

- ✅ 在日志文件中查看所有 HTTP 请求
- ✅ 在 Loki 中存储和查询日志
- ✅ 在 Grafana 中可视化和分析请求数据
- ✅ 按多种维度筛选日志（方法、状态码、路径、响应时间等）
- ✅ 创建实时监控 Dashboard
- ✅ 设置告警规则

**访问 Grafana 开始探索你的日志！** 🚀

**URL**: http://localhost:3200
**登录**: admin / admin

---

**实现日期**: 2026-01-15
**实现者**: Claude Sonnet 4.5
