package model

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
)

// ========== 公开 API 数据结构 ==========

// ModelMetricsMini 列表页迷你摘要
type ModelMetricsMini struct {
	SuccessRate      float64 `json:"success_rate"`
	AvgLatency       float64 `json:"avg_latency"`
	AvgSpeed         float64 `json:"avg_speed"`
	TotalRequests24h int64   `json:"total_requests_24h"`
	Status           string  `json:"status"` // healthy, degraded, down, no_data
}

// ModelMetricsSummary 单模型完整摘要
type ModelMetricsSummary struct {
	ModelName string `json:"model_name"`
	Provider  string `json:"provider"`
	Current   struct {
		RPM          float64 `json:"rpm"`
		TPM          float64 `json:"tpm"`
		SuccessRate  float64 `json:"success_rate"`
		AvgLatency   float64 `json:"avg_latency"`
		AvgSpeed     float64 `json:"avg_speed"`
		AvgFirstWord float64 `json:"avg_first_word"`
		P50Latency   float64 `json:"p50_latency"`
		P95Latency   float64 `json:"p95_latency"`
		P99Latency   float64 `json:"p99_latency"`
	} `json:"current"`
	Period24h struct {
		TotalRequests int64   `json:"total_requests"`
		SuccessRate   float64 `json:"success_rate"`
		AvgLatency    float64 `json:"avg_latency"`
		AvgSpeed      float64 `json:"avg_speed"`
		TotalTokens   int64   `json:"total_tokens"`
	} `json:"period_24h"`
}

// MetricsTimePoint 时间序列数据点
type MetricsTimePoint struct {
	Timestamp     int64   `json:"timestamp"`
	TotalRequests int64   `json:"total_requests"`
	SuccessRate   float64 `json:"success_rate"`
	AvgLatency    float64 `json:"avg_latency"`
	AvgSpeed      float64 `json:"avg_speed"`
	AvgFirstWord  float64 `json:"avg_first_word"`
	TotalTokens   int64   `json:"total_tokens"`
	PromptTokens  int64   `json:"prompt_tokens"`
	CompTokens    int64   `json:"completion_tokens"`
}

// ChannelMetricsSummary 管理员 channel 级摘要
type ChannelMetricsSummary struct {
	ChannelId        int     `json:"channel_id"`
	ChannelName      string  `json:"channel_name"`
	SuccessRate      float64 `json:"success_rate"`
	AvgLatency       float64 `json:"avg_latency"`
	AvgSpeed         float64 `json:"avg_speed"`
	TotalRequests24h int64   `json:"total_requests_24h"`
}

// ========== 内存缓存 ==========

var metricsCache struct {
	sync.RWMutex

	// 公开数据 (channel_id=0 聚合)
	AllMini   map[string]*ModelMetricsMini    // model_name -> 迷你摘要
	Summaries map[string]*ModelMetricsSummary // model_name -> 完整摘要
	Series24h map[string][]MetricsTimePoint   // model_name -> 24h 时间序列

	// 管理员数据 (channel_id>0 明细)
	AdminChannels map[string][]ChannelMetricsSummary // model_name -> channel 摘要列表

	LastRefresh int64
}

