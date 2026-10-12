package common

import (
	"maps"
	"testing"
)

func TestGPT6LongContextMultipliers(t *testing.T) {
	old := LongContextThreshold
	LongContextThreshold = maps.Clone(old)
	t.Cleanup(func() { LongContextThreshold = old })
	models := []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna", "gpt-6", "gpt-6.1", "gpt-7-test", "gpt-10.2-test-2099-01-01"}
	for model := range old {
		models = append(models, model)
	}
	for _, name := range models {
		for _, input := range []int{271999, 272000, 272001} {
			want := LongContextMultipliers{InputMultiplier: 1, OutputMultiplier: 1}
			if input > 272000 {
				want.InputMultiplier, want.OutputMultiplier = 2, 1.5
			}
			if got := GetLongContextMultipliers(name, input); got != want {
				t.Errorf("%s/%d: %+v, want %+v", name, input, got, want)
			}
		}
	}
	for _, name := range []string{"gpt-5.9-unregistered", "gpt6", "my-gpt-6", "GPT-6-astra", "gpt-6foo", "gpt-6.foo", "gpt-06", "gpt-6_priority", "gpt-6.1foo", "gpt-999999999999999999999999999", "claude-test"} {
		got := GetLongContextMultipliers(name, 999999)
		if got.InputMultiplier != 1 || got.OutputMultiplier != 1 {
			t.Errorf("unexpected long context for %s: %+v", name, got)
		}
	}
	LongContextThreshold["gpt-6-astra"] = 500000
	if got := GetLongContextMultipliers("gpt-6-astra", 300000); got.InputMultiplier != 1 {
		t.Fatal("default threshold overrides explicit threshold")
	}
	LongContextThreshold["gpt-6-astra"] = 1000
	if got := GetLongContextMultipliers("gpt-6-astra", 1000); got.InputMultiplier != 1 {
		t.Fatal(got)
	}
	if got := GetLongContextMultipliers("gpt-6-astra", 1001); got.InputMultiplier != 2 || got.OutputMultiplier != 1.5 {
		t.Fatal(got)
	}
}
