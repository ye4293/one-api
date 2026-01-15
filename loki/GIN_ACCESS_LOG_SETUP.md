# Gin 访问日志配置指南

本文档说明如何在 one-api 中启用 HTTP 访问日志记录，以便在 Grafana 中查询和分析请求。

---

## 问题现象

### ❌ 当前状态

**日志中缺少 HTTP 访问信息：**

```json
// 只有系统日志
{"ts":"2026-01-15T19:02:42.773917045+08:00","level":"info","msg":"No unfinished tasks found"}

// 缺少访问日志（status, method, path 等）
```

**Grafana 中无法查询：**
- ✗ 无法按 HTTP 方法筛选 (GET/POST)
- ✗ 无法按状态码筛选 (200/404/500)
- ✗ 无法分析响应时间
- ✗ 无法查看请求路径

---

## 解决方案：实现访问日志中间件

### 1. 创建访问日志中间件

在 `middleware` 或 `common` 包中创建日志中间件：

```go
package middleware

import (
    "time"
    "github.com/gin-gonic/gin"
    "your-project/common/logger" // 使用你的日志库
)

// AccessLogger 记录 HTTP 访问日志的中间件
func AccessLogger() gin.HandlerFunc {
    return func(c *gin.Context) {
        // 记录开始时间
        startTime := time.Now()

        // 处理请求
        c.Next()

        // 计算耗时
        latency := time.Since(startTime)
        latencyMs := latency.Milliseconds()

        // 获取响应状态码
        statusCode := c.Writer.Status()

        // 构建日志字段
        fields := map[string]interface{}{
            "status":     statusCode,
            "method":     c.Request.Method,
            "path":       c.Request.URL.Path,
            "latency_ms": latencyMs,
            "client_ip":  c.ClientIP(),
        }

        // 可选：添加查询参数（注意隐私和安全）
        if len(c.Request.URL.RawQuery) > 0 {
            fields["query"] = c.Request.URL.RawQuery
        }

        // 可选：添加请求 ID
        if requestID := c.GetString("request_id"); requestID != "" {
            fields["request_id"] = requestID
        }

        // 根据状态码选择日志级别
        logMessage := "HTTP request"

        switch {
        case statusCode >= 500:
            // 5xx 错误 - ERROR 级别
            logger.Error(c.Request.Context(), logMessage, fields)
        case statusCode >= 400:
            // 4xx 错误 - WARN 级别
            logger.Warn(c.Request.Context(), logMessage, fields)
        case statusCode >= 200 && statusCode < 300:
            // 2xx 成功 - INFO 级别（可选：只在 debug 模式记录）
            if gin.Mode() == gin.DebugMode || isImportantEndpoint(c.Request.URL.Path) {
                logger.Info(c.Request.Context(), logMessage, fields)
            }
        default:
            // 其他状态码 - INFO 级别
            logger.Info(c.Request.Context(), logMessage, fields)
        }
    }
}

// isImportantEndpoint 判断是否为重要端点（始终记录日志）
func isImportantEndpoint(path string) bool {
    // 重要的 API 端点列表
    importantPaths := []string{
        "/api/chat/completions",
        "/v1/chat/completions",
        "/api/embeddings",
        "/api/images/generations",
    }

    for _, p := range importantPaths {
        if path == p {
            return true
        }
    }
    return false
}
```

### 2. 注册中间件

在主应用中注册中间件：

```go
package main

import (
    "github.com/gin-gonic/gin"
    "your-project/middleware"
)

func SetUpLogger(server *gin.Engine) {
    // 移除默认的 Gin Logger（避免重复日志）
    // server.Use(gin.Logger())

    // 使用自定义的访问日志中间件
    server.Use(middleware.AccessLogger())
}

func main() {
    router := gin.New()

    // 设置日志中间件
    SetUpLogger(router)

    // 其他中间件...
    router.Use(gin.Recovery())

    // 路由配置...
    // ...

    router.Run(":3000")
}
```

