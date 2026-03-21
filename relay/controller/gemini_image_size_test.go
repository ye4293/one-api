package controller

import (
	"testing"
)

func TestNormalizeGeminiImageSize(t *testing.T) {
	testCases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "lowercase half k alias", input: "0.5k", want: "512"},
		{name: "uppercase half k alias", input: "0.5K", want: "512"},
		{name: "official 512 value", input: "512", want: "512"},
		{name: "lowercase 2k", input: "2k", want: "2K"},
		{name: "trim spaces", input: " 1k ", want: "1K"},
		{name: "empty value", input: "", want: ""},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeGeminiImageSize(tc.input); got != tc.want {
				t.Fatalf("normalizeGeminiImageSize(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
