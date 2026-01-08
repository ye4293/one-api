package monitor

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
)

// MetricData 指标数据缓冲区
type MetricData struct {
	// 延迟指标
	SuccessLatencies []float64
	FailureLatencies []float64

	// 流量指标
	RequestCount    int64
	ConcurrentCount int64
	MaxConcurrent   int64

	// 错误指标
	ExplicitErrors int64 // 4xx 错误
	ImplicitErrors int64 // 5xx 错误
	PolicyErrors   int64 // 策略性错误（429, 401, 403）

	mutex sync.Mutex
}

// SaturationSamples 饱和度采样缓冲区
type SaturationSamples struct {
	GoroutineSamples   []int
	MemoryAllocSamples []uint64
	MemorySysSamples   []uint64
	mutex              sync.Mutex
}

// CloudWatchReporter CloudWatch 报告器
type CloudWatchReporter struct {
	client             *cloudwatch.Client
	namespace          string
	buffer             *MetricData
	saturationSamples  *SaturationSamples
	concurrentRequests int64 // 当前并发请求数
	flushTicker        *time.Ticker
	sampleTicker       *time.Ticker
	ctx                context.Context
	cancel             context.CancelFunc
}

var globalReporter *CloudWatchReporter
var reporterMutex sync.Mutex

// StartCloudWatchReporter 启动 CloudWatch Reporter
func StartCloudWatchReporter(ctx context.Context) error {
	if !config.CloudWatchEnabled {
		return nil
	}

	reporterMutex.Lock()
	defer reporterMutex.Unlock()

	if globalReporter != nil {
		return fmt.Errorf("cloudwatch reporter already started")
	}

	// 加载 AWS 配置
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(config.CloudWatchRegion),
	)
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}

	// 创建 CloudWatch 客户端
	client := cloudwatch.NewFromConfig(cfg)

	// 创建 reporter
	reporterCtx, cancel := context.WithCancel(ctx)
	reporter := &CloudWatchReporter{
		client:            client,
		namespace:         config.CloudWatchNamespace,
		buffer:            &MetricData{},
		saturationSamples: &SaturationSamples{},
		flushTicker:       time.NewTicker(time.Duration(config.CloudWatchFlushInterval) * time.Second),
		sampleTicker:      time.NewTicker(time.Duration(config.CloudWatchSampleInterval) * time.Second),
		ctx:               reporterCtx,
		cancel:            cancel,
	}

	globalReporter = reporter

	// 启动刷新和采样 goroutine
	go reporter.flushLoop()
	go reporter.sampleLoop()

	logger.SysLog(fmt.Sprintf("CloudWatch reporter started (namespace: %s, region: %s, flush: %ds, sample: %ds)",
		config.CloudWatchNamespace, config.CloudWatchRegion, config.CloudWatchFlushInterval, config.CloudWatchSampleInterval))

	return nil
}

// StopCloudWatchReporter 停止 CloudWatch Reporter
func StopCloudWatchReporter() {
	reporterMutex.Lock()
	defer reporterMutex.Unlock()

	if globalReporter != nil {
		globalReporter.cancel()
		globalReporter.flushTicker.Stop()
		globalReporter.sampleTicker.Stop()
		// 最后一次刷新
		globalReporter.flush()
		globalReporter = nil
		logger.SysLog("CloudWatch reporter stopped")
	}
}

// RecordRequest 记录请求指标
func RecordRequest(latency time.Duration, statusCode int, success bool) {
	if !config.CloudWatchEnabled || globalReporter == nil {
		return
	}

	globalReporter.recordRequest(latency, statusCode, success)
}

// IncrementConcurrent 增加并发计数
func IncrementConcurrent() {
	if !config.CloudWatchEnabled || globalReporter == nil {
		return
	}

	current := atomic.AddInt64(&globalReporter.concurrentRequests, 1)
	globalReporter.updateMaxConcurrent(current)
}

// DecrementConcurrent 减少并发计数
func DecrementConcurrent() {
	if !config.CloudWatchEnabled || globalReporter == nil {
		return
	}

	atomic.AddInt64(&globalReporter.concurrentRequests, -1)
}

