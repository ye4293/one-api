package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

type tierTestTransport func(*http.Request) (*http.Response, error)

func (f tierTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// 走真实 controller、请求转换、响应处理及异步结算；仅替换 HTTP transport，不访问外网。
func TestOpenAITierPricingIntegration(t *testing.T) {
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/completions", "/v1/responses"} {
		for _, stream := range []bool{false, true} {
			for _, tc := range []struct {
				name, requestTier, responseTier string
				mapped, finalDefault, retry     bool
				group                           float64
				chat, responses                 int64
			}{
				{"priority", "priority", "priority", false, false, false, 1, 2580, 2580},
				{"fast_priority", "fast", "priority", false, false, false, 1, 2580, 2580},
				{"priority_fast", "priority", "fast", false, false, false, 1, 2290, 2290},
				{"flex", "priority", "flex", false, false, false, 1, 523, 522},
				{"ultrafast", "ultrafast", "ultrafast", false, false, false, 1, 2580, 2580},
				{"default", "priority", "default", false, false, false, 1, 1145, 1145},
				{"absent", "priority", "", false, false, false, 1, 1145, 1145},
				{"unconfigured", "priority", "future", false, false, false, 1, 1145, 1145},
				{"null", "priority", "null", false, false, false, 1, 1145, 1145},
				{"empty", "priority", "empty", false, false, false, 1, 1145, 1145},
				{"last_default", "priority", "default", false, true, false, 1, 1145, 1145},
				{"mapping", "priority", "priority", true, false, false, 1, 523, 2580},
				{"discount", "priority", "flex", false, false, false, 0.5, 262, 261},
				{"combined_discount", "priority", "priority", false, false, false, 0.5, 516, 516},
				{"retry_missing", "priority", "", false, false, true, 1, 1145, 1145},
				{"Standard", "priority", "Standard", false, false, false, 1, 2580, 2580},
				{"auto", "auto", "auto", false, false, false, 1, 2580, 2580},
				{"mapping_long_actual", "priority", "priority", true, false, false, 1, 995, 2580},
				{"mapping_long_original", "priority", "priority", true, false, false, 1, 523, 4760},
				{"stream_null_after_tier", "fast", "priority", false, false, false, 1, 2580, 2580},
				{"stream_empty_after_tier", "fast", "priority", false, false, false, 1, 2580, 2580},
				{"stream_eof_after_usage", "fast", "priority", false, false, false, 1, 2580, 2580},
				{"stream_zero_usage", "fast", "priority", false, false, false, 1, 0, 0},
				{"stream_malformed", "fast", "priority", false, false, false, 1, 0, 0},
				{"stream_error_retry", "fast", "", false, false, true, 1, 1145, 1145},
			} {
				if strings.HasPrefix(tc.name, "stream_") && !stream {
					continue
				}
				if tc.name == "stream_error_retry" && endpoint != "/v1/responses" {
					continue
				}
				t.Run(fmt.Sprintf("%s/stream=%t/%s", endpoint, stream, tc.name), func(t *testing.T) {
					// synctest 等待本请求派生的 goroutine，避免关库/恢复 map 时结算尚未结束。
					synctest.Test(t, func(t *testing.T) {
						setupOpenAIPriceMaps(t)
						db := setupServiceTierLogDB(t)
						setOpenAITestPrice(pricingTestModel+"_priority", 2, 4, 0.2, 1.5)
						setOpenAITestPrice(pricingTestModel+"_ultrafast", 2, 4, 0.2, 1.5)
						setOpenAITestPrice(pricingTestModel+"_fast", 2, 3, 0.1, 1.25)
						setOpenAITestPrice(pricingTestModel+"_flex", 0.5, 2, 0.1, 1.25)
						setOpenAITestPrice(pricingTestModel+"_default", 99, 99, 99, 99)
						setOpenAITestPrice(pricingTestModel+"_Standard", 2, 4, 0.2, 1.5)
						setOpenAITestPrice(pricingTestModel+"_auto", 2, 4, 0.2, 1.5)
						setOpenAITestPrice("mapped_tier_test", 1, 3, 0.1, 1.25)
						setOpenAITestPrice("mapped_tier_test_priority", 0.5, 2, 0.1, 1.25)
						switch tc.name {
						case "mapping_long_actual":
							common.LongContextThreshold["mapped_tier_test"] = 999
						case "mapping_long_original":
							common.LongContextThreshold[pricingTestModel] = 999
						}
						writer := &serviceTierRecorder{ResponseRecorder: httptest.NewRecorder(), closed: make(chan bool)}
						c, _ := gin.CreateTestContext(writer)
						body := fmt.Sprintf(`{"model":%q,"service_tier":%q,"stream":%t,"messages":[{"role":"user","content":"test"}],"prompt":"test","input":"test","max_tokens":1}`, pricingTestModel, tc.requestTier, stream)
						setRequest := func() {
							c.Request = httptest.NewRequest("POST", endpoint, strings.NewReader(body))
							c.Request.Header.Set("Content-Type", "application/json")
						}
						setRequest()
						c.Set("id", 1)
						c.Set("token_id", 1)
						c.Set("channel_id", 1)
						c.Set("channel", common.ChannelTypeOpenAI)
						c.Set("group", "default")
						c.Set("token_name", "test")
						c.Set("original_model", pricingTestModel)
						c.Set("base_url", "http://tier-test.invalid")
						c.Set("channel_discount", tc.group)
						if tc.name == "combined_discount" {
							common.GroupRatio["default"] = 0.8
							c.Set("user_channel_ratio", 0.5)
						}
						if tc.mapped {
							c.Set("model_mapping", map[string]string{pricingTestModel: "mapped_tier_test"})
						}
						c.Set(openai.ResponseServiceTierContextKey, "stale_tier")
						oldClient, oldPing := util.HTTPClient, config.PingIntervalEnabled
						config.PingIntervalEnabled = false
						t.Cleanup(func() { util.HTTPClient = oldClient; config.PingIntervalEnabled = oldPing })
						calls := 0
						var upstreamBody string
						util.HTTPClient = &http.Client{Transport: tierTestTransport(func(req *http.Request) (*http.Response, error) {
							calls++
							var sent map[string]any
							if err := json.NewDecoder(req.Body).Decode(&sent); err != nil {
								return nil, err
							}
							model := pricingTestModel
							if tc.mapped {
								model = "mapped_tier_test"
							}
							if sent["model"] != model || sent["service_tier"] != tc.requestTier {
								return nil, fmt.Errorf("unexpected upstream body: %v", sent)
							}
							if tc.retry && calls == 1 {
								if tc.name == "stream_error_retry" {
									return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"insufficient_quota\",\"message\":\"retry\"}}\n\n"))}, nil
								}
								return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"retry"},"service_tier":"priority"}`))}, nil
							}
							usage := `"usage":{"prompt_tokens":1000,"completion_tokens":100,"total_tokens":1100,"prompt_tokens_details":{"cached_tokens":200,"cache_write_tokens":100}}`
							if endpoint == "/v1/responses" {
								usage = `"usage":{"input_tokens":1000,"output_tokens":100,"total_tokens":1100,"input_tokens_details":{"cached_tokens":200,"cache_write_tokens":100}}`
							}
							if tc.name == "stream_zero_usage" {
								usage = `"usage":{}`
							}
							tier := ""
							switch tc.responseTier {
							case "":
							case "null":
								tier = `"service_tier":null,`
							case "empty":
								tier = `"service_tier":"",`
							default:
								tier = fmt.Sprintf(`"service_tier":%q,`, tc.responseTier)
							}
							response := "{" + tier + usage + `,"choices":[]}`
							contentType := "application/json"
							if stream {
								contentType = "text/event-stream"
								firstTier := tier
								lastTier := ""
								if tc.finalDefault {
									firstTier = `"service_tier":"priority",`
									lastTier = `"service_tier":"default",`
								}
								switch tc.name {
								case "stream_null_after_tier":
									lastTier = `"service_tier":null,`
								case "stream_empty_after_tier":
									lastTier = `"service_tier":"",`
								}
								if endpoint == "/v1/responses" {
									response = serviceTierSSE(`{"type":"response.created","response":{`+firstTier+`"id":"test"}}`, `{"type":"response.completed","response":{`+lastTier+usage+`}}`)
								} else if endpoint == "/v1/completions" {
									response = serviceTierSSE("{"+firstTier+`"choices":[{"text":"test"}]}`, "{"+lastTier+usage+`,"choices":[]}`)
								} else {
									response = serviceTierSSE("{"+firstTier+`"choices":[]}`, "{"+lastTier+usage+`,"choices":[]}`)
								}
								if tc.name == "stream_eof_after_usage" {
									response = strings.TrimSuffix(response, "data: [DONE]\n\n")
								}
								if tc.name == "stream_malformed" {
									if endpoint == "/v1/responses" {
										response = serviceTierSSE(`{"type":"response.created","response":{`+firstTier+`"id":"test"}}`, `{broken`)
									} else {
										response = serviceTierSSE("{"+firstTier+`"choices":[]}`, `{broken`)
									}
								}
							}
							upstreamBody = response
							return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(response))}, nil
						})}
						run := func() *relaymodel.ErrorWithStatusCode {
							if endpoint == "/v1/responses" {
								return RelayOpenaiResponseNative(c)
							}
							return RelayTextHelper(c)
						}
						if tc.retry {
							if err := run(); err == nil {
								t.Fatal("expected upstream failure")
							}
							setRequest()
							// 复用重试 context，确保上次留下的已配置档位不会进入成功结算。
							c.Set(openai.ResponseServiceTierContextKey, "priority")
						}
						if tc.name == "stream_malformed" && endpoint == "/v1/responses" {
							if err := run(); err == nil {
								t.Fatal("malformed Responses SSE must retain its existing error path")
							}
							synctest.Wait()
							assertPricingConsumption(t, db, 0, 0)
							return
						}
						if err := run(); err != nil {
							t.Fatalf("relay error: %+v", err)
						}
						synctest.Wait()
						want := tc.chat
						if endpoint == "/v1/responses" {
							want = tc.responses
						} else if endpoint == "/v1/completions" && stream {
							// 旧 Completions 流式入口忽略 usage，保持本地估算：输入/输出各 1 token。
							want = 4
							switch tc.responseTier {
							case "priority", "ultrafast", "Standard", "auto":
								want = 10
							case "fast":
								want = 8
							case "flex":
								want = 2
							}
							if tc.mapped {
								want = 2
							}
							if tc.group == 0.5 {
								want = 1
							}
							if tc.name == "combined_discount" {
								want = 2
							}
							if tc.name == "stream_malformed" {
								want = 2 // 缺失有效输出时只保留输入估算。
							}
						}
						wantLogs := int64(1)
						if endpoint != "/v1/responses" && want == 0 {
							wantLogs = 0 // Chat 零用量沿用不记录消费日志的行为。
						}
						log := assertPricingConsumption(t, db, want, wantLogs)
						wantCalls := 1
						if tc.retry {
							wantCalls = 2
						}
						if calls != wantCalls {
							t.Fatalf("upstream calls=%d, want %d", calls, wantCalls)
						}
						if !stream && writer.Body.String() != upstreamBody {
							t.Fatal("non-stream response body changed")
						}
						if wantLogs == 0 {
							return
						}
						if log.ModelName != pricingTestModel {
							t.Fatal(log.ModelName)
						}
						billing := readPricingDetails(t, log, "billingDetails")
						if tc.name == "combined_discount" {
							for k, want := range map[string]float64{"group_ratio": 0.2, "tier_ratio": 0.8, "channel_discount": 0.5, "user_channel_ratio": 0.5} {
								if billing[k] != want {
									t.Fatalf("%s=%v, want %v", k, billing[k], want)
								}
							}
						}
						if tc.name == "stream_zero_usage" && endpoint == "/v1/responses" {
							for _, key := range []string{"cache_ratio", "cache_read_ratio", "cache_write_ratio", "cache_creation_ratio"} {
								if _, ok := billing[key]; ok {
									t.Fatalf("无缓存用量不应产生 %s", key)
								}
							}
						}
						usage := readPricingDetails(t, log, "usageDetails")
						tier, exists := usage["service_tier"]
						if tc.responseTier == "" || tc.responseTier == "null" || tc.responseTier == "empty" {
							if exists {
								t.Fatal(usage)
							}
						} else if tier != tc.responseTier {
							t.Fatal(usage)
						}
						if tc.responseTier != "" && tc.responseTier != "null" && tc.responseTier != "empty" && !strings.Contains(writer.Body.String(), fmt.Sprintf(`"service_tier":%q`, tc.responseTier)) {
							t.Fatal("response lost service_tier")
						}
					})
				})
			}
		}
	}
}
