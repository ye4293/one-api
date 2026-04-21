# OpenAI Responses API Encrypted-Content Channel Affinity 实施方案

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 修复 `/v1/responses` 续轮请求在渠道轮询下返回 `400 invalid_encrypted_content` 的问题，对齐上游 [CLIProxyAPI PR #1796](https://github.com/router-for-me/CLIProxyAPI/pull/1796) 的修复策略。

**Architecture:**
- 复用现有 `CacheResponseIdToChannel` + `GetClaudeCacheIdFromRedis` 基础设施，新增一个并行的 `cache_enc_content_{hash}` Redis namespace 用于 encrypted_content 哈希 → channel 的粘性路由
- 在 `middleware/distributor.go` 针对 `/v1/responses` 路径额外读取请求体的 `previous_response_id` 字段和 `input[].encrypted_content` 并查缓存，命中则 pin 到原渠道；未命中走正常选渠
- 在 `relay/controller/opeai_response.go` 响应成功后，把响应 `output[]` 内的 `encrypted_content` 哈希写入缓存；pinned 渠道返回 `invalid_encrypted_content` 错误时，strip 掉所有 encrypted_content 后不 pin 重试一次

**Tech Stack:** Go 1.23, github.com/tidwall/gjson（已有依赖）, github.com/tidwall/sjson（需新增，用于 strip encrypted_content），crypto/sha256, Redis via `common.RDB`

**设计决策（已与需求方确认）:**
- 亲和粒度：`channel_id + key_index`（复用现有 Redis value 格式 `"channelId:keyIndex"`）
- SSE fallback：只处理 `StatusCode != 200` 场景，不做 SSE event sniffer（P80 覆盖，实现简单）
- 写入时机：只写响应 `output[]` 中的 encrypted_content，不写请求 `input[]` 的
- `previous_response_id` 提取在 distributor 硬编码，不走 `ChannelAffinityConfig` 规则（语义固定）
- strip-and-retry 内置在 `RelayOpenaiResponseNative`，最多 1 次；失败后让 `RelayResponse` 外层 retry loop 接管

---

## 背景：现有基础设施

- `model/cache.go:813 CacheResponseIdToChannel(responseId, channelId, keyIndex, logPrefix)` —— 写入 `cache_response_id_{id}` key
- `model/cache.go:775 GetClaudeCacheIdFromRedis(id)` —— 读取，返回 `(channelID string, keyIndex int, err)`
- `model/cache.go:389 CacheGetRandomSatisfiedChannel(group, model, skipPriorityLevels, responseID)` —— 当 `responseID != ""` 时自动优先命中缓存渠道
- `middleware/distributor.go:138` 现有调用：`model.CacheGetRandomSatisfiedChannel(userGroup, modelRequest.Model, 0, responseID)`
- `relay/controller/opeai_response.go:400,451` 现有写入：非流 / 流式成功后写 `openaiResponse.ID → channel`
- `middleware/distributor.go:293 cached_key_index` —— 已有 gin context key，`SetupContextForSelectedChannel` 会还原 key 索引

---

## Task 1: 新增 encrypted_content 提取/哈希/strip 工具

**Files:**
- Create: `relay/controller/responses_affinity_helper.go`
- Create: `relay/controller/responses_affinity_helper_test.go`

**Step 1: 添加 sjson 依赖**

```bash
cd /Users/yueqingli/code/one-api
go get github.com/tidwall/sjson@latest
go build ./...
```
Expected: 无报错

**Step 2: 写失败测试**

创建 `relay/controller/responses_affinity_helper_test.go`:

```go
package controller

import (
	"testing"
)

func TestExtractPreviousResponseID(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"with id", `{"model":"gpt-5","previous_response_id":"resp_abc123"}`, "resp_abc123"},
		{"without id", `{"model":"gpt-5"}`, ""},
		{"empty body", ``, ""},
		{"malformed", `{not json`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractPreviousResponseID([]byte(tt.body))
			if got != tt.want {
				t.Errorf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestExtractEncryptedContentHashes(t *testing.T) {
	// input[] 数组中两条 reasoning，各带一个 encrypted_content
	body := `{
		"model":"gpt-5",
		"input":[
			{"type":"reasoning","encrypted_content":"AAA","id":"r1"},
			{"type":"message","content":"hi"},
			{"type":"reasoning","encrypted_content":"BBB","id":"r2"}
		]
	}`
	hashes := ExtractEncryptedContentHashes([]byte(body))
	if len(hashes) != 2 {
		t.Fatalf("expected 2 hashes got %d", len(hashes))
	}
	// SHA-256("AAA") = 形如 cb1ad2119d8fafb69566510ee712661f9f14b83385006ef92aec47f523a38358
	// 只断言长度、十六进制
	for _, h := range hashes {
		if len(h) != 64 {
			t.Errorf("hash len = %d want 64", len(h))
		}
	}
}

func TestExtractEncryptedContentHashes_Empty(t *testing.T) {
	body := `{"model":"gpt-5","input":[{"type":"message","content":"hi"}]}`
	hashes := ExtractEncryptedContentHashes([]byte(body))
	if len(hashes) != 0 {
		t.Errorf("expected 0 hashes got %d", len(hashes))
	}
}

func TestStripEncryptedContentFromInput(t *testing.T) {
	in := `{"input":[
		{"type":"reasoning","encrypted_content":"AAA","id":"r1"},
		{"type":"message","content":"hi"}
	]}`
	out, err := StripEncryptedContentFromInput([]byte(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ExtractEncryptedContentHashes(out) != nil && len(ExtractEncryptedContentHashes(out)) > 0 {
		t.Errorf("strip did not remove encrypted_content: %s", string(out))
	}
	// 其他字段保留
	if !bytesContains(out, `"id":"r1"`) || !bytesContains(out, `"content":"hi"`) {
		t.Errorf("strip removed unrelated fields: %s", string(out))
	}
}

func TestExtractOutputEncryptedContentHashes(t *testing.T) {
	// 模拟 OpenAI Responses API 响应中 output[] 的 reasoning item
	body := `{
		"id":"resp_1",
		"output":[
			{"type":"reasoning","encrypted_content":"OUT_A","id":"r3"},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}
		]
	}`
	hashes := ExtractOutputEncryptedContentHashes([]byte(body))
	if len(hashes) != 1 {
		t.Fatalf("expected 1 hash got %d", len(hashes))
	}
	if len(hashes[0]) != 64 {
		t.Errorf("hash len = %d want 64", len(hashes[0]))
	}
}

func TestIsInvalidEncryptedContentError(t *testing.T) {
	cases := []struct {
		code    string
		message string
		want    bool
	}{
		{"invalid_encrypted_content", "blah", true},
		{"status_400", "invalid_encrypted_content in request", true},
		{"status_400", "could not be decrypted or parsed", true},
		{"status_400", "random 400 error", false},
		{"status_401", "invalid_encrypted_content", false}, // 只认 4xx 中的这个 code
	}
	for _, c := range cases {
		got := IsInvalidEncryptedContentError(c.code, c.message)
		if got != c.want {
			t.Errorf("code=%q msg=%q got=%v want=%v", c.code, c.message, got, c.want)
		}
	}
}

func bytesContains(b []byte, s string) bool {
	return len(b) > 0 && len(s) > 0 && indexOf(b, s) >= 0
}

func indexOf(b []byte, s string) int {
	for i := 0; i+len(s) <= len(b); i++ {
		if string(b[i:i+len(s)]) == s {
			return i
		}
	}
	return -1
}
```

**Step 3: 运行测试验证失败**

```bash
go test ./relay/controller/ -run TestExtract -v
```
Expected: FAIL — `undefined: ExtractPreviousResponseID` 等

**Step 4: 实现 helper**

创建 `relay/controller/responses_affinity_helper.go`:

```go
package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ExtractPreviousResponseID 从 /v1/responses 请求体中读取 previous_response_id
// 返回空串表示未提供或解析失败
func ExtractPreviousResponseID(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	res := gjson.GetBytes(body, "previous_response_id")
	if !res.Exists() {
		return ""
	}
	return strings.TrimSpace(res.String())
}

// ExtractEncryptedContentHashes 从请求体的 input[] 中提取所有 reasoning.encrypted_content 字段并 SHA-256
// 返回十六进制字符串数组（按出现顺序），长度 0 表示没有任何 encrypted_content
func ExtractEncryptedContentHashes(body []byte) []string {
	if len(body) == 0 {
		return nil
	}
	var hashes []string
	gjson.GetBytes(body, "input").ForEach(func(_, item gjson.Result) bool {
		if item.Get("type").String() != "reasoning" {
			return true
		}
		enc := item.Get("encrypted_content").String()
		if enc == "" {
			return true
		}
		sum := sha256.Sum256([]byte(enc))
		hashes = append(hashes, hex.EncodeToString(sum[:]))
		return true
	})
	return hashes
}

// ExtractOutputEncryptedContentHashes 从响应体 output[] 中提取 reasoning.encrypted_content 哈希
// 用途：响应成功后把本轮新生成的 reasoning 绑定到当前渠道，下一轮续轮时可定向回同渠道
func ExtractOutputEncryptedContentHashes(body []byte) []string {
	if len(body) == 0 {
		return nil
	}
	var hashes []string
	gjson.GetBytes(body, "output").ForEach(func(_, item gjson.Result) bool {
		if item.Get("type").String() != "reasoning" {
			return true
		}
		enc := item.Get("encrypted_content").String()
		if enc == "" {
			return true
		}
		sum := sha256.Sum256([]byte(enc))
		hashes = append(hashes, hex.EncodeToString(sum[:]))
		return true
	})
	return hashes
}

// StripEncryptedContentFromInput 清除请求体 input[] 中所有 reasoning.encrypted_content 字段
// 返回清理后的 body。其他字段保持不变
func StripEncryptedContentFromInput(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}
	inputArr := gjson.GetBytes(body, "input")
	if !inputArr.IsArray() {
		return body, nil
	}
	out := body
	var err error
	// 从后往前删，避免 index 偏移
	items := inputArr.Array()
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Get("type").String() != "reasoning" {
			continue
		}
		if !items[i].Get("encrypted_content").Exists() {
			continue
		}
		out, err = sjson.DeleteBytes(out, "input."+itoa(i)+".encrypted_content")
		if err != nil {
			return body, err
		}
	}
	return out, nil
}

func itoa(i int) string {
	// 避免引入 strconv 大包，简单实现（i 一般 <100）
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}

// IsInvalidEncryptedContentError 判断错误是否为 encrypted_content 解密失败
// 触发 strip-and-retry fallback 的关键判定
func IsInvalidEncryptedContentError(code, message string) bool {
	lowerCode := strings.ToLower(code)
	lowerMsg := strings.ToLower(message)
	// 明确的 code
	if lowerCode == "invalid_encrypted_content" {
		return true
	}
	// 4xx 状态码 + 消息匹配
	if strings.HasPrefix(lowerCode, "status_4") {
		if strings.Contains(lowerMsg, "invalid_encrypted_content") {
			return true
		}
		if strings.Contains(lowerMsg, "could not be decrypted or parsed") {
			return true
		}
	}
	return false
}
```

**Step 5: 运行测试验证通过**

```bash
go test ./relay/controller/ -run TestExtract -v
go test ./relay/controller/ -run TestStrip -v
go test ./relay/controller/ -run TestIsInvalid -v
```
Expected: PASS

**Step 6: Commit**

```bash
git add go.mod go.sum relay/controller/responses_affinity_helper.go relay/controller/responses_affinity_helper_test.go
git commit -m "feat(responses-affinity): add encrypted_content extract/hash/strip helpers"
```

---

## Task 2: 新增 encrypted_content → channel Redis 缓存函数

**Files:**
- Modify: `common/constants.go:155-158`
- Modify: `model/cache.go` (追加到末尾)

**Step 1: 在 `common/constants.go` 里加新的 cache key 格式**

找到 line 155-158 的 `CacheClaudeRsID` 块，追加一行：

```go
const (
	CacheClaudeRsID     = "cache_response_id_%s" // 改为通用格式（原：claude_cache_response_id_%s）
	CacheClaudeLength   = "claude_length_%s"     // 保持不变，仅 Claude 使用
	CacheEncContentHash = "cache_enc_content_%s" // OpenAI Responses API encrypted_content 哈希 → channelId[:keyIndex]
)
```

**Step 2: 在 `model/cache.go` 追加读写函数（紧跟 `CacheResponseIdToChannel` 之后）**

```go
// CacheEncryptedContentToChannel 写入 encrypted_content 哈希到 channel_id 的映射
// hash: sha256(encrypted_content) 的 hex 字符串
// 24h TTL，与 CacheResponseIdToChannel 一致
// Redis 写失败不阻断主流程
func CacheEncryptedContentToChannel(hash string, channelId int, keyIndex int, logPrefix string) {
	if hash == "" || channelId <= 0 {
		return
	}
	if !common.RedisEnabled {
		return
	}
	cacheKey := fmt.Sprintf(common.CacheEncContentHash, hash)
	value := fmt.Sprintf("%d", channelId)
	if keyIndex >= 0 {
		value = fmt.Sprintf("%d:%d", channelId, keyIndex)
	}
	expire := 24 * time.Hour
	if err := common.RDB.Set(context.Background(), cacheKey, value, expire).Err(); err != nil {
		logger.SysLog(fmt.Sprintf("[%s] Failed to cache enc_content_hash=%s -> channel=%d keyIndex=%d: %v",
			logPrefix, hash[:8], channelId, keyIndex, err))
		return
	}
	logger.SysLog(fmt.Sprintf("[%s] Cached enc_content_hash=%s... -> channel=%d keyIndex=%d (TTL: 24h)",
		logPrefix, hash[:8], channelId, keyIndex))
}

// GetEncryptedContentCacheIdFromRedis 根据 encrypted_content 哈希查 channel
// 返回 (channelID 字符串, keyIndex, error)；keyIndex < 0 表示兼容旧值无 key 索引
func GetEncryptedContentCacheIdFromRedis(hash string) (string, int, error) {
	if !common.RedisEnabled {
		return "", -1, errors.New("redis disabled")
	}
	if hash == "" {
		return "", -1, errors.New("empty hash")
	}
	cacheKey := fmt.Sprintf(common.CacheEncContentHash, hash)
	value, err := common.RedisGet(cacheKey)
	if err != nil {
		return "", -1, err
	}
	parts := strings.SplitN(value, ":", 2)
	channelID := parts[0]
	keyIndex := -1
	if len(parts) == 2 {
		if idx, parseErr := strconv.Atoi(parts[1]); parseErr == nil {
			keyIndex = idx
		}
	}
	return channelID, keyIndex, nil
}
```

**Step 3: 编译验证**

```bash
go build ./...
go vet ./...
```
Expected: 无报错、无 warning

**Step 4: Commit**

```bash
git add common/constants.go model/cache.go
git commit -m "feat(responses-affinity): add Redis namespace for encrypted_content hash -> channel"
```

---

## Task 3: 响应成功后写入 encrypted_content 哈希缓存

在已有的 `CacheResponseIdToChannel` 调用旁边，同时写 output[] 的 encrypted_content 哈希。只写入，不改变读路径，零风险独立部署。

**Files:**
- Modify: `relay/controller/opeai_response.go:395-401` (非流式)
- Modify: `relay/controller/opeai_response.go:444-463` (流式 response.completed)

**Step 1: 非流式响应成功后写入**

找到 `doNativeOpenaiResponse` 中的这段（约 line 395-401）：

```go
util.IOCopyBytesGracefully(c, resp, responseBody)
logger.Info(c.Request.Context(), fmt.Sprintf("OpenAI Response : %v", openaiResponse))
// 缓存 response_id 到 Redis
dbmodel.CacheResponseIdToChannel(openaiResponse.ID, c.GetInt("channel_id"), c.GetInt("key_index"), "OpenAI Response Cache")
c.Set("x_response_id", openaiResponse.ID)
```

替换为：

```go
util.IOCopyBytesGracefully(c, resp, responseBody)
logger.Info(c.Request.Context(), fmt.Sprintf("OpenAI Response : %v", openaiResponse))
// 缓存 response_id 到 Redis
channelId := c.GetInt("channel_id")
keyIdx := c.GetInt("key_index")
dbmodel.CacheResponseIdToChannel(openaiResponse.ID, channelId, keyIdx, "OpenAI Response Cache")
c.Set("x_response_id", openaiResponse.ID)
// 缓存 output[] 中 reasoning.encrypted_content 的哈希 → channel（支持下轮 encrypted_content 续轮定向）
for _, h := range ExtractOutputEncryptedContentHashes(responseBody) {
	dbmodel.CacheEncryptedContentToChannel(h, channelId, keyIdx, "OpenAI Response EncContent Cache")
}
```

**Step 2: 流式响应 response.completed 事件写入**

找到 `doNativeOpenaiResponseStream` 中 `case "response.completed":` 块（约 line 444-463）：

```go
case "response.completed":
    if streamResponse.Response != nil {
        if streamResponse.Response.Usage != nil {
            lastUsageMetadata = streamResponse.Response.Usage
        }

        // 缓存 response_id 到 Redis
        dbmodel.CacheResponseIdToChannel(streamResponse.Response.ID, c.GetInt("channel_id"), c.GetInt("key_index"), "OpenAI Response Cache Stream")
        c.Set("x_response_id", streamResponse.Response.ID)
        ...
```

替换为（在 CacheResponseIdToChannel 下面加一段）：

```go
case "response.completed":
    if streamResponse.Response != nil {
        if streamResponse.Response.Usage != nil {
            lastUsageMetadata = streamResponse.Response.Usage
        }

        channelId := c.GetInt("channel_id")
        keyIdx := c.GetInt("key_index")
        dbmodel.CacheResponseIdToChannel(streamResponse.Response.ID, channelId, keyIdx, "OpenAI Response Cache Stream")
        c.Set("x_response_id", streamResponse.Response.ID)

        // 流式场景下响应对象里也会带 output[]，同样提取 encrypted_content 哈希写缓存
        // 注意：这里序列化后再 hash，保持与非流式一致
        if streamResponse.Response.Output != nil {
            if respBytes, errMarshal := json.Marshal(streamResponse.Response); errMarshal == nil {
                for _, h := range ExtractOutputEncryptedContentHashes(respBytes) {
                    dbmodel.CacheEncryptedContentToChannel(h, channelId, keyIdx, "OpenAI Response EncContent Stream Cache")
                }
            }
        }
        ...
```

**Step 3: 编译验证**

```bash
go build ./relay/controller/...
go vet ./relay/controller/...
```
Expected: 无报错

**Step 4: 手动冒烟验证（可选，需 Redis）**

```bash
# 启动服务后，发一个 /v1/responses 请求（gpt-5 或 o3）
curl -X POST http://localhost:3000/v1/responses \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-5","input":"hello"}'

# 检查 Redis，应看到至少一个 cache_enc_content_* key（如果响应包含 reasoning）
redis-cli keys "cache_enc_content_*"
```

**Step 5: Commit**

```bash
git add relay/controller/opeai_response.go
git commit -m "feat(responses-affinity): cache encrypted_content hashes on response success"
```

---

## Task 4: distributor 预读 /v1/responses 亲和 key（pin 渠道）

**Files:**
- Modify: `middleware/distributor.go`

**核心改动:** 在 `responseID := c.GetHeader("X-Response-ID")` 之后，若 path 是 `/v1/responses` 且 header 为空，则从 body 读 `previous_response_id` 作为 responseID 传入；如果 previous_response_id 不存在但有 encrypted_content，查 enc_content 缓存并直接选 channel。

**Step 1: 在 import 中确认已有 `github.com/songquanpeng/one-api/relay/controller`**

当前 `middleware/distributor.go` 没有 import `relay/controller`，但我们要用 `controller.ExtractPreviousResponseID` 等函数。**注意循环依赖风险**：

- `middleware` 被 `controller` import（`RelayResponse` 里调 `middleware.SetupContextForSelectedChannel`）
- 如果 `middleware` import `relay/controller`，形成 middleware → relay/controller → controller → middleware 环

**解决方案**：把 helper 函数从 `relay/controller` 包移到 `common/encrypted_affinity.go`（无依赖的纯工具包）。

**Step 2: 把 Task 1 的 helper 移到 common**

```bash
mv relay/controller/responses_affinity_helper.go common/encrypted_affinity.go
mv relay/controller/responses_affinity_helper_test.go common/encrypted_affinity_test.go
```

修改两个文件的 package 声明：
```go
package common
```

更新 `relay/controller/opeai_response.go` Task 3 的两处调用：
```go
for _, h := range common.ExtractOutputEncryptedContentHashes(responseBody) { ... }
```

确认 `common` package 已在 opeai_response.go import 中。

**Step 3: 运行测试验证 helper 仍通过**

```bash
go test ./common/ -run TestExtract -v
go test ./common/ -run TestStrip -v
go test ./common/ -run TestIsInvalid -v
```
Expected: PASS

**Step 4: 修改 distributor 主体逻辑**

找到 `middleware/distributor.go:92-94`：

```go
// 路径 A：X-Response-ID 存在 → 内部走 GetClaudeCacheIdFromRedis（原有逻辑）
responseID := c.GetHeader("X-Response-ID")
```

替换为：

```go
// 路径 A：X-Response-ID header 存在 → 内部走 GetClaudeCacheIdFromRedis
responseID := c.GetHeader("X-Response-ID")

// 路径 A-2：OpenAI /v1/responses 自动从 body 读 previous_response_id
// 上游官方 SDK 不会传 X-Response-ID header，改在 body 里传 previous_response_id
// 把它视作等价的 responseID，复用 Claude Cache 的读路径
if responseID == "" && strings.HasPrefix(c.Request.URL.Path, "/v1/responses") {
	if body, bodyErr := common.GetRequestBody(c); bodyErr == nil {
		if prevID := common.ExtractPreviousResponseID(body); prevID != "" {
			responseID = prevID
			logger.Infof(c.Request.Context(), "[ResponsesAffinity] pinned by previous_response_id=%s", prevID)
		} else {
			// 路径 A-3：没有 previous_response_id，尝试用 encrypted_content 哈希查缓存
			// 任意一个 hash 命中即 pin（通常多个 reasoning 会绑到同一 channel）
			hashes := common.ExtractEncryptedContentHashes(body)
			for _, h := range hashes {
				cachedChannelID, cachedKeyIdx, err := model.GetEncryptedContentCacheIdFromRedis(h)
				if err == nil && cachedChannelID != "" {
					if chID, parseErr := strconv.Atoi(cachedChannelID); parseErr == nil && chID > 0 {
						if ch, getErr := model.CacheGetChannelCopy(chID); getErr == nil && ch != nil && ch.Status == common.ChannelStatusEnabled {
							// 校验 group/model 匹配
							groupOK := false
							for _, g := range strings.Split(ch.Group, ",") {
								if strings.TrimSpace(g) == userGroup {
									groupOK = true
									break
								}
							}
							modelOK := false
							for _, m := range strings.Split(ch.Models, ",") {
								if strings.TrimSpace(m) == modelRequest.Model {
									modelOK = true
									break
								}
							}
							if groupOK && modelOK {
								channel = ch
								if cachedKeyIdx >= 0 {
									c.Set("cached_key_index", cachedKeyIdx)
								}
								c.Set("responses_pinned_by_enc_content", true)
								logger.Infof(c.Request.Context(), "[ResponsesAffinity] pinned by enc_content hash=%s... channel=%d keyIndex=%d",
									h[:8], chID, cachedKeyIdx)
								break
							}
						}
					}
				}
			}
		}
	}
}
```

**Step 5: 在 pin 路径设置 skip_retry 标记**

在上面新增逻辑的 `channel = ch` 那一行之后，设置一个 gin context flag：

```go
channel = ch
if cachedKeyIdx >= 0 {
	c.Set("cached_key_index", cachedKeyIdx)
}
c.Set("responses_pinned_by_enc_content", true)
c.Set("responses_affinity_pinned", true) // 供 RelayOpenaiResponseNative 识别
```

另外在 `responseID != ""` 的 CacheGetRandomSatisfiedChannel 命中时（即 previous_response_id 命中）也设同样 flag：

在 `model.CacheGetRandomSatisfiedChannel` 调用完之后（distributor.go:138 之后）加：
```go
if responseID != "" && channel != nil {
	c.Set("responses_affinity_pinned", true)
}
```

**Step 6: 编译验证**

```bash
go build ./...
go vet ./...
```
Expected: 无报错。若有循环依赖提示，检查 import 布局。

**Step 7: Commit**

```bash
git add common/encrypted_affinity.go common/encrypted_affinity_test.go middleware/distributor.go relay/controller/opeai_response.go
git commit -m "feat(responses-affinity): pin channel by previous_response_id or encrypted_content hash in distributor"
```

---

## Task 5: strip-and-retry fallback（pinned 渠道失败时）

当 pinned 渠道返回 `invalid_encrypted_content` 错误时，strip 掉请求中所有 encrypted_content，重新选一次渠道（不 pin），重试一次。只覆盖 `StatusCode != 200` 的错误路径（非流式 + SSE 请求阶段）。

**Files:**
- Modify: `relay/controller/opeai_response.go` (`RelayOpenaiResponseNative` 主体)

**Step 1: 写集成测试（可选，如果有 mock 基础设施则加）**

简单起见，本任务用手动冒烟验证，不写单元测试。依赖上游行为需要真实环境。

**Step 2: 改 `RelayOpenaiResponseNative`**

找到 `relay/controller/opeai_response.go:100-119`：

```go
adaptor.Init(meta)
resp, err := adaptor.DoRequest(c, meta, bytes.NewBuffer(originRequestBody))
if err != nil {
	return openai.ErrorWrapper(err, "failed_to_send_request", http.StatusBadGateway)
}

var usageMetadata *openai.ResponseUsage
var openaiErr *model.ErrorWithStatusCode

// AWS adaptor 的 DoRequest 返回 nil, nil，因为 AWS SDK 直接处理请求
// 这种情况下应该使用 DoResponse 来处理
if meta.IsStream {
	usageMetadata, openaiErr = doNativeOpenaiResponseStream(c, resp, meta)
} else {
	usageMetadata, openaiErr = doNativeOpenaiResponse(c, resp, meta)
}

if openaiErr != nil {
	return openaiErr
}
```

替换为：

```go
adaptor.Init(meta)
resp, err := adaptor.DoRequest(c, meta, bytes.NewBuffer(originRequestBody))
if err != nil {
	return openai.ErrorWrapper(err, "failed_to_send_request", http.StatusBadGateway)
}

var usageMetadata *openai.ResponseUsage
var openaiErr *model.ErrorWithStatusCode

if meta.IsStream {
	usageMetadata, openaiErr = doNativeOpenaiResponseStream(c, resp, meta)
} else {
	usageMetadata, openaiErr = doNativeOpenaiResponse(c, resp, meta)
}

// strip-and-retry fallback：
// pinned 渠道返回 invalid_encrypted_content → 剔除 encrypted_content 后以不 pin 方式重试一次
if openaiErr != nil && c.GetBool("responses_affinity_pinned") && !c.GetBool("responses_affinity_retried") &&
	common.IsInvalidEncryptedContentError(openaiErr.Error.Code, openaiErr.Error.Message) {

	logger.Infof(ctx, "[ResponsesAffinity] pinned channel %d failed with invalid_encrypted_content, stripping and retrying unpinned", c.GetInt("channel_id"))

	strippedBody, stripErr := common.StripEncryptedContentFromInput(originRequestBody)
	if stripErr != nil {
		logger.Errorf(ctx, "[ResponsesAffinity] strip failed: %v, giving up fallback", stripErr)
		return openaiErr
	}

	// 标记已重试，防止无限递归
	c.Set("responses_affinity_retried", true)
	// 清除 pin 标记，让下次 strip-and-retry 判断失败
	c.Set("responses_affinity_pinned", false)

	// 重新选一个不同的 channel
	group := c.GetString("group")
	excludedID := c.GetInt("channel_id")
	newChannel, newKeyIdx, selErr := dbmodel.CacheGetRandomSatisfiedChannel(group, modelName, 0, "", []int{excludedID})
	if selErr != nil || newChannel == nil {
		logger.Errorf(ctx, "[ResponsesAffinity] no alternative channel for retry: %v", selErr)
		return openaiErr
	}
	if newKeyIdx >= 0 {
		c.Set("cached_key_index", newKeyIdx)
	}
	// 复用 middleware 的 setup 逻辑（需要 import middleware 或抽公共函数）
	// 简化：手动 set channel_id/key_index/base_url/Authorization header
	//   这里复制 SetupContextForSelectedChannel 的核心逻辑
	c.Set("channel", newChannel.Type)
	c.Set("channel_id", newChannel.Id)
	c.Set("channel_name", newChannel.Name)
	c.Set("base_url", newChannel.GetBaseURL())
	actualKey, ki, kerr := newChannel.GetNextAvailableKey()
	if kerr == nil {
		c.Set("actual_key", actualKey)
		c.Set("key_index", ki)
		c.Request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", actualKey))
	}

	// 用 stripped body 重建请求
	originRequestBody = strippedBody
	c.Request.Body = io.NopCloser(bytes.NewBuffer(originRequestBody))

	// 重新计算 meta 的 channel 信息
	meta = util.GetRelayMeta(c)
	meta.ActualModelName = meta.OriginModelName
	meta.PromptTokens = prePromptTokens
	adaptor = helper.GetAdaptor(meta.APIType)
	if adaptor == nil {
		return openai.ErrorWrapper(fmt.Errorf("invalid api type: %d", meta.APIType), "invalid_api_type", http.StatusBadRequest)
	}
	adaptor.Init(meta)

	resp2, err2 := adaptor.DoRequest(c, meta, bytes.NewBuffer(originRequestBody))
	if err2 != nil {
		return openai.ErrorWrapper(err2, "failed_to_send_request", http.StatusBadGateway)
	}
	if meta.IsStream {
		usageMetadata, openaiErr = doNativeOpenaiResponseStream(c, resp2, meta)
	} else {
		usageMetadata, openaiErr = doNativeOpenaiResponse(c, resp2, meta)
	}
	if openaiErr != nil {
		return openaiErr
	}
	// 重试成功：继续走 below 的计费/日志
	channelId = newChannel.Id
}

if openaiErr != nil {
	return openaiErr
}
```

**Step 3: 确认 `CacheGetRandomSatisfiedChannel` 签名接受 excludedIds**

看 `model/cache.go:389`：

```go
func CacheGetRandomSatisfiedChannel(group string, model string, skipPriorityLevels int, responseID string, excludeChannelIds ...[]int) (*Channel, int, error) {
```

正确，变长参数。调用时传 `[]int{excludedID}` OK。

**Step 4: 编译验证**

```bash
go build ./...
go vet ./...
```
Expected: 无报错。若报 `io`/`fmt`/`helper` 未引入，补 import。

**Step 5: Commit**

```bash
git add relay/controller/opeai_response.go
git commit -m "feat(responses-affinity): strip encrypted_content and retry unpinned on invalid_encrypted_content"
```

---

## Task 6: 端到端手动验证

**Step 1: 全量编译 + vet + 测试**

```bash
go build ./... && go vet ./... && go test ./common/ -run TestExtract -v
```

**Step 2: 部署到测试环境（至少 2 个 gpt-5/o3 渠道）**

```bash
# 启动服务
go run . --port 3000
```

**Step 3: 发第一轮请求（带 reasoning）**

```bash
curl -X POST http://localhost:3000/v1/responses \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-5","input":"分析一下斐波那契数列","reasoning":{"effort":"medium"}}'
```

观察日志中：
- 选渠（非粘性）
- 响应成功
- `[OpenAI Response EncContent Cache] Cached enc_content_hash=...`

**Step 4: 检查 Redis**

```bash
redis-cli keys "cache_enc_content_*"
# 至少有一条
redis-cli keys "cache_response_id_resp_*"
# 至少有一条
```

**Step 5: 发续轮（只传 previous_response_id，应 pin）**

```bash
# 用第一轮响应的 resp_xxx 作为 previous_response_id
curl -X POST http://localhost:3000/v1/responses \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-5","previous_response_id":"resp_xxx","input":"继续"}'
```

日志中应见：
```
[ResponsesAffinity] pinned by previous_response_id=resp_xxx
```

**Step 6: 发续轮（无 previous_response_id，直接把上轮 reasoning 带进 input，应 pin）**

```bash
# 需要先从上一轮响应里提取 output.reasoning.encrypted_content，填进 input
curl -X POST http://localhost:3000/v1/responses \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "model":"gpt-5",
    "input":[
      {"type":"reasoning","id":"rs_xxx","encrypted_content":"<上一轮 output 里的 encrypted_content>"},
      {"type":"message","role":"user","content":"继续"}
    ]
  }'
```

日志应见：
```
[ResponsesAffinity] pinned by enc_content hash=<前8位>... channel=N
```

**Step 7: 模拟 pinned 渠道挂掉（手工 disable 渠道或伪造错误）**

如果有 mock 基础设施，验证 strip-and-retry 触发日志：
```
[ResponsesAffinity] pinned channel N failed with invalid_encrypted_content, stripping and retrying unpinned
```

若无 mock 条件，跳过此步，生产环境再观察。

---

## Task 7: 文档更新

**Files:**
- Modify: `docs/plans/2026-04-16-channel-affinity.md` (在末尾加一段)

**Step 1: 在 channel-affinity plan 末尾追加说明**

```markdown
---

## 2026-04-21 更新：OpenAI Responses API encrypted_content 亲和

上游 OpenAI `/v1/responses` API 在续轮请求中会传 `reasoning.encrypted_content`，该 blob 绑定到特定上游账号。
当前 channel_affinity 机制基于客户端传 X-Response-ID header 或 body 字段（gjson 规则），但 OpenAI 官方 SDK **不会**传 X-Response-ID。

因此额外实现了两条硬编码亲和路径（见 `docs/plans/2026-04-21-openai-responses-encrypted-affinity.md`）：
1. 从 body 自动提取 `previous_response_id` 视作 X-Response-ID
2. encrypted_content 哈希 → channel 独立 Redis namespace `cache_enc_content_{hash}`

失败回退：pinned 渠道返回 `invalid_encrypted_content` → strip 掉请求中所有 encrypted_content → 不 pin 重试一次。
```

**Step 2: Commit**

```bash
git add docs/plans/2026-04-16-channel-affinity.md
git commit -m "docs(affinity): document openai responses encrypted_content path addition"
```

---

## Known Limitations（未来改进）

1. **SSE 中 response.failed 事件未捕获**：如果上游返回 200 OK + 首个 SSE event 是 `response.failed{code:invalid_encrypted_content}`，当前实现无法触发 strip-and-retry（headers 可能已 flush）。需要 chunk sniffer 才能处理，未来可补。
2. **auth 粒度 vs channel+key_index 粒度**：如果一个渠道下多个 key 对应不同 OpenAI 组织，encrypted_content 会绑到具体 `key_index`，目前满足需求。如后续要支持"同 key 背后池化多账号"，需要引入 auth_id 抽象层。
3. **strip-and-retry 仅 1 次**：和 PR #1796 一致，strip 后仍失败则返回错误。业务逻辑依赖场景中若 output 了 reasoning 但 input 被剥离，上游可能再次报 400，属可接受。

---

## 回退方案

如生产出现问题，按顺序回退以下 commit（逆序）：

```bash
git log --oneline | grep responses-affinity
# 从最新到最老一个个 revert
```

Redis 残留的 `cache_enc_content_*` key 有 24h TTL，会自动过期，无需清理。
