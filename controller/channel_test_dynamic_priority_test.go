package controller

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/songquanpeng/one-api/common/metrics"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/util"
)

// 模拟评分窗口的读写，集成测试无需启动外部 Redis 或访问真实上游。
type testPriorityRedisHook struct {
	server    *miniredis.Miniredis
	writeFail bool
	readFail  bool
}

func (h *testPriorityRedisHook) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	return ctx, nil
}
func (h *testPriorityRedisHook) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (h *testPriorityRedisHook) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	for _, cmd := range cmds {
		if h.writeFail && cmd.Name() == "zadd" || h.readFail && cmd.Name() == "zrangebyscore" {
			return ctx, errors.New("模拟 Redis 不可用")
		}
	}
	return ctx, nil
}
func (h *testPriorityRedisHook) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func setupTestPriority(t *testing.T) *testPriorityRedisHook {
	t.Helper()
	setupRetryPolicyDB(t, false)
	oldRDB, oldRedis := common.RDB, common.RedisEnabled
	oldEnabled, oldLog := config.DynamicPriorityEnabled, config.LogConsumeEnabled
	oldSuccess, oldLatency, oldPrice := config.DynamicPriorityWeightSuccess, config.DynamicPriorityWeightLatency, config.DynamicPriorityWeightPrice
	oldClient := util.HTTPClient
	hook := &testPriorityRedisHook{server: miniredis.RunT(t)}
	client := redis.NewClient(&redis.Options{Addr: hook.server.Addr()})
	client.AddHook(hook)
	common.RDB, common.RedisEnabled = client, true
	config.DynamicPriorityEnabled, config.LogConsumeEnabled = true, false
	config.DynamicPriorityWeightSuccess, config.DynamicPriorityWeightLatency, config.DynamicPriorityWeightPrice = 100, 0, 0
	util.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	t.Cleanup(func() {
		common.RDB, common.RedisEnabled = oldRDB, oldRedis
		config.DynamicPriorityEnabled, config.LogConsumeEnabled = oldEnabled, oldLog
		config.DynamicPriorityWeightSuccess, config.DynamicPriorityWeightLatency, config.DynamicPriorityWeightPrice = oldSuccess, oldLatency, oldPrice
		util.HTTPClient = oldClient
		_ = client.Close()
	})
	return hook
}

