package controller

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/metrics"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/util"
)

type responseStreamTransport func(*http.Request) (*http.Response, error)

func (f responseStreamTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRelayResponseStreamRetryBoundary(t *testing.T) {
	quota := "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"insufficient_quota\",\"code\":\"credit_balance_exhausted\",\"message\":\"You have no credits remaining.\"}}\n\n"
	for _, tc := range []struct {
		name, prefix string
		wantCalls    int
		wantFailure  bool
		lateOnRetry  bool
		retries      int
	}{
		{"before_stream_retries", "", 2, false, false, 2},
		{"started_stream_stops", "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n", 1, true, false, 2},
		{"retry_started_stream_stops", "", 2, true, true, 2},
		{"last_retry_started_stream_stops", "", 2, true, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				setupRetryPolicyDB(t, false)
				db := dbmodel.DB
				if err := db.AutoMigrate(&dbmodel.User{}, &dbmodel.Token{}, &dbmodel.Log{}); err != nil {
					t.Fatal(err)
				}
				for _, row := range []any{&dbmodel.User{Id: 1, Username: "stream-test", Quota: 1000000}, &dbmodel.Token{Id: 1, UserId: 1, Key: "test", RemainQuota: 1000000}} {
					if err := db.Create(row).Error; err != nil {
						t.Fatal(err)
					}
				}
				seedRetryChannel(t, 1, common.ChannelTypeOpenAI, "", 1)
				seedRetryChannel(t, 2, common.ChannelTypeOpenAI, "", 1)
				oldLogDB, oldClient := dbmodel.LOG_DB, util.HTTPClient
				oldRetries, oldLog, oldPing := config.RetryTimes, config.LogConsumeEnabled, config.PingIntervalEnabled
				oldApproximate := config.ApproximateTokenEnabled
				dbmodel.LOG_DB = db
				config.RetryTimes, config.LogConsumeEnabled, config.PingIntervalEnabled, config.ApproximateTokenEnabled = tc.retries, true, false, true
				t.Cleanup(func() {
					dbmodel.LOG_DB, util.HTTPClient = oldLogDB, oldClient
					config.RetryTimes, config.LogConsumeEnabled, config.PingIntervalEnabled, config.ApproximateTokenEnabled = oldRetries, oldLog, oldPing, oldApproximate
				})
				calls := 0
				util.HTTPClient = &http.Client{Transport: responseStreamTransport(func(*http.Request) (*http.Response, error) {
					calls++
					body := tc.prefix + quota
					if calls > 1 {
						body = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":1,\"total_tokens\":11}}}\n\n"
					}
					if tc.lateOnRetry && calls == 2 {
						body = "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n" + quota
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
				})}
				writer := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(writer)
				c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"test-model","stream":true,"input":"test"}`))
				c.Request.Header.Set("Content-Type", "application/json")
				for key, value := range map[string]any{"id": 1, "token_id": 1, "channel_id": 1, "channel": common.ChannelTypeOpenAI, "group": "default", "token_name": "test", "original_model": "test-model", "base_url": "http://stream-test.invalid"} {
					c.Set(key, value)
				}
				RelayResponse(c)
				synctest.Wait()
				if calls != tc.wantCalls {
					t.Fatalf("upstream calls=%d, want %d: must not concatenate a retry after SSE starts", calls, tc.wantCalls)
				}
				var logs []dbmodel.Log
				if err := db.Find(&logs).Error; err != nil || len(logs) != 1 {
					t.Fatalf("expected exactly one final log: %+v %v", logs, err)
				}
				if tc.wantFailure {
					if logs[0].Type != dbmodel.LogTypeError || c.GetString(metrics.CtxRelayFailedKey) == "" {
						t.Fatalf("started stream failure was not recorded: %+v", logs)
					}
					if strings.Contains(writer.Body.String(), "response.completed") || strings.Count(writer.Body.String(), "credit_balance_exhausted") != 1 {
						t.Fatalf("failed SSE contains retry or trailing JSON: %s", writer.Body.String())
					}
				} else if logs[0].Type != dbmodel.LogTypeConsume || strings.Contains(writer.Body.String(), "credit_balance_exhausted") {
					t.Fatalf("prefix error leaked into the successful retry: %s %+v", writer.Body.String(), logs)
				}
			})
		})
	}
}
