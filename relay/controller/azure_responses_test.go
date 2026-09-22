package controller

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/util"
	"github.com/stretchr/testify/require"
)

func TestAzureResponsesPreservesEncryptedHistory(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/responses/compact"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/流式=%t", path, stream), func(t *testing.T) {
				original := []byte(fmt.Sprintf(`{"model":"deployment","stream":%t,"store":false,"custom_number":9007199254740993,"input":[{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":[]},{"type":"compaction","id":"cmp_1","encrypted_content":"compressed"},{"type":"function_call_output","call_id":"call_1","output":"完成"}]}`, stream))
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					body, err := io.ReadAll(r.Body)
					require.NoError(t, err)
					require.Equal(t, original, body)
					require.Equal(t, "/openai/"+path[len("/v1/"):], r.URL.Path)
					require.Equal(t, "test-key", r.Header.Get("api-key"))
					require.Equal(t, "test-version", r.URL.Query().Get("api-version"))
					require.Equal(t, int64(len(body)), r.ContentLength)
					w.WriteHeader(400)
					_, _ = io.WriteString(w, `{"error":{"code":"invalid_encrypted_content"}}`)
				}))
				defer server.Close()
				previous := util.HTTPClient
				util.HTTPClient = server.Client()
				defer func() { util.HTTPClient = previous }()
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", path, bytes.NewReader(original))
				meta := &util.RelayMeta{ChannelType: common.ChannelTypeAzure, BaseURL: server.URL, RequestURLPath: path, APIKey: "test-key", ActualModelName: "deployment", IsStream: stream, DisablePing: true, Config: dbmodel.ChannelConfig{APIVersion: "test-version"}}
				adaptor := &openai.Adaptor{}
				adaptor.Init(meta)
				response, err := doOpenaiResponseRequest(c, meta, adaptor, original)
				require.NoError(t, err)
				defer response.Body.Close()
				require.Equal(t, 400, response.StatusCode)
				require.Equal(t, 1, calls, "禁止删除历史后额外发送一次请求")
			})
		}
	}
}

func TestResponsesModelMappingPreservesUnknownFields(t *testing.T) {
	body := []byte(`{"model":"before","store":false,"n":0,"large":9007199254740993,"unknown":{"enabled":false},"input":[{"type":"compaction","encrypted_content":"密文"}]}`)
	mapped, err := mapResponsesRequestModel(body, "after")
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"after","store":false,"n":0,"large":9007199254740993,"unknown":{"enabled":false},"input":[{"type":"compaction","encrypted_content":"密文"}]}`, string(mapped))
	require.Contains(t, string(mapped), "9007199254740993")
}
