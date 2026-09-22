package flux

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	dbmodel "github.com/songquanpeng/one-api/model"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupVideoPricing(t *testing.T) {
	t.Helper()
	oldRules, oldUnit := common.VideoPricingRules2JSONString(), config.QuotaPerUnit
	config.QuotaPerUnit = 500000
	err := common.UpdateVideoPricingRulesByJSONString(`[
		{"model":"flux-3-video","type":"*","mode":"*","duration":"*","resolution":"hd","sound":"*","pricing_type":"per_second","price":0.2,"currency":"USD","priority":10},
		{"model":"flux-3-video","type":"*","mode":"*","duration":"*","resolution":"fhd","sound":"*","pricing_type":"per_second","price":0.4,"currency":"USD","priority":10},
		{"model":"flux-3-video","type":"*","mode":"fixed","duration":"*","resolution":"*","sound":"*","pricing_type":"fixed","price":0.7,"currency":"USD","priority":20}
	]`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		config.QuotaPerUnit = oldUnit
		_ = common.UpdateVideoPricingRulesByJSONString(oldRules)
	})
}

func TestReplicateVideoResultUsesOutputDuration(t *testing.T) {
	setupVideoPricing(t)
	for _, tc := range []struct {
		name, body, mode, duration string
		quota                      *int64
		cost                       float64
	}{
		{"真实指标精度按美分结算", `{"metrics":{"video_output_duration_seconds":10.042,"resolution_target":"720p"}}`, "t2v", "10.042", quotaPointer(1005000), 0},
		{"自动时长含小数", `{"metrics":{"video_output_duration_seconds":10.25,"resolution_target":"720p","predict_time":95}}`, "t2v", "10.25", quotaPointer(1025000), 0},
		{"实际全高清", `{"metrics":{"video_output_duration_seconds":10.25,"resolution_target":"1080p"}}`, "t2v", "10.25", quotaPointer(2050000), 0},
		{"不能将计算耗时当成视频时长", `{"metrics":{"predict_time":95}}`, "t2v", "auto", nil, 0},
		{"零时长保持预扣", `{"metrics":{"video_output_duration_seconds":0}}`, "t2v", "auto", nil, 0},
		{"负时长保持预扣", `{"metrics":{"video_output_duration_seconds":-1}}`, "t2v", "auto", nil, 0},
		{"上游费用优先", `{"cost":170,"metrics":{"video_output_duration_seconds":10.25}}`, "t2v", "10.25", nil, 170},
		{"固定价不乘时长", `{"metrics":{"video_output_duration_seconds":10.25}}`, "fixed", "10.25", quotaPointer(350000), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/predictions/task" || r.Header.Get("Authorization") != "Bearer test-key" {
					t.Error("Replicate 查询路径或鉴权不正确")
				}
				var body map[string]any
				_ = json.Unmarshal([]byte(tc.body), &body)
				body["id"], body["status"], body["output"] = "task", "succeeded", "https://example.com/video.mp4"
				_ = json.NewEncoder(w).Encode(body)
			}))
			defer server.Close()
			task := &dbmodel.Video{TaskId: "task", Model: "flux-3-video", Type: "text-to-video", Mode: tc.mode, Duration: "auto", Resolution: "hd", Sound: "on"}
			result, err := (&VideoAdaptor{}).handleReplicateVideoResult(task, &dbmodel.Channel{Key: "test-key"}, server.URL)
			if err != nil {
				t.Fatal(err)
			}
			if result.TaskStatus != "succeed" || result.Duration != tc.duration || result.UpstreamCost != tc.cost {
				t.Fatalf("实际结果=%+v", result)
			}
			if tc.quota == nil {
				if result.FluxActualQuota != nil {
					t.Fatalf("不应按时长结算，实际=%d", *result.FluxActualQuota)
				}
			} else if result.FluxActualQuota == nil {
				t.Fatal("应当按实际时长结算")
			} else if *result.FluxActualQuota != *tc.quota {
				t.Fatalf("期望配额=%d，实际=%d", *tc.quota, *result.FluxActualQuota)
			}
		})
	}
}

func quotaPointer(n int64) *int64 { return &n }

func setupVideoSettlementDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&dbmodel.Video{}, &dbmodel.User{}, &dbmodel.Token{}, &dbmodel.Channel{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	oldDB, oldBatch, oldLog := dbmodel.DB, config.BatchUpdateEnabled, config.LogConsumeEnabled
	dbmodel.DB, config.BatchUpdateEnabled, config.LogConsumeEnabled = db, false, false
	t.Cleanup(func() {
		dbmodel.DB, config.BatchUpdateEnabled, config.LogConsumeEnabled = oldDB, oldBatch, oldLog
		_ = sqlDB.Close()
	})
	return db
}

