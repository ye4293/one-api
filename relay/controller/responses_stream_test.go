package controller

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/util"
	"github.com/songquanpeng/one-api/service"
	"github.com/stretchr/testify/require"
)

func responseOutputContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	enabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = enabled })
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Set("id", 71)
	service.SetResponseAttemptSource(c, dbmodel.ResponseSource{Provider: "OpenAI", ChannelID: 1, KeyHash: "key", ResourceHash: "resource"})
	return c, w
}

func TestResponsesSSERegistersBeforeOutputAndPreservesLargeEvents(t *testing.T) {
	c, w := responseOutputContext(t)
	cipher := strings.Repeat("x", 100000)
	created := `{"type":"response.created","response":{"id":"resp_stream_large","output":[]}}`
	done := fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_stream_large","output":[{"type":"reasoning","id":"rs_stream_large","encrypted_content":"%s"}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}`, cipher)
	body := "event: response.created\ndata: " + created + "\n\nevent: response.completed\ndata: " + done + "\n\n"
	usage, err := doNativeOpenaiResponseStream(c, &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, &util.RelayMeta{})
	require.Nil(t, err)
	require.Equal(t, 5, usage.TotalTokens)
	require.Equal(t, body, w.Body.String())
	constraint, resolveErr := service.ResolveResponsesConstraint(context.Background(), 71, []byte(`{"previous_response_id":"resp_stream_large"}`), "", "")
	require.NoError(t, resolveErr)
	require.Equal(t, 1, constraint.Resource.ChannelID)
	constraint, resolveErr = service.ResolveResponsesConstraint(context.Background(), 71, []byte(fmt.Sprintf(`{"input":[{"type":"reasoning","id":"rs_stream_large","encrypted_content":"%s"}]}`, cipher)), "", "")
	require.NoError(t, resolveErr)
	require.Nil(t, constraint.Resource)
	require.Equal(t, "OpenAI", constraint.Provider)
}

func TestResponsesOutputStoreFailureDoesNotCommit(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			c, w := responseOutputContext(t)
			common.RedisEnabled = true
			previous := common.RDB
			common.RDB = nil
			t.Cleanup(func() { common.RDB = previous })
			body := `{"id":"resp_fail","output":[],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}`
			if stream {
				body = "data: {\"type\":\"response.completed\",\"response\":" + body + "}\n\n"
			}
			response := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
			if stream {
				usage, err := doNativeOpenaiResponseStream(c, response, &util.RelayMeta{})
				require.NotNil(t, err)
				require.Equal(t, 503, err.StatusCode)
				require.Equal(t, 5, usage.TotalTokens)
			} else {
				usage, err := doNativeOpenaiResponse(c, response, &util.RelayMeta{})
				require.NotNil(t, err)
				require.Equal(t, 503, err.StatusCode)
				require.Equal(t, 5, usage.TotalTokens)
			}
			require.False(t, c.Writer.Written())
			require.Empty(t, w.Body.String())
		})
	}
}

func TestResponsesStreamEOFAndMultiline(t *testing.T) {
	frame, data, event, err := readResponseEvent(bufio.NewReader(strings.NewReader("event: response.created\r\ndata: {\"type\":\"response.created\",\r\ndata: \"response\":{\"id\":\"a\"}}\r\n\r\n")))
	require.NoError(t, err)
	require.NotEmpty(t, frame)
	require.Equal(t, "response.created", event)
	require.Contains(t, string(data), "\n")
	c, w := responseOutputContext(t)
	body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_eof\"}}\n\n"
	_, streamErr := doNativeOpenaiResponseStream(c, &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, &util.RelayMeta{})
	require.NotNil(t, streamErr)
	require.True(t, c.Writer.Written())
	require.Equal(t, body, w.Body.String())
}
