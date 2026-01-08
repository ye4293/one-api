# 日志系统优化与 CloudWatch 集成实施方案

## 📋 项目概述

本文档描述了优化现有日志系统并集成 AWS CloudWatch 监控的实施方案。主要目标：

1. **日志分离**：将错误日志独立存储到 `oneapi-error-YYYYMMDD.log` 文件
2. **CloudWatch 集成**：实现全面的系统监控，覆盖延迟、流量、错误和饱和度四大黄金指标

## 🎯 核心需求

### 1. 日志系统优化
- 错误日志（ERROR 级别）单独写入 `oneapi-error-YYYYMMDD.log`
- 普通日志（INFO/WARN/DEBUG）继续写入 `oneapi-YYYYMMDD.log`
- 避免日志重复写入
- 保持每日轮转机制

### 2. CloudWatch 监控指标

#### 延迟 (Latency)
- **成功请求延迟**：2xx/3xx 状态码的请求处理时间
- **失败请求延迟**：4xx/5xx 状态码的请求处理时间
- 统计维度：平均值、P50、P95、P99、最大值

#### 流量 (Traffic)
- **QPS (Queries Per Second)**：每秒请求数
- **并发数**：同时处理的请求数量
- **请求总量**：按时间窗口统计的请求总数

#### 错误 (Errors)
- **显式失败**：4xx 错误（客户端错误）
- **隐式失败**：5xx 错误（服务端错误）
- **策略性失败**：
  - 401/403：认证/授权失败
  - 429：限流
  - 特定错误消息（如 API key invalid）
- **失败率**：失败请求占比

#### 饱和度 (Saturation)
- **Goroutine 数量**：当前运行的 goroutine 总数
- **内存使用**：
  - Alloc：当前分配的内存
  - Sys：从系统申请的内存
  - NumGC：GC 次数
- **并发请求占比**：当前并发数 / 最大并发数

## 🏗️ 技术架构

```mermaid
flowchart TB
    subgraph client [客户端请求]
        req[HTTP Request]
    end
    
    subgraph middleware [中间件层]
        metrics[Metrics Middleware]
        logger[Logger Middleware]
    end
    
    subgraph monitoring [监控层]
        cw[CloudWatch Reporter]
        buffer[Metrics Buffer]
    end
    
    subgraph logging [日志层]
        generalLog[oneapi-YYYYMMDD.log]
        errorLog[oneapi-error-YYYYMMDD.log]
    end
    
    subgraph aws [AWS Services]
        cloudwatch[AWS CloudWatch]
    end
    
    req --> metrics
    metrics --> |记录延迟/并发| cw
    metrics --> logger
    logger --> |INFO/WARN/DEBUG| generalLog
    logger --> |ERROR| errorLog
    cw --> buffer
    buffer --> |定期刷新| cloudwatch
```

## 📝 实施任务清单

### 任务 1: 添加 AWS CloudWatch SDK 依赖
**文件**: `go.mod`

**操作**:
```bash
go get github.com/aws/aws-sdk-go-v2/service/cloudwatch
```

**说明**: 项目已有 AWS SDK v2（用于 S3、Bedrock），添加 CloudWatch 服务模块。

---

### 任务 2: 分离错误日志
**文件**: `common/logger/logger.go`

**当前问题**:
- 所有日志都写入同一个文件 `oneapi-YYYYMMDD.log`
- ERROR 日志和 INFO 日志混在一起，不便于错误排查

**实施方案**:
1. 在 `SetupLogger()` 中打开两个文件：
   - `oneapi-YYYYMMDD.log`：普通日志文件
   - `oneapi-error-YYYYMMDD.log`：错误日志文件
   
2. 修改日志写入逻辑：
   - `gin.DefaultWriter` → stdout + 普通日志文件（INFO/WARN/DEBUG）
   - `gin.DefaultErrorWriter` → stderr + 错误日志文件（ERROR）