func TestVideoSuccessSettlesOnceAcrossConcurrentPollers(t *testing.T) {
	setupVideoPricing(t)
	for _, tc := range []struct {
		name  string
		quota *int64
		cost  float64
		want  int64
	}{
		{"补扣", quotaPointer(1025000), 0, 1025000},
		{"退款", quotaPointer(300000), 0, 300000},
		{"免费规则退还全部预扣", quotaPointer(0), 0, 0},
		{"上游费用优先于估算", quotaPointer(1025000), 170, 850000},
		{"缺失费用和时长保持预扣", nil, 0, 500000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupVideoSettlementDB(t)
			task := dbmodel.Video{TaskId: "task", Status: "processing", Provider: "flux", Model: "flux-3-video", TokenId: 1, UserId: 1, ChannelId: 1, Quota: 500000, Duration: "auto"}
			for _, row := range []any{
				&dbmodel.User{Id: 1, Username: "test", Quota: 2000000, UsedQuota: 500000, RequestCount: 1},
				&dbmodel.Token{Id: 1, UserId: 1, Key: "test-key", RemainQuota: 2000000, UsedQuota: 500000},
				&dbmodel.Channel{Id: 1, UsedQuota: 500000}, &task,
			} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			result := &relaymodel.GeneralFinalVideoResponse{TaskStatus: "succeed", Duration: "10.25", VideoResult: "https://example.com/video.mp4", RawResult: `{"status":"succeeded","output":"https://example.com/video.mp4"}`, FluxActualQuota: tc.quota, UpstreamCost: tc.cost}
			var wg sync.WaitGroup
			outcomes := make(chan bool, 2)
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					staleTask := task
					applied, err := ApplyVideoSuccess(context.Background(), &staleTask, result)
					if err != nil {
						t.Error(err)
					}
					outcomes <- applied
				}()
			}
			wg.Wait()
			close(outcomes)
			wins := 0
			for applied := range outcomes {
				if applied {
					wins++
				}
			}
			if wins != 1 {
				t.Fatalf("只能结算一次，实际=%d", wins)
			}
			stored, err := dbmodel.GetVideoTaskById(task.TaskId)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Quota != tc.want || stored.Duration != "10.25" || stored.Status != "succeed" || stored.StoreUrl != result.VideoResult || stored.Result != result.RawResult {
				t.Fatalf("任务结算结果不正确: %+v", stored)
			}
			var user dbmodel.User
			var token dbmodel.Token
			var channel dbmodel.Channel
			for _, row := range []any{&user, &token, &channel} {
				if err := db.First(row, 1).Error; err != nil {
					t.Fatal(err)
				}
			}
			wantBalance := int64(2000000) - (tc.want - 500000)
			if user.Quota != wantBalance || token.RemainQuota != wantBalance || user.UsedQuota != tc.want || token.UsedQuota != tc.want || channel.UsedQuota != tc.want || user.RequestCount != 1 {
				t.Fatalf("重复扣费或退款: 用户余额=%d 用量=%d 请求数=%d token余额=%d 用量=%d 渠道用量=%d", user.Quota, user.UsedQuota, user.RequestCount, token.RemainQuota, token.UsedQuota, channel.UsedQuota)
			}
		})
	}
}

