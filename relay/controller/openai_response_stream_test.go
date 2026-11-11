package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/helper"
	"github.com/songquanpeng/one-api/relay/util"
)

const responseQuotaError = `{"type":"error","error":{"type":"insufficient_quota","code":"credit_balance_exhausted","message":"You have no credits remaining.","param":null}}`
const responseFailedError = `{"type":"response.failed","response":{"id":"resp_test","status":"failed","error":{"type":"insufficient_quota","code":"credit_balance_exhausted","message":"You have no credits remaining."}}}`

func responseTestEvent(event, data string) string {
	if event == "" {
		return "data: " + data + "\n\n"
	}
	return "event: " + event + "\ndata: " + data + "\n\n"
}

func responseLargeCreatedEvent() string {
	// Match the production shape: a long UTF-8 function description in one SSE line.
	return responseTestEvent("response.created", `{"type":"response.created","response":{"id":"resp_test","tools":[{"type":"function","name":"write_file","description":"`+strings.Repeat("文件", 12000)+`"}]}}`)
}

func TestBufferStreamPrefixQuotaAfterLargeEvents(t *testing.T) {
	for _, event := range []struct{ name, data string }{{"error", responseQuotaError}, {"response.failed", responseFailedError}} {
		t.Run(event.name, func(t *testing.T) {
			stream := responseLargeCreatedEvent() + responseLargeCreatedEvent() + responseTestEvent(event.name, event.data)
			_, err := bufferStreamPrefix(strings.NewReader(stream))
			if err == nil || err.StatusCode != 429 || err.Type != "insufficient_quota" || err.Code != "credit_balance_exhausted" || err.Message != "You have no credits remaining." {
				t.Fatalf("quota failure must preserve upstream fields and trigger retry: %+v", err)
			}
		})
	}
}

func TestBufferStreamPrefixDataOnlyError(t *testing.T) {
	_, err := bufferStreamPrefix(strings.NewReader(responseTestEvent("", responseQuotaError)))
	if err == nil || err.Code != "credit_balance_exhausted" {
		t.Fatalf("data-only SSE error was missed: %+v", err)
	}
}

func TestBufferStreamPrefixPreservesReadAhead(t *testing.T) {
	stream := responseLargeCreatedEvent() + responseTestEvent("response.output_text.delta", `{"type":"response.output_text.delta","delta":"ok"}`) + responseTestEvent("response.completed", `{"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":1}}}`)
	reader := strings.NewReader(stream)
	prefix, err := bufferStreamPrefix(reader)
	if err != nil {
		t.Fatal(err)
	}
	replayed, readErr := io.ReadAll(io.MultiReader(bytes.NewReader(prefix), reader))
	if readErr != nil || string(replayed) != stream {
		t.Fatalf("prefix buffering lost or duplicated response bytes: %v", readErr)
	}
}

type responseReadError struct{ err error }

func (r responseReadError) Read([]byte) (int, error) { return 0, r.err }

func TestBufferStreamPrefixReadFailure(t *testing.T) {
	_, err := bufferStreamPrefix(io.MultiReader(strings.NewReader(responseTestEvent("response.created", `{"type":"response.created"}`)), responseReadError{io.ErrUnexpectedEOF}))
	if err == nil || err.StatusCode != 502 || !strings.Contains(err.Message, "unexpected EOF") {
		t.Fatalf("prefix read failure must not become success: %+v", err)
	}
}

func TestBufferStreamPrefixLineLimitFailure(t *testing.T) {
	_, err := bufferStreamPrefix(strings.NewReader("data: " + strings.Repeat("x", helper.MaxScannerBufferSize+1)))
	if err == nil || err.StatusCode != 502 || !strings.Contains(err.Message, "token too long") {
		t.Fatalf("oversized event must report scanner failure: %+v", err)
	}
}