3. 在 `logHelper()` 中根据日志级别选择输出：
   ```go
   // INFO/WARN/DEBUG → gin.DefaultWriter
   // ERROR → gin.DefaultErrorWriter
   ```

4. 保持每日轮转机制（已有实现）

**预期结果**:
```
logs/
  ├── oneapi-20260108.log       # INFO/WARN/DEBUG
  └── oneapi-error-20260108.log # ERROR only
```

---

### 任务 3: CloudWatch 配置
**文件**: `common/config/config.go`

**新增配置项**:
```go
// CloudWatch 配置
var CloudWatchEnabled = env.Bool("CLOUDWATCH_ENABLED", false)
var CloudWatchNamespace = env.String("CLOUDWATCH_NAMESPACE", "OneAPI")
var CloudWatchRegion = env.String("CLOUDWATCH_REGION", "us-east-1")
var CloudWatchFlushInterval = env.Int("CLOUDWATCH_FLUSH_INTERVAL", 60) // 指标发送间隔（秒）
var CloudWatchSampleInterval = env.Int("CLOUDWATCH_SAMPLE_INTERVAL", 10) // 饱和度采样间隔（秒）
```

**环境变量说明**:
| 变量名 | 默认值 | 说明 |
|--------|--------|------|
| `CLOUDWATCH_ENABLED` | `false` | 是否启用 CloudWatch |
| `CLOUDWATCH_NAMESPACE` | `OneAPI` | CloudWatch 命名空间 |
| `CLOUDWATCH_REGION` | `us-east-1` | AWS 区域 |
| `CLOUDWATCH_FLUSH_INTERVAL` | `60` | 指标发送到 CloudWatch 的间隔（秒） |
| `CLOUDWATCH_SAMPLE_INTERVAL` | `10` | **饱和度指标采样间隔（秒）** |
| `AWS_ACCESS_KEY_ID` | - | AWS 访问密钥（复用现有） |
| `AWS_SECRET_ACCESS_KEY` | - | AWS 密钥（复用现有） |
| `AWS_REGION` | - | AWS 区域（复用现有） |

**频率控制说明**:
- **延迟/流量/错误指标**：每个请求实时记录，每 `CLOUDWATCH_FLUSH_INTERVAL` 秒发送一次
- **饱和度指标**：每 `CLOUDWATCH_SAMPLE_INTERVAL` 秒采样一次，每 `CLOUDWATCH_FLUSH_INTERVAL` 秒发送一次
- 默认配置（采样 10 秒，发送 60 秒）意味着每次发送包含 6 个采样点的统计数据（平均值、最大值）

---

### 任务 4: CloudWatch Reporter 实现
**文件**: `monitor/cloudwatch.go`（新建）

**核心功能**:

#### 4.1 指标数据结构
```go
type MetricData struct {
    // 延迟指标
    SuccessLatencies []float64 // 成功请求延迟
    FailureLatencies []float64 // 失败请求延迟
    
    // 流量指标
    RequestCount     int64      // 请求总数
    ConcurrentCount  int64      // 当前并发数
    MaxConcurrent    int64      // 最大并发数
    
    // 错误指标
    ExplicitErrors   int64      // 4xx 错误
    ImplicitErrors   int64      // 5xx 错误
    PolicyErrors     int64      // 策略性错误（429, 401, 403）
    
    // 饱和度指标
    GoroutineCount   int        // Goroutine 数量
    MemoryAllocMB    uint64     // 内存分配（MB）
    MemorySysMB      uint64     // 系统内存（MB）
}
```

#### 4.2 CloudWatch Reporter
```go
type CloudWatchReporter struct {
    client      *cloudwatch.Client
    namespace   string
    buffer      *MetricData
    bufferMutex sync.Mutex
    ticker      *time.Ticker
}

// 启动 Reporter
func StartCloudWatchReporter(ctx context.Context) error

// 记录请求指标
func RecordRequest(latency time.Duration, statusCode int, success bool)

// 刷新指标到 CloudWatch
func (r *CloudWatchReporter) Flush() error
```

