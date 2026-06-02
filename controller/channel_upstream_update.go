package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
)

// ──────────────────────────────────────────
// 常量 & 全局状态
// ──────────────────────────────────────────

const (
	upstreamUpdateDefaultIntervalMinutes  = 30
	upstreamUpdateBatchSize               = 100
	upstreamUpdateMinCheckIntervalSeconds = 300
)

var (
	upstreamUpdateTaskOnce    sync.Once
	upstreamUpdateTaskRunning atomic.Bool
)

// ──────────────────────────────────────────
// DTO
// ──────────────────────────────────────────

type applyChannelUpstreamModelUpdatesRequest struct {
	ID           int      `json:"id"`
	AddModels    []string `json:"add_models"`
	RemoveModels []string `json:"remove_models"`
	IgnoreModels []string `json:"ignore_models"`
}

type detectChannelUpstreamModelUpdatesResult struct {
	ChannelID       int      `json:"channel_id"`
	ChannelName     string   `json:"channel_name"`
	AddModels       []string `json:"add_models"`
	RemoveModels    []string `json:"remove_models"`
	LastCheckTime   int64    `json:"last_check_time"`
	AutoAddedModels int      `json:"auto_added_models"`
}

type applyAllChannelUpstreamModelUpdatesResult struct {
	ChannelID             int      `json:"channel_id"`
	ChannelName           string   `json:"channel_name"`
	AddedModels           []string `json:"added_models"`
	RemovedModels         []string `json:"removed_models"`
	RemainingModels       []string `json:"remaining_models"`
	RemainingRemoveModels []string `json:"remaining_remove_models"`
}

// ──────────────────────────────────────────
// 模型名称工具函数
// ──────────────────────────────────────────

func upstreamNormalizeModelNames(models []string) []string {
	return lo.Uniq(lo.FilterMap(models, func(m string, _ int) (string, bool) {
		t := strings.TrimSpace(m)
		return t, t != ""
	}))
}

func upstreamMergeModelNames(base, appended []string) []string {
	merged := upstreamNormalizeModelNames(base)
	seen := make(map[string]struct{}, len(merged))
	for _, m := range merged {
		seen[m] = struct{}{}
	}
	for _, m := range upstreamNormalizeModelNames(appended) {
		if _, ok := seen[m]; !ok {
			seen[m] = struct{}{}
			merged = append(merged, m)
		}
	}
	return merged
}

func upstreamSubtractModelNames(base, removed []string) []string {
	removeSet := make(map[string]struct{}, len(removed))
	for _, m := range upstreamNormalizeModelNames(removed) {
		removeSet[m] = struct{}{}
	}
	return lo.Filter(upstreamNormalizeModelNames(base), func(m string, _ int) bool {
		_, ok := removeSet[m]
		return !ok
	})
}

func upstreamIntersectModelNames(base, allowed []string) []string {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, m := range upstreamNormalizeModelNames(allowed) {
		allowedSet[m] = struct{}{}
	}
	return lo.Filter(upstreamNormalizeModelNames(base), func(m string, _ int) bool {
		_, ok := allowedSet[m]
		return ok
	})
}

func upstreamApplySelectedModelChanges(origin, addModels, removeModels []string) []string {
	normalizedAdd := upstreamNormalizeModelNames(addModels)
	normalizedRemove := upstreamSubtractModelNames(upstreamNormalizeModelNames(removeModels), normalizedAdd)
	return upstreamSubtractModelNames(upstreamMergeModelNames(origin, normalizedAdd), normalizedRemove)
}

