package controller

import (
	"strings"
	"testing"
)

func TestAppendUsageDetailsToOtherIncludesCacheFields(t *testing.T) {
	other := appendUsageDetailsToOther("adminInfo:[49]", UsageDetailsForLog{
		InputText:                0,
		InputImage:               0,
		OutputText:               0,
		OutputImage:              0,
		OutputReasoning:          0,
		CachedTokens:             2137,
		CacheReadInputTokens:     2137,
		CacheCreationInputTokens: 0,
	})

	if !strings.Contains(other, "adminInfo:[49]") {
		t.Fatalf("other missing adminInfo: %s", other)
	}
	if !strings.Contains(other, "\"cached_tokens\":2137") {
		t.Fatalf("other missing cached_tokens: %s", other)
	}
	if !strings.Contains(other, "\"cache_read_input_tokens\":2137") {
		t.Fatalf("other missing cache_read_input_tokens: %s", other)
	}
}

func TestAppendUsageDetailsToOtherIncludesCacheCreationField(t *testing.T) {
	other := appendUsageDetailsToOther("", UsageDetailsForLog{
		InputText:                0,
		InputImage:               0,
		OutputText:               0,
		OutputImage:              0,
		OutputReasoning:          0,
		CacheCreationInputTokens: 2137,
	})

	if !strings.Contains(other, "\"cache_creation_input_tokens\":2137") {
		t.Fatalf("other missing cache_creation_input_tokens: %s", other)
	}
}