// recordRequest 记录单个请求的指标
func (r *CloudWatchReporter) recordRequest(latency time.Duration, statusCode int, success bool) {
	r.buffer.mutex.Lock()
	defer r.buffer.mutex.Unlock()

	latencyMs := float64(latency.Milliseconds())

	// 记录延迟
	if success {
		r.buffer.SuccessLatencies = append(r.buffer.SuccessLatencies, latencyMs)
	} else {
		r.buffer.FailureLatencies = append(r.buffer.FailureLatencies, latencyMs)
	}

	// 记录请求数
	r.buffer.RequestCount++

	// 分类错误
	errorType := classifyError(statusCode)
	switch errorType {
	case "explicit_error":
		r.buffer.ExplicitErrors++
	case "implicit_error":
		r.buffer.ImplicitErrors++
	case "policy_error":
		r.buffer.PolicyErrors++
	}
}

// updateMaxConcurrent 更新最大并发数
func (r *CloudWatchReporter) updateMaxConcurrent(current int64) {
	r.buffer.mutex.Lock()
	defer r.buffer.mutex.Unlock()

	if current > r.buffer.MaxConcurrent {
		r.buffer.MaxConcurrent = current
	}
}

// classifyError 错误分类
func classifyError(statusCode int) string {
	switch {
	case statusCode >= 200 && statusCode < 400:
		return "success"
	case statusCode == 401 || statusCode == 403:
		return "policy_error"
	case statusCode == 429:
		return "policy_error"
	case statusCode >= 400 && statusCode < 500:
		return "explicit_error"
	case statusCode >= 500:
		return "implicit_error"
	default:
		return "unknown"
	}
}

// sampleLoop 饱和度采样循环
func (r *CloudWatchReporter) sampleLoop() {
	// 立即采样一次
	r.sampleSaturation()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.sampleTicker.C:
			r.sampleSaturation()
		}
	}
}

// sampleSaturation 采样系统资源
func (r *CloudWatchReporter) sampleSaturation() {
	// Goroutine 数量
	goroutineCount := runtime.NumGoroutine()

	// 内存统计
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	r.saturationSamples.mutex.Lock()
	defer r.saturationSamples.mutex.Unlock()

	r.saturationSamples.GoroutineSamples = append(r.saturationSamples.GoroutineSamples, goroutineCount)
	r.saturationSamples.MemoryAllocSamples = append(r.saturationSamples.MemoryAllocSamples, m.Alloc/1024/1024)
	r.saturationSamples.MemorySysSamples = append(r.saturationSamples.MemorySysSamples, m.Sys/1024/1024)
}

// flushLoop 定期刷新指标到 CloudWatch
func (r *CloudWatchReporter) flushLoop() {
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.flushTicker.C:
			r.flush()
		}
	}
}

