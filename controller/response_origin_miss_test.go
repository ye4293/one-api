package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/middleware"
	dbmodel "github.com/songquanpeng/one-api/model"
)

const originReplayBody = `{"model":"test-model","store":false,"input":[{"type":"reasoning","id":"rs_old","encrypted_content":"opaque","summary":[]},{"role":"assistant","content":"Earlier answer"},{"role":"user","content":"continue"}]}`

func setupOriginMissRouter(t *testing.T, handler gin.HandlerFunc) (*gin.Engine, *miniredis.Miniredis) {
	t.Helper()
	setupRetryPolicyDB(t, false)
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	oldClient, oldLog := common.RDB, dbmodel.LOG_DB
	oldMemory, oldAffinity := config.MemoryCacheEnabled, dbmodel.ResponsesAffinityEnabled
	common.RDB, common.RedisEnabled, dbmodel.LOG_DB = client, true, nil
	config.MemoryCacheEnabled, dbmodel.ResponsesAffinityEnabled = false, true
	t.Cleanup(func() {
		client.Close()
		common.RDB, dbmodel.LOG_DB = oldClient, oldLog
		config.MemoryCacheEnabled, dbmodel.ResponsesAffinityEnabled = oldMemory, oldAffinity
	})
	if err := dbmodel.DB.AutoMigrate(&dbmodel.User{}); err != nil {
		t.Fatal(err)
	}
	if err := dbmodel.DB.Create(&dbmodel.User{Id: 1, Username: "origin-test", Group: "default"}).Error; err != nil {
		t.Fatal(err)
	}
	seedRetryChannel(t, 1, common.ChannelTypeAzure, `{"region":"eastus"}`, 1)
	router := gin.New()
	for _, path := range []string{"/v1/responses", "/v1/responses/compact", "/v1/chat/completions"} {
		router.POST(path, func(c *gin.Context) { c.Set("id", 1); c.Next() }, middleware.Distribute(), handler)
	}
	return router, server
}

func serveOriginRequest(router *gin.Engine, path, body, header string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if header != "" {
		req.Header.Set("X-Response-ID", header)
	}
	router.ServeHTTP(w, req)
	return w
}

func TestResponseOriginMissReplay(t *testing.T) {
	for _, header := range []string{"", "resp_old"} {
		t.Run("header="+header, func(t *testing.T) {
			calls := 0
			router, server := setupOriginMissRouter(t, func(c *gin.Context) {
				calls++
				body, err := common.GetRequestBody(c)
				if err != nil || string(body) != originReplayBody || c.GetHeader("X-Response-ID") != header {
					t.Fatalf("replay changed: body=%s header=%q err=%v", body, c.GetHeader("X-Response-ID"), err)
				}
				parent := "rs_old"
				if header != "" {
					parent = header
				}
				if got := dbmodel.PreviousResponseIDFromContext(c.Request.Context()); got != parent {
					t.Fatalf("lost correlation: got=%q want=%q", got, parent)
				}
				if _, ok := dbmodel.ResponseOriginFromContext(c.Request.Context()); ok {
					t.Fatal("missing historical origin must not be fabricated")
				}
				if c.GetInt("channel_id") != 1 {
					t.Fatalf("relay received no selected channel: %d", c.GetInt("channel_id"))
				}
				ctx := newRetryContext(c)
				correlation := dbmodel.AppendResponseCorrelationOther(ctx, "")
				if !strings.Contains(correlation, `"previous_response_id":"`+parent+`"`) || strings.Contains(correlation, `"origin_channel_id"`) {
					t.Fatalf("retry selection fabricated historical log provenance: %s", correlation)
				}
				newID := fmt.Sprintf("resp_new_%d", calls)
				if err := dbmodel.RegisterResponseOrigin(dbmodel.ResponseOrigin{ResponseID: newID, ChannelID: 1, ChannelType: common.ChannelTypeAzure, KeyIndex: 0, Region: "eastus"}); err != nil {
					t.Fatal(err)
				}
				c.JSON(http.StatusOK, gin.H{"id": newID})
			})
			for i := 0; i < 3; i++ {
				w := serveOriginRequest(router, "/v1/responses", originReplayBody, header)
				if w.Code != http.StatusOK {
					t.Fatalf("attempt %d: status=%d body=%s", i+1, w.Code, w.Body.String())
				}
			}
			if calls != 3 || server.Exists("cache_response_id_rs_old") || server.Exists("cache_response_id_resp_old") || !server.Exists("cache_response_id_resp_new_3") {
				t.Fatalf("new responses must not rebind old IDs: calls=%d keys=%v", calls, server.Keys())
			}
		})
	}
}