// RefreshMetricsCache 从数据库刷新内存缓存
func RefreshMetricsCache() {
	now := time.Now().UTC()
	currentHour := FloorHour(now.Unix())
	h24Ago := currentHour - 24*3600

	// 1. 查询最近 24h 的 provider 级数据 (channel_id=0)
	publicRows, err := GetModelMetricsRange("", 0, h24Ago, currentHour+3600)
	if err != nil {
		logger.SysError("metrics cache refresh: failed to query public data: " + err.Error())
		return
	}

	// 2. 查询最近 24h 的 channel 级数据 (channel_id>0)
	channelRows, err := GetModelMetricsRangeAllChannels("", h24Ago, currentHour+3600)
	if err != nil {
		logger.SysError("metrics cache refresh: failed to query channel data: " + err.Error())
		// 不影响公开数据，继续
	}

	// 3. 构建公开缓存
	newAllMini := make(map[string]*ModelMetricsMini)
	newSummaries := make(map[string]*ModelMetricsSummary)
	newSeries24h := make(map[string][]MetricsTimePoint)

	// 按 model_name 分组
	type modelAgg struct {
		provider         string
		totalReqs        int64
		successReqs      int64
		sumDuration      float64
		sumSpeed         float64
		speedCount       int64
		sumFirstWord     float64
		firstWordCount   int64
		totalTokens      int64
		latencyHist      *HistogramBuckets
		hourlyRows       []ModelMetrics
	}

	modelMap := make(map[string]*modelAgg)
	for _, row := range publicRows {
		agg, ok := modelMap[row.ModelName]
		if !ok {
			agg = &modelAgg{provider: row.Provider}
			modelMap[row.ModelName] = agg
		}
		agg.totalReqs += row.TotalRequests
		agg.successReqs += row.SuccessRequests
		agg.sumDuration += row.SumDuration
		agg.sumSpeed += row.SumSpeed
		agg.speedCount += row.SpeedCount
		agg.sumFirstWord += row.SumFirstWord
		agg.firstWordCount += row.FirstWordCount
		agg.totalTokens += row.TotalTokens
		agg.latencyHist = MergeHistograms(agg.latencyHist, UnmarshalHistogram(row.LatencyBuckets))
		agg.hourlyRows = append(agg.hourlyRows, row)
	}

	for modelName, agg := range modelMap {
		// Mini 摘要
		successRate := safeDivF(float64(agg.successReqs), float64(agg.totalReqs))
		avgLatency := safeDiv(agg.sumDuration, agg.totalReqs)
		avgSpeed := safeDiv(agg.sumSpeed, agg.speedCount)

		status := "no_data"
		if agg.totalReqs > 0 {
			if successRate >= 0.95 {
				status = "healthy"
			} else if successRate >= 0.80 {
				status = "degraded"
			} else {
				status = "down"
			}
		}

		newAllMini[modelName] = &ModelMetricsMini{
			SuccessRate:      successRate,
			AvgLatency:       avgLatency,
			AvgSpeed:         avgSpeed,
			TotalRequests24h: agg.totalReqs,
			Status:           status,
		}

		// 完整摘要
		summary := &ModelMetricsSummary{
			ModelName: modelName,
			Provider:  agg.provider,
		}

		// Current: 取最近1小时的数据
		var currentReqs, currentSuccess, currentTokens int64
		var currentDuration, currentSpeed, currentFirstWord float64
		var currentSpeedCnt, currentFWCnt int64
		var currentLatencyHist *HistogramBuckets
		for _, row := range agg.hourlyRows {
			if row.HourTimestamp >= currentHour {
				currentReqs += row.TotalRequests
				currentSuccess += row.SuccessRequests
				currentTokens += row.TotalTokens
				currentDuration += row.SumDuration
				currentSpeed += row.SumSpeed
				currentSpeedCnt += row.SpeedCount
				currentFirstWord += row.SumFirstWord
				currentFWCnt += row.FirstWordCount
				currentLatencyHist = MergeHistograms(currentLatencyHist, UnmarshalHistogram(row.LatencyBuckets))
			}
		}
		// RPM/TPM: 当前小时已过的分钟数
		elapsedMin := float64(now.Unix()-currentHour) / 60.0
		if elapsedMin < 1 {
			elapsedMin = 1
		}
		summary.Current.RPM = float64(currentReqs) / elapsedMin
		summary.Current.TPM = float64(currentTokens) / elapsedMin
		summary.Current.SuccessRate = safeDivF(float64(currentSuccess), float64(currentReqs))
		summary.Current.AvgLatency = safeDiv(currentDuration, currentReqs)
		summary.Current.AvgSpeed = safeDiv(currentSpeed, currentSpeedCnt)
		summary.Current.AvgFirstWord = safeDiv(currentFirstWord, currentFWCnt)
		summary.Current.P50Latency = EstimatePercentile(currentLatencyHist, 0.50)
		summary.Current.P95Latency = EstimatePercentile(currentLatencyHist, 0.95)
		summary.Current.P99Latency = EstimatePercentile(currentLatencyHist, 0.99)

		// Period 24h
		summary.Period24h.TotalRequests = agg.totalReqs
		summary.Period24h.SuccessRate = successRate
		summary.Period24h.AvgLatency = avgLatency
		summary.Period24h.AvgSpeed = avgSpeed
		summary.Period24h.TotalTokens = agg.totalTokens

		newSummaries[modelName] = summary

		// 24h 时间序列 (每小时一个点)
		hourMap := make(map[int64]*MetricsTimePoint)
		for _, row := range agg.hourlyRows {
			if p, ok := hourMap[row.HourTimestamp]; ok {
				p.TotalRequests += row.TotalRequests
				p.TotalTokens += row.TotalTokens
				p.PromptTokens += row.PromptTokens
				p.CompTokens += row.CompletionTokens
			} else {
				hourMap[row.HourTimestamp] = &MetricsTimePoint{
					Timestamp:     row.HourTimestamp,
					TotalRequests: row.TotalRequests,
					SuccessRate:   safeDivF(float64(row.SuccessRequests), float64(row.TotalRequests)),
					AvgLatency:    safeDiv(row.SumDuration, row.TotalRequests),
					AvgSpeed:      safeDiv(row.SumSpeed, row.SpeedCount),
					AvgFirstWord:  safeDiv(row.SumFirstWord, row.FirstWordCount),
					TotalTokens:   row.TotalTokens,
					PromptTokens:  row.PromptTokens,
					CompTokens:    row.CompletionTokens,
				}
			}
		}
		// 按时间排序输出
		var points []MetricsTimePoint
		for h := h24Ago; h <= currentHour; h += 3600 {
			if p, ok := hourMap[h]; ok {
				points = append(points, *p)
			} else {
				points = append(points, MetricsTimePoint{Timestamp: h})
			}
		}
		newSeries24h[modelName] = points
	}

	// 4. 构建管理员 channel 缓存（批量加载 channel 名称，避免 N+1）
	newAdminChannels := make(map[string][]ChannelMetricsSummary)
	type channelAgg struct {
		channelId   int
		totalReqs   int64
		successReqs int64
		sumDuration float64
		sumSpeed    float64
		speedCount  int64
	}
	channelMap := make(map[string]map[int]*channelAgg) // model -> channel_id -> agg
	channelIdSet := make(map[int]bool)                 // 收集所有 channel_id
	for _, row := range channelRows {
		if _, ok := channelMap[row.ModelName]; !ok {
			channelMap[row.ModelName] = make(map[int]*channelAgg)
		}
		ca, ok := channelMap[row.ModelName][row.ChannelId]
		if !ok {
			ca = &channelAgg{channelId: row.ChannelId}
			channelMap[row.ModelName][row.ChannelId] = ca
		}
		ca.totalReqs += row.TotalRequests
		ca.successReqs += row.SuccessRequests
		ca.sumDuration += row.SumDuration
		ca.sumSpeed += row.SumSpeed
		ca.speedCount += row.SpeedCount
		channelIdSet[row.ChannelId] = true
	}

	// 批量加载 channel 名称
	channelNameMap := batchGetChannelNames(channelIdSet)

	for modelName, channels := range channelMap {
		var summaries []ChannelMetricsSummary
		for _, ca := range channels {
			channelName := channelNameMap[ca.channelId]
			if channelName == "" {
				channelName = fmt.Sprintf("Channel-%d", ca.channelId)
			}
			summaries = append(summaries, ChannelMetricsSummary{
				ChannelId:        ca.channelId,
				ChannelName:      channelName,
				SuccessRate:      safeDivF(float64(ca.successReqs), float64(ca.totalReqs)),
				AvgLatency:       safeDiv(ca.sumDuration, ca.totalReqs),
				AvgSpeed:         safeDiv(ca.sumSpeed, ca.speedCount),
				TotalRequests24h: ca.totalReqs,
			})
		}
		newAdminChannels[modelName] = summaries
	}

	// 5. 原子替换缓存
	metricsCache.Lock()
	metricsCache.AllMini = newAllMini
	metricsCache.Summaries = newSummaries
	metricsCache.Series24h = newSeries24h
	metricsCache.AdminChannels = newAdminChannels
	metricsCache.LastRefresh = time.Now().Unix()
	metricsCache.Unlock()

	// 6. 写入 Redis（可选）
	if common.RedisEnabled {
		cacheToRedis(newAllMini, newSummaries)
	}
}

