# Channel Affinity 规则路由 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 当请求没有 `X-Response-ID` 时，通过可配置规则从请求体提取亲和 key，将同一 key 的请求路由到上次成功的渠道（粘性路由），以提升 prompt cache 命中率。

**Architecture:**
- 在 `service/` 层新增完整亲和服务（规则匹配、Redis 读写、gin context 状态管理）
- 在 `middleware/distributor.go` 的选渠逻辑前加规则预选，选渠成功后（`c.Next()` 返回）写回缓存
- 在 `controller/relay.go` 的重试路径加 `skip_retry_on_failure` 检查
- 已有 `X-Response-ID` 路径（`CacheGetRandomSatisfiedChannel` 内部逻辑）保持不变

**Tech Stack:** Go 1.23, github.com/tidwall/gjson（新增 dep），Redis via `common.RDB`

---

## 背景知识

### 两条路径不冲突
- **路径 A（已有）**：客户端传 `X-Response-ID` → `CacheGetRandomSatisfiedChannel` 内部走 `GetClaudeCacheIdFromRedis` → Redis key 格式 `cache_response_id_{id}`
- **路径 B（本次新增）**：客户端无 `X-Response-ID` → 规则从请求体提取 key → Redis key 格式 `channel_affinity:v1:{suffix}` → 命中则优先使用历史渠道

两套 Redis key namespace 完全独立。

### gin 中间件 post-Next 写法
```go
c.Next()
// 这里的代码在所有后续 handler（含重试）完成后执行
if c.Writer.Status() < 400 {
    RecordChannelAffinity(c, c.GetInt("channel_id"))
}
```
distributor 的 post-Next 捕获最终成功的 channel_id（重试后也正确）。

### 亲和 key 构成
```
channel_affinity:v1:{ruleName}:{model}:{group}:{affinityValue}
```
由规则配置的 `IncludeRuleName / IncludeModelName / IncludeUsingGroup` 控制哪些段参与构成 key，允许不同粒度的隔离。

---

## Task 1: 添加 gjson 依赖

**Files:**
- Modify: `go.mod`, `go.sum`

**Step 1: 添加依赖**
```bash
cd /Users/yueqingli/code/one-api
go get github.com/tidwall/gjson@latest
```

**Step 2: 验证编译**
```bash
go build ./...
```
Expected: 无报错

**Step 3: Commit**
```bash
git add go.mod go.sum
git commit -m "deps: add gjson for channel affinity body key extraction"
```

---

## Task 2: 创建亲和设置（Settings）

**Files:**
- Create: `service/channel_affinity_setting.go`

**Step 1: 创建文件**

