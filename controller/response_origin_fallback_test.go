package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/util"
	"github.com/songquanpeng/one-api/service"
)

func TestResponseOriginFallbackAfterExpiry(t *testing.T) {
	for _, extra := range []string{
		`{"type":"additional_tools","id":"at_1","role":"developer","tools":[]},`,
		`{"type":"agent_message","id":"amsg_1","author":"worker","recipient":"root","content":"result"},`,
	} {
		t.Run(extra, func(t *testing.T) {
			body := strings.Replace(originReplayBody, `"input":[`, `"input":[`+extra, 1)
			calls := 0
			router, server := setupOriginMissRouter(t, func(c *gin.Context) {
				calls++
				got, err := common.GetRequestBody(c)
				if err != nil || string(got) != body {
					t.Fatalf("expired replay changed: %s %v", got, err)
				}
				if calls > 1 {
					other := dbmodel.AppendResponseCorrelationOther(newRetryContext(c), "")
					if !strings.Contains(other, `"origin_lookup_status":"not_found"`) || strings.Contains(other, `"origin_channel_id"`) {
						t.Fatalf("expired source provenance fabricated: %s", other)
					}
				}
				id := fmt.Sprintf("resp_new_%d", calls)
				if err := dbmodel.RegisterResponseOrigin(dbmodel.ResponseOrigin{ResponseID: id, ChannelID: 1, ChannelType: common.ChannelTypeAzure, KeyIndex: -1, Region: "eastus"}); err != nil {
					t.Fatal(err)
				}
				c.JSON(http.StatusOK, gin.H{"id": id})
			})
			if err := dbmodel.CacheResponseOrigin("rs_old", dbmodel.ResponseOrigin{ResponseID: "resp_old", ChannelID: 1, ChannelType: common.ChannelTypeAzure, KeyIndex: -1, Region: "eastus"}); err != nil {
				t.Fatal(err)
			}
			raw, _ := server.Get("cache_response_id_rs_old")
			ttl := server.TTL("cache_response_id_rs_old")
			w := serveOriginRequest(router, "/v1/responses", body, "")
			if w.Code != http.StatusOK {
				t.Fatalf("known origin: %d %s", w.Code, w.Body.String())
			}
			if got, _ := server.Get("cache_response_id_rs_old"); got != raw || server.TTL("cache_response_id_rs_old") != ttl {
				t.Fatal("successful continuation changed the parent mapping or TTL")
			}
			server.FastForward(ttl + time.Second)
			for i := 0; i < 3; i++ {
				w = serveOriginRequest(router, "/v1/responses", body, "")
				if w.Code != http.StatusOK {
					t.Fatalf("expired attempt %d: %d %s", i, w.Code, w.Body.String())
				}
			}
			if calls != 4 || server.Exists("cache_response_id_rs_old") || !server.Exists("cache_response_id_resp_new_4") {
				t.Fatalf("fallback must register new responses only: calls=%d keys=%v", calls, server.Keys())
			}
		})
	}
}