#### 4.3 错误分类逻辑
```go
func classifyError(statusCode int) string {
    switch {
    case statusCode >= 200 && statusCode < 400:
        return "success"
    case statusCode == 401 || statusCode == 403:
        return "policy_error"
    case statusCode == 429:
        return "rate_limit_error"
    case statusCode >= 400 && statusCode < 500:
        return "explicit_error"
    case statusCode >= 500:
        return "implicit_error"
    default:
        return "unknown"
    }
}
```

#### 4.4 定期刷新逻辑
- 使用 `time.Ticker` 定期刷新（默认 60 秒）
- 每次刷新时将缓冲区数据发送到 CloudWatch
- 刷新后清空缓冲区
- 计算统计值（平均值、P95、P99 等）

#### 4.5 饱和度监控
```go
// 饱和度采样缓冲区
type SaturationSamples struct {
    GoroutineSamples []int       // Goroutine 采样点
    MemoryAllocSamples []uint64  // 内存分配采样点
    MemorySysSamples []uint64    // 系统内存采样点
    mutex sync.Mutex
}

// 定期采样系统资源（每 CLOUDWATCH_SAMPLE_INTERVAL 秒）
func (r *CloudWatchReporter) startSaturationSampling(ctx context.Context) {
    ticker := time.NewTicker(time.Duration(config.CloudWatchSampleInterval) * time.Second)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            r.sampleSaturation()
        }
    }
}

// 采样系统资源
func (r *CloudWatchReporter) sampleSaturation() {
    // Goroutine 数量
    goroutineCount := runtime.NumGoroutine()
    
    // 内存统计
    var m runtime.MemStats
    runtime.ReadMemStats(&m)
    
    // 添加到采样缓冲区
    r.saturationSamples.mutex.Lock()
    r.saturationSamples.GoroutineSamples = append(r.saturationSamples.GoroutineSamples, goroutineCount)
    r.saturationSamples.MemoryAllocSamples = append(r.saturationSamples.MemoryAllocSamples, m.Alloc/1024/1024)
    r.saturationSamples.MemorySysSamples = append(r.saturationSamples.MemorySysSamples, m.Sys/1024/1024)
    r.saturationSamples.mutex.Unlock()
}

// 刷新时计算饱和度统计值
func (r *CloudWatchReporter) calculateSaturationStats() (avgGoroutine, maxGoroutine int, avgMemory, maxMemory uint64) {
    r.saturationSamples.mutex.Lock()
    defer r.saturationSamples.mutex.Unlock()
    
    // 计算 Goroutine 统计
    if len(r.saturationSamples.GoroutineSamples) > 0 {
        sum := 0
        maxGoroutine = r.saturationSamples.GoroutineSamples[0]
        for _, v := range r.saturationSamples.GoroutineSamples {
            sum += v
            if v > maxGoroutine {
                maxGoroutine = v
            }
        }
        avgGoroutine = sum / len(r.saturationSamples.GoroutineSamples)
    }
    
    // 计算内存统计（类似逻辑）
    // ...
    
    // 清空采样缓冲区
    r.saturationSamples.GoroutineSamples = r.saturationSamples.GoroutineSamples[:0]
    r.saturationSamples.MemoryAllocSamples = r.saturationSamples.MemoryAllocSamples[:0]
    r.saturationSamples.MemorySysSamples = r.saturationSamples.MemorySysSamples[:0]
    
    return
}
```

**采样和发送逻辑**:
- 采样频率：每 `CLOUDWATCH_SAMPLE_INTERVAL` 秒（默认 10 秒）
- 发送频率：每 `CLOUDWATCH_FLUSH_INTERVAL` 秒（默认 60 秒）
- 每次发送时，计算采样周期内的平均值和最大值
- 发送后清空采样缓冲区

**示例（默认配置）**:
```
时间轴: 0s -> 10s -> 20s -> 30s -> 40s -> 50s -> 60s
采样:   ✓     ✓      ✓      ✓      ✓      ✓      
发送:                                             ✓ (包含 6 个采样点的统计)
```