// ========== 缓存读取 ==========

// GetCachedAllModelMini 获取全部模型迷你摘要
func GetCachedAllModelMini() map[string]*ModelMetricsMini {
	metricsCache.RLock()
	defer metricsCache.RUnlock()
	if metricsCache.AllMini != nil {
		return metricsCache.AllMini
	}
	return map[string]*ModelMetricsMini{}
}

// GetCachedModelSummary 获取单模型完整摘要
func GetCachedModelSummary(modelName string) *ModelMetricsSummary {
	metricsCache.RLock()
	defer metricsCache.RUnlock()
	if metricsCache.Summaries != nil {
		return metricsCache.Summaries[modelName]
	}
	return nil
}

// GetCachedModel24hSeries 获取单模型 24h 时间序列
// 即使模型不在缓存中，也返回 25 个零值小时点（保证前端图表能渲染空框）
func GetCachedModel24hSeries(modelName string) []MetricsTimePoint {
	metricsCache.RLock()
	defer metricsCache.RUnlock()
	if metricsCache.Series24h != nil {
		if points, ok := metricsCache.Series24h[modelName]; ok && len(points) > 0 {
			return points
		}
	}
	// 返回零值点序列，保证图表有框
	return generate24hEmptyPoints()
}

// generate24hEmptyPoints 生成最近 24 小时的零值时间点
func generate24hEmptyPoints() []MetricsTimePoint {
	now := time.Now().UTC()
	currentHour := FloorHour(now.Unix())
	h24Ago := currentHour - 24*3600
	var points []MetricsTimePoint
	for h := h24Ago; h <= currentHour; h += 3600 {
		points = append(points, MetricsTimePoint{Timestamp: h})
	}
	return points
}