// flush 刷新指标到 CloudWatch
func (r *CloudWatchReporter) flush() {
	// 获取并重置缓冲区
	r.buffer.mutex.Lock()
	data := r.buffer
	r.buffer = &MetricData{}
	r.buffer.mutex.Unlock()

	// 获取并重置饱和度样本
	r.saturationSamples.mutex.Lock()
	satSamples := r.saturationSamples
	r.saturationSamples = &SaturationSamples{}
	r.saturationSamples.mutex.Unlock()

	// 如果没有数据，跳过
	if data.RequestCount == 0 && len(satSamples.GoroutineSamples) == 0 {
		return
	}

	// 构建 CloudWatch 指标
	metricData := []types.MetricDatum{}
	timestamp := aws.Time(time.Now())

	// 延迟指标
	if len(data.SuccessLatencies) > 0 {
		metricData = append(metricData, r.buildLatencyMetrics("SuccessLatency", data.SuccessLatencies, timestamp)...)
	}
	if len(data.FailureLatencies) > 0 {
		metricData = append(metricData, r.buildLatencyMetrics("FailureLatency", data.FailureLatencies, timestamp)...)
	}

	// 流量指标
	if data.RequestCount > 0 {
		metricData = append(metricData, types.MetricDatum{
			MetricName: aws.String("RequestCount"),
			Value:      aws.Float64(float64(data.RequestCount)),
			Unit:       types.StandardUnitCount,
			Timestamp:  timestamp,
		})

		// QPS = RequestCount / FlushInterval
		qps := float64(data.RequestCount) / float64(config.CloudWatchFlushInterval)
		metricData = append(metricData, types.MetricDatum{
			MetricName: aws.String("QPS"),
			Value:      aws.Float64(qps),
			Unit:       types.StandardUnitCountSecond,
			Timestamp:  timestamp,
		})
	}

	if data.MaxConcurrent > 0 {
		metricData = append(metricData, types.MetricDatum{
			MetricName: aws.String("MaxConcurrentRequests"),
			Value:      aws.Float64(float64(data.MaxConcurrent)),
			Unit:       types.StandardUnitCount,
			Timestamp:  timestamp,
		})
	}

	// 错误指标
	if data.ExplicitErrors > 0 {
		metricData = append(metricData, types.MetricDatum{
			MetricName: aws.String("ExplicitErrors"),
			Value:      aws.Float64(float64(data.ExplicitErrors)),
			Unit:       types.StandardUnitCount,
			Timestamp:  timestamp,
		})
	}
	if data.ImplicitErrors > 0 {
		metricData = append(metricData, types.MetricDatum{
			MetricName: aws.String("ImplicitErrors"),
			Value:      aws.Float64(float64(data.ImplicitErrors)),
			Unit:       types.StandardUnitCount,
			Timestamp:  timestamp,
		})
	}
	if data.PolicyErrors > 0 {
		metricData = append(metricData, types.MetricDatum{
			MetricName: aws.String("PolicyErrors"),
			Value:      aws.Float64(float64(data.PolicyErrors)),
			Unit:       types.StandardUnitCount,
			Timestamp:  timestamp,
		})
	}

	// 错误率
	if data.RequestCount > 0 {
		totalErrors := data.ExplicitErrors + data.ImplicitErrors + data.PolicyErrors
		errorRate := float64(totalErrors) / float64(data.RequestCount) * 100
		metricData = append(metricData, types.MetricDatum{
			MetricName: aws.String("ErrorRate"),
			Value:      aws.Float64(errorRate),
			Unit:       types.StandardUnitPercent,
			Timestamp:  timestamp,
		})
	}

	// 饱和度指标
	if len(satSamples.GoroutineSamples) > 0 {
		avgGoroutine, maxGoroutine := calculateStats(satSamples.GoroutineSamples)
		metricData = append(metricData,
			types.MetricDatum{
				MetricName: aws.String("GoroutineCount"),
				Value:      aws.Float64(avgGoroutine),
				Unit:       types.StandardUnitCount,
				Timestamp:  timestamp,
			},
			types.MetricDatum{
				MetricName: aws.String("MaxGoroutineCount"),
				Value:      aws.Float64(maxGoroutine),
				Unit:       types.StandardUnitCount,
				Timestamp:  timestamp,
			},
		)
	}

	if len(satSamples.MemoryAllocSamples) > 0 {
		avgMemAlloc, maxMemAlloc := calculateStatsUint64(satSamples.MemoryAllocSamples)
		metricData = append(metricData,
			types.MetricDatum{
				MetricName: aws.String("MemoryAllocMB"),
				Value:      aws.Float64(avgMemAlloc),
				Unit:       types.StandardUnitMegabytes,
				Timestamp:  timestamp,
			},
			types.MetricDatum{
				MetricName: aws.String("MaxMemoryAllocMB"),
				Value:      aws.Float64(maxMemAlloc),
				Unit:       types.StandardUnitMegabytes,
				Timestamp:  timestamp,
			},
		)
	}

	if len(satSamples.MemorySysSamples) > 0 {
		avgMemSys, maxMemSys := calculateStatsUint64(satSamples.MemorySysSamples)
		metricData = append(metricData,
			types.MetricDatum{
				MetricName: aws.String("MemorySysMB"),
				Value:      aws.Float64(avgMemSys),
				Unit:       types.StandardUnitMegabytes,
				Timestamp:  timestamp,
			},
			types.MetricDatum{
				MetricName: aws.String("MaxMemorySysMB"),
				Value:      aws.Float64(maxMemSys),
				Unit:       types.StandardUnitMegabytes,
				Timestamp:  timestamp,
			},
		)
	}

	// 发送到 CloudWatch
	if len(metricData) > 0 {
		r.sendMetrics(metricData)
	}
}