func TestResponseStreamErrorsDoNotConsume(t *testing.T) {
	for _, tc := range []struct{ name, prefix, event, data string }{
		{"large_prefix", responseLargeCreatedEvent(), "error", responseQuotaError},
		{"after_prefix", strings.Repeat(responseTestEvent("response.in_progress", `{"type":"response.in_progress"}`), 5), "error", responseQuotaError},
		{"after_content", responseTestEvent("response.output_text.delta", `{"type":"response.output_text.delta","delta":"partial"}`), "error", responseQuotaError},
		{"failed_after_content", responseTestEvent("response.output_text.delta", `{"type":"response.output_text.delta","delta":"partial"}`), "response.failed", responseFailedError},
		{"event_only_after_content", responseTestEvent("response.output_text.delta", `{"type":"response.output_text.delta","delta":"partial"}`), "error", `{"error":{"type":"insufficient_quota","code":"credit_balance_exhausted","message":"You have no credits remaining."}}`},
		{"failed_event_only_after_content", responseTestEvent("response.output_text.delta", `{"type":"response.output_text.delta","delta":"partial"}`), "response.failed", `{"response":{"id":"resp_test","status":"failed","error":{"type":"insufficient_quota","code":"credit_balance_exhausted","message":"You have no credits remaining."}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				db := setupServiceTierLogDB(t)
				stream := tc.prefix + responseTestEvent(tc.event, tc.data)
				c, writer := newResponseStreamTestContext(t, io.NopCloser(strings.NewReader(stream)))
				err := RelayOpenaiResponseNative(c)
				synctest.Wait()
				if err == nil || err.StatusCode != 429 || err.Type != "insufficient_quota" || err.Code != "credit_balance_exhausted" {
					t.Fatalf("stream failure incorrectly succeeded: %+v", err)
				}
				var count int64
				if err := db.Model(&dbmodel.Log{}).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("failed stream wrote a consume log: count=%d err=%v", count, err)
				}
				if tc.name == "large_prefix" {
					if c.Writer.Written() || writer.Body.Len() != 0 {
						t.Fatal("prefix failure committed a response before retry")
					}
				} else if !strings.Contains(writer.Body.String(), tc.data) {
					t.Fatal("late stream failure lost the upstream SSE error")
				}
			})
		})
	}
}

func TestResponseStreamInterrupted(t *testing.T) {
	oldApproximate := config.ApproximateTokenEnabled
	config.ApproximateTokenEnabled = true
	t.Cleanup(func() { config.ApproximateTokenEnabled = oldApproximate })
	for _, tc := range []struct {
		name string
		body io.Reader
		code string
	}{
		{"eof_before_completion", strings.NewReader(responseTestEvent("response.created", `{"type":"response.created"}`)), "stream_incomplete"},
		{"read_error_after_content", io.MultiReader(strings.NewReader(responseTestEvent("response.output_text.delta", `{"type":"response.output_text.delta","delta":"partial"}`)), responseReadError{io.ErrUnexpectedEOF}), "stream_read_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newResponseStreamTestContext(t, io.NopCloser(tc.body))
			_, err := doNativeOpenaiResponseStream(c, &http.Response{StatusCode: 200, Body: io.NopCloser(tc.body)}, util.GetRelayMeta(c))
			if err == nil || err.StatusCode != 502 || err.Code != tc.code {
				t.Fatalf("interrupted stream must not become successful consumption: %+v", err)
			}
		})
	}
}

func newResponseStreamTestContext(t *testing.T, body io.ReadCloser) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-4o","stream":true,"input":"test"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	for key, value := range map[string]any{"id": 1, "token_id": 1, "channel_id": 1, "channel": common.ChannelTypeOpenAI, "group": "default", "token_name": "test", "original_model": "gpt-4o", "base_url": "http://stream-test.invalid"} {
		c.Set(key, value)
	}
	oldClient, oldPing := util.HTTPClient, config.PingIntervalEnabled
	config.PingIntervalEnabled = false
	util.HTTPClient = &http.Client{Transport: tierTestTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}, nil
	})}
	t.Cleanup(func() { util.HTTPClient, config.PingIntervalEnabled = oldClient, oldPing })
	return c, writer
}

type responseCancelBody struct {
	io.Reader
	onClose func()
	once    sync.Once
}

func (b *responseCancelBody) Close() error { b.once.Do(b.onClose); return nil }

type responseMetricContextHook struct{ err error }

func (*responseMetricContextHook) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	return ctx, errors.New("test: suppress Redis cache network access")
}
func (*responseMetricContextHook) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (h *responseMetricContextHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	h.err = ctx.Err()
	return ctx, errors.New("test: suppress Redis metric network access")
}
func (*responseMetricContextHook) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func TestResponseStreamSettlementSurvivesCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db := setupServiceTierLogDB(t)
		oldRDB := common.RDB
		client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
		hook := &responseMetricContextHook{err: fmt.Errorf("metric not attempted")}
		client.AddHook(hook)
		common.RDB = client
		t.Cleanup(func() { common.RDB = oldRDB; _ = client.Close() })
		var cancel context.CancelFunc
		body := &responseCancelBody{Reader: strings.NewReader(responseTestEvent("response.completed", `{"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":1,"total_tokens":11}}}`)), onClose: func() {
			common.RedisEnabled = true
			cancel()
		}}
		c, _ := newResponseStreamTestContext(t, body)
		ctx, cancelRequest := context.WithCancel(c.Request.Context())
		cancel = cancelRequest
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		if err := RelayOpenaiResponseNative(c); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if ctx.Err() == nil || hook.err != nil {
			t.Fatalf("async metric inherited cancellation: request=%v metric=%v", ctx.Err(), hook.err)
		}
		var count int64
		if err := db.Model(&dbmodel.Log{}).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("successful settlement missing: count=%d err=%v", count, err)
		}
	})
}
