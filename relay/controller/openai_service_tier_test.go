package controller

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel"
	"github.com/songquanpeng/one-api/relay/channel/ali"
	"github.com/songquanpeng/one-api/relay/channel/minimax"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/channel/xai"
	"github.com/songquanpeng/one-api/relay/channel/zhipu"
	"github.com/songquanpeng/one-api/relay/constant"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestOpenAIServiceTierRequestPassthrough(t *testing.T) {
	for _, api := range []string{"chat", "responses"} {
		for _, tier := range []string{"", "auto", "default", "flex", "priority", "future_tier"} {
			t.Run(api+"/"+tier, func(t *testing.T) {
				body := `{"model":"alias","stream":true,"max_tokens":100}`
				if tier != "" {
					body = strings.TrimSuffix(body, "}") + `,"service_tier":"` + tier + `"}`
				}
				var converted any
				if api == "chat" {
					var request relaymodel.GeneralOpenAIRequest
					if err := json.Unmarshal([]byte(body), &request); err != nil {
						t.Fatal(err)
					}
					request.Model = "gpt-4o" // 模型映射及 max_tokens 转换都会触发重新序列化。
					var err error
					converted, err = (&openai.Adaptor{}).ConvertRequest(nil, constant.RelayModeChatCompletions, &request)
					if err != nil {
						t.Fatal(err)
					}
				} else {
					var request openai.OpeanaiResaponseRequest
					if err := json.Unmarshal([]byte(body), &request); err != nil {
						t.Fatal(err)
					}
					request.Model = "gpt-4o"
					converted = request
				}
				encoded, err := json.Marshal(converted)
				if err != nil {
					t.Fatal(err)
				}
				var upstream map[string]any
				if err := json.Unmarshal(encoded, &upstream); err != nil {
					t.Fatal(err)
				}
				got, exists := upstream["service_tier"]
				if tier == "" {
					if exists {
						t.Fatalf("absent service_tier was injected: %s", encoded)
					}
				} else if got != tier {
					t.Fatalf("upstream service_tier = %v, want %q: %s", got, tier, encoded)
				}
				if upstream["model"] != "gpt-4o" {
					t.Fatalf("model mapping lost: %s", encoded)
				}
			})
		}
	}
}

type serviceTierRecorder struct {
	*httptest.ResponseRecorder
	closed chan bool
}

func (r *serviceTierRecorder) CloseNotify() <-chan bool { return r.closed }

func serviceTierSSE(events ...string) string {
	return "data: " + strings.Join(events, "\n\ndata: ") + "\n\ndata: [DONE]\n\n"
}

