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
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/util"
	"github.com/stretchr/testify/require"
)

func TestAzureResponsesRetriesInvalidEncryptedContent(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("流式=%t", stream), func(t *testing.T) {
			original := []byte(fmt.Sprintf(`{"model":"azure-deployment","stream":%t,"store":false,"custom_number":9007199254740993,"input":[{"type":"reasoning","id":"rs_openai","encrypted_content":"opaque-history","summary":[{"type":"summary_text","text":"已有摘要"}]},{"type":"message","role":"user","content":"继续"},{"type":"function_call","id":"fc_1","call_id":"call_1","name":"example","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"完成"}]}`, stream))
			want := fmt.Sprintf(`{"model":"azure-deployment","stream":%t,"store":false,"custom_number":9007199254740993,"input":[{"type":"reasoning","summary":[{"type":"summary_text","text":"已有摘要"}]},{"type":"message","role":"user","content":"继续"},{"type":"function_call","id":"fc_1","call_id":"call_1","name":"example","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"完成"}]}`, stream)
			var requests [][]byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("读取上游请求失败：%v", err)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				requests = append(requests, body)
				if r.URL.Path != "/openai/responses" || r.Header.Get("api-key") != "test-key" {
					t.Errorf("Azure 路径或鉴权发生变化：%s", r.URL.Path)
				}
				if r.ContentLength != int64(len(body)) {
					t.Errorf("请求长度未更新：声明 %d，实际 %d", r.ContentLength, len(body))
				}
				if bytes.Contains(body, []byte(`"encrypted_content"`)) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"error":{"code":"invalid_encrypted_content","message":"The encrypted content could not be verified.","type":"invalid_request_error"}}`)
					return
				}
				w.Header().Set("X-Test-Upstream", "retry")
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\"}\n\n")
				} else {
					_, _ = io.WriteString(w, `{"id":"resp_azure","output":[]}`)
				}
			}))
			defer server.Close()

			oldClient := util.HTTPClient
			util.HTTPClient = server.Client()
			t.Cleanup(func() { util.HTTPClient = oldClient })
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses?client=test", bytes.NewReader(original))
			c.Request.Header.Set("Content-Type", "application/json")
			cached, err := common.GetRequestBody(c)
			require.NoError(t, err)
			meta := &util.RelayMeta{
				ChannelType: common.ChannelTypeAzure, BaseURL: server.URL,
				RequestURLPath: c.Request.URL.String(), ActualModelName: "azure-deployment",
				APIKey: "test-key", IsStream: stream, DisablePing: true,
				Config: dbmodel.ChannelConfig{APIVersion: "test-version"},
			}
			adaptor := &openai.Adaptor{}
			adaptor.Init(meta)
			resp, err := doOpenaiResponseRequest(c, meta, adaptor, cached)
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode, "加密验证失败后应清理并重试")
			require.Len(t, requests, 2, "同一渠道应只重试一次")
			require.Equal(t, original, requests[0], "首次请求应保留加密历史")
			require.JSONEq(t, want, string(requests[1]))
			require.Contains(t, string(requests[1]), `9007199254740993`, "未知字段的大整数不能丢失精度")
			require.Equal(t, "retry", resp.Header.Get("X-Test-Upstream"))
			responseBody, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			if stream {
				require.Equal(t, "data: {\"type\":\"response.completed\"}\n\n", string(responseBody))
			} else {
				require.JSONEq(t, `{"id":"resp_azure","output":[]}`, string(responseBody))
			}
			cached, err = common.GetRequestBody(c)
			require.NoError(t, err)
			require.Equal(t, original, cached, "跨渠道重试仍应读取原始请求")
		})
	}
}

type azureResponseTestAdaptor struct {
	openai.Adaptor
	doRequest func([]byte) (*http.Response, error)
}

func (a *azureResponseTestAdaptor) DoRequest(_ *gin.Context, _ *util.RelayMeta, body io.Reader) (*http.Response, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	return a.doRequest(data)
}

type azureResponseTestBody struct {
	io.Reader
	closed bool
}

func (b *azureResponseTestBody) Close() error {
	b.closed = true
	return nil
}

func TestAzureResponsesFallbackBoundaries(t *testing.T) {
	const encryptedRequest = `{"input":[{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":[]}]}`
	const encryptedError = `{"error":{"code":"invalid_encrypted_content","message":"The encrypted content could not be verified."}}`
	tests := []struct {
		name        string
		channelType int
		path        string
		status      int
		body        string
		errorBody   string
		wantCalls   int
		canceled    bool
	}{
		{"错误码匹配且只重试一次", common.ChannelTypeAzure, "/v1/responses", 400, encryptedRequest, encryptedError, 2, false},
		{"错误类型匹配", common.ChannelTypeAzure, "/v1/responses", 400, encryptedRequest, `{"error":{"type":"invalid_encrypted_content"}}`, 2, false},
		{"通用错误码和验证失败正文", common.ChannelTypeAzure, "/v1/responses/", 400, encryptedRequest, `{"error":{"code":"BadRequest","message":"The encrypted_content could not be verified."}}`, 2, false},
		{"OpenAI 渠道不清理", common.ChannelTypeOpenAI, "/v1/responses", 400, encryptedRequest, encryptedError, 1, false},
		{"压缩接口不清理", common.ChannelTypeAzure, "/v1/responses/compact", 400, encryptedRequest, encryptedError, 1, false},
		{"其他接口不清理", common.ChannelTypeAzure, "/v1/chat/completions", 400, encryptedRequest, encryptedError, 1, false},
		{"正常加密历史不清理", common.ChannelTypeAzure, "/v1/responses", 200, encryptedRequest, `{"id":"resp_ok"}`, 1, false},
		{"流式正文不提前读取", common.ChannelTypeAzure, "/v1/responses", 200, encryptedRequest, "data: {\"type\":\"response.completed\"}\n\n", 1, false},
		{"服务故障不降级", common.ChannelTypeAzure, "/v1/responses", 500, encryptedRequest, encryptedError, 1, false},
		{"缺少必填字段不降级", common.ChannelTypeAzure, "/v1/responses", 400, encryptedRequest, `{"error":{"message":"Missing required parameter: input[0].encrypted_content"}}`, 1, false},
		{"普通参数错误不降级", common.ChannelTypeAzure, "/v1/responses", 400, encryptedRequest, `{"error":{"message":"Invalid model"}}`, 1, false},
		{"非 JSON 错误原样返回", common.ChannelTypeAzure, "/v1/responses", 400, encryptedRequest, "invalid_encrypted_content", 1, false},
		{"超长错误保留完整正文", common.ChannelTypeAzure, "/v1/responses", 400, encryptedRequest, encryptedError + strings.Repeat(" ", 70<<10), 1, false},
		{"无可清理内容不重试", common.ChannelTypeAzure, "/v1/responses", 400, `{"input":[{"role":"user","content":"继续"}]}`, encryptedError, 1, false},
		{"字符串输入不重试", common.ChannelTypeAzure, "/v1/responses", 400, `{"input":"继续"}`, encryptedError, 1, false},
		{"非法请求不重试", common.ChannelTypeAzure, "/v1/responses", 400, `{"input":`, encryptedError, 1, false},
		{"取消请求不重试", common.ChannelTypeAzure, "/v1/responses", 400, encryptedRequest, encryptedError, 1, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			if tc.canceled {
				ctx, cancel := context.WithCancel(c.Request.Context())
				cancel()
				c.Request = c.Request.WithContext(ctx)
			}
			firstBody := &azureResponseTestBody{Reader: strings.NewReader(tc.errorBody)}
			firstResponse := &http.Response{StatusCode: tc.status, Body: firstBody, Header: http.Header{"X-Test-Upstream": {"original"}}}
			calls := 0
			adaptor := &azureResponseTestAdaptor{doRequest: func(body []byte) (*http.Response, error) {
				calls++
				if calls == 1 {
					require.Equal(t, tc.body, string(body))
					return firstResponse, nil
				}
				require.True(t, firstBody.closed, "重试前应关闭失败响应")
				require.JSONEq(t, `{"input":[{"type":"reasoning","summary":[]}]}`, string(body))
				return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(encryptedError))}, nil
			}}
			resp, err := doOpenaiResponseRequest(c, &util.RelayMeta{ChannelType: tc.channelType}, adaptor, []byte(tc.body))
			require.NoError(t, err)
			require.Equal(t, tc.wantCalls, calls)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			if tc.wantCalls == 1 {
				require.Same(t, firstResponse, resp)
				require.Equal(t, tc.errorBody, string(body), "错误正文应恢复完整")
				require.Equal(t, "original", resp.Header.Get("X-Test-Upstream"))
			} else {
				require.Equal(t, encryptedError, string(body))
			}
			require.NoError(t, resp.Body.Close())
			require.True(t, firstBody.closed)
		})
	}
}