func TestResponseOriginFallbackRetryDoesNotRediscoverParent(t *testing.T) {
	for _, failure := range []string{"missing", "read error"} {
		t.Run(failure, func(t *testing.T) {
			var server *miniredis.Miniredis
			const restored = `{"channel_id":3,"channel_type":3,"key_index":-1,"region":"westus","response_id":"resp_old"}`
			router, r := setupOriginMissRouter(t, func(c *gin.Context) {
				if c.GetInt("channel_id") != 2 {
					t.Fatalf("fallback selected channel %d, want 2", c.GetInt("channel_id"))
				}
				// The store recovers after the request has already selected channel 2.
				server.SetError("")
				server.Set("cache_response_id_resp_old", restored)
				server.SetTTL("cache_response_id_resp_old", time.Hour)
				ctx := newRetryContext(c)
				for _, existing := range []string{`{"usageDetails":{"input_tokens":2}}`, `adminInfo:[2];usageDetails:{"input_tokens":2}`} {
					other := dbmodel.AppendResponseCorrelationOther(ctx, existing)
					status := "not_found"
					if failure == "read error" {
						status = "unavailable"
					}
					if !strings.Contains(other, `"previous_response_id":"resp_old"`) || !strings.Contains(other, `"origin_lookup_status":"`+status+`"`) || strings.Contains(other, `"origin_channel_id"`) || !strings.Contains(other, `"input_tokens":2`) {
						t.Fatalf("retry rediscovered or fabricated old provenance: %s", other)
					}
				}
				failed := []int{2}
				ch, err := selectRetryChannel(ctx, "default", "test-model", &failed)
				if err != nil || ch == nil || ch.Id != 1 {
					t.Fatalf("retry changed from the selected eastus region: channel=%+v err=%v", ch, err)
				}
				if c.GetHeader("X-Response-ID") != "resp_old" {
					t.Fatal("fallback erased the client header")
				}
				c.Status(http.StatusOK)
			})
			server = r
			seedRetryChannel(t, 2, common.ChannelTypeAzure, `{"region":"eastus"}`, 100)
			seedRetryChannel(t, 3, common.ChannelTypeAzure, `{"region":"westus"}`, 10)
			if failure == "read error" {
				server.Set("cache_response_id_resp_old", restored)
				server.SetError("LOADING origin store unavailable")
			}
			w := serveOriginRequest(router, "/v1/responses", originReplayBody, "resp_old")
			if w.Code != http.StatusOK {
				t.Fatalf("fallback rejected: %d %s", w.Code, w.Body.String())
			}
			if got, _ := server.Get("cache_response_id_resp_old"); got != restored || server.TTL("cache_response_id_resp_old") != time.Hour {
				t.Fatal("store recovery changed historical mapping")
			}
		})
	}
}

type responseOriginLookupHook struct{ gets int }

func (h *responseOriginLookupHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	if cmd.Name() == "get" && len(cmd.Args()) > 1 && strings.HasPrefix(fmt.Sprint(cmd.Args()[1]), "cache_response_id_") {
		h.gets++
		if h.gets > 1 {
			return ctx, errors.New("origin store became unavailable after the first lookup")
		}
	}
	return ctx, nil
}
func (*responseOriginLookupHook) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (*responseOriginLookupHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}
func (*responseOriginLookupHook) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func TestResponseOriginKnownLookupSurvivesStoreFailure(t *testing.T) {
	router, server := setupOriginMissRouter(t, func(c *gin.Context) {
		if c.GetInt("channel_id") != 1 {
			t.Fatalf("known source changed: %d", c.GetInt("channel_id"))
		}
		other := dbmodel.AppendResponseCorrelationOther(newRetryContext(c), "")
		if !strings.Contains(other, `"origin_channel_id":1`) || strings.Contains(other, "origin_lookup_status") {
			t.Fatalf("known provenance lost: %s", other)
		}
		c.Status(http.StatusOK)
	})
	server.Set("cache_response_id_rs_old", `{"channel_id":1,"channel_type":3,"key_index":-1,"region":"eastus","response_id":"resp_old"}`)
	seedRetryChannel(t, 2, common.ChannelTypeAzure, `{"region":"westus"}`, 100)
	hook := &responseOriginLookupHook{}
	common.RDB.AddHook(hook)
	w := serveOriginRequest(router, "/v1/responses", originReplayBody, "")
	if w.Code != http.StatusOK || hook.gets != 1 {
		t.Fatalf("repeated lookup discarded known origin: status=%d reads=%d body=%s", w.Code, hook.gets, w.Body.String())
	}
}