func TestOpenAIServiceTierUsageLogs(t *testing.T) {
	db := setupServiceTierLogDB(t)
	const chatUsage = `"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":2}}`
	const responseUsage = `"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15,"input_tokens_details":{"cached_tokens":2}}`
	for _, tc := range []struct {
		name   string
		native bool
		stream bool
		body   string
		want   string
	}{
		{"chat/priority", false, false, `{"service_tier":"priority",` + chatUsage + `}`, "priority"},
		{"chat/different_from_request", false, false, `{"service_tier":"default",` + chatUsage + `}`, "default"},
		{"chat/absent", false, false, `{` + chatUsage + `}`, ""},
		{"chat/null", false, false, `{"service_tier":null,` + chatUsage + `}`, ""},
		{"chat/empty", false, false, `{"service_tier":"",` + chatUsage + `}`, ""},
		{"chat/estimated_usage", false, false, `{"service_tier":"flex","choices":[]}`, "flex"},
		{"chat/stream_separate_usage", false, true, serviceTierSSE(`{"service_tier":"flex","choices":[],"usage":null}`, `{`+chatUsage+`}`), "flex"},
		{"chat/stream_final_tier", false, true, serviceTierSSE(`{"service_tier":"priority","choices":[]}`, `{"service_tier":"default",`+chatUsage+`}`), "default"},
		{"chat/stream_absent", false, true, serviceTierSSE(`{` + chatUsage + `}`), ""},
		{"chat/stream_null_and_empty", false, true, serviceTierSSE(`{"service_tier":null,"choices":[]}`, `{"service_tier":"",`+chatUsage+`}`), ""},
		{"chat/stream_null_after_tier", false, true, serviceTierSSE(`{"service_tier":"flex","choices":[]}`, `{"service_tier":null,`+chatUsage+`}`), "flex"},
		{"chat/stream_estimated_usage", false, true, serviceTierSSE(`{"service_tier":"flex","choices":[]}`), "flex"},
		{"completions/stream_tier", false, true, serviceTierSSE(`{"service_tier":"flex","choices":[]}`), "flex"},
		{"completions/stream_absent", false, true, serviceTierSSE(`{"choices":[]}`), ""},
		{"responses/priority", true, false, `{"service_tier":"priority",` + responseUsage + `}`, "priority"},
		{"responses/absent", true, false, `{` + responseUsage + `}`, ""},
		{"responses/null", true, false, `{"service_tier":null,` + responseUsage + `}`, ""},
		{"responses/empty", true, false, `{"service_tier":"",` + responseUsage + `}`, ""},
		{"responses/stream_separate_usage", true, true, serviceTierSSE(`{"type":"response.created","response":{"service_tier":"flex"}}`, `{"type":"response.completed","response":{`+responseUsage+`}}`), "flex"},
		{"responses/stream_final_tier", true, true, serviceTierSSE(`{"type":"response.created","response":{"service_tier":"priority"}}`, `{"type":"response.completed","response":{"service_tier":"default",`+responseUsage+`}}`), "default"},
		{"responses/stream_absent", true, true, serviceTierSSE(`{"type":"response.completed","response":{` + responseUsage + `}}`), ""},
		{"responses/stream_null_and_empty", true, true, serviceTierSSE(`{"type":"response.created","response":{"service_tier":null}}`, `{"type":"response.completed","response":{"service_tier":"",`+responseUsage+`}}`), ""},
		{"responses/stream_null_after_tier", true, true, serviceTierSSE(`{"type":"response.created","response":{"service_tier":"flex"}}`, `{"type":"response.completed","response":{"service_tier":null,`+responseUsage+`}}`), "flex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writer := &serviceTierRecorder{ResponseRecorder: httptest.NewRecorder(), closed: make(chan bool)}
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"service_tier":"priority"}`))
			// 重试可复用 context；本次响应缺少档位时，不能沿用上次响应或请求档位。
			c.Set(openai.ResponseServiceTierContextKey, "stale_tier")
			resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}
			meta := &util.RelayMeta{Mode: constant.RelayModeChatCompletions, IsStream: tc.stream, ActualModelName: "gpt-4o", OriginModelName: "gpt-4o", PromptTokens: 10, UserId: 1, TokenId: 1, ChannelId: 1, Group: "default", DisablePing: true}
			if strings.HasPrefix(tc.name, "completions/") {
				meta.Mode = constant.RelayModeCompletions
			}
			if tc.native {
				var usage *openai.ResponseUsage
				var relayErr *relaymodel.ErrorWithStatusCode
				if tc.stream {
					usage, relayErr = doNativeOpenaiResponseStream(c, resp, meta)
				} else {
					usage, relayErr = doNativeOpenaiResponse(c, resp, meta)
				}
				if relayErr != nil {
					t.Fatalf("response handler failed: %+v", relayErr)
				}
				recordOpenaiResponseConsumption(c.Request.Context(), 1, 1, 1, "gpt-4o", "test", usage.InputTokens, usage.OutputTokens, usage.TotalTokens, 2, 0, 10, "/v1/responses", 1, tc.stream, c.Copy(), usage, 0, 1, 1)
			} else {
				usage, relayErr := (&openai.Adaptor{}).DoResponse(c, resp, meta)
				if relayErr != nil {
					t.Fatalf("response handler failed: %+v", relayErr)
				}
				postConsumeQuota(c.Request.Context(), c.Copy(), usage, meta, &relaymodel.GeneralOpenAIRequest{Model: "gpt-4o", ServiceTier: "priority"}, 1, 0, 1, 1, 1, "", "", 0)
			}
			var log dbmodel.Log
			if err := db.Last(&log).Error; err != nil {
				t.Fatal(err)
			}
			var details map[string]any
			for _, part := range strings.Split(log.Other, ";") {
				if strings.HasPrefix(part, "usageDetails:") {
					if err := json.Unmarshal([]byte(strings.TrimPrefix(part, "usageDetails:")), &details); err != nil {
						t.Fatal(err)
					}
				}
			}
			if details == nil {
				t.Fatalf("log missing usageDetails: %s", log.Other)
			}
			got, exists := details["service_tier"]
			if tc.want == "" {
				if exists {
					t.Fatalf("log invented service_tier: %s", log.Other)
				}
			} else if got != tc.want {
				t.Fatalf("logged service_tier = %v, want %q: %s", got, tc.want, log.Other)
			}
			if strings.Contains(tc.body, "cached_tokens") && details["cache_read_input_tokens"] != float64(2) {
				t.Fatalf("existing cache usage lost: %s", log.Other)
			}
			if log.PromptTokens != 10 {
				t.Fatalf("logged prompt tokens = %d, want 10", log.PromptTokens)
			}
			if !tc.stream && writer.Body.String() != tc.body {
				t.Fatalf("response changed: %s", writer.Body.String())
			}
			if tc.stream && tc.want != "" && !strings.Contains(writer.Body.String(), `"service_tier":"`+tc.want+`"`) {
				t.Fatalf("stream dropped service_tier: %s", writer.Body.String())
			}
			if err := db.Delete(&log).Error; err != nil {
				t.Fatal(err)
			}
		})
	}
}

// 是否记录取决于响应处理器，而非模型名称或请求中的 service_tier。
func TestServiceTierOtherModelUsageLogs(t *testing.T) {
	db := setupServiceTierLogDB(t)
	for _, tc := range []struct {
		name    string
		model   string
		adaptor channel.Adaptor
		records bool
	}{
		{"openai_compatible", "qwen-plus", &openai.Adaptor{}, true},
		{"ali", "qwen-plus", &ali.Adaptor{}, true},
		{"zhipu_v4", "glm-4", &zhipu.Adaptor{APIVersion: "v4"}, true},
		{"xai_independent", "grok-4", &xai.Adaptor{}, false},
		{"minimax_independent", "MiniMax-M2", &minimax.Adaptor{}, false},
	} {
		for _, stream := range []bool{false, true} {
			mode := "normal"
			if stream {
				mode = "stream"
			}
			for _, tier := range []string{"", "priority"} {
				t.Run(tc.name+"/"+mode+"/"+tier, func(t *testing.T) {
					writer := &serviceTierRecorder{ResponseRecorder: httptest.NewRecorder(), closed: make(chan bool)}
					c, _ := gin.CreateTestContext(writer)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
					meta := &util.RelayMeta{Mode: constant.RelayModeChatCompletions, IsStream: stream, ActualModelName: tc.model, OriginModelName: tc.model, PromptTokens: 10, UserId: 1, TokenId: 1, ChannelId: 1, Group: "default"}
					body := `{"object":"chat.completion","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}`
					if tier != "" {
						body += `,"service_tier":"` + tier + `"`
					}
					body += `}`
					if stream {
						body = serviceTierSSE(body)
					}
					resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
					usage, relayErr := tc.adaptor.DoResponse(c, resp, meta)
					if relayErr != nil || usage == nil {
						t.Fatalf("response handler failed: err=%+v usage=%+v", relayErr, usage)
					}
					postConsumeQuota(c.Request.Context(), c.Copy(), usage, meta, &relaymodel.GeneralOpenAIRequest{Model: tc.model, ServiceTier: "flex"}, 1, 0, 1, 1, 1, "", "", 0)
					var log dbmodel.Log
					if err := db.Last(&log).Error; err != nil {
						t.Fatal(err)
					}
					var details map[string]any
					for _, part := range strings.Split(log.Other, ";") {
						if strings.HasPrefix(part, "usageDetails:") {
							if err := json.Unmarshal([]byte(strings.TrimPrefix(part, "usageDetails:")), &details); err != nil {
								t.Fatal(err)
							}
						}
					}
					if details == nil {
						t.Fatalf("missing usageDetails: %s", log.Other)
					}
					if got, exists := details["service_tier"]; tc.records && tier != "" {
						if got != tier {
							t.Fatalf("logged service_tier = %v, want %q", got, tier)
						}
					} else if exists {
						t.Fatalf("unexpected service_tier: %s", log.Other)
					}
					if err := db.Delete(&log).Error; err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func setupServiceTierLogDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&dbmodel.User{}, &dbmodel.Token{}, &dbmodel.Channel{}, &dbmodel.Log{}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&dbmodel.User{Id: 1, Username: "service-tier-test", Quota: 1000000},
		&dbmodel.Token{Id: 1, UserId: 1, Key: "test", RemainQuota: 1000000},
		&dbmodel.Channel{Id: 1},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	oldDB, oldLogDB := dbmodel.DB, dbmodel.LOG_DB
	oldRedis, oldBatch := common.RedisEnabled, config.BatchUpdateEnabled
	oldLog, oldMetrics, oldApproximate := config.LogConsumeEnabled, config.ModelMetricsEnabled, config.ApproximateTokenEnabled
	dbmodel.DB, dbmodel.LOG_DB = db, db
	common.RedisEnabled, config.BatchUpdateEnabled = false, false
	config.LogConsumeEnabled, config.ModelMetricsEnabled, config.ApproximateTokenEnabled = true, false, true
	t.Cleanup(func() {
		dbmodel.DB, dbmodel.LOG_DB = oldDB, oldLogDB
		common.RedisEnabled, config.BatchUpdateEnabled = oldRedis, oldBatch
		config.LogConsumeEnabled, config.ModelMetricsEnabled, config.ApproximateTokenEnabled = oldLog, oldMetrics, oldApproximate
	})
	return db
}
