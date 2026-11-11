package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/tidwall/gjson"
)

const (
	geminiSignatureTTL          = 24 * time.Hour
	geminiRequestSignaturesKey  = "gemini_request_signature_keys"
	geminiSignatureMaxWriteKeys = 256
)

// No queue: a slow cache must not retain unbounded responses or goroutines.
var geminiSignatureWriteSlots = make(chan struct{}, 16)

type geminiResourceTypeKey struct{}

// IsGeminiModel does not classify other models hosted on Vertex as Gemini.
func IsGeminiModel(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return name == "gemini" || strings.HasPrefix(name, "gemini-")
}

// WithGeminiResourceType freezes the first type; subsequent attempts cannot replace it.
func WithGeminiResourceType(ctx context.Context, channelType int) context.Context {
	if _, exists := GeminiResourceType(ctx); exists {
		return ctx
	}
	return context.WithValue(ctx, geminiResourceTypeKey{}, channelType)
}

func GeminiResourceType(ctx context.Context) (int, bool) {
	channelType, ok := ctx.Value(geminiResourceTypeKey{}).(int)
	return channelType, ok
}

type GeminiAffinityError struct {
	StatusCode int
	Message    string
}

func (e *GeminiAffinityError) Error() string { return e.Message }

func geminiSignatureKey(userID int, signature string) string {
	data := []byte(signature)
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if decoded, err := encoding.DecodeString(signature); err == nil {
			data = decoded
			break
		}
	}
	// Every signature in a request belongs to the same user. A shared hash tag
	// keeps the whole Lua batch in one Redis Cluster slot while isolating users.
	return fmt.Sprintf("gemini_signature_resource:v2:{%d}:%x", userID, sha256.Sum256(data))
}

