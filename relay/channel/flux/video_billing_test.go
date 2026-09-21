package flux

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

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

func TestVideoSuccessCannotOverwriteFailedTask(t *testing.T) {
	db := setupVideoSettlementDB(t)
	task := dbmodel.Video{TaskId: "failed-task", Status: "failed", Quota: 500000, FailReason: "任务已经失败", Duration: "auto"}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	stale := task
	stale.Status = "processing"
	applied, err := ApplyVideoSuccess(context.Background(), &stale, &relaymodel.GeneralFinalVideoResponse{TaskStatus: "succeed", Duration: "10.25", FluxActualQuota: quotaPointer(1025000)})
	if err != nil || applied || stale.Status != "failed" || stale.Quota != 500000 || stale.Duration != "auto" {
		t.Fatalf("已失败任务不应再次扣费或改为成功: applied=%v task=%+v err=%v", applied, stale, err)
	}
}
