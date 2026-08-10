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

// blockInsertOfModel 装一个 SQLite 触发器，让插入指定模型名的 ability 必然失败。
//
// 用触发器而非"重复模型名"来制造失败，是为了让回滚测试只验证「INSERT 失败就回滚」
// 这一个属性，不耦合任何具体的失败原因 —— 重复模型名已被 normalizeAbilityKeys
// 去重，不再是有效的失败源。
func blockInsertOfModel(t *testing.T, modelName string) {
	t.Helper()
	// SQLite 触发器体内不允许绑定变量（"trigger cannot use variables"），
	// 只能把值拼进 SQL。这里的 modelName 是测试内的固定常量，无注入风险。
	err := DB.Exec(`
		CREATE TRIGGER block_bad_model BEFORE INSERT ON abilities
		FOR EACH ROW WHEN NEW.model = '` + modelName + `'
		BEGIN
			SELECT RAISE(ABORT, 'blocked by test trigger');
		END;
	`).Error
	if err != nil {
		t.Fatalf("创建触发器失败: %v", err)
	}
}

// TestUpdateAbilitiesRollsBackOnInsertFailure 是事务改造的核心回归测试。
//
// 改造前 UpdateAbilities 是「先 DELETE 再 INSERT」且无事务：INSERT 失败时
// DELETE 已提交，该渠道的 abilities 会变成空 —— 等价于所有模型不可路由，
// 而且不会有任何迹象表明模型消失了。
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

	blockInsertOfModel(t, "__boom__")

	ch.Models = "gpt-4,__boom__"
	err := ch.UpdateAbilities()
	if err == nil {
		t.Fatal("被触发器阻止的插入应导致 UpdateAbilities 返回错误")
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

	blockInsertOfModel(t, "__boom__")

	// 目标渠道插入失败
	target.Models = "a,__boom__"
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

// TestAddAbilitiesDeduplicates 覆盖 models / groups 的去重。
//
// 去重前，重复模型名会构造出主键 (group, model, channel_id) 相同的记录导致
// 插入失败 —— 而管理员从 UI 手工编辑模型列表时没有任何去重保护，
// 表现为「编辑渠道后该渠道所有模型突然不可用」。
func TestAddAbilitiesDeduplicates(t *testing.T) {
	setupAbilityTestDB(t)

	tests := []struct {
		name   string
		models string
		group  string
		want   int64
	}{
		{"模型重复", "gpt-4,gpt-4", "default", 1},
		{"模型重复且带空格", "gpt-4, gpt-4 ,gpt-4", "default", 1},
		{"分组重复", "gpt-4", "default,default", 1},
		{"分组重复且带空格", "gpt-4", "default, default", 1},
		{"模型与分组都重复", "a,a,b", "g1,g1,g2", 4}, // 2 models × 2 groups
		{"混入空项", "a,,b, ,a", "default", 2},
		{"无重复时不受影响", "a,b,c", "g1,g2", 6},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := 200 + i
			ch := &Channel{Id: id, Group: tt.group, Models: tt.models, Status: common.ChannelStatusEnabled}
			if err := ch.UpdateAbilities(); err != nil {
				t.Fatalf("UpdateAbilities 不应因重复项失败: %v", err)
			}
			if got := countAbilities(t, id); got != tt.want {
				t.Errorf("ability 数 = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestNormalizeAbilityKeys(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"基础拆分", "a,b", []string{"a", "b"}},
		{"去空白", " a , b ", []string{"a", "b"}},
		{"去重保序", "b,a,b", []string{"b", "a"}},
		{"丢弃空项", "a,,b, ", []string{"a", "b"}},
		{"空字符串", "", nil},
		{"全是分隔符", ",,,", nil},
		{"trim 后重复视为同一项", "a, a", []string{"a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeAbilityKeys(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got %v, want %v", got, tt.want)
					break
				}
			}
		})
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