func TestBFLDraftCacheSurvivesTerminalResult(t *testing.T) {
	task := &dbmodel.Video{TaskId: "draft", Status: "succeed", Result: `{"status":"Ready","result":{"sample":"video.mp4","draft_cache":"https://example.com/cache.bin"}}`}
	result, ok := BuildTerminalResultFromDB(task, "https://api.bfl.ai")
	if !ok {
		t.Fatal("应命中已完成草稿")
	}
	var fields map[string]any
	if err := json.Unmarshal(result.FluxResult, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["draft_cache"] != "https://example.com/cache.bin" {
		t.Fatalf("草稿缓存没有保留: %+v", fields)
	}
}

// TestVideoSuccessRevivesFailedTaskWithCorrectBilling 验证 failed→succeed 复活：
// 迟到的成功回调/轮询可以覆盖已失败（已退款）任务，并使账务逐项对齐「从未失败的成功路径」。
//
// 场景推导（真实原始余额 B0=2000000，预扣 Q0=500000，最终结算 Q_final=1025000）：
//   预扣后：余额1500000 用量500000 请求1 token余额1500000 token用量500000 渠道500000
//   失败退款后（token 不还，与退款侧对称）：余额2000000 用量0 请求0 token余额1500000 token用量500000 渠道0
//   复活 = 撤销退款回到预扣基线 + 差额结算(diff=525000)：
//     余额975000 用量1025000 请求1 token余额975000 token用量1025000 渠道1025000
//   与从未失败的成功路径终态完全一致。
func TestVideoSuccessRevivesFailedTaskWithCorrectBilling(t *testing.T) {
	setupVideoPricing(t)
	db := setupVideoSettlementDB(t)
	// 种子：已失败并已退款（CompensateVideoTaskQuota 只还余额/用量/请求，不还 token）的终态。
	task := dbmodel.Video{TaskId: "revive", Status: "failed", Provider: "flux", Model: "flux-3-video", TokenId: 1, UserId: 1, ChannelId: 1, Quota: 500000, Duration: "auto", FailReason: "flux video task not found (upstream 404)"}
	for _, row := range []any{
		&dbmodel.User{Id: 1, Username: "test", Quota: 2000000, UsedQuota: 0, RequestCount: 0},
		&dbmodel.Token{Id: 1, UserId: 1, Key: "test-key", RemainQuota: 1500000, UsedQuota: 500000},
		&dbmodel.Channel{Id: 1, UsedQuota: 0}, &task,
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	result := &relaymodel.GeneralFinalVideoResponse{
		TaskStatus: "succeed", Duration: "10.25", VideoResult: "https://example.com/video.mp4",
		RawResult: `{"status":"Ready","result":{"sample":"https://example.com/video.mp4"}}`, FluxActualQuota: quotaPointer(1025000),
	}
	staleTask := task
	applied, err := ApplyVideoSuccess(context.Background(), &staleTask, result)
	if err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Fatal("失败任务应被成功结果复活")
	}
	stored, err := dbmodel.GetVideoTaskById(task.TaskId)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "succeed" || stored.Quota != 1025000 || stored.StoreUrl != result.VideoResult || stored.Result != result.RawResult || stored.Duration != "10.25" {
		t.Fatalf("复活后任务状态/结算不正确: %+v", stored)
	}
	assertReviveAccounting := func(stage string) {
		var user dbmodel.User
		var token dbmodel.Token
		var channel dbmodel.Channel
		for _, row := range []any{&user, &token, &channel} {
			if err := db.First(row, 1).Error; err != nil {
				t.Fatal(err)
			}
		}
		if user.Quota != 975000 || token.RemainQuota != 975000 || user.UsedQuota != 1025000 || token.UsedQuota != 1025000 || channel.UsedQuota != 1025000 || user.RequestCount != 1 {
			t.Fatalf("[%s] 复活账务未对齐从未失败路径: 用户余额=%d 用量=%d 请求数=%d token余额=%d 用量=%d 渠道用量=%d",
				stage, user.Quota, user.UsedQuota, user.RequestCount, token.RemainQuota, token.UsedQuota, channel.UsedQuota)
		}
	}
	assertReviveAccounting("首次复活")

	// 幂等：任务已 succeed，再次成功回调/轮询应 no-op，不得重复扣费。
	staleTask2 := task // 仍是失败态的旧内存快照，模拟迟到回调
	applied2, err := ApplyVideoSuccess(context.Background(), &staleTask2, result)
	if err != nil {
		t.Fatal(err)
	}
	if applied2 {
		t.Fatal("已成功任务不应被再次结算")
	}
	assertReviveAccounting("重复回调后")
}

// TestBFLVideoResult404GraceWindow 验证 404 宽限期门控：
//   - 宽限期内（任务年龄 < grace）：返回 processing，不写 404 body 到 RawResult/Message，不判失败。
//   - 宽限期外：返回 failed，保留 404 body 供审计，触发上层退款。
func TestBFLVideoResult404GraceWindow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"Task not found"}`))
	}))
	defer server.Close()

	baseURL := server.URL
	ch := &dbmodel.Channel{Key: "test-key", BaseURL: &baseURL}
	newCtx := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		return c
	}

	t.Run("宽限期内视为瞬时处理中", func(t *testing.T) {
		task := &dbmodel.Video{TaskId: "grace-in", Duration: "auto", CreatedAt: time.Now().Unix()}
		result, apiErr := (&VideoAdaptor{}).HandleVideoResult(newCtx(), task, ch, nil)
		if apiErr != nil {
			t.Fatalf("宽限期内不应返回错误: %+v", apiErr)
		}
		if result.TaskStatus != "processing" {
			t.Fatalf("宽限期内应返回 processing，实际=%s", result.TaskStatus)
		}
		if result.Message != "" || result.RawResult != "" {
			t.Fatalf("宽限期内不应写入 404 body: message=%q raw=%q", result.Message, result.RawResult)
		}
	})

	t.Run("宽限期外判失败并保留审计", func(t *testing.T) {
		task := &dbmodel.Video{TaskId: "grace-out", Duration: "auto", CreatedAt: time.Now().Unix() - 700}
		result, apiErr := (&VideoAdaptor{}).HandleVideoResult(newCtx(), task, ch, nil)
		if apiErr != nil {
			t.Fatalf("宽限期外应结算失败而非返回错误: %+v", apiErr)
		}
		if result.TaskStatus != "failed" {
			t.Fatalf("宽限期外应返回 failed，实际=%s", result.TaskStatus)
		}
		if result.RawResult == "" || result.Message == "" {
			t.Fatalf("宽限期外应保留 404 body 供审计: message=%q raw=%q", result.Message, result.RawResult)
		}
	})
}