// Only inspect protocol signature fields, never tool arguments, schemas or text.
// Hashing here also deduplicates equivalent padded/unpadded Base64 signatures.
func geminiSignatureKeys(userID int, body []byte, response bool) []string {
	root := gjson.ParseBytes(body)
	seen := make(map[string]bool)
	var keys []string
	add := func(value gjson.Result) {
		for _, field := range []string{"thoughtSignature", "thought_signature"} {
			signature := value.Get(field)
			if signature.Type != gjson.String || signature.Str == "" {
				continue
			}
			key := geminiSignatureKey(userID, signature.Str)
			if !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}
	each := func(array gjson.Result, visit func(gjson.Result)) {
		if array.IsArray() {
			array.ForEach(func(_, value gjson.Result) bool {
				visit(value)
				return true
			})
		}
	}
	message := func(value gjson.Result) {
		add(value)
		add(value.Get("extra_content.google"))
		each(value.Get("parts"), add)
		each(value.Get("content"), add)
		each(value.Get("tool_calls"), func(tool gjson.Result) {
			add(tool.Get("extra_content.google"))
		})
	}
	if response {
		each(root.Get("candidates"), func(candidate gjson.Result) { message(candidate.Get("content")) })
		each(root.Get("choices"), func(choice gjson.Result) {
			message(choice.Get("message"))
			message(choice.Get("delta"))
		})
		each(root.Get("output"), message)
		each(root.Get("response.output"), message)
		message(root.Get("item"))
	} else {
		for _, field := range []string{"contents", "messages", "input"} {
			each(root.Get(field), message)
		}
	}
	return keys
}

func geminiRequestSignatureKeys(c *gin.Context) ([]string, error) {
	if cached, ok := c.Get(geminiRequestSignaturesKey); ok {
		keys, _ := cached.([]string)
		return keys, nil
	}
	body, err := common.GetRequestBody(c)
	if err != nil {
		return nil, err
	}
	keys := geminiSignatureKeys(c.GetInt("id"), body, false)
	c.Set(geminiRequestSignaturesKey, keys)
	return keys, nil
}

// GeminiRequestHasSignatures also checks Response-ID hits, which skip the Redis
// source lookup but must only constrain retries when actual signatures are present.
func GeminiRequestHasSignatures(c *gin.Context, modelName string) bool {
	if !IsGeminiModel(modelName) {
		return false
	}
	keys, err := geminiRequestSignatureKeys(c)
	return err == nil && len(keys) > 0
}

// PrepareGeminiResourceAffinity restores the source of submitted signatures when
// no usable Response-ID affinity channel was found. Unknown/conflicting sources
// and cache failures leave the original selection policy in charge; the selected
// type still constrains retries.
func PrepareGeminiResourceAffinity(c *gin.Context, modelName string) *GeminiAffinityError {
	if !IsGeminiModel(modelName) {
		return nil
	}
	if _, specific := c.Get("specific_channel_id"); specific {
		return nil // Explicit administrator selection keeps its original precedence.
	}
	keys, err := geminiRequestSignatureKeys(c)
	if err != nil {
		return &GeminiAffinityError{http.StatusBadRequest, "Unable to read Gemini request body"}
	}
	if len(keys) == 0 {
		return nil
	}
	if !common.RedisEnabled || common.RDB == nil {
		logger.Warnf(c.Request.Context(), "[Gemini Resource] using original channel selection: Redis unavailable user=%d model=%s", c.GetInt("id"), modelName)
		return nil
	}
	// Read and renew together so a concurrent expiry cannot silently lose a source.
	const script = `
local channel_type = nil
for _, key in ipairs(KEYS) do
    local value = tonumber(redis.call('GET', key))
    if not value then return 0 end
    if value <= 0 then return -1 end
    if channel_type and value ~= channel_type then return -1 end
    channel_type = value
end
for _, key in ipairs(KEYS) do redis.call('EXPIRE', key, ARGV[1]) end
return channel_type`
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	channelType, err := common.RDB.Eval(ctx, script, keys, int(geminiSignatureTTL/time.Second)).Int()
	if err != nil {
		logger.Warnf(c.Request.Context(), "[Gemini Resource] signature lookup failed, using original channel selection user=%d model=%s: %v", c.GetInt("id"), modelName, err)
		return nil
	}
	if channelType <= 0 {
		reason := "unknown_or_expired"
		if channelType < 0 {
			reason = "conflicting_sources"
		}
		logger.Infof(c.Request.Context(), "[Gemini Resource] using original channel selection user=%d model=%s reason=%s signatures=%d", c.GetInt("id"), modelName, reason, len(keys))
		return nil
	}
	c.Request = c.Request.WithContext(WithGeminiResourceType(c.Request.Context(), channelType))
	logger.Infof(c.Request.Context(), "[Gemini Resource] restored user=%d model=%s type=%d signatures=%d", c.GetInt("id"), modelName, channelType, len(keys))
	return nil
}

// RecordGeminiResponseSignatures is called after writing a response or SSE chunk.
// It snapshots only IDs and signature hashes, then starts a bounded background
// write. No Redis I/O runs on the response path, and no Gin context or body is
// retained. An immediate follow-up may observe a cache miss and use normal routing.
func RecordGeminiResponseSignatures(c *gin.Context, body []byte) {
	if !IsGeminiModel(c.GetString("original_model")) && !IsGeminiModel(c.GetString("model")) {
		return
	}
	userID, channelType := c.GetInt("id"), c.GetInt("channel")
	if userID <= 0 || channelType <= 0 {
		return // Administrative probes have no authenticated conversation to bind.
	}
	keys := geminiSignatureKeys(userID, body, true)
	if len(keys) == 0 {
		return
	}
	if !common.RedisEnabled || common.RDB == nil {
		logger.Warnf(c.Request.Context(), "[Gemini Resource] cannot record signatures: Redis unavailable user=%d channel=%d type=%d", userID, c.GetInt("channel_id"), channelType)
		return
	}
	job := geminiSignatureWrite{
		client: common.RDB, userID: userID, channelID: c.GetInt("channel_id"),
		channelType: channelType, keys: keys,
	}
	if id := c.Request.Context().Value(logger.RequestIdKey); id != nil {
		job.requestID = fmt.Sprint(id)
	}
	if len(keys) > geminiSignatureMaxWriteKeys {
		logger.Warnf(c.Request.Context(), "[Gemini Resource] signature write skipped: too many signatures user=%d channel=%d count=%d", userID, job.channelID, len(keys))
		return
	}
	select {
	case geminiSignatureWriteSlots <- struct{}{}:
		go func() {
			defer func() { <-geminiSignatureWriteSlots }()
			job.run()
		}()
	default:
		logger.Warnf(c.Request.Context(), "[Gemini Resource] signature write skipped: workers busy user=%d channel=%d", userID, job.channelID)
	}
}

type geminiSignatureWrite struct {
	client                         *redis.Client
	userID, channelID, channelType int
	requestID                      string
	keys                           []string
}

func (job geminiSignatureWrite) run() {
	// Request cancellation must not cancel a write for an already delivered response.
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), logger.RequestIdKey, job.requestID), 2*time.Second)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Errorf(ctx, "[Gemini Resource] signature write panic user=%d channel=%d: %v", job.userID, job.channelID, recovered)
		}
	}()
	// A collision across source types is marked as conflicting, never rebound. The whole
	// response batch is atomic, including concurrent responses from other nodes.
	const script = `
local conflicts = 0
for _, key in ipairs(KEYS) do
    local old = redis.call('GET', key)
    local value = ARGV[1]
    if old and old ~= value then
        value = '-1'
        conflicts = conflicts + 1
    end
    redis.call('SET', key, value, 'EX', ARGV[2])
end
return conflicts`
	conflicts, err := job.client.Eval(ctx, script, job.keys, job.channelType, int(geminiSignatureTTL/time.Second)).Int()
	if err != nil {
		logger.Warnf(ctx, "[Gemini Resource] signature write failed user=%d channel=%d type=%d: %v", job.userID, job.channelID, job.channelType, err)
	} else if conflicts > 0 {
		logger.Warnf(ctx, "[Gemini Resource] signature source conflict user=%d channel=%d type=%d count=%d", job.userID, job.channelID, job.channelType, conflicts)
	}
}