### 3. 优化版本：只记录部分请求

如果不想记录所有 2xx 请求（减少日志量），可以这样配置：

```go
func AccessLogger() gin.HandlerFunc {
    return func(c *gin.Context) {
        startTime := time.Now()
        c.Next()

        latency := time.Since(startTime)
        statusCode := c.Writer.Status()

        // 过滤规则
        shouldLog := false

        switch {
        case statusCode >= 400:
            // 所有 4xx 和 5xx 错误都记录
            shouldLog = true
        case statusCode >= 200 && statusCode < 300:
            // 2xx 成功：只记录重要端点或慢请求
            shouldLog = isImportantEndpoint(c.Request.URL.Path) || latency > 500*time.Millisecond
        default:
            shouldLog = true
        }

        if !shouldLog {
            return
        }

        // 记录日志...（同上）
    }
}
```

---

## 预期日志格式

启用后，日志应该包含以下字段：

```json
{
  "ts": "2026-01-15T19:10:23.456789+08:00",
  "level": "info",
  "request_id": "2026011519102345678901234567890",
  "status": 200,
  "method": "GET",
  "path": "/api/status",
  "latency_ms": 12,
  "client_ip": "192.168.65.1",
  "msg": "HTTP request",
  "service": "one-api",
  "instance": "dev-localhost-li"
}
```

---

## Grafana LogQL 查询示例

启用访问日志后，你可以使用以下查询：

### 1. 查看所有 HTTP 请求
```logql
{job="oneapi"} | json | status != ""
```

### 2. 查询状态码 >= 200 的请求（正确语法）
```logql
{instance="dev-localhost-li"} | json | status >= 200
```

**❌ 错误语法**：
```logql
{instance="dev-localhost-li"} |= `status >= 200`  # 这是错误的！
```

**✅ 正确语法**：
```logql
{instance="dev-localhost-li"} | json | status >= 200
```

### 3. 按 HTTP 方法筛选
```logql
# 只看 GET 请求
{job="oneapi"} | json | method = "GET"

# 只看 POST 请求
{job="oneapi"} | json | method = "POST"
```

### 4. 查询特定路径
```logql
# 查看 /api/chat/completions 的请求
{job="oneapi"} | json | path = "/api/chat/completions"

# 使用正则匹配多个路径
{job="oneapi"} | json | path =~ "/api/(chat|embeddings)/.*"
```

### 5. 查询错误请求
```logql
# 4xx 错误
{job="oneapi"} | json | status >= 400 | status < 500

# 5xx 错误
{job="oneapi"} | json | status >= 500

# 所有错误
{job="oneapi", level="error"}
```

### 6. 查询慢请求
```logql
# 响应时间 > 500ms
{job="oneapi"} | json | latency_ms > 500

# 响应时间 > 1000ms
{job="oneapi"} | json | latency_ms > 1000
```

### 7. 按客户端 IP 筛选
```logql
{job="oneapi"} | json | client_ip = "192.168.65.1"
```

### 8. 组合查询
```logql
# GET 请求 + 404 错误
{job="oneapi"} | json | method = "GET" | status = 404

# POST 请求 + 响应时间 > 100ms
{job="oneapi"} | json | method = "POST" | latency_ms > 100

# 特定路径 + 状态码 >= 500
{job="oneapi"} | json | path =~ "/api/chat/.*" | status >= 500
```

### 9. 聚合统计
```logql
# 每分钟请求数（按方法分组）
sum by (method) (rate({job="oneapi"} | json | status != "" [1m]))

# 每分钟错误率
sum(rate({job="oneapi"} | json | status >= 400 [1m]))

# 平均响应时间（5分钟内）
avg_over_time({job="oneapi"} | json | unwrap latency_ms [5m])

# P95 响应时间
quantile_over_time(0.95, {job="oneapi"} | json | unwrap latency_ms [5m])
```