// GetCachedAdminChannels 获取管理员 channel 明细
func GetCachedAdminChannels(modelName string) []ChannelMetricsSummary {
	metricsCache.RLock()
	defer metricsCache.RUnlock()
	if metricsCache.AdminChannels != nil {
		return metricsCache.AdminChannels[modelName]
	}
	return nil
}

// ========== 7d/30d 时间序列查询（按天聚合） ==========

// GetModelTimeSeriesDaily 获取指定天数的日级时间序列（从 DB 查询，缓存到 Redis）
func GetModelTimeSeriesDaily(modelName string, days int) ([]MetricsTimePoint, error) {
	cacheKey := fmt.Sprintf("model_metrics:%dd:%s", days, modelName)

	// 尝试 Redis 缓存
	if common.RedisEnabled {
		cached, err := common.RedisGet(cacheKey)
		if err == nil && cached != "" {
			var points []MetricsTimePoint
			if json.Unmarshal([]byte(cached), &points) == nil {
				return points, nil
			}
		}
	}

	// 从 DB 查询
	now := time.Now().UTC()
	endHour := FloorHour(now.Unix()) + 3600
	startHour := endHour - int64(days)*24*3600

	metrics, err := GetModelMetricsRange(modelName, 0, startHour, endHour)
	if err != nil {
		return nil, err
	}

	// 按天聚合
	dayMap := make(map[int64]*struct {
		totalReqs    int64
		successReqs  int64
		sumDuration  float64
		sumSpeed     float64
		speedCount   int64
		sumFirstWord float64
		fwCount      int64
		totalTokens  int64
		promptTokens int64
		compTokens   int64
	})

	for _, m := range metrics {
		dayStart := m.HourTimestamp - (m.HourTimestamp % 86400)
		d, ok := dayMap[dayStart]
		if !ok {
			d = &struct {
				totalReqs    int64
				successReqs  int64
				sumDuration  float64
				sumSpeed     float64
				speedCount   int64
				sumFirstWord float64
				fwCount      int64
				totalTokens  int64
				promptTokens int64
				compTokens   int64
			}{}
			dayMap[dayStart] = d
		}
		d.totalReqs += m.TotalRequests
		d.successReqs += m.SuccessRequests
		d.sumDuration += m.SumDuration
		d.sumSpeed += m.SumSpeed
		d.speedCount += m.SpeedCount
		d.sumFirstWord += m.SumFirstWord
		d.fwCount += m.FirstWordCount
		d.totalTokens += m.TotalTokens
		d.promptTokens += m.PromptTokens
		d.compTokens += m.CompletionTokens
	}

	// 生成有序数据点
	var points []MetricsTimePoint
	for dayStart := startHour - (startHour % 86400); dayStart < endHour; dayStart += 86400 {
		if d, ok := dayMap[dayStart]; ok {
			points = append(points, MetricsTimePoint{
				Timestamp:     dayStart,
				TotalRequests: d.totalReqs,
				SuccessRate:   safeDivF(float64(d.successReqs), float64(d.totalReqs)),
				AvgLatency:    safeDiv(d.sumDuration, d.totalReqs),
				AvgSpeed:      safeDiv(d.sumSpeed, d.speedCount),
				AvgFirstWord:  safeDiv(d.sumFirstWord, d.fwCount),
				TotalTokens:   d.totalTokens,
				PromptTokens:  d.promptTokens,
				CompTokens:    d.compTokens,
			})
		} else {
			points = append(points, MetricsTimePoint{Timestamp: dayStart})
		}
	}

	// 写入 Redis
	if common.RedisEnabled {
		ttl := 30 * time.Minute
		if days >= 30 {
			ttl = 60 * time.Minute
		}
		if data, err := json.Marshal(points); err == nil {
			_ = common.RedisSet(cacheKey, string(data), ttl)
		}
	}

	return points, nil
}

