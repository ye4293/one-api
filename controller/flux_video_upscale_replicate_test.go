package controller

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/middleware"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/flux"
)

func TestReplicateVideoUpscaleLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, status         string
		background, callback bool
	}{
		{"客户端查询成功", "succeeded", false, false},
		{"后台轮询成功", "succeeded", true, false},
		{"客户端查询失败退款", "failed", false, false},
		{"后台轮询取消退款", "canceled", true, false},
		{"回调成功", "succeeded", false, true},
		{"回调失败退款", "failed", false, true},
		{"回调取消退款", "canceled", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupFluxVideoLifecycleDB(t)
			config.ServerAddress = "https://one-api.example"
			const signingKey = "replicate-test-signing-key"
			t.Setenv("REPLICATE_WEBHOOK_SIGNING_KEY", "whsec_"+base64.StdEncoding.EncodeToString([]byte(signingKey)))
			var terminal atomic.Bool
			var pollCalls atomic.Int32
			const sample = "https://replicate.delivery/video.mp4"
			terminalBody := map[string]any{"id": "replicate-task", "status": tc.status}
			wantStatus, wantQuota := "Error", int64(0)
			if tc.status == "succeeded" {
				terminalBody["output"], terminalBody["cost"] = sample, 20
				wantStatus, wantQuota = "Ready", 100000
			} else {
				terminalBody["error"] = "视频处理失败"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer replicate-key" {
					t.Error("Replicate 请求没有使用渠道密钥")
				}
				switch r.Method + " " + r.URL.Path {
				case "POST /replicate.com/v1/models/black-forest-labs/flux-video-upscale/predictions":
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body["webhook"] != "https://one-api.example/flux/internal/replicate/callback" {
						t.Error("默认回调没有使用本站 Replicate 入口")
					}
					_ = json.NewEncoder(w).Encode(gin.H{"id": "replicate-task", "status": "starting"})
				case "GET /replicate.com/v1/predictions/replicate-task":
					pollCalls.Add(1)
					if terminal.Load() {
						_ = json.NewEncoder(w).Encode(terminalBody)
					} else {
						_ = json.NewEncoder(w).Encode(gin.H{"id": "replicate-task", "status": "starting", "output": nil})
					}
				default:
					t.Errorf("意外的上游请求：%s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			baseURL := server.URL + "/replicate.com"
			for _, record := range []any{
				&dbmodel.User{Id: 1, Username: "replicate-test", Status: common.UserStatusEnabled, Quota: 1000000},
				&dbmodel.Token{Id: 1, UserId: 1, Key: "clientkey", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 1000000},
				&dbmodel.Channel{Id: 1, Key: "replicate-key", Type: common.ChannelTypeFlux, BaseURL: &baseURL},
			} {
				if err := db.Create(record).Error; err != nil {
					t.Fatal(err)
				}
			}
			const callbackPath = "/flux/internal/replicate/callback"
			const queryPath = "/flux/v1/get_result?id=replicate-task"
			router := gin.New()
			router.POST(callbackPath, HandleReplicateCallback)
			router.Use(middleware.TokenAuth())
			router.POST("/v1/flux-tools/video-upscale-v1", func(c *gin.Context) {
				c.Set("channel_id", 1)
				c.Set("channel", common.ChannelTypeFlux)
				c.Set("base_url", baseURL)
				c.Set("original_model", flux.VideoUpscaleModel)
				RelayVideoGenerate(c)
			})
			router.GET("/flux/v1/get_result", GetFlux)
			call := func(method, path, body string) map[string]any {
				t.Helper()
				req := httptest.NewRequest(method, path, strings.NewReader(body))
				req.Header.Set("x-key", "clientkey")
				req.Header.Set("Content-Type", "application/json")
				if path == callbackPath {
					req.Header.Set("webhook-id", "event-id")
					req.Header.Set("webhook-timestamp", "1700000000")
					mac := hmac.New(sha256.New, []byte(signingKey))
					_, _ = mac.Write([]byte("event-id.1700000000." + body))
					req.Header.Set("webhook-signature", "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
				}
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Fatalf("请求失败：%d %s", w.Code, w.Body.String())
				}
				var result map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				return result
			}
			submitted := call(http.MethodPost, "/v1/flux-tools/video-upscale-v1", `{"input_video":"https://example.com/source.mp4","creativity":0}`)
			if submitted["id"] != "replicate-task" || submitted["polling_url"] != "https://one-api.example"+queryPath {
				t.Fatalf("本站提交响应不正确：%v", submitted)
			}
			if pending := call(http.MethodGet, queryPath, ""); pending["status"] != "Pending" {
				t.Fatalf("starting 应转换为 Pending：%v", pending)
			}
			terminal.Store(true)
			if tc.callback {
				body, _ := json.Marshal(terminalBody)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, callbackPath, strings.NewReader(string(body))))
				if w.Code != http.StatusUnauthorized {
					t.Fatal("没有有效签名的回调应被拒绝")
				}
				call(http.MethodPost, callbackPath, string(body))
				call(http.MethodPost, callbackPath, string(body))
				call(http.MethodPost, callbackPath, `{"id":"replicate-task","status":"processing"}`)
			} else if tc.background {
				runFluxVideoReconcile(context.Background())
				deadline := time.Now().Add(3 * time.Second)
				for pollCalls.Load() < 2 || len(fluxVideoQuerySem) != 0 {
					if time.Now().After(deadline) {
						t.Fatal("后台轮询未及时结束")
					}
					time.Sleep(time.Millisecond)
				}
			}
			result := call(http.MethodGet, queryPath, "")
			if result["id"] != "replicate-task" || result["status"] != wantStatus {
				t.Fatalf("终态未正确转换：%v", result)
			}
			if tc.status == "succeeded" {
				video, ok := result["result"].(map[string]any)
				if !ok || video["sample"] != sample {
					t.Fatalf("缺少 result.sample：%v", result)
				}
			}
			call(http.MethodGet, queryPath, "")
			runFluxVideoReconcile(context.Background())
			wantCalls := int32(2)
			if tc.callback {
				wantCalls = 1
			}
			if pollCalls.Load() != wantCalls {
				t.Fatalf("终态不应再次访问上游：次数=%d", pollCalls.Load())
			}
			task, err := dbmodel.GetVideoTaskById("replicate-task")
			if err != nil || task.Model != "flux-upscale" || task.Provider != "flux" || !strings.Contains(task.Result, tc.status) {
				t.Fatalf("任务模型或原始结果保存不正确：%+v err=%v", task, err)
			}
			var user dbmodel.User
			if err := db.First(&user, 1).Error; err != nil {
				t.Fatal(err)
			}
			if user.Quota != 1000000-wantQuota {
				t.Fatalf("费用结算不正确：余额=%d 期望=%d", user.Quota, 1000000-wantQuota)
			}
		})
	}
}