func TestResponseOriginMissToolReplay(t *testing.T) {
	for _, tc := range []struct{ name, items string }{
		{"function", `{"type":"function_call","id":"fc_1","call_id":"call_1","name":"shell","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"done"}`},
		{"custom", `{"type":"custom_tool_call","id":"ctc_1","call_id":"call_1","name":"apply_patch","input":"patch text"},{"type":"custom_tool_call_output","call_id":"call_1","output":"done"}`},
		{"additional tools", `{"type":"additional_tools","id":"at_1","role":"developer","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`},
		{"agent message", `{"type":"agent_message","id":"amsg_1","author":"worker","recipient":"root","content":"Earlier result"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(originReplayBody, `"input":[`, `"input":[`+tc.items+`,`, 1)
			calls := 0
			router, _ := setupOriginMissRouter(t, func(c *gin.Context) {
				calls++
				got, err := common.GetRequestBody(c)
				if err != nil || string(got) != body {
					t.Fatalf("tool records changed: body=%s err=%v", got, err)
				}
				c.Status(http.StatusOK)
			})
			w := serveOriginRequest(router, "/v1/responses", body, "")
			if w.Code != http.StatusOK || calls != 1 {
				t.Fatalf("self-carried tool replay blocked: status=%d calls=%d body=%s", w.Code, calls, w.Body.String())
			}
		})
	}
}

func TestResponseOriginMissDelegatesHistoryValidation(t *testing.T) {
	for _, tc := range []struct{ name, path, body, header string }{
		{"previous response", "/v1/responses", strings.Replace(originReplayBody, `"store":false`, `"previous_response_id":"resp_old"`, 1), ""},
		{"stored conversation", "/v1/responses", strings.Replace(originReplayBody, `"store":false`, `"conversation":"conv_old"`, 1), ""},
		{"item reference", "/v1/responses", strings.Replace(originReplayBody, `"input":[`, `"input":[{"type":"item_reference","id":"msg_old"},`, 1), ""},
		{"id only", "/v1/responses", strings.Replace(originReplayBody, `"input":[`, `"input":[{"id":"msg_old"},`, 1), ""},
		{"compaction", "/v1/responses", strings.Replace(originReplayBody, `"type":"reasoning"`, `"type":"compaction"`, 1), ""},
		{"unknown encrypted item", "/v1/responses", strings.Replace(originReplayBody, `"type":"reasoning"`, `"type":"future_state"`, 1), ""},
		{"opaque only", "/v1/responses", `{"model":"test-model","input":[{"type":"reasoning","id":"rs_old","encrypted_content":"opaque"}]}`, ""},
		{"empty message", "/v1/responses", `{"model":"test-model","input":[{"type":"reasoning","id":"rs_old","encrypted_content":"opaque"},{"role":"user","content":[]}]}`, ""},
		{"reasoning reference", "/v1/responses", strings.Replace(originReplayBody, `"input":[`, `"input":[{"type":"reasoning","id":"rs_ref"},`, 1), ""},
		{"custom tool reference", "/v1/responses", strings.Replace(originReplayBody, `"input":[`, `"input":[{"type":"custom_tool_call","id":"ctc_ref","call_id":"call_1","name":"apply_patch"},`, 1), ""},
		{"compact endpoint", "/v1/responses/compact", originReplayBody, ""},
		{"header only", "/v1/responses", `{"model":"test-model","input":"continue"}`, "resp_old"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			const upstreamError = `{"error":{"code":"invalid_history","message":"Upstream rejected the supplied history"}}`
			router, _ := setupOriginMissRouter(t, func(c *gin.Context) {
				calls++
				body, err := common.GetRequestBody(c)
				if err != nil || string(body) != tc.body || c.GetHeader("X-Response-ID") != tc.header {
					t.Fatalf("history changed: body=%s header=%q err=%v", body, c.GetHeader("X-Response-ID"), err)
				}
				c.Data(http.StatusBadRequest, "application/json", []byte(upstreamError))
			})
			for i := 0; i < 3; i++ {
				w := serveOriginRequest(router, tc.path, tc.body, tc.header)
				if w.Code != http.StatusBadRequest || w.Body.String() != upstreamError {
					t.Fatalf("attempt=%d status=%d body=%s", i, w.Code, w.Body.String())
				}
			}
			if calls != 3 {
				t.Fatalf("only %d continuation attempts reached upstream validation", calls)
			}
		})
	}
}

func TestResponseOriginBackendFailure(t *testing.T) {
	for _, failure := range []string{"read error", "connection closed", "disabled", "disabled without client"} {
		t.Run(failure, func(t *testing.T) {
			calls := 0
			router, server := setupOriginMissRouter(t, func(c *gin.Context) {
				calls++
				body, err := common.GetRequestBody(c)
				if err != nil || string(body) != originReplayBody || c.GetInt("channel_id") != 1 {
					t.Fatalf("fallback changed body or selected no channel: channel=%d body=%s err=%v", c.GetInt("channel_id"), body, err)
				}
				other := dbmodel.AppendResponseCorrelationOther(newRetryContext(c), "")
				if !strings.Contains(other, `"origin_lookup_status":"unavailable"`) || !strings.Contains(other, `"previous_response_id":"rs_old"`) || strings.Contains(other, `"origin_channel_id"`) {
					t.Fatalf("fallback provenance lost or fabricated: %s", other)
				}
				c.Status(http.StatusOK)
			})
			const raw = `{"channel_id":1,"channel_type":3,"key_index":0,"region":"eastus","response_id":"resp_old"}`
			server.Set("cache_response_id_rs_old", raw)
			server.SetTTL("cache_response_id_rs_old", time.Hour)
			switch failure {
			case "read error":
				server.SetError("LOADING origin store unavailable")
			case "connection closed":
				common.RDB.Close()
			case "disabled":
				common.RedisEnabled = false
			case "disabled without client":
				common.RedisEnabled = false // user-group lookup also uses this client
				common.RDB = nil
			}
			for i := 0; i < 3; i++ {
				w := serveOriginRequest(router, "/v1/responses", originReplayBody, "")
				if w.Code != http.StatusOK {
					t.Fatalf("backend failure blocked continuation: status=%d body=%s", w.Code, w.Body.String())
				}
			}
			if got, _ := server.Get("cache_response_id_rs_old"); calls != 3 || got != raw || server.TTL("cache_response_id_rs_old") != time.Hour {
				t.Fatalf("backend error changed historical mapping or skipped relay: calls=%d mapping=%s", calls, got)
			}
		})
	}
}

func TestResponseOriginMissKnownMapping(t *testing.T) {
	for _, state := range []string{"available", "disabled", "invalid"} {
		t.Run(state, func(t *testing.T) {
			calls := 0
			wantChannel := 1
			if state == "invalid" {
				wantChannel = 2
			}
			router, server := setupOriginMissRouter(t, func(c *gin.Context) {
				calls++
				if c.GetInt("channel_id") != wantChannel {
					t.Fatalf("selected channel=%d want=%d", c.GetInt("channel_id"), wantChannel)
				}
				if state == "invalid" {
					other := dbmodel.AppendResponseCorrelationOther(newRetryContext(c), "")
					if !strings.Contains(other, `"origin_lookup_status":"invalid"`) || strings.Contains(other, `"origin_channel_id"`) {
						t.Fatalf("invalid source was treated as known: %s", other)
					}
				}
				c.Status(http.StatusOK)
			})
			seedRetryChannel(t, 2, common.ChannelTypeAzure, `{"region":"westus"}`, 100)
			raw := `{"channel_id":1,"channel_type":3,"key_index":-1,"region":"eastus","response_id":"resp_old"}`
			want := http.StatusOK
			if state == "disabled" {
				if err := dbmodel.DB.Model(&dbmodel.Channel{}).Where("id = ?", 1).Update("status", common.ChannelStatusManuallyDisabled).Error; err != nil {
					t.Fatal(err)
				}
				want = http.StatusServiceUnavailable
			} else if state == "invalid" {
				raw = `{"channel_id":0}`
			}
			server.Set("cache_response_id_rs_old", raw)
			w := serveOriginRequest(router, "/v1/responses", originReplayBody, "")
			if w.Code != want || (want != http.StatusOK && calls != 0) {
				t.Fatalf("status=%d want=%d calls=%d body=%s", w.Code, want, calls, w.Body.String())
			}
			if got, _ := server.Get("cache_response_id_rs_old"); got != raw {
				t.Fatal("known mapping changed")
			}
		})
	}
}
