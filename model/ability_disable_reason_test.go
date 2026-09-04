package model

import (
	"strings"
	"testing"
	"time"
)

// seedEnabledAbility 写一条 enabled 的 abilities 行，供 AutoDisableModelOnChannel 更新。
func seedEnabledAbility(t *testing.T, channelId int, group, modelName string) {
	t.Helper()
	pri := int64(0)
	a := Ability{
		Group:     group,
		Model:     modelName,
		ChannelId: channelId,
		Enabled:   true,
		Priority:  &pri,
	}
	if err := DB.Create(&a).Error; err != nil {
		t.Fatalf("插入 ability 失败: %v", err)
	}
}

// getAbilityReason 读回指定 (channel, model, group) 行的禁用原因，便于断言。
func getAbilityReason(t *testing.T, channelId int, group, modelName string) string {
	t.Helper()
	var a Ability
	if err := DB.Where("channel_id = ? AND `group` = ? AND model = ?", channelId, group, modelName).First(&a).Error; err != nil {
		t.Fatalf("查询 ability 失败: %v", err)
	}
	return a.AutoDisabledReason
}

func TestAutoDisableModelOnChannel_PersistsReason(t *testing.T) {
	setupUsageJudgeDB(t)

	seedEnabledAbility(t, 1, "default", "gpt-4")
	seedEnabledAbility(t, 1, "vip", "gpt-4") // 多 group 同模型，应一并写入
	seedEnabledAbility(t, 1, "default", "gpt-4o")

	if err := AutoDisableModelOnChannel(1, "gpt-4", "Invalid API key provided"); err != nil {
		t.Fatalf("模型级禁用失败: %v", err)
	}

	if got := getAbilityReason(t, 1, "default", "gpt-4"); got != "Invalid API key provided" {
		t.Fatalf("default group 行应写入禁用原因，实际 %q", got)
	}
	if got := getAbilityReason(t, 1, "vip", "gpt-4"); got != "Invalid API key provided" {
		t.Fatalf("vip group 行应写入禁用原因，实际 %q", got)
	}
	if got := getAbilityReason(t, 1, "default", "gpt-4o"); got != "" {
		t.Fatalf("未禁用模型不应有原因，实际 %q", got)
	}
}

func TestAutoDisableModelOnChannel_TruncatesLongReason(t *testing.T) {
	setupUsageJudgeDB(t)
	seedEnabledAbility(t, 1, "default", "gpt-4")

	longReason := strings.Repeat("错", 2000) // 6000 字节，超过 1024 字符
	if err := AutoDisableModelOnChannel(1, "gpt-4", longReason); err != nil {
		t.Fatalf("模型级禁用失败: %v", err)
	}
	got := getAbilityReason(t, 1, "default", "gpt-4")
	if runeCount := len([]rune(got)); runeCount != maxAbilityDisableReasonLen {
		t.Fatalf("超长原因应截断到 %d 字符，实际 %d", maxAbilityDisableReasonLen, runeCount)
	}
	if !strings.HasPrefix(longReason, got) {
		t.Fatalf("截断应保留前缀")
	}
}

func TestEnableModelOnChannel_ClearsReason(t *testing.T) {
	setupUsageJudgeDB(t)
	// EnableModelOnChannel 会顺带把 auto_disabled 渠道 status 提升回 enabled，需要 channels 表
	if err := DB.AutoMigrate(&Channel{}); err != nil {
		t.Fatalf("建 channels 表失败: %v", err)
	}

	seedEnabledAbility(t, 1, "default", "gpt-4")
	if err := AutoDisableModelOnChannel(1, "gpt-4", "insufficient quota"); err != nil {
		t.Fatalf("模型级禁用失败: %v", err)
	}
	if err := EnableModelOnChannel(1, "gpt-4"); err != nil {
		t.Fatalf("模型级恢复失败: %v", err)
	}
	if got := getAbilityReason(t, 1, "default", "gpt-4"); got != "" {
		t.Fatalf("恢复后应清空禁用原因，实际 %q", got)
	}

	var a Ability
	if err := DB.Where("channel_id = ? AND model = ?", 1, "gpt-4").First(&a).Error; err != nil {
		t.Fatalf("查询 ability 失败: %v", err)
	}
	if !a.Enabled || a.AutoDisabled || a.AutoDisabledTime != 0 {
		t.Fatalf("恢复后应 enabled=true 且 auto_disabled/auto_disabled_time 复位，实际 %+v", a)
	}
}