---

## LogQL 语法说明

### 过滤器类型

| 过滤器 | 语法 | 说明 | 示例 |
|--------|------|------|------|
| 标签匹配 | `{label="value"}` | 匹配标签值 | `{job="oneapi"}` |
| 行过滤 | `\|= "text"` | 包含文本 | `\|= "error"` |
| 行排除 | `\|!= "text"` | 不包含文本 | `\|!= "health"` |
| 正则匹配 | `\|~ "regex"` | 匹配正则 | `\|~ "error\|fail"` |
| JSON 解析 | `\| json` | 解析 JSON | `\| json` |
| 字段过滤 | `\| field = value` | 过滤 JSON 字段 | `\| status = 200` |

### 比较运算符

```logql
# 数值比较
status = 200      # 等于
status != 200     # 不等于
status > 200      # 大于
status >= 200     # 大于等于
status < 500      # 小于
status <= 500     # 小于等于

# 字符串比较
method = "GET"    # 等于
method != "GET"   # 不等于
path =~ "/api/.*" # 正则匹配
path !~ "/health" # 正则不匹配
```

---

## 验证步骤

### 1. 部署代码
```bash
# 重新构建镜像
docker build -t one-api:latest .

# 重启容器
docker compose -f docker-compose-deps.yml up -d --build one-api
```

### 2. 发送测试请求
```bash
# 成功请求（200）
curl http://localhost:3000/api/status

# 404 错误
curl http://localhost:3000/api/nonexistent

# POST 请求
curl -X POST http://localhost:3000/api/user/login \
  -H "Content-Type: application/json" \
  -d '{"username":"test","password":"wrong"}'
```

### 3. 检查日志文件
```bash
# 查看最新日志
tail -20 /path/to/logs/oneapi-20260115.log

# 应该能看到包含 status, method, path 的 JSON 日志
```

### 4. 在 Grafana 中查询
```logql
# 查询所有 HTTP 请求
{job="oneapi"} | json | status != ""
```

---

## 性能优化建议

### 1. 减少日志量

```go
// 不记录健康检查端点
if c.Request.URL.Path == "/health" || c.Request.URL.Path == "/ready" {
    c.Next()
    return
}

// 只记录慢请求和错误
if statusCode < 400 && latency < 100*time.Millisecond {
    c.Next()
    return
}
```

### 2. 采样记录

```go
import "math/rand"

// 只记录 10% 的成功请求
if statusCode < 400 && rand.Float64() > 0.1 {
    c.Next()
    return
}
```

### 3. 异步写入

```go
// 将日志写入通道，异步处理
logChan <- LogEntry{
    Status: statusCode,
    Method: c.Request.Method,
    // ...
}
```

---

## 常见问题

### Q1: 为什么搜索不到 Operations？

**A**: 因为日志中没有记录 HTTP 访问信息。需要实现访问日志中间件。

### Q2: 查询语法 `|= \`status >= 200\`` 为什么不工作？

**A**: 这是错误的语法。应该使用：
```logql
| json | status >= 200
```

`|=` 用于文本搜索，不是 JSON 字段过滤。

### Q3: 如何只记录错误请求？

**A**: 在中间件中添加条件：
```go
if statusCode < 400 {
    return  // 不记录成功请求
}
```

### Q4: 日志量太大怎么办？

**A**:
1. 只记录重要端点
2. 采样记录（如 10%）
3. 不记录健康检查
4. 配置 Loki 保留策略

---

## 相关文档

- [Promtail 配置](../promtail-config.yaml)
- [Loki 配置](./loki-config.yaml)
- [故障排查指南](./TROUBLESHOOTING.md)
- [LogQL 官方文档](https://grafana.com/docs/loki/latest/query/)

---

## 更新日志

- **2026-01-15**: 初始版本，说明如何启用 HTTP 访问日志记录