```go
package service

// ChannelAffinityKeySource 定义从请求中提取亲和 key 的方式
type ChannelAffinityKeySource struct {
	Type string // "context_int" | "context_string" | "gjson"
	Key  string // for context_int / context_string
	Path string // for gjson: JSON path in request body
}

// ChannelAffinityRule 一条亲和规则
type ChannelAffinityRule struct {
	Name             string
	ModelRegex       []string // 模型名正则，任一匹配即生效
	PathRegex        []string // 请求路径正则，为空则不限制
	UserAgentInclude []string // UA 包含检测，为空则不限制

	KeySources []ChannelAffinityKeySource // 按序尝试，取第一个非空值

	ValueRegex string // 提取到的 key 值必须匹配此正则（为空不限制）
	TTLSeconds int    // 缓存过期时间；0 表示使用全局默认

	SkipRetryOnFailure bool // true: 亲和渠道失败后不重试其他渠道

	IncludeRuleName  bool // key 包含规则名
	IncludeModelName bool // key 包含模型名
	IncludeUsingGroup bool // key 包含用户分组
}

// ChannelAffinitySetting 全局亲和配置
type ChannelAffinitySetting struct {
	Enabled           bool
	SwitchOnSuccess   bool // true: 重试成功后更新缓存到实际成功渠道
	DefaultTTLSeconds int
	Rules             []ChannelAffinityRule
}

var defaultChannelAffinitySetting = ChannelAffinitySetting{
	Enabled:           true,
	SwitchOnSuccess:   true,
	DefaultTTLSeconds: 3600,
	Rules: []ChannelAffinityRule{
		{
			Name:       "claude-cli",
			ModelRegex: []string{`^claude-`},
			PathRegex:  []string{`/v1/messages`},
			KeySources: []ChannelAffinityKeySource{
				{Type: "gjson", Path: "metadata.user_id"},
			},
			TTLSeconds:         0,
			SkipRetryOnFailure: true,
			IncludeRuleName:    true,
			IncludeUsingGroup:  true,
		},
		{
			Name:       "openai-responses",
			ModelRegex: []string{`^gpt-`, `^o1`, `^o3`, `^o4`},
			PathRegex:  []string{`/v1/responses`},
			KeySources: []ChannelAffinityKeySource{
				{Type: "gjson", Path: "prompt_cache_key"},
			},
			TTLSeconds:         0,
			SkipRetryOnFailure: true,
			IncludeRuleName:    true,
			IncludeUsingGroup:  true,
		},
		{
			Name:       "gemini-chat",
			ModelRegex: []string{`^gemini-`},
			PathRegex:  []string{},
			KeySources: []ChannelAffinityKeySource{
				{Type: "context_string", Key: "id"},      // user id
				{Type: "context_int",    Key: "id"},
			},
			TTLSeconds:         1800,
			SkipRetryOnFailure: false,
			IncludeRuleName:    true,
			IncludeModelName:   true,
			IncludeUsingGroup:  true,
		},
	},
}

var channelAffinitySetting = defaultChannelAffinitySetting

// GetChannelAffinitySetting 返回当前亲和配置
func GetChannelAffinitySetting() *ChannelAffinitySetting {
	return &channelAffinitySetting
}
```

**Step 2: 编译验证**
```bash
go build ./service/...
```

**Step 3: Commit**
```bash
git add service/channel_affinity_setting.go
git commit -m "feat(affinity): add channel affinity settings with default rules"
```

---

## Task 3: 重写 `service/channel_affinity.go`

**Files:**
- Modify: `service/channel_affinity.go`（完整替换，保留 `IsAffinityEligibleModel`）

**Step 1: 用以下内容替换文件**

```go
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

// ─── gin context keys ────────────────────────────────────────────────────────

const (
	ginKeyAffinityCacheKey  = "affinity_cache_key"
	ginKeyAffinityTTL       = "affinity_ttl_seconds"
	ginKeyAffinitySkipRetry = "affinity_skip_retry"
	ginKeyAffinityLogInfo   = "affinity_log_info"
)

// affinityContext 本次请求命中的亲和元信息
type affinityContext struct {
	CacheKey   string
	TTLSeconds int
	SkipRetry  bool
	RuleName   string
	UsingGroup string
	ModelName  string
	KeyHint    string
}

// ─── regex cache ─────────────────────────────────────────────────────────────

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
			"rule_name":   rule.Name,
			"model":       modelName,
			"group":       group,
			"path":        path,
			"key_hint":    affinityKeyHint(value),
			"key_fp":      affinityFingerprint(value),
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
```

**Step 2: 编译验证**
```bash
go build ./service/...
```
Expected: 无报错

**Step 3: Commit**
```bash
git add service/channel_affinity.go service/channel_affinity_setting.go
git commit -m "feat(affinity): implement rule-based channel affinity service"
```

---

## Task 4: 更新 `middleware/distributor.go`

**Files:**
- Modify: `middleware/distributor.go`

改动只在 `Distribute()` 函数的 `else` 分支（正常选渠逻辑），分两处：
1. 在调用 `CacheGetRandomSatisfiedChannel` 前，若无 `X-Response-ID`，先做规则亲和预查
2. 在 `c.Next()` 之后，成功时写回亲和缓存

**Step 1: 找到当前选渠代码块（约第 84-108 行）**