// upstreamNormalizeChannelModelMapping 解析 channel.ModelMapping JSON
func upstreamNormalizeChannelModelMapping(channel *model.Channel) map[string]string {
	if channel.ModelMapping == nil {
		return nil
	}
	raw := strings.TrimSpace(*channel.ModelMapping)
	if raw == "" || raw == "{}" {
		return nil
	}
	parsed := make(map[string]string)
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil
	}
	normalized := make(map[string]string, len(parsed))
	for src, tgt := range parsed {
		s, t := strings.TrimSpace(src), strings.TrimSpace(tgt)
		if s != "" && t != "" {
			normalized[s] = t
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

// ──────────────────────────────────────────
// 从上游获取模型 ID 列表
// ──────────────────────────────────────────

// fetchChannelUpstreamModelList 复用已有的 buildModelsURL / getAuthHeader / fetchModelsFromURL
func fetchChannelUpstreamModelList(channel *model.Channel) ([]string, error) {
	// 选取第一个可用 Key（兼容单 Key、多 Key 及旧式 \n 分隔格式）
	key := channel.Key
	if keys := channel.ParseKeys(); len(keys) > 0 {
		for i, k := range keys {
			if channel.GetKeyStatus(i) == common.ChannelStatusEnabled {
				key = k
				break
			}
		}
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("渠道密钥为空")
	}

	baseURL := channel.GetBaseURL()
	url := buildModelsURL(channel.Type, baseURL)
	headers := getAuthHeader(channel.Type, key)

	return fetchModelsFromURL(url, headers)
}

// ──────────────────────────────────────────
// 差异计算
// ──────────────────────────────────────────

func upstreamCollectPendingChangesFromModels(
	localModels, upstreamModels, ignoredModels []string,
	modelMapping map[string]string,
) (pendingAdd, pendingRemove []string) {
	localModels = upstreamNormalizeModelNames(localModels)
	upstreamModels = upstreamNormalizeModelNames(upstreamModels)

	localSet := make(map[string]struct{}, len(localModels))
	for _, m := range localModels {
		localSet[m] = struct{}{}
	}
	upstreamSet := make(map[string]struct{}, len(upstreamModels))
	for _, m := range upstreamModels {
		upstreamSet[m] = struct{}{}
	}

	redirectSrcSet := make(map[string]struct{}, len(modelMapping))
	redirectTgtSet := make(map[string]struct{}, len(modelMapping))
	for src, tgt := range modelMapping {
		redirectSrcSet[src] = struct{}{}
		redirectTgtSet[tgt] = struct{}{}
	}

	// 已覆盖的上游模型 = 本地模型 ∪ redirect target
	covered := make(map[string]struct{}, len(localSet)+len(redirectTgtSet))
	for m := range localSet {
		covered[m] = struct{}{}
	}
	for m := range redirectTgtSet {
		covered[m] = struct{}{}
	}

	normalizedIgnored := upstreamNormalizeModelNames(ignoredModels)

	pendingAdd = lo.Filter(upstreamModels, func(m string, _ int) bool {
		if _, ok := covered[m]; ok {
			return false
		}
		return !lo.ContainsBy(normalizedIgnored, func(ignored string) bool {
			if body, ok := strings.CutPrefix(ignored, "regex:"); ok {
				matched, err := regexp.MatchString(strings.TrimSpace(body), m)
				return err == nil && matched
			}
			return ignored == m
		})
	})

	pendingRemove = lo.Filter(localModels, func(m string, _ int) bool {
		if _, ok := redirectSrcSet[m]; ok {
			return false // redirect source 不因上游缺失而删除
		}
		_, exists := upstreamSet[m]
		return !exists
	})

	return upstreamNormalizeModelNames(pendingAdd), upstreamNormalizeModelNames(pendingRemove)
}

func upstreamCollectPendingChanges(channel *model.Channel, settings config.ChannelOtherSettings) ([]string, []string, error) {
	upstream, err := fetchChannelUpstreamModelList(channel)
	if err != nil {
		return nil, nil, err
	}
	add, remove := upstreamCollectPendingChangesFromModels(
		channel.GetModels(),
		upstream,
		settings.UpstreamModelUpdateIgnoredModels,
		upstreamNormalizeChannelModelMapping(channel),
	)
	return add, remove, nil
}

// ──────────────────────────────────────────
// 持久化到 DB
// ──────────────────────────────────────────

func saveChannelUpstreamSettings(channel *model.Channel, settings config.ChannelOtherSettings, updateModels bool) error {
	channel.SetOtherSettings(settings)
	updates := map[string]interface{}{
		"settings": channel.OtherSettings,
	}
	if updateModels {
		updates["models"] = channel.Models
	}
	return model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(updates).Error
}

// getUpstreamMinCheckInterval 从环境变量读取最小检测间隔秒数
func getUpstreamMinCheckInterval() int64 {
	v := os.Getenv("CHANNEL_UPSTREAM_MODEL_UPDATE_MIN_CHECK_INTERVAL_SECONDS")
	if v == "" {
		return upstreamUpdateMinCheckIntervalSeconds
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return upstreamUpdateMinCheckIntervalSeconds
	}
	return n
}

// checkAndPersistUpstreamChanges 检测上游模型变更并持久化
// force=true 跳过冷却时间检查；allowAutoApply=true 允许自动同步新增模型
func checkAndPersistUpstreamChanges(channel *model.Channel, settings *config.ChannelOtherSettings, force, allowAutoApply bool) (modelsChanged bool, autoAdded int, err error) {
	now := helper.GetTimestamp()
	if !force {
		minInterval := getUpstreamMinCheckInterval()
		if settings.UpstreamModelUpdateLastCheckTime > 0 &&
			now-settings.UpstreamModelUpdateLastCheckTime < minInterval {
			return false, 0, nil
		}
	}

	pendingAdd, pendingRemove, fetchErr := upstreamCollectPendingChanges(channel, *settings)
	settings.UpstreamModelUpdateLastCheckTime = now

	if fetchErr != nil {
		_ = saveChannelUpstreamSettings(channel, *settings, false)
		return false, 0, fetchErr
	}

	if allowAutoApply && settings.UpstreamModelUpdateAutoSyncEnabled && len(pendingAdd) > 0 {
		origin := upstreamNormalizeModelNames(channel.GetModels())
		merged := upstreamMergeModelNames(origin, pendingAdd)
		if len(merged) > len(origin) {
			channel.Models = strings.Join(merged, ",")
			autoAdded = len(merged) - len(origin)
			modelsChanged = true
		}
		settings.UpstreamModelUpdateLastDetectedModels = []string{}
	} else {
		settings.UpstreamModelUpdateLastDetectedModels = pendingAdd
	}
	settings.UpstreamModelUpdateLastRemovedModels = pendingRemove

	if err = saveChannelUpstreamSettings(channel, *settings, modelsChanged); err != nil {
		return false, autoAdded, err
	}
	if modelsChanged {
		if err = channel.UpdateAbilities(); err != nil {
			return true, autoAdded, err
		}
	}
	return modelsChanged, autoAdded, nil
}

// upstreamRefreshCache 刷新内存中的渠道缓存
func upstreamRefreshCache() {
	if config.MemoryCacheEnabled {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.SysLog(fmt.Sprintf("InitChannelCache panic: %v", r))
				}
			}()
			model.InitChannelCache()
		}()
	}
}

