package model

import (
	"testing"
	"time"

	"github.com/songquanpeng/one-api/common"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupCircuitDB 建一个含 channels + abilities 的内存库，接管全局 DB，用于验证熔断计数逻辑。
func setupCircuitDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{Logger: logger.Discard},
	)
	if err != nil {
		t.Skipf("SQLite 不可用（可能未启用 CGO），跳过: %v", err)
	}
	if err := db.AutoMigrate(&Channel{}, &Ability{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	origDB := DB
	DB = db
	t.Cleanup(func() {
		DB = origDB
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
}

func seedCircuitChannel(t *testing.T, c *Channel) {
	t.Helper()
	// 关键：GORM Create 会把 default:true 字段的 bool 零值 false 视为"未提供"，
	// INSERT 时用 DDL 默认值 true，并把 struct 字段回写为 true。
	// 必须在 Create 之前记录用户意图，Create 之后追加 UpdateColumns 强制写 false。
	wantAutoDisabledFalse := !c.AutoDisabled
	wantAutoEnabledFalse := !c.AutoEnabled

	if err := DB.Create(c).Error; err != nil {
		t.Fatalf("插入 channel 失败: %v", err)
	}
	force := map[string]interface{}{}
	if wantAutoDisabledFalse {
		force["auto_disabled"] = false
	}
	if wantAutoEnabledFalse {
		force["auto_enabled"] = false
	}
	if len(force) > 0 {
		if err := DB.Model(&Channel{}).Where("id = ?", c.Id).UpdateColumns(force).Error; err != nil {
			t.Fatalf("force bool false 失败: %v", err)
		}
	}
}

func loadCircuitChannel(t *testing.T, id int) *Channel {
	t.Helper()
	var c Channel
	if err := DB.First(&c, "id = ?", id).Error; err != nil {
		t.Fatalf("读 channel 失败: %v", err)
	}
	return &c
}

// TestAutoDisableChannelById_CircuitBreaker 覆盖 24h 滚动窗口内的计数、达阈锁死、窗口过期重置、
// 前置退出条件（AutoDisabled=false / MultiKey / 已是 auto_disabled 的幂等）等 6 个场景。
// 参见 docs/plans/2026-08-26-auto-disable-circuit-breaker.md
func TestAutoDisableChannelById_CircuitBreaker(t *testing.T) {
	t.Run("首次触发_count=1_窗口起点为now_auto_enabled保持", func(t *testing.T) {
		setupCircuitDB(t)
		seedCircuitChannel(t, &Channel{
			Id:                     1,
			Status:                 common.ChannelStatusEnabled,
			AutoDisabled:           true,
			AutoEnabled:            true,
			AutoDisableCount:       0,
			AutoDisableWindowStart: 0,
		})
		beforeTs := time.Now().Unix()
		disabled, err := AutoDisableChannelById(1, "reason-a", "gpt-4")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !disabled {
			t.Fatalf("首次禁用应返回 disabled=true")
		}
		c := loadCircuitChannel(t, 1)
		if c.AutoDisableCount != 1 {
			t.Fatalf("count 应为 1, 实际 %d", c.AutoDisableCount)
		}
		if c.AutoDisableWindowStart < beforeTs {
			t.Fatalf("window_start 应刷新为 now(>=%d)，实际 %d", beforeTs, c.AutoDisableWindowStart)
		}
		if !c.AutoEnabled {
			t.Fatalf("count=1 尚未达阈，auto_enabled 应保持 true")
		}
		if c.Status != common.ChannelStatusAutoDisabled {
			t.Fatalf("status 应为 auto_disabled, 实际 %d", c.Status)
		}
	})

	t.Run("窗口内达阈_count=3_锁死auto_enabled=false", func(t *testing.T) {
		setupCircuitDB(t)
		now := time.Now().Unix()
		seedCircuitChannel(t, &Channel{
			Id:                     1,
			Status:                 common.ChannelStatusEnabled,
			AutoDisabled:           true,
			AutoEnabled:            true,
			AutoDisableCount:       2,
			AutoDisableWindowStart: now - 3600, // 窗口内
		})
		disabled, err := AutoDisableChannelById(1, "reason-third", "gpt-4")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !disabled {
			t.Fatalf("应算首次禁用（本次 UPDATE 生效）")
		}
		c := loadCircuitChannel(t, 1)
		if c.AutoDisableCount != 3 {
			t.Fatalf("count 应为 3, 实际 %d", c.AutoDisableCount)
		}
		if c.AutoEnabled {
			t.Fatalf("达阈 3 应锁死 auto_enabled=false, 实际 true")
		}
	})

	t.Run("窗口过期_count重置为1_auto_enabled不受影响", func(t *testing.T) {
		setupCircuitDB(t)
		now := time.Now().Unix()
		seedCircuitChannel(t, &Channel{
			Id:                     1,
			Status:                 common.ChannelStatusEnabled,
			AutoDisabled:           true,
			AutoEnabled:            true,
			AutoDisableCount:       3, // 曾经达阈
			AutoDisableWindowStart: now - 25*3600,
		})
		beforeTs := time.Now().Unix()
		disabled, err := AutoDisableChannelById(1, "reason-after-window", "gpt-4")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !disabled {
			t.Fatalf("应算首次禁用")
		}
		c := loadCircuitChannel(t, 1)
		if c.AutoDisableCount != 1 {
			t.Fatalf("过窗口应重置 count 为 1, 实际 %d", c.AutoDisableCount)
		}
		if c.AutoDisableWindowStart < beforeTs {
			t.Fatalf("window_start 应刷新为 now(>=%d)，实际 %d", beforeTs, c.AutoDisableWindowStart)
		}
		if !c.AutoEnabled {
			t.Fatalf("重置窗口后不应锁死 auto_enabled")
		}
	})

	t.Run("AutoDisabled=false_前置退出_count保持", func(t *testing.T) {
		setupCircuitDB(t)
		seedCircuitChannel(t, &Channel{
			Id:               1,
			Status:           common.ChannelStatusEnabled,
			AutoDisabled:     false,
			AutoEnabled:      true,
			AutoDisableCount: 0,
		})
		// 断言 seed 落库正确（防御性：GORM `default:true` + bool false 坑，
		// seedCircuitChannel 已通过 Create 前记录意图 + Create 后 UpdateColumns 修复）
		seeded := loadCircuitChannel(t, 1)
		if seeded.AutoDisabled {
			t.Fatalf("seed 后 AutoDisabled 应为 false, 实际 true")
		}
		disabled, err := AutoDisableChannelById(1, "reason", "gpt-4")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if disabled {
			t.Fatalf("AutoDisabled=false 应返回 disabled=false")
		}
		c := loadCircuitChannel(t, 1)
		if c.AutoDisableCount != 0 {
			t.Fatalf("前置退出不应改 count, 实际 %d", c.AutoDisableCount)
		}
		if c.Status != common.ChannelStatusEnabled {
			t.Fatalf("status 不应改, 实际 %d", c.Status)
		}
	})

	t.Run("MultiKey_前置退出_count保持", func(t *testing.T) {
		setupCircuitDB(t)
		seedCircuitChannel(t, &Channel{
			Id:               1,
			Status:           common.ChannelStatusEnabled,
			AutoDisabled:     true,
			AutoEnabled:      true,
			MultiKeyInfo:     MultiKeyInfo{IsMultiKey: true, KeyCount: 3},
			AutoDisableCount: 0,
		})
		disabled, err := AutoDisableChannelById(1, "reason", "gpt-4")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if disabled {
			t.Fatalf("多 Key 渠道应返回 disabled=false")
		}
		c := loadCircuitChannel(t, 1)
		if c.AutoDisableCount != 0 {
			t.Fatalf("多 Key 前置退出不应改 count, 实际 %d", c.AutoDisableCount)
		}
	})

	t.Run("已是auto_disabled_条件UPDATE命中0行_count不变", func(t *testing.T) {
		setupCircuitDB(t)
		now := time.Now().Unix()
		seedCircuitChannel(t, &Channel{
			Id:                     1,
			Status:                 common.ChannelStatusAutoDisabled, // 已经被别人禁了
			AutoDisabled:           true,
			AutoEnabled:            true,
			AutoDisableCount:       2,
			AutoDisableWindowStart: now - 3600,
		})
		disabled, err := AutoDisableChannelById(1, "reason", "gpt-4")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if disabled {
			t.Fatalf("已 auto_disabled 应返回 disabled=false（幂等）")
		}
		c := loadCircuitChannel(t, 1)
		if c.AutoDisableCount != 2 {
			t.Fatalf("幂等场景不应改 count, 实际 %d", c.AutoDisableCount)
		}
	})
}

// TestUpdateChannelStatusById_ManualEnableResetsCircuit 手动启用清零 count 和 window_start，
// 但 auto_enabled 保持——锁死后管理员必须在 UI 显式打开 auto_enabled。
func TestUpdateChannelStatusById_ManualEnableResetsCircuit(t *testing.T) {
	setupCircuitDB(t)
	now := time.Now().Unix()
	seedCircuitChannel(t, &Channel{
		Id:                     1,
		Status:                 common.ChannelStatusAutoDisabled,
		AutoDisabled:           true,
		AutoEnabled:            false, // 已被锁死
		AutoDisableCount:       3,
		AutoDisableWindowStart: now - 3600,
		Models:                 "gpt-4",
		Group:                  "default",
	})
	if err := UpdateChannelStatusById(1, common.ChannelStatusEnabled); err != nil {
		t.Fatalf("err: %v", err)
	}
	c := loadCircuitChannel(t, 1)
	if c.AutoDisableCount != 0 {
		t.Fatalf("手动启用应清零 count, 实际 %d", c.AutoDisableCount)
	}
	if c.AutoDisableWindowStart != 0 {
		t.Fatalf("手动启用应清零 window_start, 实际 %d", c.AutoDisableWindowStart)
	}
	if c.AutoEnabled {
		t.Fatalf("auto_enabled 应保持 false（手动启用不解除锁死）")
	}
	if c.Status != common.ChannelStatusEnabled {
		t.Fatalf("status 应为 enabled, 实际 %d", c.Status)
	}
}

// TestUpdateChannelStatusById_ManualDisableDoesNotResetCircuit 手动禁用不动熔断计数。
func TestUpdateChannelStatusById_ManualDisableDoesNotResetCircuit(t *testing.T) {
	setupCircuitDB(t)
	now := time.Now().Unix()
	seedCircuitChannel(t, &Channel{
		Id:                     1,
		Status:                 common.ChannelStatusEnabled,
		AutoDisabled:           true,
		AutoEnabled:            true,
		AutoDisableCount:       2,
		AutoDisableWindowStart: now - 3600,
	})
	if err := UpdateChannelStatusById(1, common.ChannelStatusManuallyDisabled); err != nil {
		t.Fatalf("err: %v", err)
	}
	c := loadCircuitChannel(t, 1)
	if c.AutoDisableCount != 2 {
		t.Fatalf("手动禁用不应清零 count, 实际 %d", c.AutoDisableCount)
	}
	if c.AutoDisableWindowStart == 0 {
		t.Fatalf("手动禁用不应清零 window_start, 实际 %d", c.AutoDisableWindowStart)
	}
}

// TestBatchUpdateChannelStatus_ManualEnableResetsCircuit 批量启用也清零熔断计数。
func TestBatchUpdateChannelStatus_ManualEnableResetsCircuit(t *testing.T) {
	setupCircuitDB(t)
	now := time.Now().Unix()
	seedCircuitChannel(t, &Channel{
		Id: 1, Status: common.ChannelStatusAutoDisabled,
		AutoDisabled: true, AutoEnabled: false,
		AutoDisableCount: 3, AutoDisableWindowStart: now - 3600,
	})
	seedCircuitChannel(t, &Channel{
		Id: 2, Status: common.ChannelStatusAutoDisabled,
		AutoDisabled: true, AutoEnabled: false,
		AutoDisableCount: 2, AutoDisableWindowStart: now - 7200,
	})
	if err := BatchUpdateChannelStatus([]int{1, 2}, common.ChannelStatusEnabled); err != nil {
		t.Fatalf("err: %v", err)
	}
	for _, id := range []int{1, 2} {
		c := loadCircuitChannel(t, id)
		if c.AutoDisableCount != 0 || c.AutoDisableWindowStart != 0 {
			t.Fatalf("channel %d 应清零 count/window, 实际 count=%d window_start=%d",
				id, c.AutoDisableCount, c.AutoDisableWindowStart)
		}
		if c.AutoEnabled {
			t.Fatalf("channel %d auto_enabled 应保持 false", id)
		}
		if c.Status != common.ChannelStatusEnabled {
			t.Fatalf("channel %d status 应为 enabled, 实际 %d", id, c.Status)
		}
	}
}
