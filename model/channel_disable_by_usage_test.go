package model

import (
	"testing"
	"time"

	"github.com/songquanpeng/one-api/common/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupUsageJudgeDB 建一个含 abilities + model_metrics 的内存库，同时把 LOG_DB 指向同一 db。
// 单库场景（生产默认）就是这样，用它来跑判定逻辑的语义验证。
func setupUsageJudgeDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{Logger: logger.Discard},
	)
	if err != nil {
		t.Skipf("SQLite 不可用（可能未启用 CGO），跳过: %v", err)
	}
	if err := db.AutoMigrate(&Ability{}, &ModelMetrics{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	origDB, origLog := DB, LOG_DB
	DB = db
	LOG_DB = db
	t.Cleanup(func() {
		DB = origDB
		LOG_DB = origLog
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
}

// seedUsedModel 写一条 model_metrics，表示 (channel, model) 在最近有过流量。
func seedUsedModel(t *testing.T, channelId int, modelName string, hourTs int64, totalRequests int64) {
	t.Helper()
	m := ModelMetrics{
		ModelName:     modelName,
		ChannelId:     channelId,
		HourTimestamp: hourTs,
		TotalRequests: totalRequests,
	}
	if err := LOG_DB.Create(&m).Error; err != nil {
		t.Fatalf("插入 model_metrics 失败: %v", err)
	}
}

// seedAutoDisabled 写一条 abilities，模拟模型已被自动禁用 disabledSecondsAgo 秒前。
func seedAutoDisabled(t *testing.T, channelId int, group, modelName string, disabledSecondsAgo int64) {
	t.Helper()
	pri := int64(0)
	a := Ability{
		Group:            group,
		Model:            modelName,
		ChannelId:        channelId,
		Enabled:          false,
		Priority:         &pri,
		AutoDisabled:     true,
		AutoDisabledTime: time.Now().Unix() - disabledSecondsAgo,
	}
	if err := DB.Create(&a).Error; err != nil {
		t.Fatalf("插入 ability 失败: %v", err)
	}
}

func TestShouldDisableChannelByRecentUsage(t *testing.T) {
	// 固定探针周期，测试计算 stabilizeCutoff 的一致性
	origFreq := config.AutoTestChannelFrequency
	config.AutoTestChannelFrequency = 5 // 抖动窗口 = 2 * 5 * 60 = 600s
	t.Cleanup(func() { config.AutoTestChannelFrequency = origFreq })

	now := time.Now().Unix()
	recentHour := (now - 3600) / 3600 * 3600 // 最近整点

	t.Run("无流量记录_不禁", func(t *testing.T) {
		setupUsageJudgeDB(t)
		should, used, disabled, err := ShouldDisableChannelByRecentUsage(1)
		if err != nil {
			t.Fatalf("判定失败: %v", err)
		}
		if should || used != 0 || disabled != 0 {
			t.Fatalf("期望 should=false used=0 disabled=0，实际 %v %d %d", should, used, disabled)
		}
	})

	t.Run("有流量_无禁用_不禁", func(t *testing.T) {
		setupUsageJudgeDB(t)
		seedUsedModel(t, 1, "gpt-4", recentHour, 10)
		seedUsedModel(t, 1, "gpt-4o", recentHour, 5)
		// abilities 存在但未禁用
		pri := int64(0)
		if err := DB.Create(&Ability{Group: "default", Model: "gpt-4", ChannelId: 1, Enabled: true, Priority: &pri}).Error; err != nil {
			t.Fatal(err)
		}
		if err := DB.Create(&Ability{Group: "default", Model: "gpt-4o", ChannelId: 1, Enabled: true, Priority: &pri}).Error; err != nil {
			t.Fatal(err)
		}
		should, used, disabled, err := ShouldDisableChannelByRecentUsage(1)
		if err != nil {
			t.Fatalf("判定失败: %v", err)
		}
		if should || used != 2 || disabled != 0 {
			t.Fatalf("期望 should=false used=2 disabled=0，实际 %v %d %d", should, used, disabled)
		}
	})

	t.Run("全禁用_但在抖动窗口内_不禁", func(t *testing.T) {
		setupUsageJudgeDB(t)
		seedUsedModel(t, 1, "gpt-4", recentHour, 10)
		seedUsedModel(t, 1, "gpt-4o", recentHour, 5)
		// 禁用时间 = 100 秒前，小于 600s 抖动窗口
		seedAutoDisabled(t, 1, "default", "gpt-4", 100)
		seedAutoDisabled(t, 1, "default", "gpt-4o", 100)
		should, used, disabled, err := ShouldDisableChannelByRecentUsage(1)
		if err != nil {
			t.Fatalf("判定失败: %v", err)
		}
		if should || used != 2 || disabled != 0 {
			t.Fatalf("抖动窗口内不应触发，实际 should=%v used=%d disabled=%d", should, used, disabled)
		}
	})

	t.Run("全禁用_且超过抖动窗口_应禁", func(t *testing.T) {
		setupUsageJudgeDB(t)
		seedUsedModel(t, 1, "gpt-4", recentHour, 10)
		seedUsedModel(t, 1, "gpt-4o", recentHour, 5)
		// 禁用时间 = 1200 秒前，大于 600s
		seedAutoDisabled(t, 1, "default", "gpt-4", 1200)
		seedAutoDisabled(t, 1, "default", "gpt-4o", 1200)
		should, used, disabled, err := ShouldDisableChannelByRecentUsage(1)
		if err != nil {
			t.Fatalf("判定失败: %v", err)
		}
		if !should || used != 2 || disabled != 2 {
			t.Fatalf("应触发禁渠道，实际 should=%v used=%d disabled=%d", should, used, disabled)
		}
	})

	t.Run("部分禁用_不禁", func(t *testing.T) {
		setupUsageJudgeDB(t)
		seedUsedModel(t, 1, "gpt-4", recentHour, 10)
		seedUsedModel(t, 1, "gpt-4o", recentHour, 5)
		seedAutoDisabled(t, 1, "default", "gpt-4", 1200) // 超窗口禁用
		pri := int64(0)
		if err := DB.Create(&Ability{Group: "default", Model: "gpt-4o", ChannelId: 1, Enabled: true, Priority: &pri}).Error; err != nil {
			t.Fatal(err)
		}
		should, used, disabled, err := ShouldDisableChannelByRecentUsage(1)
		if err != nil {
			t.Fatalf("判定失败: %v", err)
		}
		if should || used != 2 || disabled != 1 {
			t.Fatalf("部分禁用不应触发，实际 should=%v used=%d disabled=%d", should, used, disabled)
		}
	})

	t.Run("多group同模型_算一个model", func(t *testing.T) {
		setupUsageJudgeDB(t)
		seedUsedModel(t, 1, "gpt-4", recentHour, 10)
		// AutoDisableModelOnChannel 会同时禁 (channel, model) 下所有 group，这里模拟两个 group
		seedAutoDisabled(t, 1, "default", "gpt-4", 1200)
		seedAutoDisabled(t, 1, "vip", "gpt-4", 1200)
		should, used, disabled, err := ShouldDisableChannelByRecentUsage(1)
		if err != nil {
			t.Fatalf("判定失败: %v", err)
		}
		if !should || used != 1 || disabled != 1 {
			t.Fatalf("多group同模型应算 1 个 model，实际 should=%v used=%d disabled=%d", should, used, disabled)
		}
	})

	t.Run("窗口外流量不计入分母", func(t *testing.T) {
		setupUsageJudgeDB(t)
		// 26 小时前的流量：超出 1 天窗口
		staleHour := (now - 26*3600) / 3600 * 3600
		seedUsedModel(t, 1, "gpt-legacy", staleHour, 100)
		// 最近有 1 个模型有流量且已禁
		seedUsedModel(t, 1, "gpt-4", recentHour, 5)
		seedAutoDisabled(t, 1, "default", "gpt-4", 1200)
		should, used, disabled, err := ShouldDisableChannelByRecentUsage(1)
		if err != nil {
			t.Fatalf("判定失败: %v", err)
		}
		if !should || used != 1 || disabled != 1 {
			t.Fatalf("窗口外流量不应计入分母，实际 should=%v used=%d disabled=%d", should, used, disabled)
		}
	})

	t.Run("其他渠道不影响", func(t *testing.T) {
		setupUsageJudgeDB(t)
		// 渠道 1 有流量且全禁
		seedUsedModel(t, 1, "gpt-4", recentHour, 10)
		seedAutoDisabled(t, 1, "default", "gpt-4", 1200)
		// 渠道 2 有流量未禁
		seedUsedModel(t, 2, "gpt-4", recentHour, 10)
		pri := int64(0)
		if err := DB.Create(&Ability{Group: "default", Model: "gpt-4", ChannelId: 2, Enabled: true, Priority: &pri}).Error; err != nil {
			t.Fatal(err)
		}
		should1, _, _, _ := ShouldDisableChannelByRecentUsage(1)
		should2, _, _, _ := ShouldDisableChannelByRecentUsage(2)
		if !should1 || should2 {
			t.Fatalf("渠道 1 应禁、渠道 2 不禁，实际 %v %v", should1, should2)
		}
	})
}

// TestShouldDisableChannelByRecentUsage_StabilizeFloor 覆盖低 AutoTestChannelFrequency
// 场景下抖动窗口地板值保护：freq=1 分钟时 2×freq=120s 太短，实际窗口应被抬到
// channelDisableStabilizeFloorSeconds（10 分钟）。
func TestShouldDisableChannelByRecentUsage_StabilizeFloor(t *testing.T) {
	origFreq := config.AutoTestChannelFrequency
	config.AutoTestChannelFrequency = 1 // 2*freq=120s，被地板值 600s 覆盖
	t.Cleanup(func() { config.AutoTestChannelFrequency = origFreq })

	now := time.Now().Unix()
	recentHour := (now - 3600) / 3600 * 3600

	t.Run("禁用_300s前_地板窗口内不禁", func(t *testing.T) {
		setupUsageJudgeDB(t)
		seedUsedModel(t, 1, "gpt-4", recentHour, 10)
		// 300s 前禁用：超过 2×freq(120s) 但小于地板值 600s，应仍在抖动窗口内
		seedAutoDisabled(t, 1, "default", "gpt-4", 300)
		should, _, disabled, err := ShouldDisableChannelByRecentUsage(1)
		if err != nil {
			t.Fatalf("判定失败: %v", err)
		}
		if should || disabled != 0 {
			t.Fatalf("300s 前禁用未过地板窗口(600s)，不应触发；实际 should=%v disabled=%d", should, disabled)
		}
	})

	t.Run("禁用_700s前_超地板窗口应禁", func(t *testing.T) {
		setupUsageJudgeDB(t)
		seedUsedModel(t, 1, "gpt-4", recentHour, 10)
		seedAutoDisabled(t, 1, "default", "gpt-4", 700)
		should, _, disabled, err := ShouldDisableChannelByRecentUsage(1)
		if err != nil {
			t.Fatalf("判定失败: %v", err)
		}
		if !should || disabled != 1 {
			t.Fatalf("700s 前禁用已过地板窗口，应触发；实际 should=%v disabled=%d", should, disabled)
		}
	})
}