---

### 任务 5: Metrics 中间件实现
**文件**: `middleware/metrics.go`（新建）

**功能**:
```go
func CloudWatchMetrics() gin.HandlerFunc {
    return func(c *gin.Context) {
        // 1. 记录开始时间
        startTime := time.Now()
        
        // 2. 增加并发计数
        monitor.IncrementConcurrent()
        defer monitor.DecrementConcurrent()
        
        // 3. 处理请求
        c.Next()
        
        // 4. 计算延迟
        latency := time.Since(startTime)
        
        // 5. 获取状态码
        statusCode := c.Writer.Status()
        success := statusCode >= 200 && statusCode < 400
        
        // 6. 记录到 CloudWatch
        monitor.RecordRequest(latency, statusCode, success)
    }
}
```

**集成位置**: 在 `SetUpLogger()` 之后、业务路由之前注册

---

### 任务 6: 主程序集成
**文件**: `main.go`

**修改点**:

#### 6.1 启动 CloudWatch Reporter
```go
// 在 main() 函数中，初始化数据库之后
if config.CloudWatchEnabled {
    logger.SysLog("starting CloudWatch reporter...")
    ctx := context.Background()
    err = monitor.StartCloudWatchReporter(ctx)
    if err != nil {
        logger.SysError(fmt.Sprintf("failed to start CloudWatch reporter: %s", err.Error()))
    } else {
        logger.SysLog("CloudWatch reporter started successfully")
    }
}
```

#### 6.2 注册 Metrics 中间件
```go
// 在设置路由之前
server := gin.New()
server.Use(gin.Recovery())
server.Use(middleware.RequestId())

// 添加 CloudWatch Metrics 中间件
if config.CloudWatchEnabled {
    server.Use(middleware.CloudWatchMetrics())
}

middleware.SetUpLogger(server)
```

---

## 🔧 配置示例

### Docker Compose 配置
```yaml
environment:
  # 日志配置
  - LOG_DIR=/app/logs
  
  # CloudWatch 配置
  - CLOUDWATCH_ENABLED=true
  - CLOUDWATCH_NAMESPACE=OneAPI-Production
  - CLOUDWATCH_REGION=us-east-1
  - CLOUDWATCH_FLUSH_INTERVAL=60        # 指标发送间隔（秒）
  - CLOUDWATCH_SAMPLE_INTERVAL=10       # 饱和度采样间隔（秒）
  
  # AWS 认证
  - AWS_ACCESS_KEY_ID=AKIAXXXXXXXXXXXXXXXX
  - AWS_SECRET_ACCESS_KEY=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
  - AWS_REGION=us-east-1
```

### 环境变量配置
```bash
export CLOUDWATCH_ENABLED=true
export CLOUDWATCH_NAMESPACE=OneAPI-Dev
export CLOUDWATCH_REGION=ap-southeast-1
export CLOUDWATCH_FLUSH_INTERVAL=60      # 每 60 秒发送一次指标
export CLOUDWATCH_SAMPLE_INTERVAL=10     # 每 10 秒采样一次饱和度
export AWS_ACCESS_KEY_ID=your_access_key
export AWS_SECRET_ACCESS_KEY=your_secret_key
```

### 频率配置建议

| 场景 | 采样间隔 | 发送间隔 | 说明 |
|------|---------|---------|------|
| **生产环境（推荐）** | 10 秒 | 60 秒 | 平衡精度和成本 |
| **高精度监控** | 5 秒 | 30 秒 | 更频繁的数据，成本更高 |
| **成本优化** | 30 秒 | 120 秒 | 降低 CloudWatch 调用次数 |
| **开发测试** | 5 秒 | 10 秒 | 快速验证，不考虑成本 |

**注意**: 
- 采样间隔不应大于发送间隔
- 建议采样间隔是发送间隔的 1/5 到 1/10，以便获得有意义的统计数据
- CloudWatch API 有速率限制，发送间隔不建议低于 10 秒

