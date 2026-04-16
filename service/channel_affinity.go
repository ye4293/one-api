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

		// 存入 context，供 RecordChannelAffinity 写回时使用
		c.Set(ginKeyAffinityCacheKey, cacheKey)
		c.Set(ginKeyAffinityTTL, ttl)
		c.Set(ginKeyAffinitySkipRetry, rule.SkipRetryOnFailure)
		c.Set(ginKeyAffinityLogInfo, map[string]interface{}{
			"rule_name": rule.Name,
			"model":     modelName,
			"group":     group,
			"path":      path,
			"key_hint":  affinityKeyHint(value),
			"key_fp":    affinityFingerprint(value),
		})

		channelID, found := getAffinityRedis(cacheKey)
		if found {
			logger.SysLog(fmt.Sprintf("[Affinity] hit rule=%s model=%s group=%s key_hint=%s -> channel=%d",
				rule.Name, modelName, group, affinityKeyHint(value), channelID))
			return channelID, true
		}
		// 规则匹配但缓存未命中：context 已设置，首次请求后 RecordChannelAffinity 会写入
		logger.SysLog(fmt.Sprintf("[Affinity] miss rule=%s model=%s group=%s key_hint=%s (first visit)",
			rule.Name, modelName, group, affinityKeyHint(value)))
		return 0, false
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

	// SwitchOnSuccess: 使用实际成功的渠道（覆盖重试前的选择）
	if setting.SwitchOnSuccess {
		if finalID := c.GetInt("channel_id"); finalID > 0 {
			channelID = finalID
		}
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

// IsAffinityEligibleModel 兼容旧版 recordRelayAffinity（X-Response-ID 路径）
func IsAffinityEligibleModel(model string) bool {
	return strings.HasPrefix(model, "gpt-") ||
		strings.HasPrefix(model, "o1") ||
		strings.HasPrefix(model, "o3") ||
		strings.HasPrefix(model, "o4") ||
		strings.HasPrefix(model, "claude-") ||
		strings.HasPrefix(model, "gemini-")
}