// buildLatencyMetrics 构建延迟指标（包含平均值、P50、P95、P99）
func (r *CloudWatchReporter) buildLatencyMetrics(metricName string, latencies []float64, timestamp *time.Time) []types.MetricDatum {
	if len(latencies) == 0 {
		return nil
	}

	// 排序以计算百分位
	sorted := make([]float64, len(latencies))
	copy(sorted, latencies)
	sort.Float64s(sorted)

	// 计算统计值
	avg := calculateAverage(sorted)
	p50 := calculatePercentile(sorted, 0.50)
	p95 := calculatePercentile(sorted, 0.95)
	p99 := calculatePercentile(sorted, 0.99)
	max := sorted[len(sorted)-1]

	return []types.MetricDatum{
		{
			MetricName: aws.String(metricName + "Avg"),
			Value:      aws.Float64(avg),
			Unit:       types.StandardUnitMilliseconds,
			Timestamp:  timestamp,
		},
		{
			MetricName: aws.String(metricName + "P50"),
			Value:      aws.Float64(p50),
			Unit:       types.StandardUnitMilliseconds,
			Timestamp:  timestamp,
		},
		{
			MetricName: aws.String(metricName + "P95"),
			Value:      aws.Float64(p95),
			Unit:       types.StandardUnitMilliseconds,
			Timestamp:  timestamp,
		},
		{
			MetricName: aws.String(metricName + "P99"),
			Value:      aws.Float64(p99),
			Unit:       types.StandardUnitMilliseconds,
			Timestamp:  timestamp,
		},
		{
			MetricName: aws.String(metricName + "Max"),
			Value:      aws.Float64(max),
			Unit:       types.StandardUnitMilliseconds,
			Timestamp:  timestamp,
		},
	}
}

// sendMetrics 发送指标到 CloudWatch（分批发送，每次最多 1000 个）
func (r *CloudWatchReporter) sendMetrics(metricData []types.MetricDatum) {
	const maxMetricsPerRequest = 1000

	for i := 0; i < len(metricData); i += maxMetricsPerRequest {
		end := i + maxMetricsPerRequest
		if end > len(metricData) {
			end = len(metricData)
		}

		batch := metricData[i:end]

		input := &cloudwatch.PutMetricDataInput{
			Namespace:  aws.String(r.namespace),
			MetricData: batch,
		}

		_, err := r.client.PutMetricData(r.ctx, input)
		if err != nil {
			logger.SysError(fmt.Sprintf("Failed to send CloudWatch metrics: %s", err.Error()))
		}
	}
}

// 辅助函数：计算平均值
func calculateAverage(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

// 辅助函数：计算百分位
func calculatePercentile(sortedValues []float64, percentile float64) float64 {
	if len(sortedValues) == 0 {
		return 0
	}
	index := int(float64(len(sortedValues)) * percentile)
	if index >= len(sortedValues) {
		index = len(sortedValues) - 1
	}
	return sortedValues[index]
}

// 辅助函数：计算整数统计值
func calculateStats(values []int) (avg float64, max float64) {
	if len(values) == 0 {
		return 0, 0
	}
	sum := 0
	maxVal := values[0]
	for _, v := range values {
		sum += v
		if v > maxVal {
			maxVal = v
		}
	}
	avg = float64(sum) / float64(len(values))
	max = float64(maxVal)
	return
}

// 辅助函数：计算 uint64 统计值
func calculateStatsUint64(values []uint64) (avg float64, max float64) {
	if len(values) == 0 {
		return 0, 0
	}
	sum := uint64(0)
	maxVal := values[0]
	for _, v := range values {
		sum += v
		if v > maxVal {
			maxVal = v
		}
	}
	avg = float64(sum) / float64(len(values))
	max = float64(maxVal)
	return
}
