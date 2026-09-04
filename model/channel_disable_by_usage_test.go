package model

import (
	"testing"
	"time"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupUsageJudgeDB 建一个含 abilities + model_metrics + channels 的内存库，同时把 LOG_DB 指向同一 db。
// 单库场景（生产默认）就是这样。channels 表是必需的——判定含「渠道已非 enabled 则短路」的逻辑。
func setupUsageJudgeDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{Logger: logger.Discard},
	)
	if err != nil {
		t.Skipf("SQLite 不可用（可能未启用 CGO），跳过: %v", err)
	}
	if err := db.AutoMigrate(&Ability{}, &ModelMetrics{}, &Channel{}); err != nil {
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
	// 固定窗口，避免受运营配置影响
	origWin := config.ChannelUsageWindowMinutes
	config.ChannelUsageWindowMinutes = 60
	t.Cleanup(func() { config.ChannelUsageWindowMinutes = origWin })

	now := time.Now().Unix()
	// 当前小时整点，必落在 60 分钟窗口内（now/3600*3600 >= now-3599 > windowStart=now-3600）
	curHour := now / 3600 * 3600

	t.Run("渠道已非enabled_短路不禁", func(t *testing.T) {
		setupUsageJudgeDB(t)
		seedChannel(t, 1, common.ChannelStatusAutoDisabled)
		seedUsedModel(t, 1, "gpt-4", curHour, 10)
		seedAutoDisabled(t, 1, "default", "gpt-4", 100)
		should, used, disabled, err := ShouldDisableChannelByRecentUsage(1)
		if err != nil {
			t.Fatalf("判定失败: %v", err)
		}
		if should || used != 0 || disabled != 0 {
			t.Fatalf("渠道已禁应短路，实际 should=%v used=%d disabled=%d", should, used, disabled)
		}
	})

	t.Run("无流量记录_不禁", func(t *testing.T) {
		setupUsageJudgeDB(t)
		seedChannel(t, 1, common.ChannelStatusEnabled)
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
		seedChannel(t, 1, common.ChannelStatusEnabled)
		seedUsedModel(t, 1, "gpt-4", curHour, 10)
		seedUsedModel(t, 1, "gpt-4o", curHour, 5)
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

	t.Run("全禁用_刚禁即计入_应禁", func(t *testing.T) {
		// 去抖动核心用例：禁用时间仅 5 秒前（旧逻辑会被抖动窗口挡住），现在应立即触发
		setupUsageJudgeDB(t)
		seedChannel(t, 1, common.ChannelStatusEnabled)
		seedUsedModel(t, 1, "gpt-4", curHour, 10)
		seedUsedModel(t, 1, "gpt-4o", curHour, 5)
		seedAutoDisabled(t, 1, "default", "gpt-4", 5)
		seedAutoDisabled(t, 1, "default", "gpt-4o", 5)
		should, used, disabled, err := ShouldDisableChannelByRecentUsage(1)
		if err != nil {
			t.Fatalf("判定失败: %v", err)
		}
		if !should || used != 2 || disabled != 2 {
			t.Fatalf("去抖动后应立即触发，实际 should=%v used=%d disabled=%d", should, used, disabled)
		}
	})

	t.Run("部分禁用_不禁", func(t *testing.T) {
		setupUsageJudgeDB(t)
		seedChannel(t, 1, common.ChannelStatusEnabled)
		seedUsedModel(t, 1, "gpt-4", curHour, 10)
		seedUsedModel(t, 1, "gpt-4o", curHour, 5)
		seedAutoDisabled(t, 1, "default", "gpt-4", 5)
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
		seedChannel(t, 1, common.ChannelStatusEnabled)
		seedUsedModel(t, 1, "gpt-4", curHour, 10)
		seedAutoDisabled(t, 1, "default", "gpt-4", 5)
		seedAutoDisabled(t, 1, "vip", "gpt-4", 5)
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
		seedChannel(t, 1, common.ChannelStatusEnabled)
		// 2 小时前的流量：超出 60 分钟窗口
		staleHour := (now - 2*3600) / 3600 * 3600
		seedUsedModel(t, 1, "gpt-legacy", staleHour, 100)
		// 窗口内 1 个模型有流量且已禁
		seedUsedModel(t, 1, "gpt-4", curHour, 5)
		seedAutoDisabled(t, 1, "default", "gpt-4", 5)
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
		seedChannel(t, 1, common.ChannelStatusEnabled)
		seedChannel(t, 2, common.ChannelStatusEnabled)
		seedUsedModel(t, 1, "gpt-4", curHour, 10)
		seedAutoDisabled(t, 1, "default", "gpt-4", 5)
		seedUsedModel(t, 2, "gpt-4", curHour, 10)
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

// TestShouldDisableChannelByRecentUsage_WindowConfigurable 验证窗口取 config.ChannelUsageWindowMinutes：
// 同一条 30 分钟前的流量，在 60 分钟窗口内计入、在 10 分钟窗口外被排除。
func TestShouldDisableChannelByRecentUsage_WindowConfigurable(t *testing.T) {
	origWin := config.ChannelUsageWindowMinutes
	t.Cleanup(func() { config.ChannelUsageWindowMinutes = origWin })

	now := time.Now().Unix()
	// 40 分钟前的整点：落在 60 分钟窗口内，但在 10 分钟窗口外
	hour40mAgo := (now - 40*60) / 3600 * 3600

	t.Run("60分钟窗口_计入_应禁", func(t *testing.T) {
		setupUsageJudgeDB(t)
		config.ChannelUsageWindowMinutes = 90 // 覆盖到 40 分钟前的整点
		seedChannel(t, 1, common.ChannelStatusEnabled)
		seedUsedModel(t, 1, "gpt-4", hour40mAgo, 10)
		seedAutoDisabled(t, 1, "default", "gpt-4", 5)
		should, used, _, err := ShouldDisableChannelByRecentUsage(1)
		if err != nil {
			t.Fatalf("判定失败: %v", err)
		}
		if !should || used != 1 {
			t.Fatalf("90 分钟窗口应计入，实际 should=%v used=%d", should, used)
		}
	})

	t.Run("10分钟窗口_排除_不禁", func(t *testing.T) {
		setupUsageJudgeDB(t)
		config.ChannelUsageWindowMinutes = 10 // 40 分钟前的流量被排除
		seedChannel(t, 1, common.ChannelStatusEnabled)
		seedUsedModel(t, 1, "gpt-4", hour40mAgo, 10)
		seedAutoDisabled(t, 1, "default", "gpt-4", 5)
		should, used, _, err := ShouldDisableChannelByRecentUsage(1)
		if err != nil {
			t.Fatalf("判定失败: %v", err)
		}
		if should || used != 0 {
			t.Fatalf("10 分钟窗口应排除，实际 should=%v used=%d", should, used)
		}
	})
}

// TestShouldDisableChannelByRecentUsageImmediate 验证即刻入口把 triggerModel 并入 used：
// model_metrics 里尚无该模型（模拟 5 分钟聚合滞后），但因 triggerModel 并入 + abilities 已禁，判定成立。
func TestShouldDisableChannelByRecentUsageImmediate(t *testing.T) {
	origWin := config.ChannelUsageWindowMinutes
	config.ChannelUsageWindowMinutes = 60
	t.Cleanup(func() { config.ChannelUsageWindowMinutes = origWin })

	t.Run("triggerModel并入_metrics滞后仍判定成立", func(t *testing.T) {
		setupUsageJudgeDB(t)
		seedChannel(t, 1, common.ChannelStatusEnabled)
		// 注意：不 seed model_metrics（模拟聚合尚未跑），仅 abilities 已禁 triggerModel
		seedAutoDisabled(t, 1, "default", "gpt-5.5", 0)
		should, used, disabled, err := ShouldDisableChannelByRecentUsageImmediate(1, "gpt-5.5")
		if err != nil {
			t.Fatalf("判定失败: %v", err)
		}
		if !should || used != 1 || disabled != 1 {
			t.Fatalf("triggerModel 应并入并判定成立，实际 should=%v used=%d disabled=%d", should, used, disabled)
		}
	})

	t.Run("triggerModel未禁_不成立", func(t *testing.T) {
		setupUsageJudgeDB(t)
		seedChannel(t, 1, common.ChannelStatusEnabled)
		// triggerModel 并入 used，但 abilities 里它未被禁 → used=1 disabled=0
		should, used, disabled, err := ShouldDisableChannelByRecentUsageImmediate(1, "gpt-5.5")
		if err != nil {
			t.Fatalf("判定失败: %v", err)
		}
		if should || used != 1 || disabled != 0 {
			t.Fatalf("triggerModel 未禁不应成立，实际 should=%v used=%d disabled=%d", should, used, disabled)
		}
	})
}
