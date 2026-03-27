package model

import (
	"fmt"
	"time"

	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
)

// StartModelMetricsAggregator 启动模型指标聚合 Worker
// 内置 panic 恢复 + 自动重启，确保高可用
func StartModelMetricsAggregator() {
	for {
		runAggregator()
		// 如果 runAggregator 返回（panic 后恢复），等待后重启
		logger.SysError("model metrics aggregator: restarting after failure in 30 seconds...")
		time.Sleep(30 * time.Second)
	}
}

func runAggregator() {
	defer func() {
		if r := recover(); r != nil {
			logger.SysError(fmt.Sprintf("model metrics aggregator: panic recovered: %v", r))
		}
	}()

	logger.SysLog("model metrics aggregator: starting")

	// 首次启动回填（失败不阻塞后续聚合）
	safeRun("backfill", backfillMetrics)

	interval := time.Duration(config.ModelMetricsAggregationInterval) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	cleanupCounter := 0
	cleanupEveryN := 3600 / config.ModelMetricsAggregationInterval
	if cleanupEveryN < 1 {
		cleanupEveryN = 1
	}

	for range ticker.C {
		if !config.ModelMetricsEnabled {
			continue
		}

		safeRun("aggregate", aggregateCurrentHour)

		cleanupCounter++
		if cleanupCounter >= cleanupEveryN {
			cleanupCounter = 0
			safeRun("cleanup", cleanupOldMetrics)
		}
	}
}

// safeRun 在 recover 保护下执行函数，单次 panic 不会终止 Worker
func safeRun(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			logger.SysError(fmt.Sprintf("model metrics aggregator: %s panicked: %v", name, r))
		}
	}()
	fn()
}

// backfillMetrics 首次启动时回填历史数据
func backfillMetrics() {
	maxHour, err := GetMaxHourTimestamp()
	if err != nil {
		logger.SysError("model metrics aggregator: failed to get max hour: " + err.Error())
		return
	}

	now := time.Now().UTC()
	currentHour := FloorHour(now.Unix())

	if maxHour > 0 {
		startHour := maxHour
		hours := (currentHour - startHour) / 3600
		if hours <= 0 {
			logger.SysLog("model metrics aggregator: no backfill needed, data is up to date")
			return
		}
		logger.SysLog(fmt.Sprintf("model metrics aggregator: backfilling %d hours from %s",
			hours, time.Unix(startHour, 0).UTC().Format("2006-01-02 15:04")))
		for h := startHour; h <= currentHour; h += 3600 {
			aggregateHour(h, h+3600)
		}
	} else {
		backfillDays := config.ModelMetricsBackfillDays
		startTime := now.AddDate(0, 0, -backfillDays).Unix()

		minLog, err := GetMinLogTimestamp()
		if err != nil || minLog == 0 {
			logger.SysLog("model metrics aggregator: no logs found, skipping backfill")
			return
		}
		if minLog > startTime {
			startTime = minLog
		}

		startHour := FloorHour(startTime)
		hours := (currentHour - startHour) / 3600
		logger.SysLog(fmt.Sprintf("model metrics aggregator: first run, backfilling %d hours from %s",
			hours, time.Unix(startHour, 0).UTC().Format("2006-01-02 15:04")))

		for h := startHour; h <= currentHour; h += 3600 {
			aggregateHour(h, h+3600)
		}
	}

	RefreshMetricsCache()
	logger.SysLog("model metrics aggregator: backfill completed")
}

// aggregateCurrentHour 聚合当前小时的数据
func aggregateCurrentHour() {
	now := time.Now().UTC()
	currentHour := FloorHour(now.Unix())
	aggregateHour(currentHour, currentHour+3600)
	RefreshMetricsCache()
}