当前代码：
```go
} else {
    // 正常流程：根据模型选择渠道
    if shouldSelectChannel {
        if modelRequest.Model == "" {
            abortWithMessage(c, http.StatusBadRequest, "Model name is required")
            return
        }
        // 获取客户端传递的 X-Response-ID（用于渠道亲和性：Claude/OpenAI/Gemini 均支持）
        responseID := c.GetHeader("X-Response-ID")
        var cachedKeyIndex int
        channel, cachedKeyIndex, err = model.CacheGetRandomSatisfiedChannel(userGroup, modelRequest.Model, 0, responseID)
        if cachedKeyIndex >= 0 {
            c.Set("cached_key_index", cachedKeyIndex)
        }
        if err != nil {
            message := fmt.Sprintf("There are no channels available for model %s under the current group %s", modelRequest.Model, userGroup)
            if channel != nil {
                logger.SysError(fmt.Sprintf("Channel does not exist：%d", channel.Id))
                message = "Database consistency has been violated, please contact the administrator"
            }
            abortWithMessage(c, http.StatusServiceUnavailable, message)
            return
        }
    }
}
```

**Step 2: 替换为以下代码**

```go
} else {
    // 正常流程：根据模型选择渠道
    if shouldSelectChannel {
        if modelRequest.Model == "" {
            abortWithMessage(c, http.StatusBadRequest, "Model name is required")
            return
        }
        // 路径 A：X-Response-ID 存在 → 内部走 GetClaudeCacheIdFromRedis（原有逻辑）
        responseID := c.GetHeader("X-Response-ID")

        // 路径 B：X-Response-ID 不存在 → 规则亲和预查
        if responseID == "" {
            if preferredID, found := service.GetPreferredChannelByAffinity(c, modelRequest.Model, userGroup); found {
                preferred, getErr := model.CacheGetChannel(preferredID)
                if getErr == nil && preferred != nil && preferred.Status == common.ChannelStatusEnabled {
                    // 校验 group 和 model 仍然匹配
                    groupOK := false
                    for _, g := range strings.Split(preferred.Group, ",") {
                        if strings.TrimSpace(g) == userGroup {
                            groupOK = true
                            break
                        }
                    }
                    modelOK := false
                    for _, m := range strings.Split(preferred.Models, ",") {
                        if strings.TrimSpace(m) == modelRequest.Model {
                            modelOK = true
                            break
                        }
                    }
                    if groupOK && modelOK {
                        channel = preferred
                        logger.SysLog(fmt.Sprintf("[Affinity] using preferred channel %d for model %s group %s",
                            preferredID, modelRequest.Model, userGroup))
                    } else {
                        logger.SysLog(fmt.Sprintf("[Affinity] cached channel %d no longer valid (group=%v model=%v), fallback",
                            preferredID, groupOK, modelOK))
                    }
                }
            }
        }

        // 路径 A/C：亲和未命中或 X-Response-ID 存在，走正常随机选渠
        if channel == nil {
            var cachedKeyIndex int
            channel, cachedKeyIndex, err = model.CacheGetRandomSatisfiedChannel(userGroup, modelRequest.Model, 0, responseID)
            if cachedKeyIndex >= 0 {
                c.Set("cached_key_index", cachedKeyIndex)
            }
            if err != nil {
                message := fmt.Sprintf("There are no channels available for model %s under the current group %s", modelRequest.Model, userGroup)
                if channel != nil {
                    logger.SysError(fmt.Sprintf("Channel does not exist：%d", channel.Id))
                    message = "Database consistency has been violated, please contact the administrator"
                }
                abortWithMessage(c, http.StatusServiceUnavailable, message)
                return
            }
        }
    }
}
```

**Step 3: 在 `c.Next()` 之后添加写回逻辑**

当前末尾：
```go
    if channel != nil {
        SetupContextForSelectedChannel(c, channel, requestModel)
    }
    c.Next()
}
```

替换为：
```go
    if channel != nil {
        SetupContextForSelectedChannel(c, channel, requestModel)
    }
    c.Next()
    // 请求成功（无 4xx/5xx）后写回规则亲和缓存
    if c.Writer.Status() < 400 {
        service.RecordChannelAffinity(c, c.GetInt("channel_id"))
    }
}
```

**Step 4: 在 import 里确认 `service` 包已引入**

检查 import 块，若没有 `service` 包则添加：
```go
"github.com/songquanpeng/one-api/service"
```

**Step 5: 编译验证**
```bash
go build ./middleware/...
go build ./...
```

**Step 6: Commit**
```bash
git add middleware/distributor.go
git commit -m "feat(affinity): integrate rule-based affinity pre-check and post-write in distributor"
```

