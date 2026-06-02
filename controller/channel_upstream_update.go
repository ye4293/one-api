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
	// 最小检测间隔缓存：进程生命周期内不变，用 sync.Once 避免每渠道重复读 env
	upstreamMinCheckIntervalOnce  sync.Once
	upstreamMinCheckIntervalCache int64
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
		selected := ""
		for i, k := range keys {
			if channel.GetKeyStatus(i) == common.ChannelStatusEnabled {
				selected = k
				break
			}
		}
		if selected == "" {
			selected = keys[0] // 所有 Key 均被禁用时回退到第一个
		}
		key = selected
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

	// 预编译所有 regex: 前缀规则，避免在 per-model 循环中重复编译
	type ignoreRule struct {
		literal string
		re      *regexp.Regexp
	}
	rules := make([]ignoreRule, 0, len(normalizedIgnored))
	for _, ign := range normalizedIgnored {
		if body, ok := strings.CutPrefix(ign, "regex:"); ok {
			if re, err := regexp.Compile(strings.TrimSpace(body)); err == nil {
				rules = append(rules, ignoreRule{re: re})
			}
		} else {
			rules = append(rules, ignoreRule{literal: ign})
		}
	}

	isIgnored := func(m string) bool {
		for _, r := range rules {
			if r.re != nil {
				if r.re.MatchString(m) {
					return true
				}
			} else if r.literal == m {
				return true
			}
		}
		return false
	}

	pendingAdd = lo.Filter(upstreamModels, func(m string, _ int) bool {
		if _, ok := covered[m]; ok {
			return false
		}
		return !isIgnored(m)
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

// getUpstreamMinCheckInterval 读取最小检测间隔（进程级缓存，避免每渠道重复 os.Getenv）
func getUpstreamMinCheckInterval() int64 {
	upstreamMinCheckIntervalOnce.Do(func() {
		v := os.Getenv("CHANNEL_UPSTREAM_MODEL_UPDATE_MIN_CHECK_INTERVAL_SECONDS")
		if v == "" {
			upstreamMinCheckIntervalCache = upstreamUpdateMinCheckIntervalSeconds
			return
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			n = upstreamUpdateMinCheckIntervalSeconds
		}
		upstreamMinCheckIntervalCache = n
	})
	return upstreamMinCheckIntervalCache
}

// checkAndPersistUpstreamChanges 检测上游模型变更并持久化
// force=true 跳过冷却时间检查；allowAutoApply=true 允许自动同步（新增/删除）
// ran=false 表示因冷却未到期而跳过，调用方不应将 settings 中的旧数据计入本次指标
func checkAndPersistUpstreamChanges(channel *model.Channel, settings *config.ChannelOtherSettings, force, allowAutoApply bool) (modelsChanged bool, autoAdded int, ran bool, err error) {
	now := helper.GetTimestamp()
	if !force {
		// 优先使用渠道级配置，否则回退到全局默认
		minInterval := getUpstreamMinCheckInterval()
		if settings.UpstreamModelUpdateIntervalMinutes > 0 {
			minInterval = int64(settings.UpstreamModelUpdateIntervalMinutes) * 60
		}
		if settings.UpstreamModelUpdateLastCheckTime > 0 &&
			now-settings.UpstreamModelUpdateLastCheckTime < minInterval {
			return false, 0, false, nil // 冷却中：跳过，ran=false
		}
	}

	ran = true
	pendingAdd, pendingRemove, fetchErr := upstreamCollectPendingChanges(channel, *settings)
	settings.UpstreamModelUpdateLastCheckTime = now

	if fetchErr != nil {
		_ = saveChannelUpstreamSettings(channel, *settings, false)
		return false, 0, true, fetchErr
	}

	// 自动同步新增模型
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

	// 自动删除上游已移除的模型
	if allowAutoApply && settings.UpstreamModelUpdateAutoDeleteEnabled && len(pendingRemove) > 0 {
		current := upstreamNormalizeModelNames(channel.GetModels())
		updated := upstreamSubtractModelNames(current, pendingRemove)
		if len(updated) < len(current) {
			channel.Models = strings.Join(updated, ",")
			modelsChanged = true
		}
		settings.UpstreamModelUpdateLastRemovedModels = []string{}
	} else {
		settings.UpstreamModelUpdateLastRemovedModels = pendingRemove
	}

	if err = saveChannelUpstreamSettings(channel, *settings, modelsChanged); err != nil {
		return false, autoAdded, true, err
	}
	if modelsChanged {
		if err = channel.UpdateAbilities(); err != nil {
			return true, autoAdded, true, err
		}
	}
	return modelsChanged, autoAdded, true, nil
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
// 分页查询公共函数
// ──────────────────────────────────────────

// queryUpstreamChannelBatch 查询一批启用状态的渠道（按 id asc 游标分页）
func queryUpstreamChannelBatch(lastID int) ([]*model.Channel, error) {
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
	return channels, q.Find(&channels).Error
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
		channels, err := queryUpstreamChannelBatch(lastID)
		if err != nil {
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
			modelsChanged, autoAdded, ran, err := checkAndPersistUpstreamChanges(ch, &settings, false, true)
			if err != nil {
				failed++
				logger.SysLog(fmt.Sprintf("upstream update check failed: channel_id=%d channel_name=%s err=%v",
					ch.Id, ch.Name, err))
				continue
			}
			// 只在本次真正执行了检测时才计入指标，冷却跳过的不算
			if ran {
				add := len(settings.UpstreamModelUpdateLastDetectedModels) + autoAdded
				remove := len(settings.UpstreamModelUpdateLastRemovedModels)
				addedTotal += add
				removedTotal += remove
				autoTotal += autoAdded
				if add > 0 || remove > 0 {
					changed++
				}
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
	modelsChanged, autoAdded, _, err := checkAndPersistUpstreamChanges(channel, &settings, true, false)
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
	settings config.ChannelOtherSettings,
	addInput, ignoreInput, removeInput []string,
) (added, removed, remaining, remainingRemove []string, modelsChanged bool, err error) {
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
		channel, beforeSettings, req.AddModels, req.IgnoreModels, req.RemoveModels,
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
		channels, err := queryUpstreamChannelBatch(lastID)
		if err != nil {
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
			modelsChanged, autoAdded, _, err := checkAndPersistUpstreamChanges(ch, &settings, true, false)
			if err != nil {
				failedIDs = append(failedIDs, ch.Id)
				continue
			}
			if modelsChanged {
				refreshNeeded = true
			}
			add := settings.UpstreamModelUpdateLastDetectedModels
			remove := settings.UpstreamModelUpdateLastRemovedModels
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
		channels, err := queryUpstreamChannelBatch(lastID)
		if err != nil {
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
				ch, settings, pendingAdd, nil, pendingRemove,
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