---

## 📊 CloudWatch 指标列表

### 延迟指标
| 指标名称 | 单位 | 统计方式 | 说明 |
|---------|------|---------|------|
| `RequestLatency` | 毫秒 | Avg, P50, P95, P99, Max | 所有请求延迟 |
| `SuccessLatency` | 毫秒 | Avg, P50, P95, P99 | 成功请求延迟 |
| `FailureLatency` | 毫秒 | Avg, P50, P95, P99 | 失败请求延迟 |

### 流量指标
| 指标名称 | 单位 | 统计方式 | 说明 |
|---------|------|---------|------|
| `RequestCount` | 次数 | Sum | 总请求数 |
| `QPS` | 次/秒 | Avg | 每秒请求数 |
| `ConcurrentRequests` | 数量 | Avg, Max | 并发请求数 |

### 错误指标
| 指标名称 | 单位 | 统计方式 | 说明 |
|---------|------|---------|------|
| `ExplicitErrors` | 次数 | Sum | 4xx 错误 |
| `ImplicitErrors` | 次数 | Sum | 5xx 错误 |
| `PolicyErrors` | 次数 | Sum | 策略性错误 |
| `ErrorRate` | 百分比 | Avg | 失败率 |

### 饱和度指标
| 指标名称 | 单位 | 统计方式 | 说明 |
|---------|------|---------|------|
| `GoroutineCount` | 数量 | Avg, Max | Goroutine 数量 |
| `MemoryAllocMB` | MB | Avg, Max | 内存分配 |
| `MemorySysMB` | MB | Avg | 系统内存 |
| `ConcurrencyRate` | 百分比 | Avg, Max | 并发占比 |

---

## 🔍 监控告警建议

### 延迟告警
- **P95 延迟 > 1000ms**：性能下降
- **P99 延迟 > 5000ms**：严重性能问题

### 错误告警
- **错误率 > 5%**：系统异常
- **5xx 错误率 > 1%**：服务端问题

### 饱和度告警
- **Goroutine 数量 > 5000**：可能的 Goroutine 泄漏
- **内存使用 > 80%**：内存压力
- **并发占比 > 80%**：接近容量上限

---

## ✅ 验证清单

### 日志分离验证
- [ ] ERROR 日志只出现在 `oneapi-error-*.log`
- [ ] INFO/WARN/DEBUG 日志只出现在 `oneapi-*.log`
- [ ] 日志文件每日正常轮转
- [ ] 控制台输出正常（stdout/stderr）

### CloudWatch 验证
- [ ] CloudWatch 指标正常上报
- [ ] 延迟指标准确（成功/失败分类正确）
- [ ] 并发数统计准确
- [ ] 错误分类正确（4xx/5xx/策略性）
- [ ] 饱和度指标正常采样

### 性能验证
- [ ] 中间件对性能影响 < 1ms
- [ ] 内存占用增长正常
- [ ] 无明显 Goroutine 泄漏

---

## 🚀 实施顺序

1. **任务 1**: 添加 CloudWatch SDK 依赖（前置依赖）
2. **任务 2**: 分离错误日志（独立任务，可并行）
3. **任务 3**: CloudWatch 配置（独立任务，可并行）
4. **任务 4**: CloudWatch Reporter 实现（依赖任务 1、3）
5. **任务 5**: Metrics 中间件实现（依赖任务 4）
6. **任务 6**: 主程序集成（依赖任务 5）

---

## 📚 参考资料

- [AWS CloudWatch SDK for Go v2](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/cloudwatch)
- [Google SRE Book - The Four Golden Signals](https://sre.google/sre-book/monitoring-distributed-systems/)
- [Gin Middleware Documentation](https://gin-gonic.com/docs/examples/custom-middleware/)

---

**文档版本**: v1.0  
**创建日期**: 2026-01-08  
**最后更新**: 2026-01-08
