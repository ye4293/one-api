package service

import (
	"context"
	"crypto/sha1"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/tidwall/gjson"
)

// ─── gin context keys ─────────────────────────────────────────────────────────

const (
	ginKeyAffinityCacheKey  = "affinity_cache_key"
	ginKeyAffinityTTL       = "affinity_ttl_seconds"
	ginKeyAffinitySkipRetry = "affinity_skip_retry"
	ginKeyAffinityLogInfo   = "affinity_log_info"
)

// ─── regex cache ──────────────────────────────────────────────────────────────

var affinityRegexCache sync.Map // map[string]*regexp.Regexp

func matchAnyRegex(patterns []string, s string) bool {
	for _, p := range patterns {
		if p == "" {
			continue
		}
		reAny, ok := affinityRegexCache.Load(p)
		if !ok {
			compiled, err := regexp.Compile(p)
			if err != nil {
				continue
			}
			affinityRegexCache.Store(p, compiled)
			reAny = compiled
		}
		if reAny.(*regexp.Regexp).MatchString(s) {
			return true
		}
	}
	return false
}

func matchAnyInclude(patterns []string, s string) bool {
	sl := strings.ToLower(s)
	for _, p := range patterns {
		if strings.Contains(sl, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// ─── key extraction ───────────────────────────────────────────────────────────

func extractAffinityValue(c *gin.Context, src ChannelAffinityKeySource) string {
	switch src.Type {
	case "context_int":
		v := c.GetInt(src.Key)
		if v <= 0 {
			return ""
		}
		return strconv.Itoa(v)
	case "context_string":
		return strings.TrimSpace(c.GetString(src.Key))
	case "gjson":
		if src.Path == "" {
			return ""
		}
		body, err := common.GetRequestBody(c)
		if err != nil || len(body) == 0 {
			return ""
		}
		res := gjson.GetBytes(body, src.Path)
		if !res.Exists() {
			return ""
		}
		return strings.TrimSpace(res.String())
	}
	return ""
}

// ─── cache key builder ────────────────────────────────────────────────────────

const affinityRedisNS = "channel_affinity:v1"

func buildAffinityCacheKey(rule ChannelAffinityRule, model, group, value string) string {
	parts := make([]string, 0, 5)
	parts = append(parts, affinityRedisNS)
	if rule.IncludeRuleName && rule.Name != "" {
		parts = append(parts, rule.Name)
	}
	if rule.IncludeModelName && model != "" {
		parts = append(parts, model)
	}
	if rule.IncludeUsingGroup && group != "" {
		parts = append(parts, group)
	}
	parts = append(parts, value)
	return strings.Join(parts, ":")
}

// ─── Redis helpers ────────────────────────────────────────────────────────────

func setAffinityRedis(key string, channelID int, ttlSeconds int) {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	err := common.RDB.Set(
		context.Background(),
		key,
		strconv.Itoa(channelID),
		time.Duration(ttlSeconds)*time.Second,
	).Err()
	if err != nil {
		logger.SysLog(fmt.Sprintf("[Affinity] redis set failed key=%s err=%v", key, err))
	}
}

func getAffinityRedis(key string) (int, bool) {
	if !common.RedisEnabled || common.RDB == nil {
		return 0, false
	}
	val, err := common.RDB.Get(context.Background(), key).Result()
	if err != nil {
		return 0, false
	}
	id, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// ─── fingerprint / hint ───────────────────────────────────────────────────────

func affinityFingerprint(s string) string {
	h := sha1.Sum([]byte(s))
	return fmt.Sprintf("%x", h[:4])
}

func affinityKeyHint(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 12 {
		return s
	}
	return s[:4] + "..." + s[len(s)-4:]
}

// ─── Public API ───────────────────────────────────────────────────────────────

// GetPreferredChannelByAffinity 从请求体提取亲和 key，查 Redis，返回上次成功的 channelID。
// 仅在无 X-Response-ID 时调用。命中后会在 gin context 存储亲和元信息。
func GetPreferredChannelByAffinity(c *gin.Context, modelName, group string) (int, bool) {
	setting := GetChannelAffinitySetting()
	if !setting.Enabled {
		return 0, false
	}
	path := ""
	if c != nil && c.Request != nil && c.Request.URL != nil {
		path = c.Request.URL.Path
	}
	ua := ""
	if c != nil && c.Request != nil {
		ua = c.Request.UserAgent()
	}

	// firstMatch 保存第一条匹配（但缓存未命中）的规则信息，
	// 用于在所有规则都未命中时设置 context，使 RecordChannelAffinity 能在本次请求成功后写入缓存。
	type firstMatchInfo struct {
		cacheKey string
		ttl      int
		logInfo  map[string]interface{}
	}
	var firstMatch *firstMatchInfo

	for _, rule := range setting.Rules {
		if !matchAnyRegex(rule.ModelRegex, modelName) {
			continue
		}
		if len(rule.PathRegex) > 0 && !matchAnyRegex(rule.PathRegex, path) {
			continue
		}
		if len(rule.UserAgentInclude) > 0 && !matchAnyInclude(rule.UserAgentInclude, ua) {
			continue
		}

		var value string
		for _, src := range rule.KeySources {
			value = extractAffinityValue(c, src)
			if value != "" {
				break
			}
		}
		if value == "" {
			continue
		}
		if rule.ValueRegex != "" && !matchAnyRegex([]string{rule.ValueRegex}, value) {
			continue
		}

		ttl := rule.TTLSeconds
		if ttl <= 0 {
			ttl = setting.DefaultTTLSeconds
		}
		cacheKey := buildAffinityCacheKey(rule, modelName, group, value)
		logInfo := map[string]interface{}{
			"rule_name": rule.Name,
			"model":     modelName,
			"group":     group,
			"path":      path,
			"key_hint":  affinityKeyHint(value),
			"key_fp":    affinityFingerprint(value),
		}

		channelID, found := getAffinityRedis(cacheKey)
		if found {
			// 缓存命中：设置完整 context（含 skip_retry），立即返回
			c.Set(ginKeyAffinityCacheKey, cacheKey)
			c.Set(ginKeyAffinityTTL, ttl)
			c.Set(ginKeyAffinitySkipRetry, rule.SkipRetryOnFailure)
			c.Set(ginKeyAffinityLogInfo, logInfo)
			logger.SysLog(fmt.Sprintf("[Affinity] hit rule=%s model=%s group=%s key_hint=%s -> channel=%d",
				rule.Name, modelName, group, affinityKeyHint(value), channelID))
			return channelID, true
		}

		// 缓存未命中：记录第一条匹配规则，继续评估后续规则
		// 注意：不设置 skip_retry——未命中时走随机渠道，失败后应允许正常重试
		if firstMatch == nil {
			firstMatch = &firstMatchInfo{cacheKey: cacheKey, ttl: ttl, logInfo: logInfo}
			logger.SysLog(fmt.Sprintf("[Affinity] miss rule=%s model=%s group=%s key_hint=%s (first visit)",
				rule.Name, modelName, group, affinityKeyHint(value)))
		}
	}

	// 所有规则均未命中缓存，但有规则匹配：设置 context 供 RecordChannelAffinity 写回使用
	if firstMatch != nil {
		c.Set(ginKeyAffinityCacheKey, firstMatch.cacheKey)
		c.Set(ginKeyAffinityTTL, firstMatch.ttl)
		c.Set(ginKeyAffinitySkipRetry, false) // 未命中时不限制重试
		c.Set(ginKeyAffinityLogInfo, firstMatch.logInfo)
	}
	return 0, false
}

// RecordChannelAffinity 在请求成功后将 channelID 写入亲和缓存。
// 仅在 GetPreferredChannelByAffinity 已为本次请求设置过 context 时生效。
func RecordChannelAffinity(c *gin.Context, channelID int) {
	if channelID <= 0 {
		return
	}
	setting := GetChannelAffinitySetting()
	if !setting.Enabled {
		return
	}

	keyAny, ok := c.Get(ginKeyAffinityCacheKey)
	if !ok {
		return
	}
	cacheKey, ok := keyAny.(string)
	if !ok || cacheKey == "" {
		return
	}
	ttl := 0
	if ttlAny, ok := c.Get(ginKeyAffinityTTL); ok {
		ttl, _ = ttlAny.(int)
	}
	if ttl <= 0 {
		ttl = setting.DefaultTTLSeconds
	}
	if ttl <= 0 {
		ttl = 3600
	}

	setAffinityRedis(cacheKey, channelID, ttl)
	logger.SysLog(fmt.Sprintf("[Affinity] recorded key=%s -> channel=%d ttl=%ds", cacheKey, channelID, ttl))
}

// ShouldSkipRetryAfterChannelAffinityFailure 亲和渠道失败时是否禁止重试。
// 返回 true 时，调用方应同时调用 ClearChannelAffinityContext 防止 post-Next 写入失败渠道。
func ShouldSkipRetryAfterChannelAffinityFailure(c *gin.Context) bool {
	if c == nil {
		return false
	}
	v, ok := c.Get(ginKeyAffinitySkipRetry)
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

// ClearChannelAffinityContext 清除亲和缓存 key，阻止 post-Next 的 RecordChannelAffinity 写入。
// 在 skip_retry_on_failure 提前返回时调用，防止将失败渠道 ID 写入亲和缓存。
func ClearChannelAffinityContext(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(ginKeyAffinityCacheKey, "")
}

// MarkAffinityRelaySuccess 标记本次请求真正成功（relay 层调用）。
// distributor 的 post-Next 通过此 flag 判断是否写回亲和缓存，
// 避免流式响应下 HTTP 200 但实际传输失败时写入错误渠道。
func MarkAffinityRelaySuccess(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set("affinity_relay_success", true)
}

// IsAffinityRelaySuccess 检查 relay 层是否标记为成功。
func IsAffinityRelaySuccess(c *gin.Context) bool {
	if c == nil {
		return false
	}
	v, ok := c.Get("affinity_relay_success")
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

// GetAffinityLogTag 从 gin context 读取亲和日志信息，返回可追加到 otherInfo 的格式化字符串。
// 格式：affinity_rule:<name>;affinity_key_fp:<fp>
// 若当前请求未触发任何亲和规则，返回空字符串。
func GetAffinityLogTag(c *gin.Context) string {
	if c == nil {
		return ""
	}
	v, ok := c.Get(ginKeyAffinityLogInfo)
	if !ok {
		return ""
	}
	info, ok := v.(map[string]interface{})
	if !ok {
		return ""
	}
	ruleName, _ := info["rule_name"].(string)
	keyFP, _ := info["key_fp"].(string)
	if ruleName == "" {
		return ""
	}
	return fmt.Sprintf("affinity_rule:%s;affinity_key_fp:%s", ruleName, keyFP)
}

// IsAffinityEligibleModel 兼容旧版 recordRelayAffinity（X-Response-ID 路径）
func IsAffinityEligibleModel(model string) bool {
	return strings.HasPrefix(model, "gpt-") ||
		strings.HasPrefix(model, "o1") ||
		strings.HasPrefix(model, "o3") ||
		strings.HasPrefix(model, "o4") ||
		strings.HasPrefix(model, "claude-") ||
		strings.HasPrefix(model, "gemini-")
}
