package anthropic

import "testing"

func TestIsNoSamplingModel(t *testing.T) {
	cases := []struct {
		name  string
		model string
		want  bool
	}{
		// 4.7+ 所有系列 → no-sampling
		{"opus-4-7", "claude-opus-4-7", true},
		{"opus-4-8", "claude-opus-4-8", true},
		{"opus-4-8-thinking", "claude-opus-4-8-thinking", true},
		{"future sonnet-4-7", "claude-sonnet-4-7", true},
		{"future haiku-4-9", "claude-haiku-4-9", true},
		{"future double-digit minor 4-10", "claude-opus-4-10", true},
		{"future major 5", "claude-opus-5", true},
		{"future major 5 with minor", "claude-sonnet-5-2", true},
		{"aws native id", "anthropic.claude-opus-4-8-v1", true},
		{"region-prefixed native id", "us.anthropic.claude-opus-4-7-v1", true},

		// < 4.7 → 保留 sampling
		{"opus-4-6", "claude-opus-4-6", false},
		{"sonnet-4-6", "claude-sonnet-4-6", false},
		{"haiku-4-5 dated", "claude-haiku-4-5-20251001", false},
		{"sonnet-4-5 dated", "claude-sonnet-4-5-20250929", false},
		{"opus-4-1 dated (4.1 not 4.10)", "claude-opus-4-1-20250805", false},
		{"opus-4 dated is 4.0 (date not minor)", "claude-opus-4-20250514", false},
		{"opus-4-6-thinking", "claude-opus-4-6-thinking", false},

		// 旧格式（版本在 family 前）→ major=3，排除
		{"claude-3-7-sonnet", "claude-3-7-sonnet-20250219", false},
		{"claude-3-5-sonnet", "claude-3-5-sonnet-20241022", false},

		// 非 claude 模型 → 不判定
		{"gpt", "gpt-4o", false},
		{"gemini", "gemini-2.5-pro", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsNoSamplingModel(tc.model); got != tc.want {
				t.Fatalf("IsNoSamplingModel(%q) = %v, want %v", tc.model, got, tc.want)
			}
		})
	}
}

func TestParseClaudeMajorMinor(t *testing.T) {
	cases := []struct {
		model             string
		major, minor      int
		ok                bool
	}{
		{"claude-opus-4-8", 4, 8, true},
		{"claude-opus-4-7", 4, 7, true},
		{"claude-opus-4-6", 4, 6, true},
		{"claude-opus-4-10", 4, 10, true},
		{"claude-opus-4-1-20250805", 4, 1, true}, // minor=1，日期段被忽略
		{"claude-opus-4-20250514", 4, 0, true},   // 8 位日期不是 minor
		{"claude-opus-5", 5, 0, true},
		{"claude-sonnet-5-2", 5, 2, true},
		{"claude-3-7-sonnet-20250219", 0, 0, false}, // 旧格式不匹配新格式正则
		{"gpt-4o", 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			major, minor, ok := parseClaudeMajorMinor(tc.model)
			if major != tc.major || minor != tc.minor || ok != tc.ok {
				t.Fatalf("parseClaudeMajorMinor(%q) = (%d,%d,%v), want (%d,%d,%v)",
					tc.model, major, minor, ok, tc.major, tc.minor, tc.ok)
			}
		})
	}
}