func TestAzureResponsesTransportFailure(t *testing.T) {
	for _, failOn := range []int{1, 2} {
		t.Run(fmt.Sprintf("第%d次请求失败", failOn), func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			wantErr := errors.New("模拟网络错误")
			calls := 0
			adaptor := &azureResponseTestAdaptor{doRequest: func(_ []byte) (*http.Response, error) {
				calls++
				if calls == failOn {
					return nil, wantErr
				}
				return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_encrypted_content"}}`))}, nil
			}}
			resp, err := doOpenaiResponseRequest(c, &util.RelayMeta{ChannelType: common.ChannelTypeAzure}, adaptor,
				[]byte(`{"input":[{"type":"reasoning","encrypted_content":"opaque","summary":[]}]}`))
			require.ErrorIs(t, err, wantErr)
			require.Nil(t, resp)
			require.Equal(t, failOn, calls)
		})
	}
}

func TestSanitizeAzureResponsesInput(t *testing.T) {
	t.Run("压缩项和平台引用的处理边界", func(t *testing.T) {
		body := []byte(`{"previous_response_id":"resp_original","input":[{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":[]},{"type":"compaction","id":"cmp_1","encrypted_content":"opaque-history"},{"type":"compaction","id":"cmp_plain"},{"type":"item_reference","id":"msg_1"},{"type":"custom","encrypted_content":"keep","number":9007199254740993},null,7]}`)
		original := bytes.Clone(body)
		clean, reasoningItems, compactionItems, err := sanitizeAzureResponsesInput(body)
		require.NoError(t, err)
		require.Equal(t, 1, reasoningItems)
		require.Equal(t, 1, compactionItems)
		require.JSONEq(t, `{"previous_response_id":"resp_original","input":[{"type":"reasoning","summary":[]},{"type":"compaction","id":"cmp_plain"},{"type":"item_reference","id":"msg_1"},{"type":"custom","encrypted_content":"keep","number":9007199254740993},null,7]}`, string(clean))
		require.Contains(t, string(clean), `9007199254740993`)
		require.Equal(t, original, body)
		second, reasoningItems, compactionItems, err := sanitizeAzureResponsesInput(clean)
		require.NoError(t, err)
		require.Equal(t, clean, second, "重复清理应保持幂等")
		require.Zero(t, reasoningItems+compactionItems)
	})
	t.Run("未修改请求保留原始字节", func(t *testing.T) {
		for _, body := range []string{`{"input":"继续"}`, `{"input":[]}`, `{"input":null}`, `{}`, `null`, "{ \"input\": [{\"type\":\"reasoning\",\"summary\":[]}] }"} {
			clean, reasoningItems, compactionItems, err := sanitizeAzureResponsesInput([]byte(body))
			require.NoError(t, err)
			require.Equal(t, body, string(clean))
			require.Zero(t, reasoningItems+compactionItems)
		}
	})
}