// ──────────────────────────────────────────
// 后台定时巡检任务
// ──────────────────────────────────────────

func runUpstreamUpdateTaskOnce() {
	if !upstreamUpdateTaskRunning.CompareAndSwap(false, true) {
		return
	}
	defer upstreamUpdateTaskRunning.Store(false)

	checked, failed, changed := 0, 0, 0
	addedTotal, removedTotal, autoTotal := 0, 0, 0
	refreshNeeded := false

	lastID := 0
	for {
		var channels []*model.Channel
		q := model.DB.
			Select("id", "name", "type", "key", "status", "base_url", "models",
				"settings", "model_mapping", "multi_key_info", "header_override").
			Where("status = ?", common.ChannelStatusEnabled).
			Order("id asc").
			Limit(upstreamUpdateBatchSize)
		if lastID > 0 {
			q = q.Where("id > ?", lastID)
		}
		if err := q.Find(&channels).Error; err != nil {
			logger.SysLog(fmt.Sprintf("upstream update task query failed: %v", err))
			break
		}
		if len(channels) == 0 {
			break
		}
		lastID = channels[len(channels)-1].Id

		for _, ch := range channels {
			if ch == nil {
				continue
			}
			settings := ch.GetOtherSettings()
			if !settings.UpstreamModelUpdateCheckEnabled {
				continue
			}
			checked++
			modelsChanged, autoAdded, err := checkAndPersistUpstreamChanges(ch, &settings, false, true)
			if err != nil {
				failed++
				logger.SysLog(fmt.Sprintf("upstream update check failed: channel_id=%d channel_name=%s err=%v",
					ch.Id, ch.Name, err))
				continue
			}
			add := len(upstreamNormalizeModelNames(settings.UpstreamModelUpdateLastDetectedModels)) + autoAdded
			remove := len(upstreamNormalizeModelNames(settings.UpstreamModelUpdateLastRemovedModels))
			addedTotal += add
			removedTotal += remove
			autoTotal += autoAdded
			if add > 0 || remove > 0 {
				changed++
			}
			if modelsChanged {
				refreshNeeded = true
			}
			if config.RequestInterval > 0 {
				time.Sleep(config.RequestInterval)
			}
		}
		if len(channels) < upstreamUpdateBatchSize {
			break
		}
	}

	if refreshNeeded {
		upstreamRefreshCache()
	}
	if checked > 0 || config.DebugEnabled {
		logger.SysLog(fmt.Sprintf(
			"upstream update task done: checked=%d changed=%d add=%d remove=%d failed=%d auto_added=%d",
			checked, changed, addedTotal, removedTotal, failed, autoTotal,
		))
	}
}