func TestResponseOriginFallbackPreservesAdmissionChecks(t *testing.T) {
	for _, state := range []string{"different group", "different model", "disabled ability", "header conflict", "chat missing", "chat backend error"} {
		t.Run(state, func(t *testing.T) {
			router, server := setupOriginMissRouter(t, func(c *gin.Context) { t.Fatal("ineligible request reached relay") })
			body, path, header, want := originReplayBody, "/v1/responses", "", http.StatusServiceUnavailable
			switch state {
			case "different group":
				if err := dbmodel.DB.Model(&dbmodel.Ability{}).Where("channel_id = ?", 1).Update("group", "other").Error; err != nil {
					t.Fatal(err)
				}
			case "different model":
				body = strings.ReplaceAll(body, "test-model", "unavailable-model")
			case "disabled ability":
				if err := dbmodel.DB.Model(&dbmodel.Ability{}).Where("channel_id = ?", 1).Update("enabled", false).Error; err != nil {
					t.Fatal(err)
				}
			case "header conflict":
				body, header, want = strings.Replace(body, `"store":false`, `"previous_response_id":"resp_A"`, 1), "resp_B", http.StatusBadRequest
			case "chat missing", "chat backend error":
				path, body, header = "/v1/chat/completions", `{"model":"test-model","messages":[]}`, "resp_old"
				want = http.StatusConflict
				if state == "chat backend error" {
					server.SetError("LOADING")
					want = http.StatusServiceUnavailable
				}
			}
			w := serveOriginRequest(router, path, body, header)
			if w.Code != want {
				t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body.String())
			}
		})
	}
}

func TestResponseOriginFallbackIgnoresRuleAffinity(t *testing.T) {
	for _, state := range []string{"missing", "read error"} {
		for _, replacement := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/replacement=%t", state, replacement), func(t *testing.T) {
				router, server := setupOriginMissRouter(t, func(c *gin.Context) {
					if !replacement || c.GetInt("channel_id") != 2 {
						t.Fatalf("rule affinity bypassed model disable: channel=%d", c.GetInt("channel_id"))
					}
					if service.ShouldSkipRetryAfterChannelAffinityFailure(c) {
						t.Fatal("discarded affinity suppressed fallback retry")
					}
					c.Status(http.StatusOK)
				})
				oldAffinity := common.ChannelAffinityConfig
				common.ChannelAffinityConfig = common.ChannelAffinitySetting{
					Enabled: true,
					Rules: []common.ChannelAffinityRule{{
						ModelRegex:         []string{"^test-model$"},
						KeySources:         []common.ChannelAffinityKeySource{{Type: "context_int", Key: "id"}},
						SkipRetryOnFailure: true,
					}},
				}
				t.Cleanup(func() { common.ChannelAffinityConfig = oldAffinity })
				server.Set("channel_affinity:v1:1", "1:0")
				if err := dbmodel.DB.Model(&dbmodel.Ability{}).Where("channel_id = ?", 1).
					Updates(map[string]any{"enabled": false, "auto_disabled": true}).Error; err != nil {
					t.Fatal(err)
				}
				want := http.StatusServiceUnavailable
				if replacement {
					seedRetryChannel(t, 2, common.ChannelTypeAzure, `{"region":"eastus"}`, 100)
					want = http.StatusOK
				}
				if state == "read error" {
					// Only origin reads fail; the affinity cache remains readable.
					common.RDB.AddHook(&responseOriginLookupHook{gets: 1})
				}
				body := strings.Replace(originReplayBody, `"store":false`, `"previous_response_id":"resp_old"`, 1)
				w := serveOriginRequest(router, "/v1/responses", body, "")
				if w.Code != want {
					t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body.String())
				}
			})
		}
	}
}

