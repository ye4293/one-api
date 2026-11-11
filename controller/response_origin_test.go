package controller

import (
	"context"
	"errors"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func auditRedis(t *testing.T) {
	t.Helper()
	oldRDB, oldMemory, oldAffinity := common.RDB, config.MemoryCacheEnabled, dbmodel.ResponsesAffinityEnabled
	r := miniredis.RunT(t)
	common.RDB = redis.NewClient(&redis.Options{Addr: r.Addr()})
	common.RedisEnabled = true
	config.MemoryCacheEnabled = false
	dbmodel.ResponsesAffinityEnabled = true
	t.Cleanup(func() {
		common.RDB.Close()
		common.RDB = oldRDB
		config.MemoryCacheEnabled = oldMemory
		dbmodel.ResponsesAffinityEnabled = oldAffinity
	})
}
func TestResponseOriginNonAzureRetryKeepsChannelType(t *testing.T) {
	setupRetryPolicyDB(t, false)
	seedRetryChannel(t, 1, common.ChannelTypeOpenAI, `{}`, 1)
	seedRetryChannel(t, 2, common.ChannelTypeAzure, `{"region":"eastus"}`, 100)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("channel", common.ChannelTypeOpenAI)
	failed := []int{1}
	ch, err := selectRetryChannel(newRetryContext(c), "default", "test-model", &failed)
	if ch != nil && ch.Type != common.ChannelTypeOpenAI {
		t.Fatalf("cross-type retry: OpenAI -> type=%d channel=%d err=%v", ch.Type, ch.Id, err)
	}
}
func TestResponseOriginParentRegionFallbackKeepsType(t *testing.T) {
	setupRetryPolicyDB(t, false)
	seedRetryChannel(t, 1, common.ChannelTypeAzure, `{"region":"eastus"}`, 1)
	seedRetryChannel(t, 2, common.ChannelTypeOpenAI, `{"region":"eastus"}`, 100)
	dbmodel.DB.Model(&dbmodel.Channel{}).Where("id = ?", 1).Update("status", common.ChannelStatusManuallyDisabled)
	auditRedis(t)
	if err := dbmodel.SetResponseIdRegionToRedis("resp_A", 1, 0, "eastus", 1440); err != nil {
		t.Fatal(err)
	}
	ch, _, err := dbmodel.CacheGetRandomSatisfiedChannel(context.Background(), "default", "test-model", 0, "resp_A")
	if ch != nil && ch.Type != common.ChannelTypeAzure {
		t.Fatalf("cross-type parent fallback: Azure -> type=%d channel=%d err=%v", ch.Type, ch.Id, err)
	}
}
func TestResponseOriginParentRegionFallbackStopsWhenRegionExhausted(t *testing.T) {
	setupRetryPolicyDB(t, false)
	seedRetryChannel(t, 1, common.ChannelTypeAzure, `{"region":"eastus"}`, 1)
	seedRetryChannel(t, 2, common.ChannelTypeAzure, `{"region":"westus"}`, 100)
	dbmodel.DB.Model(&dbmodel.Channel{}).Where("id = ?", 1).Update("status", common.ChannelStatusManuallyDisabled)
	auditRedis(t)
	if err := dbmodel.SetResponseIdRegionToRedis("resp_A", 1, 0, "eastus", 1440); err != nil {
		t.Fatal(err)
	}
	ch, _, err := dbmodel.CacheGetRandomSatisfiedChannel(context.Background(), "default", "test-model", 0, "resp_A")
	if ch != nil {
		cfg, _ := ch.LoadConfig()
		if cfg.Region != "eastus" {
			t.Fatalf("cross-region parent fallback: eastus -> %s channel=%d err=%v", cfg.Region, ch.Id, err)
		}
	}
}
func TestResponseOriginRetryUsesRecordedActualRegion(t *testing.T) {
	setupRetryPolicyDB(t, false)
	seedRetryChannel(t, 1, common.ChannelTypeAzure, `{"region":"westus"}`, 1)
	seedRetryChannel(t, 2, common.ChannelTypeAzure, `{"region":"eastus"}`, 10)
	seedRetryChannel(t, 3, common.ChannelTypeAzure, `{"region":"westus"}`, 100)
	auditRedis(t)
	if err := dbmodel.SetResponseIdRegionToRedis("resp_A", 1, 0, "eastus", 1440); err != nil {
		t.Fatal(err)
	}
	ch, _, err := dbmodel.CacheGetRandomSatisfiedChannel(context.Background(), "default", "test-model", 0, "resp_A")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := ch.LoadConfig()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Header.Set("X-Response-ID", "resp_A")
	c.Set("channel", ch.Type)
	c.Set("Config", cfg)
	failed := []int{1}
	retry, err := selectRetryChannel(newRetryContext(c), "default", "test-model", &failed)
	if err != nil {
		t.Fatal(err)
	}
	retryCfg, _ := retry.LoadConfig()
	if retryCfg.Region != "eastus" {
		t.Fatalf("retry ignored recorded parent region: actual=eastus config=%s selected=%s channel=%d", cfg.Region, retryCfg.Region, retry.Id)
	}
}
func TestResponseOriginParentMappingSurvivesSuccessfulChat(t *testing.T) {
	setupRetryPolicyDB(t, false)
	auditRedis(t)
	if err := dbmodel.SetResponseIdRegionToRedis("resp_A", 1, 0, "eastus", 1440); err != nil {
		t.Fatal(err)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Header.Set("X-Response-ID", "resp_A")
	c.Set("original_model", "gpt-5")
	c.Set("key_index", 0)
	c.Set("x_response_id", "resp_B")
	c.Set("channel", common.ChannelTypeAzure)
	recordRelayAffinity(c, 2)
	channel, _, region := dbmodel.GetResponseIdRegionFromRedis("resp_A")
	if channel != 1 || region != "eastus" {
		t.Fatalf("parent overwritten: %d:%s", channel, region)
	}
}
func TestResponseOriginSkipsDisabledAbility(t *testing.T) {
	setupRetryPolicyDB(t, false)
	auditRedis(t)
	seedRetryChannel(t, 1, common.ChannelTypeAzure, `{"region":"eastus"}`, 100)
	seedRetryChannel(t, 2, common.ChannelTypeAzure, `{"region":"eastus"}`, 10)
	dbmodel.DB.Model(&dbmodel.Ability{}).Where("channel_id = ?", 1).Update("enabled", false)
	if err := dbmodel.SetResponseIdRegionToRedis("resp_A", 1, 0, "eastus", 1440); err != nil {
		t.Fatal(err)
	}
	ch, _, err := dbmodel.CacheGetRandomSatisfiedChannel(context.Background(), "default", "test-model", 0, "resp_A")
	if err != nil || ch == nil || ch.Id != 2 {
		t.Fatalf("must skip disabled model ability: channel=%+v err=%v", ch, err)
	}
}
func TestResponseOriginPublishedStreamCannotRetry(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Write([]byte("data: {\"id\":\"chatcmpl-A\"}\n\n"))
	if shouldRetry(c, 502, "unexpected EOF") {
		t.Fatal("cannot retry an already published stream")
	}
}

type recoveredOriginWriter struct {
	gin.ResponseWriter
	recover func()
}

func (w *recoveredOriginWriter) Write(b []byte) (int, error) {
	w.recover()
	return w.ResponseWriter.Write(b)
}
func TestResponseOriginRecoveryUsesActualSnapshot(t *testing.T) {
	setupRetryPolicyDB(t, false)
	auditRedis(t)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	c.Set("channel_id", 7)
	c.Set("channel", common.ChannelTypeAzure)
	c.Set("key_index", 0)
	c.Set("original_model", "gpt-test")
	c.Set("Config", dbmodel.ChannelConfig{Region: "westus"})
	common.RDB.AddHook(&recoveringOriginHook{})
	c.Writer = &recoveredOriginWriter{ResponseWriter: c.Writer, recover: func() {}}
	body := `{"id":"chatcmpl-A","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{"X-Ms-Region": []string{"eastus"}}, Body: io.NopCloser(strings.NewReader(body))}
	if err, _ := openai.Handler(c, resp, 1, "gpt-test"); err != nil {
		t.Fatal(err)
	}
	recordRelayAffinity(c, 7)
	got, err := dbmodel.LookupResponseOrigin("chatcmpl-A")
	if err != nil || got.Region != "eastus" {
		t.Fatalf("wrong recovered region: %+v %v", got, err)
	}
}

type recoveringOriginHook struct{ failed bool }

func (h *recoveringOriginHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	if cmd.Name() == "set" && !h.failed {
		h.failed = true
		return ctx, errors.New("initial write failed")
	}
	return ctx, nil
}
func (h *recoveringOriginHook) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (h *recoveringOriginHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}
func (h *recoveringOriginHook) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }
