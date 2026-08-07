package aws

import (
	"strings"
	"testing"
)

func TestAwsDisplayNameMapIsWellDefined(t *testing.T) {
	for bedrockID, display := range awsCanonicalToDisplay {
		mapped, ok := AwsModelIDMap[display]
		if !ok {
			t.Errorf("反向表条目 %q -> %q 但 AwsModelIDMap 中无此 key", bedrockID, display)
			continue
		}
		if mapped != bedrockID {
			t.Errorf("往返不闭合: awsCanonicalToDisplay[%q]=%q, AwsModelIDMap[%q]=%q (期望 %q)",
				bedrockID, display, display, mapped, bedrockID)
		}
		if strings.HasSuffix(display, "-thinking") {
			t.Errorf("反向表选到了 -thinking 键: %q -> %q", bedrockID, display)
		}
	}
}

func TestAwsThinkingVariantsShareBaseModelID(t *testing.T) {
	for key, id := range AwsModelIDMap {
		base, isThinking := strings.CutSuffix(key, "-thinking")
		if !isThinking {
			continue
		}
		baseID, ok := AwsModelIDMap[base]
		if !ok {
			t.Errorf("-thinking 键 %q 缺少对应基础键 %q", key, base)
			continue
		}
		if baseID != id {
			t.Errorf("%q 映射到 %q，但基础键 %q 映射到 %q（应相等）", key, id, base, baseID)
		}
	}
}

func TestPreferAwsDisplayName(t *testing.T) {
	tests := []struct {
		candidate, incumbent string
		want                 bool
	}{
		{"claude-opus-4-6", "claude-opus-4-6-thinking", true},
		{"claude-opus-4-6-thinking", "claude-opus-4-6", false},
		{"claude-opus-4-6-thinking", "claude-opus-4-7-thinking", true},
		{"claude-opus-4-7-thinking", "claude-opus-4-6-thinking", false},
		{"aaa", "bbb", true},
		{"bbb", "aaa", false},
		{"same", "same", false},
	}
	for _, tt := range tests {
		got := preferAwsDisplayName(tt.candidate, tt.incumbent)
		if got != tt.want {
			t.Errorf("preferAwsDisplayName(%q, %q) = %v, want %v", tt.candidate, tt.incumbent, got, tt.want)
		}
	}
}

func TestCanonicalAwsModelID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"claude-opus-4-6", "anthropic.claude-opus-4-6-v1"},
		{"claude-opus-4-6-thinking", "anthropic.claude-opus-4-6-v1"},
		{"anthropic.claude-opus-4-6-v1", "anthropic.claude-opus-4-6-v1"},
		{"us.anthropic.claude-opus-4-6-v1", "anthropic.claude-opus-4-6-v1"},
		{"eu.anthropic.claude-opus-4-6-v1", "anthropic.claude-opus-4-6-v1"},
		{"apac.anthropic.claude-opus-4-6-v1", "anthropic.claude-opus-4-6-v1"},
		{"ap.anthropic.claude-opus-4-6-v1", "anthropic.claude-opus-4-6-v1"},
		{"global.anthropic.claude-opus-4-6-v1", "anthropic.claude-opus-4-6-v1"},
		{"us.anthropic.claude-opus-4-6-v1-thinking", "anthropic.claude-opus-4-6-v1"},
		{"claude-sonnet-4-6", "anthropic.claude-sonnet-4-6"},
		{"anthropic.claude-sonnet-4-6", "anthropic.claude-sonnet-4-6"},
		{"anthropic.claude-nonexistent-9-v1:0", "anthropic.claude-nonexistent-9-v1:0"},
		{"us.anthropic.claude-nonexistent-9-v1:0", "anthropic.claude-nonexistent-9-v1:0"},
		{"", ""},
		{"  claude-opus-4-6  ", "anthropic.claude-opus-4-6-v1"},
		{"arn:aws:bedrock:us-east-1:12345:model/foo", "arn:aws:bedrock:us-east-1:12345:model/foo"},
		{"arn:aws:bedrock:us-east-1:12345:model/foo-thinking", "arn:aws:bedrock:us-east-1:12345:model/foo"},
	}
	for _, tt := range tests {
		got := CanonicalAwsModelID(tt.input)
		if got != tt.want {
			t.Errorf("CanonicalAwsModelID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCanonicalAwsModelIDKeepsClaudeV2VersionsDistinct(t *testing.T) {
	v2 := CanonicalAwsModelID("anthropic.claude-v2")
	v2_1 := CanonicalAwsModelID("anthropic.claude-v2:1")
	if v2 == v2_1 {
		t.Errorf("claude-v2 和 claude-v2:1 被合并为同一 canonical: %q", v2)
	}
}

func TestStripAwsRegionPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"us.anthropic.claude-opus-4-6-v1", "anthropic.claude-opus-4-6-v1"},
		{"eu.anthropic.claude-opus-4-6-v1", "anthropic.claude-opus-4-6-v1"},
		{"apac.anthropic.claude-opus-4-6-v1", "anthropic.claude-opus-4-6-v1"},
		{"ap.anthropic.claude-opus-4-6-v1", "anthropic.claude-opus-4-6-v1"},
		{"global.anthropic.claude-opus-4-6-v1", "anthropic.claude-opus-4-6-v1"},
		{"anthropic.claude-opus-4-6-v1", "anthropic.claude-opus-4-6-v1"},
		{"us.us.anthropic.claude-opus-4-6-v1", "us.anthropic.claude-opus-4-6-v1"},
		{"", ""},
		{"claude-opus-4-6", "claude-opus-4-6"},
	}
	for _, tt := range tests {
		got := StripAwsRegionPrefix(tt.input)
		if got != tt.want {
			t.Errorf("StripAwsRegionPrefix(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestAwsDisplayModelName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"anthropic.claude-opus-4-6-v1", "claude-opus-4-6"},
		{"us.anthropic.claude-opus-4-6-v1", "claude-opus-4-6"},
		{"claude-opus-4-6", "claude-opus-4-6"},
		{"claude-opus-4-6-thinking", "claude-opus-4-6"},
		{"anthropic.claude-nonexistent-9", "anthropic.claude-nonexistent-9"},
		{"", ""},
	}
	for _, tt := range tests {
		got := AwsDisplayModelName(tt.input)
		if got != tt.want {
			t.Errorf("AwsDisplayModelName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