func TestResponseOriginFallbackPreservesUpstreamError(t *testing.T) {
	for _, code := range []string{"previous_response_not_found", "invalid_encrypted_content"} {
		t.Run(code, func(t *testing.T) {
			body := strings.Replace(originReplayBody, `"store":false`, `"store":false,"previous_response_id":"resp_old"`, 1)
			// Real Redis sockets live outside the fake-time bubble; synctest only
			// waits for the relay's asynchronous settlement/error work.
			router, server := setupOriginMissRouter(t, func(c *gin.Context) {
				c.Set("token_id", 1)
				c.Set("token_name", "test")
				RelayResponse(c)
			})
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				if err := dbmodel.DB.AutoMigrate(&dbmodel.Token{}, &dbmodel.Log{}); err != nil {
					t.Fatal(err)
				}
				if err := dbmodel.DB.Model(&dbmodel.User{}).Where("id = ?", 1).Update("quota", 1000000).Error; err != nil {
					t.Fatal(err)
				}
				if err := dbmodel.DB.Create(&dbmodel.Token{Id: 1, UserId: 1, Key: "test", RemainQuota: 1000000}).Error; err != nil {
					t.Fatal(err)
				}
				if err := dbmodel.DB.Model(&dbmodel.Channel{}).Where("id = ?", 1).Updates(map[string]any{"base_url": "http://origin-test.invalid", "key": "test"}).Error; err != nil {
					t.Fatal(err)
				}
				oldClient, oldLog := util.HTTPClient, dbmodel.LOG_DB
				oldRetries, oldApproximate := config.RetryTimes, config.ApproximateTokenEnabled
				config.RetryTimes, config.ApproximateTokenEnabled, dbmodel.LOG_DB = 2, true, dbmodel.DB
				t.Cleanup(func() {
					util.HTTPClient, dbmodel.LOG_DB = oldClient, oldLog
					config.RetryTimes, config.ApproximateTokenEnabled = oldRetries, oldApproximate
				})
				util.HTTPClient = &http.Client{Transport: responseStreamTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					got, err := io.ReadAll(r.Body)
					if err != nil || string(got) != body {
						t.Fatalf("forwarded history changed: %s %v", got, err)
					}
					payload := fmt.Sprintf(`{"error":{"type":"invalid_request_error","code":%q,"message":"Historical context could not be verified"}}`, code)
					return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(payload))}, nil
				})}
				w := serveOriginRequest(router, "/v1/responses", body, "")
				synctest.Wait()
				if calls != 1 || w.Code != 400 || !strings.Contains(w.Body.String(), "Historical context could not be verified") {
					t.Fatalf("upstream error lost or history silently retried: calls=%d status=%d body=%s", calls, w.Code, w.Body.String())
				}
				var logs []dbmodel.Log
				if err := dbmodel.LOG_DB.Find(&logs).Error; err != nil || len(logs) != 1 {
					t.Fatalf("final logs=%+v err=%v", logs, err)
				}
				if logs[0].Type != dbmodel.LogTypeError || logs[0].Quota != 0 || !strings.Contains(logs[0].Other, `"origin_lookup_status":"not_found"`) || strings.Contains(logs[0].Other, `"origin_channel_id"`) {
					t.Fatalf("failure or provenance was misreported: %+v", logs[0])
				}
				if server.Exists("cache_response_id_resp_old") {
					t.Fatal("failed continuation created a historical mapping")
				}
			})
		})
	}
}

