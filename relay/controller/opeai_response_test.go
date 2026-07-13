package controller

import (
	"strings"
	"testing"

	"github.com/songquanpeng/one-api/relay/channel/openai"
)

func TestExtractOpenaiResponseNativeUsageDetailsIncludesCacheWriteTokens(t *testing.T) {
	usage := &openai.ResponseUsage{
		InputTokens:  2140,
		OutputTokens: 331,
		TotalTokens:  2471,
		InputTokensDetails: &openai.InputTokensDetails{
			CachedTokens:     2137,
			CacheWriteTokens: 0,
		},
		OutputTokensDetails: &openai.OutputTokensDetails{
			ReasoningTokens: 28,
		},
	}

	details := extractOpenaiReseponseNativeUsageDetails(usage)
	if details == nil {
		t.Fatal("expected usage details")
	}
	if details.CacheReadInputTokens != 2137 {
		t.Fatalf("cache_read_input_tokens = %d, want 2137", details.CacheReadInputTokens)
	}
	if details.CacheCreationInputTokens != 0 {
		t.Fatalf("cache_creation_input_tokens = %d, want 0", details.CacheCreationInputTokens)
	}
}

func TestBuildOpenaiResponseOtherInfoWithUsageDetailsIncludesCacheWriteTokens(t *testing.T) {
	other := buildOpenaiResponseOtherInfoWithUsageDetails("adminInfo:[49]", &OpenaiReseponseUsageDetails{
		InputTokens:              2140,
		OutputTokens:             306,
		TotalTokens:              2446,
		CacheReadInputTokens:     0,
		CacheCreationInputTokens: 2137,
		ReasoningTokens:          27,
	})

	if !strings.Contains(other, "\"cache_creation_input_tokens\":2137") {
		t.Fatalf("other missing cache_creation_input_tokens: %s", other)
	}
	if !strings.Contains(other, "adminInfo:[49]") {
		t.Fatalf("other missing adminInfo: %s", other)
	}
}

func TestBuildOpenaiResponseLogContentIncludesLongMultipliers(t *testing.T) {
	shortContent := buildOpenaiResponseLogContent("/v1/responses", "gpt-5.6-sol", 272000)
	if !strings.Contains(shortContent, "long输入倍率 1.0") || !strings.Contains(shortContent, "long输出倍率 1.0") {
		t.Fatalf("short content missing expected multipliers: %s", shortContent)
	}

	longContent := buildOpenaiResponseLogContent("/v1/responses", "gpt-5.6-sol", 272001)
	if !strings.Contains(longContent, "long输入倍率 2.0") || !strings.Contains(longContent, "long输出倍率 1.5") {
		t.Fatalf("long content missing expected multipliers: %s", longContent)
	}
}
