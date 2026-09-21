package controller

import (
	"context"
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
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestFluxVideoUpscaleLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name                                  string
		background, failed, webhook, callback bool
	}{
		{"客户端完成", false, false, false, false},
		{"后台完成", true, false, false, false},
		{"客户端失败退款", false, true, false, false},
		{"后台失败退款", true, true, false, false},
		{"回调提交无轮询地址时客户端查询", false, false, true, false},
		{"回调提交无轮询地址时后台查询", true, false, true, false},
		{"本站回调完成", false, false, false, true},
		{"本站回调失败退款", false, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FLUX_WEBHOOK_SECRET", "test +&secret")
			db := setupFluxVideoLifecycleDB(t)
			if tc.callback {
				config.ServerAddress = "https://one-api.example/"
			}

			var terminal atomic.Bool
			var pollCalls atomic.Int32
			const sample = "https://delivery.example/sample.mp4?se=signed"
			pollHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				pollCalls.Add(1)
				if r.URL.Path != "/v1/get_result" || r.URL.Query().Get("id") != "upscale-task" || r.Header.Get("x-key") != "upstream-key" {
					t.Error("上游轮询请求不正确")
				}
				body := map[string]any{"id": "upscale-task", "status": "Pending"}
				if terminal.Load() {
					if tc.failed {
						body["status"], body["details"] = "Error", "视频处理失败"
					} else {
						body["status"], body["result"], body["cost"] = "Ready", map[string]string{"sample": sample}, 20
					}
				}
				_ = json.NewEncoder(w).Encode(body)
			})
			poller := httptest.NewServer(pollHandler)
			defer poller.Close()
			upstreamPollingURL := poller.URL + "/v1/get_result?id=upscale-task"
			submitter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if (tc.webhook || tc.callback) && r.Method == http.MethodGet {
					pollHandler.ServeHTTP(w, r)
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/v1/flux-tools/video-upscale-v1" {
					t.Error("应使用提交响应提供的另一台服务器轮询")
					w.WriteHeader(http.StatusNotFound)
					return
				}
				var input flux.FluxVideoUpscaleRequest
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				if tc.callback {
					if input.WebhookURL == nil || *input.WebhookURL != "https://one-api.example/flux/internal/callback?key=test+%2B%26secret" {
						t.Error("未默认使用本站回调地址及密钥")
					}
					_ = json.NewEncoder(w).Encode(gin.H{"id": "upscale-task", "status": "Pending", "webhook_url": input.WebhookURL})
					return
				}
				if tc.webhook {
					if input.WebhookURL == nil || *input.WebhookURL != "https://client.example/callback" || input.WebhookSecret == nil || *input.WebhookSecret != "test-secret" {
						t.Error("回调参数没有透传")
					}
					// BFL 的回调提交响应可能只提供 id、status 和 webhook_url。
					_ = json.NewEncoder(w).Encode(gin.H{"id": "upscale-task", "status": "Pending", "webhook_url": input.WebhookURL})
					return
				}
				_ = json.NewEncoder(w).Encode(flux.FluxVideoSubmitResponse{ID: "upscale-task", PollingURL: upstreamPollingURL})
			}))
			defer submitter.Close()
			for _, record := range []any{
				&dbmodel.User{Id: 1, Username: "upscale-test", Status: common.UserStatusEnabled, Quota: 1000000},
				&dbmodel.Token{Id: 1, UserId: 1, Key: "clientkey", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 1000000},
				&dbmodel.Channel{Id: 1, Key: "upstream-key", Type: common.ChannelTypeFlux, BaseURL: &submitter.URL},
			} {
				if err := db.Create(record).Error; err != nil {
					t.Fatal(err)
				}
			}
			router := gin.New()
			router.POST("/flux/internal/callback", HandleFluxCallback)
			router.Use(middleware.TokenAuth())
			router.POST("/v1/flux-tools/video-upscale-v1", func(c *gin.Context) {
				c.Set("channel_id", 1)
				c.Set("channel", common.ChannelTypeFlux)
				c.Set("base_url", submitter.URL)
				c.Set("original_model", flux.VideoUpscaleModel)
				RelayVideoGenerate(c)
			})
			router.GET("/flux/v1/get_result", GetFlux)
			call := func(method, path, body string) map[string]any {
				t.Helper()
				req := httptest.NewRequest(method, path, strings.NewReader(body))
				req.Header.Set("x-key", "clientkey")
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Fatalf("请求失败：status=%d body=%s", w.Code, w.Body.String())
				}
				var result map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				return result
			}
			reconcile := func() {
				t.Helper()
				wantCalls := pollCalls.Load() + 1
				runFluxVideoReconcile(context.Background())
				// 扫描异步启动查询，等该轮查询和结算完成后再推进上游状态。
				deadline := time.Now().Add(3 * time.Second)
				for pollCalls.Load() < wantCalls || len(fluxVideoQuerySem) != 0 {
					if time.Now().After(deadline) {
						t.Fatal("后台轮询未及时完成")
					}
					time.Sleep(time.Millisecond)
				}
			}
			body := `{"input_video":"https://example.com/source.mp4","upscale_factor":2.0,"creativity":1,"prompt":"渔夫站在岸边"}`
			if tc.webhook {
				body = `{"input_video":"https://example.com/source.mp4","prompt":"渔夫站在岸边","webhook_url":"https://client.example/callback","webhook_secret":"test-secret"}`
			}
			submitted := call(http.MethodPost, "/v1/flux-tools/video-upscale-v1", body)
			const clientPollingURL = "/flux/v1/get_result?id=upscale-task"
			wantPollingURL := strings.TrimRight(config.ServerAddress, "/") + clientPollingURL
			if submitted["id"] != "upscale-task" || submitted["polling_url"] != wantPollingURL || len(submitted) != 2 {
				t.Fatalf("提交响应必须是原生 id/polling_url：%v", submitted)
			}
			task, err := dbmodel.GetVideoTaskById("upscale-task")
			wantCredentials := upstreamPollingURL
			if tc.webhook || tc.callback {
				wantCredentials = ""
			}
			if err != nil || task.Provider != "flux" || task.Model != "video-upscale-v1" || task.Prompt != "渔夫站在岸边" || task.Status != "processing" || task.Credentials != wantCredentials {
				t.Fatalf("视频任务未正确保存：task=%+v err=%v", task, err)
			}
			if tc.background {
				reconcile()
			} else if pending := call(http.MethodGet, clientPollingURL, ""); pending["status"] != "Pending" {
				t.Fatalf("应保留上游 Pending 状态：%v", pending)
			}
			terminal.Store(true)
			if tc.callback {
				callbackBody := `{"id":"upscale-task","status":"Ready","cost":20,"result":{"sample":"` + sample + `"}}`
				if tc.failed {
					callbackBody = `{"id":"upscale-task","status":"Content Moderated","details":{"reason":"视频未通过审核"}}`
				}
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/flux/internal/callback?key=wrong", strings.NewReader(callbackBody)))
				if w.Code != http.StatusUnauthorized {
					t.Fatalf("错误回调密钥应被拒绝，实际=%d", w.Code)
				}
				const callbackURL = "/flux/internal/callback?key=test+%2B%26secret"
				call(http.MethodPost, callbackURL, `{"task_id":"upscale-task","status":"Pending","progress":50}`)
				call(http.MethodPost, callbackURL, callbackBody)
				call(http.MethodPost, callbackURL, callbackBody)
				// 迟到的进度或相反终态回调不能反转结果或重复扣退费。
				call(http.MethodPost, callbackURL, `{"task_id":"upscale-task","status":"Pending"}`)
				call(http.MethodPost, callbackURL, `{"id":"upscale-task","status":"Error"}`)
				call(http.MethodPost, callbackURL, `{"id":"upscale-task","status":"Ready","cost":99,"result":{"sample":"https://example.com/late.mp4"}}`)
			}
			if tc.background {
				reconcile()
			}
			result := call(http.MethodGet, clientPollingURL, "")
			wantStatus, wantQuota := "Ready", int64(100000)
			if tc.failed {
				wantStatus, wantQuota = "Error", 0
				if tc.callback {
					wantStatus = "Content Moderated"
				}
			}
			if result["id"] != "upscale-task" || result["status"] != wantStatus {
				t.Fatalf("终态响应不正确：%v", result)
			}
			if !tc.failed {
				video, ok := result["result"].(map[string]any)
				if !ok || video["sample"] != sample {
					t.Fatalf("缺少 result.sample：%v", result)
				}
			}
			// 终态重复查询和后台扫描均不能再次访问上游或再次扣退费。
			call(http.MethodGet, clientPollingURL, "")
			runFluxVideoReconcile(context.Background())
			wantCalls := int32(2)
			if tc.callback {
				wantCalls = 1
			}
			if pollCalls.Load() != wantCalls {
				t.Fatalf("上游查询次数不正确：实际=%d，期望=%d", pollCalls.Load(), wantCalls)
			}
			var user dbmodel.User
			if err := db.First(&user, 1).Error; err != nil {
				t.Fatal(err)
			}
			if user.Quota != 1000000-wantQuota {
				t.Fatalf("费用结算不正确：剩余=%d，期望=%d", user.Quota, 1000000-wantQuota)
			}
		})
	}
}

func setupFluxVideoLifecycleDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&dbmodel.Video{}, &dbmodel.Image{}, &dbmodel.User{}, &dbmodel.Token{}, &dbmodel.Channel{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	oldDB, oldRedis := dbmodel.DB, common.RedisEnabled
	oldBatch, oldLog := config.BatchUpdateEnabled, config.LogConsumeEnabled
	oldAddress, oldUnit := config.ServerAddress, config.QuotaPerUnit
	dbmodel.DB, common.RedisEnabled = db, false
	config.BatchUpdateEnabled, config.LogConsumeEnabled = false, false
	config.ServerAddress, config.QuotaPerUnit = "", 500000
	t.Cleanup(func() {
		dbmodel.DB, common.RedisEnabled = oldDB, oldRedis
		config.BatchUpdateEnabled, config.LogConsumeEnabled = oldBatch, oldLog
		config.ServerAddress, config.QuotaPerUnit = oldAddress, oldUnit
		_ = sqlDB.Close()
	})
	return db
}