// ========== 1h 实时查询 ==========

// GetModelTimeSeries1h 获取最近 1h 的 5 分钟粒度时间序列（直接查 logs 表）
func GetModelTimeSeries1h(modelName string) ([]MetricsTimePoint, error) {
	now := time.Now().UTC().Unix()
	startTime := now - 3600

	logs, err := GetRecentLogsForModel(modelName, startTime, now)
	if err != nil {
		return nil, err
	}

	// 按 5 分钟分组
	type bucket struct {
		totalReqs    int64
		successReqs  int64
		sumDuration  float64
		sumSpeed     float64
		speedCount   int64
		sumFirstWord float64
		fwCount      int64
		totalTokens  int64
	}
	bucketMap := make(map[int64]*bucket)

	for _, log := range logs {
		bucketStart := log.CreatedAt - (log.CreatedAt % 300) // 5 分钟
		b, ok := bucketMap[bucketStart]
		if !ok {
			b = &bucket{}
			bucketMap[bucketStart] = b
		}
		b.totalReqs++
		if log.Type == LogTypeConsume {
			b.successReqs++
		}
		b.sumDuration += log.Duration
		if log.Speed > 0 {
			b.sumSpeed += log.Speed
			b.speedCount++
		}
		if log.FirstWordLatency > 0 {
			b.sumFirstWord += log.FirstWordLatency
			b.fwCount++
		}
		b.totalTokens += int64(log.PromptTokens + log.CompletionTokens)
	}

	// 生成 12 个 5 分钟数据点
	var points []MetricsTimePoint
	bucketStart := startTime - (startTime % 300)
	for ts := bucketStart; ts < now; ts += 300 {
		if b, ok := bucketMap[ts]; ok {
			points = append(points, MetricsTimePoint{
				Timestamp:     ts,
				TotalRequests: b.totalReqs,
				SuccessRate:   safeDivF(float64(b.successReqs), float64(b.totalReqs)),
				AvgLatency:    safeDiv(b.sumDuration, b.totalReqs),
				AvgSpeed:      safeDiv(b.sumSpeed, b.speedCount),
				AvgFirstWord:  safeDiv(b.sumFirstWord, b.fwCount),
				TotalTokens:   b.totalTokens,
			})
		} else {
			points = append(points, MetricsTimePoint{Timestamp: ts})
		}
	}

	return points, nil
}

// ========== Redis 缓存辅助 ==========

func cacheToRedis(allMini map[string]*ModelMetricsMini, summaries map[string]*ModelMetricsSummary) {
	// 全模型迷你摘要
	if data, err := json.Marshal(allMini); err == nil {
		_ = common.RedisSet("model_metrics:mini_all", string(data), 5*time.Minute)
	}
	// 各模型摘要
	for name, s := range summaries {
		if data, err := json.Marshal(s); err == nil {
			_ = common.RedisSet(fmt.Sprintf("model_metrics:summary:%s", name), string(data), 5*time.Minute)
		}
	}
}

// batchGetChannelNames 批量获取 channel 名称（一次 DB 查询，替代 N+1）
func batchGetChannelNames(idSet map[int]bool) map[int]string {
	result := make(map[int]string)
	if len(idSet) == 0 {
		return result
	}
	ids := make([]int, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	var channels []Channel
	if err := DB.Where("id IN ?", ids).Select("id, name").Find(&channels).Error; err != nil {
		logger.SysError("batchGetChannelNames: query failed: " + err.Error())
		return result
	}
	for _, ch := range channels {
		result[ch.Id] = ch.Name
	}
	return result
}