// StartChannelUpstreamModelUpdateTask 启动后台定时巡检（仅 master 节点执行）
func StartChannelUpstreamModelUpdateTask() {
	upstreamUpdateTaskOnce.Do(func() {
		if !config.IsMasterNode {
			return
		}
		if os.Getenv("CHANNEL_UPSTREAM_MODEL_UPDATE_TASK_ENABLED") == "false" {
			logger.SysLog("upstream model update task disabled by env")
			return
		}

		intervalMinutes := upstreamUpdateDefaultIntervalMinutes
		if s := os.Getenv("CHANNEL_UPSTREAM_MODEL_UPDATE_TASK_INTERVAL_MINUTES"); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n >= 1 {
				intervalMinutes = n
			}
		}
		interval := time.Duration(intervalMinutes) * time.Minute

		common.SafeGoroutine(func() {
			logger.SysLog(fmt.Sprintf("upstream model update task started: interval=%s", interval))
			runUpstreamUpdateTaskOnce()
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for range ticker.C {
				runUpstreamUpdateTaskOnce()
			}
		})
	})
}

// ──────────────────────────────────────────
// HTTP Handler：单渠道检测
// ──────────────────────────────────────────

// DetectChannelUpstreamModelUpdates 强制检测指定渠道的上游模型变更
func DetectChannelUpstreamModelUpdates(c *gin.Context) {
	var req applyChannelUpstreamModelUpdatesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if req.ID <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid channel id"})
		return
	}
	channel, err := model.GetChannelById(req.ID, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	settings := channel.GetOtherSettings()
	modelsChanged, autoAdded, err := checkAndPersistUpstreamChanges(channel, &settings, true, false)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if modelsChanged {
		upstreamRefreshCache()
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": detectChannelUpstreamModelUpdatesResult{
			ChannelID:       channel.Id,
			ChannelName:     channel.Name,
			AddModels:       upstreamNormalizeModelNames(settings.UpstreamModelUpdateLastDetectedModels),
			RemoveModels:    upstreamNormalizeModelNames(settings.UpstreamModelUpdateLastRemovedModels),
			LastCheckTime:   settings.UpstreamModelUpdateLastCheckTime,
			AutoAddedModels: autoAdded,
		},
	})
}

// ──────────────────────────────────────────
// HTTP Handler：单渠道应用变更
// ──────────────────────────────────────────