---

## Task 5: 更新 `controller/relay.go` — 添加 skip_retry 检查

**Files:**
- Modify: `controller/relay.go`

**Step 1: 找到重试循环入口（约第 172-175 行）**

当前代码（在处理首次失败之后）：
```go
lastChannel := getLastRetryFallbackChannel(channelId)

for i := retryTimes; i > 0; i-- {
```

**Step 2: 在 `for` 循环前插入 skip_retry 检查**

```go
lastChannel := getLastRetryFallbackChannel(channelId)

// 如果命中了亲和规则且配置了 skip_retry_on_failure，不跨渠道重试
if service.ShouldSkipRetryAfterChannelAffinityFailure(c) {
    logger.Infof(ctx, "Affinity skip_retry_on_failure=true, skipping retry for model=%s channel=%d", originalModel, channelId)
    recordFailureLog(c, ctx, userId, channelId, channelName, keyIndex, bizErr, originalModel, tokenName, requestID, channelHistory)
    return
}

for i := retryTimes; i > 0; i-- {
```

**Step 3: 验证 `service` 包已在 import 中（已有）**

relay.go 已经 import 了 `service` 包（`recordRelayAffinity` 就用到了），无需额外修改。

**Step 4: 编译验证**
```bash
go build ./controller/...
go build ./...
go vet ./...
```
Expected: 无报错、无 warning

**Step 5: Commit**
```bash
git add controller/relay.go
git commit -m "feat(affinity): add skip_retry_on_failure gate in relay retry loop"
```

---

## Task 6: 清理 `common/constants.go`

**Files:**
- Modify: `common/constants.go`

**Step 1: 删除未使用常量**

找到：
```go
// CacheChannelAffinityKey 渠道亲和性缓存 key 格式：group:modelPrefix:affinityKey
// 用于 request_id / user 字段到渠道 ID 的粘性路由
CacheChannelAffinityKey = "channel_affinity:v1:%s:%s:%s"
```

删除这三行（key format 现在由 `service/channel_affinity.go` 中的 `buildAffinityCacheKey` 负责，常量无意义）。

**Step 2: 编译验证**
```bash
go build ./...
```

**Step 3: Commit**
```bash
git add common/constants.go
git commit -m "chore: remove dead CacheChannelAffinityKey constant"
```

---

## Task 7: 端到端验证

**Step 1: 全量编译 + vet**
```bash
go build ./... && go vet ./...
```

**Step 2: 运行现有测试**
```bash
go test ./...
```

**Step 3: 手动验证亲和路径（需有 Redis）**

模拟 Claude CLI 场景：
```bash
# 首次请求（无缓存，走正常选渠）
curl -X POST http://localhost:3000/v1/messages \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-3-5-sonnet-20241022","max_tokens":10,"metadata":{"user_id":"test-user-001"},"messages":[{"role":"user","content":"hi"}]}'

# 检查 Redis 是否有亲和记录
redis-cli keys "channel_affinity:v1:*"

# 第二次请求（同 user_id，应命中缓存，路由到同一渠道）
# 观察日志中 "[Affinity] hit rule=claude-cli ..." 字样
curl -X POST http://localhost:3000/v1/messages \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-3-5-sonnet-20241022","max_tokens":10,"metadata":{"user_id":"test-user-001"},"messages":[{"role":"user","content":"hi again"}]}'
```

期望日志：`[Affinity] hit rule=claude-cli model=claude-... group=... key_hint=test... -> channel=N`

**Step 4: Final commit（若有调整）**
```bash
git add -p
git commit -m "fix(affinity): post-validation adjustments"
```

---

## 注意事项

1. **`strings` import in distributor**：`distributor.go` 已有 `"strings"` import（用于 Gemini 路径解析），无需重复添加
2. **`service` import in distributor**：`distributor.go` 当前没有 import `service` 包，需要新增
3. **`recordRelayAffinity` 不删除**：它服务于 X-Response-ID 路径，两条路径独立工作，暂时保留
4. **`recordFailureLog` 函数名**：Task 5 里 `recordFailureLog` 是 relay.go 中已有的失败日志函数，实际名字需查对（可能叫 `recordFailedRequestLog`），执行时需对照实际代码