func TestGetLatestAutoDisabledModelReason(t *testing.T) {
	t.Run("无候选_返回空", func(t *testing.T) {
		setupUsageJudgeDB(t)
		m, r, err := GetLatestAutoDisabledModelReason(1)
		if err != nil {
			t.Fatalf("查询失败: %v", err)
		}
		if m != "" || r != "" {
			t.Fatalf("无候选应返回空，实际 (%q, %q)", m, r)
		}
	})

	t.Run("按时间倒序_返回最新", func(t *testing.T) {
		setupUsageJudgeDB(t)

		seedEnabledAbility(t, 1, "default", "gpt-4")
		seedEnabledAbility(t, 1, "default", "gpt-4o")
		seedEnabledAbility(t, 2, "default", "gpt-4")

		// 先禁 gpt-4，再禁 gpt-4o：gpt-4o 更晚，应被返回。
		// auto_disabled_time 是秒级时间戳，直接回拨 gpt-4 的禁用时间避免真实 sleep。
		if err := AutoDisableModelOnChannel(1, "gpt-4", "old error"); err != nil {
			t.Fatal(err)
		}
		if err := DB.Model(&Ability{}).
			Where("channel_id = ? AND model = ?", 1, "gpt-4").
			Update("auto_disabled_time", time.Now().Unix()-100).Error; err != nil {
			t.Fatal(err)
		}
		if err := AutoDisableModelOnChannel(1, "gpt-4o", "latest error"); err != nil {
			t.Fatal(err)
		}
		// 其他渠道的禁用不参与
		if err := AutoDisableModelOnChannel(2, "gpt-4", "other channel"); err != nil {
			t.Fatal(err)
		}

		m, r, err := GetLatestAutoDisabledModelReason(1)
		if err != nil {
			t.Fatalf("查询失败: %v", err)
		}
		if m != "gpt-4o" || r != "latest error" {
			t.Fatalf("应返回最新被禁模型，实际 (%q, %q)", m, r)
		}
	})

	t.Run("存量行无原因_原因返回空", func(t *testing.T) {
		setupUsageJudgeDB(t)
		// 模拟旧版本写入的行：auto_disabled=1 但 reason 为空
		seedAutoDisabled(t, 1, "default", "gpt-4", 1200)
		m, r, err := GetLatestAutoDisabledModelReason(1)
		if err != nil {
			t.Fatalf("查询失败: %v", err)
		}
		if m != "gpt-4" || r != "" {
			t.Fatalf("存量行应返回模型名+空原因，实际 (%q, %q)", m, r)
		}
	})
}

func TestTruncateReason(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		maxLen int
		want   string
	}{
		{"短串原样返回", "abc", 10, "abc"},
		{"正好等于上限", "abcde", 5, "abcde"},
		{"ASCII 超长截断", "abcdefghij", 5, "abcde"},
		{"中文按字符截断不截半个字", strings.Repeat("错", 10), 3, "错错错"},
		{"字节数超限但字符数未超", strings.Repeat("错", 4), 10, strings.Repeat("错", 4)}, // 12 字节 > 10，但 4 字符 <= 10
		{"空串", "", 5, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := truncateReason(c.in, c.maxLen); got != c.want {
				t.Fatalf("truncateReason(%q, %d) = %q, want %q", c.in, c.maxLen, got, c.want)
			}
		})
	}
}