func doApplyChannelUpstreamModelUpdates(
	channel *model.Channel,
	addInput, ignoreInput, removeInput []string,
) (added, removed, remaining, remainingRemove []string, modelsChanged bool, err error) {
	settings := channel.GetOtherSettings()
	pendingAdd := upstreamNormalizeModelNames(settings.UpstreamModelUpdateLastDetectedModels)
	pendingRemove := upstreamNormalizeModelNames(settings.UpstreamModelUpdateLastRemovedModels)

	addModels := upstreamIntersectModelNames(addInput, pendingAdd)
	ignoreModels := upstreamIntersectModelNames(ignoreInput, pendingAdd)
	removeModels := upstreamSubtractModelNames(upstreamIntersectModelNames(removeInput, pendingRemove), addModels)

	origin := upstreamNormalizeModelNames(channel.GetModels())
	next := upstreamApplySelectedModelChanges(origin, addModels, removeModels)
	modelsChanged = !slices.Equal(origin, next)
	if modelsChanged {
		channel.Models = strings.Join(next, ",")
	}

	settings.UpstreamModelUpdateIgnoredModels = upstreamMergeModelNames(settings.UpstreamModelUpdateIgnoredModels, ignoreModels)
	if len(addModels) > 0 {
		settings.UpstreamModelUpdateIgnoredModels = upstreamSubtractModelNames(settings.UpstreamModelUpdateIgnoredModels, addModels)
	}
	remaining = upstreamSubtractModelNames(pendingAdd, append(addModels, ignoreModels...))
	remainingRemove = upstreamSubtractModelNames(pendingRemove, removeModels)
	settings.UpstreamModelUpdateLastDetectedModels = remaining
	settings.UpstreamModelUpdateLastRemovedModels = remainingRemove
	settings.UpstreamModelUpdateLastCheckTime = helper.GetTimestamp()

	if err = saveChannelUpstreamSettings(channel, settings, modelsChanged); err != nil {
		return nil, nil, nil, nil, false, err
	}
	if modelsChanged {
		if err = channel.UpdateAbilities(); err != nil {
			return addModels, removeModels, remaining, remainingRemove, true, err
		}
	}
	return addModels, removeModels, remaining, remainingRemove, modelsChanged, nil
}

// ApplyChannelUpstreamModelUpdates 应用指定渠道的待处理模型变更
func ApplyChannelUpstreamModelUpdates(c *gin.Context) {
	var req applyChannelUpstreamModelUpdatesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if req.ID <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid channel id"})
		return
	}
	channel, err := model.GetChannelById(req.ID, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	beforeSettings := channel.GetOtherSettings()
	ignoredModels := upstreamIntersectModelNames(req.IgnoreModels, beforeSettings.UpstreamModelUpdateLastDetectedModels)

	added, removed, remaining, remainingRemove, modelsChanged, err := doApplyChannelUpstreamModelUpdates(
		channel, req.AddModels, req.IgnoreModels, req.RemoveModels,
	)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if modelsChanged {
		upstreamRefreshCache()
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"id":                      channel.Id,
			"added_models":            added,
			"removed_models":          removed,
			"ignored_models":          ignoredModels,
			"remaining_models":        remaining,
			"remaining_remove_models": remainingRemove,
			"models":                  channel.Models,
			"settings":                channel.OtherSettings,
		},
	})
}

// ──────────────────────────────────────────
// HTTP Handler：批量检测所有渠道
// ──────────────────────────────────────────

