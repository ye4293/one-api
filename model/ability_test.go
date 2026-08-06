package model

import (
	"testing"

	"github.com/songquanpeng/one-api/common"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupAbilityTestDB 建一个只含 abilities 表的内存库，并临时替换全局 DB。
// 每个测试用独立 DSN，避免相互污染。
func setupAbilityTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{Logger: logger.Discard},
	)
	if err != nil {
		t.Skipf("SQLite 不可用（可能未启用 CGO），跳过: %v", err)
	}
	if err := db.AutoMigrate(&Ability{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	orig := DB
	DB = db
	t.Cleanup(func() {
		DB = orig
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
}

func countAbilities(t *testing.T, channelId int) int64 {
	t.Helper()
	var n int64
	if err := DB.Model(&Ability{}).Where("channel_id = ?", channelId).Count(&n).Error; err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	return n
}

func TestUpdateAbilitiesRebuilds(t *testing.T) {
	setupAbilityTestDB(t)

	ch := &Channel{
		Id:     1,
		Group:  "default,vip",
		Models: "gpt-4,gpt-4o",
		Status: common.ChannelStatusEnabled,
	}
	if err := ch.UpdateAbilities(); err != nil {
		t.Fatalf("首次 UpdateAbilities 失败: %v", err)
	}
	if got := countAbilities(t, 1); got != 4 { // 2 models × 2 groups
		t.Fatalf("ability 数 = %d, want 4", got)
	}

	// 模型减少后重建
	ch.Models = "gpt-4o"
	if err := ch.UpdateAbilities(); err != nil {
		t.Fatalf("二次 UpdateAbilities 失败: %v", err)
	}
	if got := countAbilities(t, 1); got != 2 {
		t.Fatalf("重建后 ability 数 = %d, want 2", got)
	}

	var remaining []Ability
	if err := DB.Where("channel_id = ?", 1).Find(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	for _, a := range remaining {
		if a.Model != "gpt-4o" {
			t.Errorf("残留了已移除的模型: %s", a.Model)
		}
	}
}

// TestUpdateAbilitiesRollsBackOnInsertFailure 是本次事务改造的核心回归测试。
//
// 改造前 UpdateAbilities 是「先 DELETE 再 INSERT」且无事务：INSERT 失败时
// DELETE 已提交，该渠道的 abilities 会变成空 —— 等价于所有模型不可路由，
// 而且不会有任何迹象表明模型消失了。
//
// 触发路径是真实存在的：channel.Models 含重复模型名时，addAbilitiesTx 会构造出
// 主键 (group, model, channel_id) 相同的两条记录，插入必然冲突。
func TestUpdateAbilitiesRollsBackOnInsertFailure(t *testing.T) {
	setupAbilityTestDB(t)

	ch := &Channel{
		Id:     7,
		Group:  "default",
		Models: "gpt-4,gpt-4o,claude-3",
		Status: common.ChannelStatusEnabled,
	}
	if err := ch.UpdateAbilities(); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}
	before := countAbilities(t, 7)
	if before != 3 {
		t.Fatalf("初始 ability 数 = %d, want 3", before)
	}

	// 制造 INSERT 失败：重复模型名 → 复合主键冲突
	ch.Models = "gpt-4,gpt-4"
	err := ch.UpdateAbilities()
	if err == nil {
		t.Fatal("重复模型名应导致 UpdateAbilities 返回错误")
	}

	after := countAbilities(t, 7)
	if after != before {
		t.Errorf("事务未回滚：失败前 %d 条 ability，失败后 %d 条。"+
			"若为 0 则该渠道所有模型已不可路由", before, after)
	}

	// 原有记录必须原封不动
	var remaining []Ability
	if err := DB.Where("channel_id = ?", 7).Order("model").Find(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"gpt-4": true, "gpt-4o": true, "claude-3": true}
	for _, a := range remaining {
		if !want[a.Model] {
			t.Errorf("出现了预期外的 ability: %s", a.Model)
		}
		delete(want, a.Model)
	}
	if len(want) != 0 {
		t.Errorf("回滚后丢失了模型: %v", want)
	}
}

func TestUpdateAbilitiesDoesNotTouchOtherChannels(t *testing.T) {
	setupAbilityTestDB(t)

	other := &Channel{Id: 2, Group: "default", Models: "shared-model", Status: common.ChannelStatusEnabled}
	if err := other.UpdateAbilities(); err != nil {
		t.Fatal(err)
	}
	target := &Channel{Id: 3, Group: "default", Models: "a,b", Status: common.ChannelStatusEnabled}
	if err := target.UpdateAbilities(); err != nil {
		t.Fatal(err)
	}

	// 目标渠道插入失败
	target.Models = "dup,dup"
	if err := target.UpdateAbilities(); err == nil {
		t.Fatal("应当失败")
	}

	if got := countAbilities(t, 2); got != 1 {
		t.Errorf("其他渠道的 ability 被影响：%d 条，want 1", got)
	}
	if got := countAbilities(t, 3); got != 2 {
		t.Errorf("目标渠道未回滚：%d 条，want 2", got)
	}
}

func TestAddAbilitiesEnabledFollowsChannelStatus(t *testing.T) {
	setupAbilityTestDB(t)

	tests := []struct {
		name        string
		status      int
		wantEnabled bool
	}{
		{"启用渠道", common.ChannelStatusEnabled, true},
		{"手动禁用", common.ChannelStatusManuallyDisabled, false},
		{"自动禁用", common.ChannelStatusAutoDisabled, false},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := 100 + i
			ch := &Channel{Id: id, Group: "default", Models: "m", Status: tt.status}
			if err := ch.UpdateAbilities(); err != nil {
				t.Fatal(err)
			}
			var a Ability
			if err := DB.Where("channel_id = ?", id).First(&a).Error; err != nil {
				t.Fatal(err)
			}
			if a.Enabled != tt.wantEnabled {
				t.Errorf("Enabled = %v, want %v", a.Enabled, tt.wantEnabled)
			}
		})
	}
}

func TestUpdateAbilitiesWithEmptyModels(t *testing.T) {
	setupAbilityTestDB(t)

	ch := &Channel{Id: 9, Group: "default", Models: "a,b", Status: common.ChannelStatusEnabled}
	if err := ch.UpdateAbilities(); err != nil {
		t.Fatal(err)
	}
	// 模型清空：应成功删干净且不报错（上游同步删空模型时会走这条路）
	ch.Models = ""
	if err := ch.UpdateAbilities(); err != nil {
		t.Fatalf("清空模型不应报错: %v", err)
	}
	if got := countAbilities(t, 9); got != 0 {
		t.Errorf("清空后仍有 %d 条 ability", got)
	}
}
