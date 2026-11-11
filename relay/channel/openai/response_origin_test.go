package openai

import (
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/songquanpeng/one-api/common"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/constant"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type originWriter struct {
	gin.ResponseWriter
	before func([]byte)
}

func (w *originWriter) Write(b []byte) (int, error) { w.before(b); return w.ResponseWriter.Write(b) }
func (w *originWriter) CloseNotify() <-chan bool    { return make(chan bool) }
func TestResponseOriginToolOnlyChatStream(t *testing.T) {
	r := miniredis.RunT(t)
	oldClient, oldEnabled := common.RDB, common.RedisEnabled
	common.RDB = redis.NewClient(&redis.Options{Addr: r.Addr()})
	common.RedisEnabled = true
	t.Cleanup(func() { common.RDB.Close(); common.RDB = oldClient; common.RedisEnabled = oldEnabled })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	c.Set("channel_id", 7)
	c.Set("channel", common.ChannelTypeAzure)
	c.Set("key_index", 2)
	c.Writer = &originWriter{ResponseWriter: c.Writer, before: func(b []byte) {
		if strings.Contains(string(b), "chatcmpl-A") {
			origin, err := dbmodel.LookupResponseOrigin("chatcmpl-A")
			if err != nil || origin.ChannelType != common.ChannelTypeAzure || origin.Region != "eastus" {
				t.Errorf("tool-only origin invisible: %+v %v", origin, err)
			}
		}
	}}
	body := "data: {\"id\":\"chatcmpl-A\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"save_text\",\"arguments\":\"{}\"}}]}}]}\n\ndata: [DONE]\n\n"
	resp := &http.Response{StatusCode: 200, Header: http.Header{"X-Ms-Region": []string{"eastus"}}, Body: io.NopCloser(strings.NewReader(body))}
	if err, _, _ := StreamHandler(c, resp, constant.RelayModeChatCompletions); err != nil {
		t.Fatal(err)
	}
}
