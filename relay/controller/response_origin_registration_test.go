package controller

import (
	"context"
	"errors"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/songquanpeng/one-api/common"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/util"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type originProbeWriter struct {
	gin.ResponseWriter
	before func([]byte)
}

func (w *originProbeWriter) Write(b []byte) (int, error) {
	w.before(b)
	return w.ResponseWriter.Write(b)
}
func originRegistrationContext(t *testing.T) (*gin.Context, *miniredis.Miniredis) {
	t.Helper()
	r := miniredis.RunT(t)
	oldClient, oldEnabled := common.RDB, common.RedisEnabled
	common.RDB = redis.NewClient(&redis.Options{Addr: r.Addr()})
	common.RedisEnabled = true
	t.Cleanup(func() { common.RDB.Close(); common.RDB = oldClient; common.RedisEnabled = oldEnabled })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Set("channel_id", 7)
	c.Set("channel", common.ChannelTypeAzure)
	c.Set("key_index", 2)
	return c, r
}
func TestResponseOriginNonStreamRegistersBeforePublish(t *testing.T) {
	c, _ := originRegistrationContext(t)
	body := `{"id":"resp_A","output":[{"id":"rs_A","type":"reasoning","encrypted_content":"opaque"},{"id":"cmp_A","type":"compaction","encrypted_content":"opaque"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	c.Writer = &originProbeWriter{ResponseWriter: c.Writer, before: func(data []byte) {
		for _, id := range []string{"resp_A", "rs_A", "cmp_A"} {
			origin, err := dbmodel.LookupResponseOrigin(id)
			if err != nil || origin.ResponseID != "resp_A" || origin.ChannelType != common.ChannelTypeAzure || origin.Region != "eastus" {
				t.Errorf("origin invisible before publish %s: %+v %v", id, origin, err)
			}
		}
	}}
	resp := &http.Response{StatusCode: 200, Header: http.Header{"X-Ms-Region": []string{"eastus"}}, Body: io.NopCloser(strings.NewReader(body))}
	_, err := doNativeOpenaiResponse(c, resp, &util.RelayMeta{})
	if err != nil {
		t.Fatal(err)
	}
}
func TestResponseOriginStreamRegistersItemsBeforePublish(t *testing.T) {
	c, _ := originRegistrationContext(t)
	events := []string{
		`{"type":"response.created","response":{"id":"resp_A"}}`,
		`{"type":"response.output_item.done","item":{"id":"rs_A","type":"reasoning","encrypted_content":"opaque"}}`,
		`{"type":"response.completed","response":{"id":"resp_A","output":[{"id":"cmp_A","type":"compaction","encrypted_content":"opaque"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
	}
	body := ""
	for _, e := range events {
		body += "data: " + e + "\n\n"
	}
	body += "data: [DONE]\n\n"
	c.Writer = &originProbeWriter{ResponseWriter: c.Writer, before: func(data []byte) {
		id := "resp_A"
		if strings.Contains(string(data), `"id":"rs_A"`) {
			id = "rs_A"
		}
		if strings.Contains(string(data), `"id":"cmp_A"`) {
			id = "cmp_A"
		}
		origin, err := dbmodel.LookupResponseOrigin(id)
		if err != nil || origin.ResponseID != "resp_A" || origin.Region != "eastus" || origin.ChannelType != common.ChannelTypeAzure {
			t.Errorf("stream origin invisible %s: %+v %v", id, origin, err)
		}
	}}
	resp := &http.Response{StatusCode: 200, Header: http.Header{"X-Ms-Region": []string{"eastus"}}, Body: io.NopCloser(strings.NewReader(body))}
	_, err := doNativeOpenaiResponseStream(c, resp, &util.RelayMeta{})
	if err != nil {
		t.Fatal(err)
	}
}

type failFirstOriginWrite struct{ failed bool }

func (h *failFirstOriginWrite) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	if cmd.Name() == "set" && !h.failed {
		h.failed = true
		return ctx, errors.New("injected initial Redis write failure")
	}
	return ctx, nil
}
func (h *failFirstOriginWrite) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (h *failFirstOriginWrite) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}
func (h *failFirstOriginWrite) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }
func TestResponseOriginCompletedEventRetriesFailedRegistration(t *testing.T) {
	c, _ := originRegistrationContext(t)
	common.RDB.AddHook(&failFirstOriginWrite{})
	body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_retry\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_retry\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\ndata: [DONE]\n\n"
	c.Writer = &originProbeWriter{ResponseWriter: c.Writer, before: func(data []byte) {
		if strings.Contains(string(data), "response.completed") {
			got, err := dbmodel.LookupResponseOrigin("resp_retry")
			if err != nil || got.Region != "eastus" || got.ChannelType != common.ChannelTypeAzure {
				t.Errorf("completed failed to repair registration: %+v %v", got, err)
			}
		}
	}}
	resp := &http.Response{StatusCode: 200, Header: http.Header{"X-Ms-Region": []string{"eastus"}}, Body: io.NopCloser(strings.NewReader(body))}
	_, err := doNativeOpenaiResponseStream(c, resp, &util.RelayMeta{})
	if err != nil {
		t.Fatal(err)
	}
}
func TestResponseOriginPrefixFailureCapturesGeneratedID(t *testing.T) {
	c, _ := originRegistrationContext(t)
	body := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_B\"}}\n\nevent: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_B\",\"error\":{\"code\":\"insufficient_quota\",\"message\":\"quota\"}}}\n\n"
	resp := &http.Response{StatusCode: 200, Header: http.Header{"X-Ms-Region": []string{"eastus"}}, Body: io.NopCloser(strings.NewReader(body))}
	_, err := doNativeOpenaiResponseStream(c, resp, &util.RelayMeta{})
	if err == nil || c.GetString("x_response_id") != "resp_B" {
		t.Fatalf("generated ID lost on prefix failure: id=%q err=%v", c.GetString("x_response_id"), err)
	}
}
