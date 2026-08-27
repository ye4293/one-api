package model

import (
	"testing"

	"github.com/songquanpeng/one-api/common"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupChannelFilterDB 内存库，用于验证 GetAllChannelsForTest 的 filter 分支 SQL 语义。
func setupChannelFilterDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{Logger: logger.Discard},
	)
	if err != nil {
		t.Skipf("SQLite 不可用（可能未启用 CGO），跳过: %v", err)
	}
	if err := db.AutoMigrate(&Channel{}); err != nil {
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

func seedFilterChannel(t *testing.T, id int, name string, chType int, status int) {
	t.Helper()
	c := &Channel{
		Id:     id,
		Name:   name,
		Type:   chType,
		Status: status,
	}
	if err := DB.Create(c).Error; err != nil {
		t.Fatalf("seed channel %d 失败: %v", id, err)
	}
}

// TestGetAllChannelsForTest_FilterKeyword 只按 name 模糊匹配
func TestGetAllChannelsForTest_FilterKeyword(t *testing.T) {
	setupChannelFilterDB(t)
	seedFilterChannel(t, 1, "mi-tl-oai-001", 1, common.ChannelStatusEnabled)
	seedFilterChannel(t, 2, "mi-tl-oai-002", 1, common.ChannelStatusEnabled)
	seedFilterChannel(t, 3, "other-channel", 1, common.ChannelStatusEnabled)

	channels, err := GetAllChannelsForTest(0, 0, "filter", "mi-tl-oai", nil, nil)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(channels) != 2 {
		t.Fatalf("期望 2 个匹配，实际 %d", len(channels))
	}
}

// TestGetAllChannelsForTest_FilterType 只按 type 精确匹配
func TestGetAllChannelsForTest_FilterType(t *testing.T) {
	setupChannelFilterDB(t)
	seedFilterChannel(t, 1, "a", 1, common.ChannelStatusEnabled)
	seedFilterChannel(t, 2, "b", 1, common.ChannelStatusEnabled)
	seedFilterChannel(t, 3, "c", 24, common.ChannelStatusEnabled)

	t1 := 1
	channels, err := GetAllChannelsForTest(0, 0, "filter", "", &t1, nil)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(channels) != 2 {
		t.Fatalf("期望 2 个 type=1，实际 %d", len(channels))
	}
}

// TestGetAllChannelsForTest_FilterDefaultStatusIsEnabled 不传 statusList → 默认只取 enabled
func TestGetAllChannelsForTest_FilterDefaultStatusIsEnabled(t *testing.T) {
	setupChannelFilterDB(t)
	seedFilterChannel(t, 1, "a", 1, common.ChannelStatusEnabled)
	seedFilterChannel(t, 2, "b", 1, common.ChannelStatusManuallyDisabled)
	seedFilterChannel(t, 3, "c", 1, common.ChannelStatusAutoDisabled)

	t1 := 1
	channels, err := GetAllChannelsForTest(0, 0, "filter", "", &t1, nil)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(channels) != 1 || channels[0].Id != 1 {
		t.Fatalf("期望只返回 enabled 的 id=1，实际 %+v", channels)
	}
}

// TestGetAllChannelsForTest_FilterMultiStatus 多状态 CSV 过滤
func TestGetAllChannelsForTest_FilterMultiStatus(t *testing.T) {
	setupChannelFilterDB(t)
	seedFilterChannel(t, 1, "a", 1, common.ChannelStatusEnabled)
	seedFilterChannel(t, 2, "b", 1, common.ChannelStatusManuallyDisabled)
	seedFilterChannel(t, 3, "c", 1, common.ChannelStatusAutoDisabled)
	seedFilterChannel(t, 4, "d", 1, common.ChannelStatusAutoDisabled)

	t1 := 1
	// status=1,3
	channels, err := GetAllChannelsForTest(0, 0, "filter", "", &t1,
		[]int{common.ChannelStatusEnabled, common.ChannelStatusAutoDisabled})
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(channels) != 3 {
		t.Fatalf("期望 3 个 (status 1 或 3)，实际 %d", len(channels))
	}
	gotIds := map[int]bool{}
	for _, c := range channels {
		gotIds[c.Id] = true
	}
	if gotIds[2] {
		t.Fatalf("id=2 (manually_disabled) 不应命中")
	}
	if !gotIds[1] || !gotIds[3] || !gotIds[4] {
		t.Fatalf("id=1/3/4 应全部命中，实际 %+v", gotIds)
	}
}

// TestGetAllChannelsForTest_FilterKeywordAndType keyword AND type 联合过滤
func TestGetAllChannelsForTest_FilterKeywordAndType(t *testing.T) {
	setupChannelFilterDB(t)
	seedFilterChannel(t, 1, "mi-oai-001", 1, common.ChannelStatusEnabled)  // 命中
	seedFilterChannel(t, 2, "mi-oai-002", 24, common.ChannelStatusEnabled) // type 不对
	seedFilterChannel(t, 3, "other-1", 1, common.ChannelStatusEnabled)     // name 不对

	t1 := 1
	channels, err := GetAllChannelsForTest(0, 0, "filter", "mi-oai", &t1, nil)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(channels) != 1 || channels[0].Id != 1 {
		t.Fatalf("期望只返回 id=1，实际 %+v", channels)
	}
}

// TestGetAllChannelsForTest_FilterManuallyDisabled 明确指定 status=2 只测手动禁用
func TestGetAllChannelsForTest_FilterManuallyDisabled(t *testing.T) {
	setupChannelFilterDB(t)
	seedFilterChannel(t, 1, "a", 1, common.ChannelStatusEnabled)
	seedFilterChannel(t, 2, "b", 1, common.ChannelStatusManuallyDisabled)
	seedFilterChannel(t, 3, "c", 1, common.ChannelStatusAutoDisabled)

	t1 := 1
	channels, err := GetAllChannelsForTest(0, 0, "filter", "", &t1,
		[]int{common.ChannelStatusManuallyDisabled})
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(channels) != 1 || channels[0].Id != 2 {
		t.Fatalf("期望只返回 id=2 (manually_disabled)，实际 %+v", channels)
	}
}

// TestGetAllChannelsForTest_BackwardCompatible 现有 scope=all/disabled/auto_disabled 不受影响
func TestGetAllChannelsForTest_BackwardCompatible(t *testing.T) {
	setupChannelFilterDB(t)
	seedFilterChannel(t, 1, "a", 1, common.ChannelStatusEnabled)
	seedFilterChannel(t, 2, "b", 1, common.ChannelStatusManuallyDisabled)
	seedFilterChannel(t, 3, "c", 1, common.ChannelStatusAutoDisabled)

	// scope=all 应返回全部（filter 参数传空/nil 应被 case=all 分支忽略）
	channels, err := GetAllChannelsForTest(0, 0, "all", "", nil, nil)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(channels) != 3 {
		t.Fatalf("scope=all 期望 3 个，实际 %d", len(channels))
	}

	// scope=disabled 应返回 status=2,3
	channels, err = GetAllChannelsForTest(0, 0, "disabled", "", nil, nil)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(channels) != 2 {
		t.Fatalf("scope=disabled 期望 2 个，实际 %d", len(channels))
	}
}
