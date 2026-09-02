package monitor

import "testing"

// TestComposeUsageDisableReason 覆盖整渠道禁用原因的拼接与回退。
// 参见 docs/plans/2026-08-31-ability-disable-reason.md
func TestComposeUsageDisableReason(t *testing.T) {
	cases := []struct {
		name       string
		usedModels int
		lastModel  string
		lastReason string
		want       string
	}{
		{
			name:       "带最后被禁模型的真实原因",
			usedModels: 3,
			lastModel:  "gpt-4o",
			lastReason: "Invalid API key provided",
			want:       "最近使用中的 3 个模型全部被自动禁用，最后模型禁用原因：Invalid API key provided（模型：gpt-4o）",
		},
		{
			name:       "无模型名_回退通用文案",
			usedModels: 2,
			lastModel:  "",
			lastReason: "some error",
			want:       "最近使用中的 2 个模型全部被自动禁用",
		},
		{
			name:       "原因为空_存量数据_回退通用文案",
			usedModels: 5,
			lastModel:  "gpt-4",
			lastReason: "",
			want:       "最近使用中的 5 个模型全部被自动禁用",
		},
		{
			name:       "两者皆空_回退通用文案",
			usedModels: 1,
			lastModel:  "",
			lastReason: "",
			want:       "最近使用中的 1 个模型全部被自动禁用",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := composeUsageDisableReason(c.usedModels, c.lastModel, c.lastReason); got != c.want {
				t.Fatalf("composeUsageDisableReason(%d, %q, %q) = %q, want %q",
					c.usedModels, c.lastModel, c.lastReason, got, c.want)
			}
		})
	}
}