func TestResponseOriginFallbackRelayRetriesWithoutRebinding(t *testing.T) {
	for _, state := range []string{"missing", "read error"} {
		t.Run(state, func(t *testing.T) {
			body := strings.Replace(originReplayBody, `"input":[`, `"previous_response_id":"resp_old","input":[{"type":"additional_tools","role":"developer","tools":[]},{"type":"agent_message","author":"worker","recipient":"root","content":"result"},`, 1)
			router, server := setupOriginMissRouter(t, func(c *gin.Context) {
				c.Set("token_id", 1)
				c.Set("token_name", "test")
				RelayResponse(c)
			})
			seedRetryChannel(t, 2, common.ChannelTypeOpenAI, "", 100)
			if err := dbmodel.DB.Model(&dbmodel.Channel{}).Where("id IN ?", []int{1, 2}).Updates(map[string]any{"type": common.ChannelTypeOpenAI, "config": "", "base_url": "http://origin-test.invalid", "key": "test"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := dbmodel.DB.AutoMigrate(&dbmodel.Token{}, &dbmodel.Log{}); err != nil {
				t.Fatal(err)
			}
			if err := dbmodel.DB.Model(&dbmodel.User{}).Where("id = ?", 1).Update("quota", 1000000).Error; err != nil {
				t.Fatal(err)
			}
			if err := dbmodel.DB.Create(&dbmodel.Token{Id: 1, UserId: 1, Key: "test", RemainQuota: 1000000}).Error; err != nil {
				t.Fatal(err)
			}
			const oldOrigin = `{"channel_id":3,"channel_type":3,"key_index":0,"region":"westus","response_id":"resp_old"}`
			wantReason := "not_found"
			if state == "read error" {
				wantReason = "unavailable"
				server.Set("cache_response_id_resp_old", oldOrigin)
				server.SetTTL("cache_response_id_resp_old", time.Hour)
				common.RDB.AddHook(&responseOriginLookupHook{gets: 1})
			}
			synctest.Test(t, func(t *testing.T) {
				oldClient, oldLogDB := util.HTTPClient, dbmodel.LOG_DB
				oldRetries, oldApproximate, oldLogEnabled := config.RetryTimes, config.ApproximateTokenEnabled, config.LogConsumeEnabled
				config.RetryTimes, config.ApproximateTokenEnabled, config.LogConsumeEnabled, dbmodel.LOG_DB = 2, true, true, dbmodel.DB
				t.Cleanup(func() {
					util.HTTPClient, dbmodel.LOG_DB = oldClient, oldLogDB
					config.RetryTimes, config.ApproximateTokenEnabled, config.LogConsumeEnabled = oldRetries, oldApproximate, oldLogEnabled
				})
				calls := 0
				util.HTTPClient = &http.Client{Transport: responseStreamTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					got, err := io.ReadAll(r.Body)
					if err != nil || string(got) != body {
						t.Fatalf("retry changed history: %s %v", got, err)
					}
					status, payload := 429, `{"error":{"code":"rate_limit_exceeded","message":"Try another channel"}}`
					if calls > 1 {
						status, payload = 200, `{"id":"resp_new","status":"completed","output":[{"id":"rs_new","type":"reasoning","summary":[]}],"usage":{"input_tokens":10,"output_tokens":1,"total_tokens":11}}`
					}
					return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(payload))}, nil
				})}
				w := serveOriginRequest(router, "/v1/responses", body, "resp_old")
				synctest.Wait()
				if w.Code != 200 || calls != 2 || !strings.Contains(w.Body.String(), `"id":"resp_new"`) {
					t.Fatalf("fallback retry failed: status=%d calls=%d body=%s", w.Code, calls, w.Body.String())
				}
				var logs []dbmodel.Log
				if err := dbmodel.LOG_DB.Find(&logs).Error; err != nil || len(logs) != 1 {
					t.Fatalf("final logs=%+v err=%v", logs, err)
				}
				if logs[0].Type != dbmodel.LogTypeConsume || logs[0].ChannelId != 1 || logs[0].XResponseID != "resp_new" || !strings.Contains(logs[0].Other, `"origin_lookup_status":"`+wantReason+`"`) || !strings.Contains(logs[0].Other, `"previous_response_id":"resp_old"`) || strings.Contains(logs[0].Other, `"origin_channel_id"`) {
					t.Fatalf("retry consumption misreported source: %+v", logs[0])
				}
				for _, id := range []string{"resp_new", "rs_new"} {
					raw, err := server.Get("cache_response_id_" + id)
					origin := dbmodel.ParseResponseIdCacheValue(raw)
					if err != nil || origin.ChannelID != 1 || origin.ResponseID != "resp_new" {
						t.Fatalf("new response missing actual successful source: id=%s origin=%+v err=%v", id, origin, err)
					}
				}
				if state == "read error" {
					if raw, _ := server.Get("cache_response_id_resp_old"); raw != oldOrigin || server.TTL("cache_response_id_resp_old") != time.Hour {
						t.Fatalf("retry changed or deleted old source: %s", raw)
					}
				} else if server.Exists("cache_response_id_resp_old") {
					t.Fatal("retry rebound an expired parent")
				}
			})
		})
	}
}