// DetectAllChannelUpstreamModelUpdates 对所有启用了巡检的渠道执行检测
func DetectAllChannelUpstreamModelUpdates(c *gin.Context) {
	var results []detectChannelUpstreamModelUpdatesResult
	var failedIDs []int
	detectedAdd, detectedRemove := 0, 0
	refreshNeeded := false

	lastID := 0
	for {
		var channels []*model.Channel
		q := model.DB.
			Select("id", "name", "type", "key", "status", "base_url", "models",
				"settings", "model_mapping", "multi_key_info", "header_override").
			Where("status = ?", common.ChannelStatusEnabled).
			Order("id asc").
			Limit(upstreamUpdateBatchSize)
		if lastID > 0 {
			q = q.Where("id > ?", lastID)
		}
		if err := q.Find(&channels).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
		if len(channels) == 0 {
			break
		}
		lastID = channels[len(channels)-1].Id

		for _, ch := range channels {
			if ch == nil {
				continue
			}
			settings := ch.GetOtherSettings()
			if !settings.UpstreamModelUpdateCheckEnabled {
				continue
			}
			modelsChanged, autoAdded, err := checkAndPersistUpstreamChanges(ch, &settings, true, false)
			if err != nil {
				failedIDs = append(failedIDs, ch.Id)
				continue
			}
			if modelsChanged {
				refreshNeeded = true
			}
			add := upstreamNormalizeModelNames(settings.UpstreamModelUpdateLastDetectedModels)
			remove := upstreamNormalizeModelNames(settings.UpstreamModelUpdateLastRemovedModels)
			detectedAdd += len(add)
			detectedRemove += len(remove)
			results = append(results, detectChannelUpstreamModelUpdatesResult{
				ChannelID:       ch.Id,
				ChannelName:     ch.Name,
				AddModels:       add,
				RemoveModels:    remove,
				LastCheckTime:   settings.UpstreamModelUpdateLastCheckTime,
				AutoAddedModels: autoAdded,
			})
		}
		if len(channels) < upstreamUpdateBatchSize {
			break
		}
	}

	if refreshNeeded {
		upstreamRefreshCache()
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"processed_channels":       len(results),
			"failed_channel_ids":       failedIDs,
			"detected_add_models":      detectedAdd,
			"detected_remove_models":   detectedRemove,
			"channel_detected_results": results,
		},
	})
}

// ──────────────────────────────────────────
// HTTP Handler：批量应用所有渠道待处理变更
// ──────────────────────────────────────────

// ApplyAllChannelUpstreamModelUpdates 对所有启用了巡检且有待处理变更的渠道统一应用
func ApplyAllChannelUpstreamModelUpdates(c *gin.Context) {
	var results []applyAllChannelUpstreamModelUpdatesResult
	var failedIDs []int
	addedTotal, removedTotal := 0, 0
	refreshNeeded := false

	lastID := 0
	for {
		var channels []*model.Channel
		q := model.DB.
			Select("id", "name", "type", "key", "status", "base_url", "models",
				"settings", "model_mapping", "multi_key_info", "header_override").
			Where("status = ?", common.ChannelStatusEnabled).
			Order("id asc").
			Limit(upstreamUpdateBatchSize)
		if lastID > 0 {
			q = q.Where("id > ?", lastID)
		}
		if err := q.Find(&channels).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
		if len(channels) == 0 {
			break
		}
		lastID = channels[len(channels)-1].Id

		for _, ch := range channels {
			if ch == nil {
				continue
			}
			settings := ch.GetOtherSettings()
			if !settings.UpstreamModelUpdateCheckEnabled {
				continue
			}
			pendingAdd := upstreamNormalizeModelNames(settings.UpstreamModelUpdateLastDetectedModels)
			pendingRemove := upstreamNormalizeModelNames(settings.UpstreamModelUpdateLastRemovedModels)
			if len(pendingAdd) == 0 && len(pendingRemove) == 0 {
				continue
			}

			added, removed, remaining, remainingRemove, modelsChanged, err := doApplyChannelUpstreamModelUpdates(
				ch, pendingAdd, nil, pendingRemove,
			)
			if err != nil {
				failedIDs = append(failedIDs, ch.Id)
				continue
			}
			if modelsChanged {
				refreshNeeded = true
			}
			addedTotal += len(added)
			removedTotal += len(removed)
			results = append(results, applyAllChannelUpstreamModelUpdatesResult{
				ChannelID:             ch.Id,
				ChannelName:           ch.Name,
				AddedModels:           added,
				RemovedModels:         removed,
				RemainingModels:       remaining,
				RemainingRemoveModels: remainingRemove,
			})
		}
		if len(channels) < upstreamUpdateBatchSize {
			break
		}
	}

	if refreshNeeded {
		upstreamRefreshCache()
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"processed_channels": len(results),
			"added_models":       addedTotal,
			"removed_models":     removedTotal,
			"failed_channel_ids": failedIDs,
			"results":            results,
		},
	})
}
