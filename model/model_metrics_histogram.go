package model

import (
	"fmt"
	"sync"
	"time"

	"github.com/songquanpeng/one-api/common/config"
)

// 实时直方图累加器
// 每次请求完成写 log 时同步更新，聚合 Worker 定期读取快照
// 设计要点：
//   - 写入 O(1)，不阻塞请求
//   - 读取时返回深拷贝，避免竞态
//   - 自动清理超过 2 小时的旧数据，防止内存泄漏

const histMaxAge = 2 * 3600 // 2 小时，超过此时间的直方图数据自动清理

var histAccumulator struct {
	sync.Mutex
	// key: "model|provider|channelId|hourTimestamp"
	latency map[string]*HistogramBuckets
	speed   map[string]*HistogramBuckets
}

func init() {
	histAccumulator.latency = make(map[string]*HistogramBuckets)
	histAccumulator.speed = make(map[string]*HistogramBuckets)
}

func histKey(modelName, provider string, channelId int, hourTs int64) string {
	return fmt.Sprintf("%s|%s|%d|%d", modelName, provider, channelId, hourTs)
}

// histBaseKey 从完整 key 中提取 "model|provider|channelId" 部分
func histBaseKey(fullKey string, hourTs int64) (string, bool) {
	suffix := fmt.Sprintf("|%d", hourTs)
	if len(fullKey) <= len(suffix) {
		return "", false
	}
	if fullKey[len(fullKey)-len(suffix):] != suffix {
		return "", false
	}
	return fullKey[:len(fullKey)-len(suffix)], true
}

// RecordMetricsHistogram 在请求完成时调用，增量更新直方图
// 从 RecordConsumeLogWithOtherAndRequestID 内部调用，零额外 DB 查询
func RecordMetricsHistogram(modelName, provider string, channelId int, duration, speed float64) {
	if modelName == "" || !config.ModelMetricsEnabled {
		return
	}
	hourTs := FloorHour(time.Now().UTC().Unix())
	key := histKey(modelName, provider, channelId, hourTs)

	histAccumulator.Lock()
	defer histAccumulator.Unlock()

	if duration > 0 {
		h, ok := histAccumulator.latency[key]
		if !ok {
			h = NewHistogramBuckets(LatencyBoundaries)
			histAccumulator.latency[key] = h
		}
		addToHistogram(h, duration)
	}

	if speed > 0 {
		h, ok := histAccumulator.speed[key]
		if !ok {
			h = NewHistogramBuckets(SpeedBoundaries)
			histAccumulator.speed[key] = h
		}
		addToHistogram(h, speed)
	}
}

// SnapshotHistogramsForHour 提取指定小时的直方图快照（深拷贝）
// 返回的 map key 格式为 "model|provider|channelId"
// 返回深拷贝，调用方无需持锁，无竞态风险
func SnapshotHistogramsForHour(hourTs int64) (latencyMap, speedMap map[string]*HistogramBuckets) {
	latencyMap = make(map[string]*HistogramBuckets)
	speedMap = make(map[string]*HistogramBuckets)

	histAccumulator.Lock()
	defer histAccumulator.Unlock()

	// 提取指定小时的数据（深拷贝）
	for key, h := range histAccumulator.latency {
		if baseKey, ok := histBaseKey(key, hourTs); ok {
			latencyMap[baseKey] = copyHistogram(h)
		}
	}
	for key, h := range histAccumulator.speed {
		if baseKey, ok := histBaseKey(key, hourTs); ok {
			speedMap[baseKey] = copyHistogram(h)
		}
	}

	// 清理过期数据：删除所有超过 histMaxAge 的条目
	cutoff := time.Now().UTC().Unix() - histMaxAge
	for key := range histAccumulator.latency {
		if isHistKeyExpired(key, cutoff) {
			delete(histAccumulator.latency, key)
		}
	}
	for key := range histAccumulator.speed {
		if isHistKeyExpired(key, cutoff) {
			delete(histAccumulator.speed, key)
		}
	}

	return latencyMap, speedMap
}

// copyHistogram 深拷贝直方图
func copyHistogram(src *HistogramBuckets) *HistogramBuckets {
	if src == nil {
		return nil
	}
	dst := &HistogramBuckets{
		Boundaries: make([]float64, len(src.Boundaries)),
		Counts:     make([]int64, len(src.Counts)),
	}
	copy(dst.Boundaries, src.Boundaries)
	copy(dst.Counts, src.Counts)
	return dst
}

// isHistKeyExpired 判断 key 中的 hourTimestamp 是否过期
func isHistKeyExpired(key string, cutoffTs int64) bool {
	// key 格式: "model|provider|channelId|hourTs"
	// 从末尾解析 hourTs
	var hourTs int64
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == '|' {
			_, err := fmt.Sscanf(key[i+1:], "%d", &hourTs)
			if err != nil {
				return false // 解析失败，保守不删
			}
			return hourTs < cutoffTs
		}
	}
	return false
}

// ClearHistogramAccumulator 清空所有直方图数据（用于功能关闭时释放内存）
func ClearHistogramAccumulator() {
	histAccumulator.Lock()
	defer histAccumulator.Unlock()
	histAccumulator.latency = make(map[string]*HistogramBuckets)
	histAccumulator.speed = make(map[string]*HistogramBuckets)
}