// aggregateHour 聚合指定小时区间 [hourStart, hourEnd) 的日志数据
func aggregateHour(hourStart, hourEnd int64) {
	// 1. 聚合基础指标（按 model, provider, channel 分组）
	rows, err := AggregateLogsForHour(hourStart, hourEnd)
	if err != nil {
		logger.SysError(fmt.Sprintf("model metrics aggregator: failed to aggregate hour %d: %s",
			hourStart, err.Error()))
		return
	}
	if len(rows) == 0 {
		return
	}

	// 2. 从内存累加器获取直方图快照（深拷贝，无竞态）
	latencyHists, speedHists := SnapshotHistogramsForHour(hourStart)

	// 3. 组装 channel 级明细行
	var channelMetrics []ModelMetrics
	type providerKey struct {
		ModelName string
		Provider  string
	}
	providerAgg := make(map[providerKey]*ModelMetrics)

	for _, row := range rows {
		histKey := fmt.Sprintf("%s|%s|%d", row.ModelName, row.Provider, row.ChannelId)
		latencyBucketsStr := ""
		speedBucketsStr := ""
		if h, ok := latencyHists[histKey]; ok {
			latencyBucketsStr = MarshalHistogram(h)
		}
		if h, ok := speedHists[histKey]; ok {
			speedBucketsStr = MarshalHistogram(h)
		}

		m := ModelMetrics{
			ModelName:        row.ModelName,
			Provider:         row.Provider,
			ChannelId:        row.ChannelId,
			HourTimestamp:    hourStart,
			TotalRequests:    row.TotalRequests,
			SuccessRequests:  row.SuccessRequests,
			ErrorRequests:    row.ErrorRequests,
			StreamRequests:   row.StreamRequests,
			TotalTokens:      row.TotalTokens,
			PromptTokens:     row.PromptTokens,
			CompletionTokens: row.CompletionTokens,
			CachedTokens:     row.CachedTokens,
			TotalQuota:       row.TotalQuota,
			SumDuration:      row.SumDuration,
			SumSpeed:         row.SumSpeed,
			SpeedCount:       row.SpeedCount,
			SumFirstWord:     row.SumFirstWord,
			FirstWordCount:   row.FirstWordCount,
			LatencyBuckets:   latencyBucketsStr,
			SpeedBuckets:     speedBucketsStr,
		}
		channelMetrics = append(channelMetrics, m)

		// 累加到 provider 级汇总
		pk := providerKey{ModelName: row.ModelName, Provider: row.Provider}
		if agg, ok := providerAgg[pk]; ok {
			agg.TotalRequests += row.TotalRequests
			agg.SuccessRequests += row.SuccessRequests
			agg.ErrorRequests += row.ErrorRequests
			agg.StreamRequests += row.StreamRequests
			agg.TotalTokens += row.TotalTokens
			agg.PromptTokens += row.PromptTokens
			agg.CompletionTokens += row.CompletionTokens
			agg.CachedTokens += row.CachedTokens
			agg.TotalQuota += row.TotalQuota
			agg.SumDuration += row.SumDuration
			agg.SumSpeed += row.SumSpeed
			agg.SpeedCount += row.SpeedCount
			agg.SumFirstWord += row.SumFirstWord
			agg.FirstWordCount += row.FirstWordCount
			existingLatency := UnmarshalHistogram(agg.LatencyBuckets)
			newLatency := UnmarshalHistogram(latencyBucketsStr)
			agg.LatencyBuckets = MarshalHistogram(MergeHistograms(existingLatency, newLatency))
			existingSpeed := UnmarshalHistogram(agg.SpeedBuckets)
			newSpeed := UnmarshalHistogram(speedBucketsStr)
			agg.SpeedBuckets = MarshalHistogram(MergeHistograms(existingSpeed, newSpeed))
		} else {
			providerAgg[pk] = &ModelMetrics{
				ModelName:        row.ModelName,
				Provider:         row.Provider,
				ChannelId:        0,
				HourTimestamp:    hourStart,
				TotalRequests:    row.TotalRequests,
				SuccessRequests:  row.SuccessRequests,
				ErrorRequests:    row.ErrorRequests,
				StreamRequests:   row.StreamRequests,
				TotalTokens:      row.TotalTokens,
				PromptTokens:     row.PromptTokens,
				CompletionTokens: row.CompletionTokens,
				CachedTokens:     row.CachedTokens,
				TotalQuota:       row.TotalQuota,
				SumDuration:      row.SumDuration,
				SumSpeed:         row.SumSpeed,
				SpeedCount:       row.SpeedCount,
				SumFirstWord:     row.SumFirstWord,
				FirstWordCount:   row.FirstWordCount,
				LatencyBuckets:   latencyBucketsStr,
				SpeedBuckets:     speedBucketsStr,
			}
		}
	}

	// 4. 写入 channel 级明细
	if err := UpsertModelMetrics(channelMetrics); err != nil {
		logger.SysError(fmt.Sprintf("model metrics aggregator: failed to upsert channel metrics for hour %d: %s",
			hourStart, err.Error()))
	}

	// 5. 写入 provider 级汇总 (channel_id=0)
	var providerMetrics []ModelMetrics
	for _, m := range providerAgg {
		providerMetrics = append(providerMetrics, *m)
	}
	if err := UpsertModelMetrics(providerMetrics); err != nil {
		logger.SysError(fmt.Sprintf("model metrics aggregator: failed to upsert provider metrics for hour %d: %s",
			hourStart, err.Error()))
	}
}

// cleanupOldMetrics 清理超过保留期的数据
func cleanupOldMetrics() {
	retentionDays := config.ModelMetricsRetentionDays
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays).Unix()
	deleted, err := DeleteOldMetrics(cutoff)
	if err != nil {
		logger.SysError("model metrics aggregator: cleanup failed: " + err.Error())
		return
	}
	if deleted > 0 {
		logger.SysLog(fmt.Sprintf("model metrics aggregator: cleaned up %d expired rows", deleted))
	}
}
