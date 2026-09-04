package controller

import (
	"testing"
	"time"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// setupEvaluatorTestDB 为 evaluateUsageBasedChannelDisable 集成测试准备 sqlite 库：
//   - abilities + channels 表（DB）
//   - model_metrics 表（LOG_DB，测试环境指向同一 db）
//
// 每个测试独立 DSN，避免相互污染。
func setupEvaluatorTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{Logger: gormlogger.Discard},
	)
	if err != nil {
		t.Skipf("SQLite 不可用（可能未启用 CGO），跳过: %v", err)
	}
	if err := db.AutoMigrate(&model.Ability{}, &model.Channel{}, &model.ModelMetrics{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	origDB, origLog := model.DB, model.LOG_DB
	model.DB = db
	model.LOG_DB = db
	t.Cleanup(func() {
		model.DB = origDB
		model.LOG_DB = origLog
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
}

// seedEvalChannel 插入渠道行，默认允许自动禁用。
func seedEvalChannel(t *testing.T, id int, status int) {
	t.Helper()
	c := model.Channel{
		Id:           id,
		Name:         "test",
		Type:         common.ChannelTypeOpenAI,
		Status:       status,
		AutoDisabled: true, // 渠道级"允许自动禁用"开关
	}
	if err := model.DB.Create(&c).Error; err != nil {
		t.Fatalf("插入 channel 失败: %v", err)
	}
}

// seedEvalAbility 插入 auto_disabled 状态的 ability，disabledSecondsAgo 决定 auto_disabled_time。
func seedEvalAbility(t *testing.T, channelId int, modelName string, disabledSecondsAgo int64) {
	t.Helper()
	pri := int64(0)
	a := model.Ability{
		Group:            "default",
		Model:            modelName,
		ChannelId:        channelId,
		Enabled:          false,
		Priority:         &pri,
		AutoDisabled:     true,
		AutoDisabledTime: time.Now().Unix() - disabledSecondsAgo,
	}
	if err := model.DB.Create(&a).Error; err != nil {
		t.Fatalf("插入 ability 失败: %v", err)
	}
}

// seedEvalMetric 写一条 model_metrics 表示 (channel, model) 在最近有过流量。
func seedEvalMetric(t *testing.T, channelId int, modelName string) {
	t.Helper()
	m := model.ModelMetrics{
		ModelName:     modelName,
		ChannelId:     channelId,
		HourTimestamp: (time.Now().Unix() - 3600) / 3600 * 3600,
		TotalRequests: 1,
	}
	if err := model.LOG_DB.Create(&m).Error; err != nil {
		t.Fatalf("插入 model_metrics 失败: %v", err)
	}
}

// withEvaluatorTestEnv 统一 setup：sqlite + 全局开关 + master 标志 + 探针频率 + hook disable。
// 返回记录 disable 调用的 slice 指针，测试用它断言触发行为。
type disableCall struct {
	channelId int
	used      int
}

func withEvaluatorTestEnv(t *testing.T) *[]disableCall {
	t.Helper()
	setupEvaluatorTestDB(t)

	origAutoDisable := config.AutomaticDisableChannelEnabled
	origMaster := config.IsMasterNode
	origFreq := config.AutoTestChannelFrequency
	config.AutomaticDisableChannelEnabled = true
	config.IsMasterNode = true
	config.AutoTestChannelFrequency = 5 // 抖动窗口 = max(2*5*60, 600) = 600s

	calls := make([]disableCall, 0)
	origFn := disableChannelByRecentUsageFn
	disableChannelByRecentUsageFn = func(cid, used int) {
		calls = append(calls, disableCall{cid, used})
	}

	t.Cleanup(func() {
		config.AutomaticDisableChannelEnabled = origAutoDisable
		config.IsMasterNode = origMaster
		config.AutoTestChannelFrequency = origFreq
		disableChannelByRecentUsageFn = origFn
	})
	return &calls
}

// T1：全局开关关 → 直接 return，不查库不触发 disable
func TestEvaluateUsageBasedChannelDisable_AutoDisableFlagOff(t *testing.T) {
	calls := withEvaluatorTestEnv(t)
	config.AutomaticDisableChannelEnabled = false

	// 即使数据满足触发条件，也不该被禁
	seedEvalChannel(t, 1, common.ChannelStatusEnabled)
	seedEvalAbility(t, 1, "gpt-4", 3600)
	seedEvalMetric(t, 1, "gpt-4")

	evaluateUsageBasedChannelDisable()

	if len(*calls) != 0 {
		t.Fatalf("全局开关关时不应触发 disable，实际调用 %d 次", len(*calls))
	}
}

// T2：非 master 节点 → 直接 return
func TestEvaluateUsageBasedChannelDisable_NonMaster(t *testing.T) {
	calls := withEvaluatorTestEnv(t)
	config.IsMasterNode = false

	seedEvalChannel(t, 1, common.ChannelStatusEnabled)
	seedEvalAbility(t, 1, "gpt-4", 3600)
	seedEvalMetric(t, 1, "gpt-4")

	evaluateUsageBasedChannelDisable()

	if len(*calls) != 0 {
		t.Fatalf("非 master 节点不应触发 disable，实际调用 %d 次", len(*calls))
	}
}

// T3：单渠道满足条件 → 触发一次
func TestEvaluateUsageBasedChannelDisable_SingleChannelTriggered(t *testing.T) {
	calls := withEvaluatorTestEnv(t)

	// ch=1 只用过 gpt-4，且 gpt-4 已被禁 1 小时 → 满足 used == disabled 且过窗口
	seedEvalChannel(t, 1, common.ChannelStatusEnabled)
	seedEvalAbility(t, 1, "gpt-4", 3600)
	seedEvalMetric(t, 1, "gpt-4")

	evaluateUsageBasedChannelDisable()

	if len(*calls) != 1 {
		t.Fatalf("期望触发 1 次，实际 %d 次", len(*calls))
	}
	if (*calls)[0].channelId != 1 || (*calls)[0].used != 1 {
		t.Fatalf("期望 disable(cid=1, used=1)，实际 %+v", (*calls)[0])
	}
}

// T4：多渠道混合，只该禁的被禁
func TestEvaluateUsageBasedChannelDisable_MixedChannels(t *testing.T) {
	calls := withEvaluatorTestEnv(t)

	// ch=1 应触发：全禁 + 过窗口
	seedEvalChannel(t, 1, common.ChannelStatusEnabled)
	seedEvalAbility(t, 1, "gpt-4", 3600)
	seedEvalMetric(t, 1, "gpt-4")

	// ch=2 不应触发：使用了 2 个模型但只禁了 1 个（used > disabled）
	seedEvalChannel(t, 2, common.ChannelStatusEnabled)
	seedEvalAbility(t, 2, "claude-3", 3600)
	seedEvalMetric(t, 2, "claude-3")
	seedEvalMetric(t, 2, "claude-3-sonnet") // used 集里有它，但没被禁
	// 注意：seedEvalAbility 只种了 claude-3，所以 disabled=1, used=2

	// ch=3 不应被评估：manually_disabled 状态
	seedEvalChannel(t, 3, common.ChannelStatusManuallyDisabled)
	seedEvalAbility(t, 3, "gemini-pro", 3600)
	seedEvalMetric(t, 3, "gemini-pro")

	// ch=4 应触发：全禁 + 过窗口（跟 ch=1 一起验证多个同时触发）
	seedEvalChannel(t, 4, common.ChannelStatusEnabled)
	seedEvalAbility(t, 4, "llama-3", 3600)
	seedEvalMetric(t, 4, "llama-3")

	evaluateUsageBasedChannelDisable()

	if len(*calls) != 2 {
		t.Fatalf("期望触发 2 次（ch=1, ch=4），实际 %d 次: %+v", len(*calls), *calls)
	}
	got := map[int]bool{(*calls)[0].channelId: true, (*calls)[1].channelId: true}
	if !got[1] || !got[4] {
		t.Fatalf("期望触发 ch=1 和 ch=4，实际 %+v", *calls)
	}
}

// T5：候选查询失败 → 安全退出，不 panic 不调用 disable
func TestEvaluateUsageBasedChannelDisable_CandidateQueryFailure(t *testing.T) {
	calls := withEvaluatorTestEnv(t)

	seedEvalChannel(t, 1, common.ChannelStatusEnabled)
	seedEvalAbility(t, 1, "gpt-4", 3600)
	seedEvalMetric(t, 1, "gpt-4")

	// 关闭 DB 会话，让 GetChannelsWithAutoDisabledAbilities 查询失败
	if sqlDB, err := model.DB.DB(); err == nil {
		_ = sqlDB.Close()
	}

	// 不应 panic，且 disable 不被调用
	evaluateUsageBasedChannelDisable()

	if len(*calls) != 0 {
		t.Fatalf("候选查询失败时不应触发 disable，实际调用 %d 次", len(*calls))
	}
}