func seedTestPriorityChannel(t *testing.T, modelName string, upstream http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	seedRetryChannel(t, 1, common.ChannelTypeOpenAI, "", 0)
	if err := model.DB.Model(&model.Channel{}).Where("id = ?", 1).Updates(map[string]any{
		"models": modelName, "base_url": server.URL, "key": "test-key",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.Ability{}).Where("channel_id = ?", 1).Update("model", modelName).Error; err != nil {
		t.Fatal(err)
	}
}

func runTestPriorityHandler(t *testing.T, modelName string, query bool) map[string]any {
	t.Helper()
	method, path, body := http.MethodPost, "/api/channel/test/1", `{"model":"`+modelName+`"}`
	if query {
		method, path, body = http.MethodGet, path+"?model="+modelName, ""
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	TestChannel(c)
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("测试接口响应不是合法 JSON：%s，错误：%v", w.Body.String(), err)
	}
	return result
}

func TestManualModelTestGeneratesDynamicPriority(t *testing.T) {
	for _, tc := range []struct {
		name, modelName, body string
		status                int
		query                 bool
	}{
		{"聊天成功", "gpt-test", `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`, 200, false},
		{"查询参数指定模型", "gpt-test", `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`, 200, true},
		{"聊天失败", "gpt-test", `{"error":{"message":"upstream unavailable","type":"server_error"}}`, 503, false},
		{"Responses成功", "gpt-codex", `{"id":"resp_test","status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`, 200, false},
		{"Responses无用量", "gpt-codex", `{"id":"resp_test","status":"completed"}`, 200, false},
		{"Responses失败", "gpt-codex", `{"error":{"message":"upstream unavailable","type":"server_error"}}`, 503, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hook := setupTestPriority(t)
			seedTestPriorityChannel(t, tc.modelName, func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["model"] != tc.modelName {
					t.Errorf("上游请求模型不匹配：%v，错误：%v", body, err)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			unchanged := int64(77)
			for _, ability := range []model.Ability{
				{ChannelId: 1, Model: tc.modelName, Group: "vip", Enabled: false, DynamicPriority: &unchanged},
				{ChannelId: 1, Model: "other-model", Group: "default", Enabled: true, DynamicPriority: &unchanged},
				{ChannelId: 2, Model: tc.modelName, Group: "default", Enabled: true, DynamicPriority: &unchanged},
			} {
				if err := model.DB.Create(&ability).Error; err != nil {
					t.Fatal(err)
				}
			}
			result := runTestPriorityHandler(t, tc.modelName, tc.query)
			if result["success"] != (tc.status == 200) || result["dynamic_priority_updated"] != true {
				t.Fatalf("测试结果或评分更新不符合预期：%v", result)
			}
			want := float64(58)
			if tc.status != 200 {
				want = 41
			}
			if result["dynamic_priority"] != want || !strings.Contains(result["message"].(string), "动态优先级已更新") {
				t.Fatalf("未即时返回新评分：%v", result)
			}
			members, sampleErr := hook.server.ZMembers(metrics.AbilityMetricKeyPrefix + "1:" + tc.modelName)
			if sampleErr != nil || len(members) != 1 {
				t.Fatalf("单次测试只能记录一个样本：%v，错误：%v", members, sampleErr)
			}
			var abilities []model.Ability
			if err := model.DB.Find(&abilities).Error; err != nil {
				t.Fatal(err)
			}
			for _, ability := range abilities {
				wantScore := unchanged
				if ability.ChannelId == 1 && ability.Model == tc.modelName {
					wantScore = int64(want)
				}
				if ability.DynamicPriority == nil || *ability.DynamicPriority != wantScore {
					t.Fatalf("评分更新范围错误：%+v", ability)
				}
				if ability.Group == "vip" && ability.Enabled {
					t.Fatal("测试评分不应启用被禁用的模型")
				}
			}
		})
	}
}

func TestManualModelTestPriorityFailureDoesNotFailTest(t *testing.T) {
	for _, scenario := range []string{"关闭评分", "未启用Redis", "Redis写入失败", "Redis读取失败"} {
		t.Run(scenario, func(t *testing.T) {
			hook := setupTestPriority(t)
			seedTestPriorityChannel(t, "gpt-codex", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"id":"resp_test","status":"completed"}`))
			})
			switch scenario {
			case "关闭评分":
				config.DynamicPriorityEnabled = false
			case "未启用Redis":
				common.RedisEnabled = false
			case "Redis写入失败":
				hook.writeFail = true
			case "Redis读取失败":
				hook.readFail = true
			}
			result := runTestPriorityHandler(t, "gpt-codex", false)
			if result["success"] != true || result["dynamic_priority_updated"] != false || result["dynamic_priority"] != nil {
				t.Fatalf("评分失败不应改变测试成功状态：%v", result)
			}
			var ability model.Ability
			if err := model.DB.First(&ability).Error; err != nil || ability.DynamicPriority == nil || *ability.DynamicPriority != 0 {
				t.Fatalf("评分失败不应覆盖已有值：%+v，错误：%v", ability, err)
			}
		})
	}
}

func TestManualModelTestRequiresExactModel(t *testing.T) {
	hook := setupTestPriority(t)
	seedTestPriorityChannel(t, "gpt-codex", func(http.ResponseWriter, *http.Request) {
		t.Error("不支持的模型不应发送上游请求")
	})
	result := runTestPriorityHandler(t, "codex", false)
	if result["success"] != false || result["dynamic_priority_updated"] != nil || len(hook.server.Keys()) != 0 {
		t.Fatalf("模型子串不能通过校验或生成评分：%v", result)
	}
}

func TestManualModelTestUsesOriginalModelAndWindow(t *testing.T) {
	hook := setupTestPriority(t)
	seedTestPriorityChannel(t, "gpt-alias", func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request["model"] != "gpt-upstream" {
			t.Errorf("测试应调用映射后的上游模型：%v，错误：%v", request, err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	})
	if err := model.DB.Model(&model.Channel{}).Where("id = ?", 1).
		Update("model_mapping", `{"gpt-alias":"gpt-upstream"}`).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := metrics.RecordAbilityMetric(context.Background(), metrics.AbilityMetric{ChannelId: 1, Model: "gpt-alias", Success: false}); err != nil {
			t.Fatal(err)
		}
	}
	result := runTestPriorityHandler(t, "gpt-alias", false)
	if result["success"] != true || result["dynamic_priority_updated"] != true || result["dynamic_priority"] != float64(38) {
		t.Fatalf("本次测试应与历史失败合并评分，不能重置窗口：%v", result)
	}
	members, err := hook.server.ZMembers(metrics.AbilityMetricKeyPrefix + "1:gpt-alias")
	if err != nil || len(members) != 4 || hook.server.Exists(metrics.AbilityMetricKeyPrefix+"1:gpt-upstream") {
		t.Fatalf("应在原始模型窗口中保留三次失败和一次成功：%v，错误：%v", members, err)
	}
}

func TestAutomaticChannelTestRecordsSingleSample(t *testing.T) {
	for _, modelName := range []string{"gpt-test", "gpt-codex"} {
		t.Run(modelName, func(t *testing.T) {
			hook := setupTestPriority(t)
			seedTestPriorityChannel(t, modelName, func(w http.ResponseWriter, _ *http.Request) {
				body := `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
				if isCodexModel(modelName) {
					body = `{"id":"resp_test","status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
				}
				_, _ = w.Write([]byte(body))
			})
			channel, err := model.GetChannelById(1, true)
			if err != nil {
				t.Fatal(err)
			}
			if err, _, _, _ := testChannel(channel, modelName, true); err != nil {
				t.Fatal(err)
			}
			members, err := hook.server.ZMembers(metrics.AbilityMetricKeyPrefix + "1:" + modelName)
			if err != nil || len(members) != 1 {
				t.Fatalf("自动测试仍应且仅应记录一次成功：%v，错误：%v", members, err)
			}
		})
	}
}

func TestManualModelTestDoesNotCreateAbility(t *testing.T) {
	hook := setupTestPriority(t)
	seedTestPriorityChannel(t, "gpt-codex", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"resp_test","status":"completed"}`))
	})
	if err := model.DB.Model(&model.Ability{}).Where("channel_id = ?", 1).Update("model", "other-model").Error; err != nil {
		t.Fatal(err)
	}
	result := runTestPriorityHandler(t, "gpt-codex", false)
	var count int64
	if err := model.DB.Model(&model.Ability{}).Where("channel_id = ? AND model = ?", 1, "gpt-codex").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if result["success"] != true || result["dynamic_priority_updated"] != false || count != 0 || len(hook.server.Keys()) != 0 {
		t.Fatalf("测试评分不能创建分发能力：%v，新增行数：%d", result, count)
	}
}
