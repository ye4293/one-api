package controller

import (
	"testing"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
)

func TestHealthProbeInterval(t *testing.T) {
	origFast := config.UpstreamModelHealthProbeFastIntervalMinutes
	origSteady := config.UpstreamModelHealthProbeSteadyIntervalMinutes
	t.Cleanup(func() {
		config.UpstreamModelHealthProbeFastIntervalMinutes = origFast
		config.UpstreamModelHealthProbeSteadyIntervalMinutes = origSteady
	})

	config.UpstreamModelHealthProbeFastIntervalMinutes = 10
	config.UpstreamModelHealthProbeSteadyIntervalMinutes = 60

	tests := []struct {
		name string
		st   config.ModelHealthState
		want int64 // 秒
	}{
		{"S=0 → fast", config.ModelHealthState{Successes: 0}, 600},
		{"S=1 → fast", config.ModelHealthState{Successes: 1}, 600},
		{"S=2 → fast", config.ModelHealthState{Successes: 2}, 600},
		{"S=3 → steady", config.ModelHealthState{Successes: 3}, 3600},
		{"S=4 → steady", config.ModelHealthState{Successes: 4}, 3600},
		{"S=99 → steady", config.ModelHealthState{Successes: 99}, 3600},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := healthProbeInterval(tt.st); got != tt.want {
				t.Errorf("healthProbeInterval() = %d, want %d", got, tt.want)
			}
		})
	}

	// 配置为 0 时兜底默认值
	t.Run("fast=0兜底10", func(t *testing.T) {
		config.UpstreamModelHealthProbeFastIntervalMinutes = 0
		got := healthProbeInterval(config.ModelHealthState{Successes: 0})
		if got != 600 {
			t.Errorf("got %d, want 600", got)
		}
		config.UpstreamModelHealthProbeFastIntervalMinutes = 10
	})
	t.Run("steady=0兜底60", func(t *testing.T) {
		config.UpstreamModelHealthProbeSteadyIntervalMinutes = 0
		got := healthProbeInterval(config.ModelHealthState{Successes: 3})
		if got != 3600 {
			t.Errorf("got %d, want 3600", got)
		}
		config.UpstreamModelHealthProbeSteadyIntervalMinutes = 60
	})
}

func TestHealthProbeCandidates(t *testing.T) {
	origFast := config.UpstreamModelHealthProbeFastIntervalMinutes
	origSteady := config.UpstreamModelHealthProbeSteadyIntervalMinutes
	t.Cleanup(func() {
		config.UpstreamModelHealthProbeFastIntervalMinutes = origFast
		config.UpstreamModelHealthProbeSteadyIntervalMinutes = origSteady
	})
	config.UpstreamModelHealthProbeFastIntervalMinutes = 10
	config.UpstreamModelHealthProbeSteadyIntervalMinutes = 60

	now := int64(10000)

	tests := []struct {
		name        string
		local       []string
		ignored     []string
		pendingAdd  []string
		pendingRem  []string
		health      map[string]config.ModelHealthState
		channelType int
		wantTracked []string
		wantDue     []string
	}{
		{
			name:        "无状态模型必进 due",
			local:       []string{"a", "b", "c"},
			health:      map[string]config.ModelHealthState{},
			wantTracked: []string{"a", "b", "c"},
			wantDue:     []string{"a", "b", "c"},
		},
		{
			name:  "未到期模型进 tracked 但不进 due",
			local: []string{"a", "b"},
			health: map[string]config.ModelHealthState{
				"a": {LastProbe: now - 100, Successes: 0},  // fast=600s, 仅过了100s → 未到期
				"b": {LastProbe: now - 700, Successes: 0},  // 过了700s > 600s → 到期
			},
			wantTracked: []string{"a", "b"},
			wantDue:     []string{"b"},
		},
		{
			name:        "ignored 不进 tracked",
			local:       []string{"a", "b", "ignored-model"},
			ignored:     []string{"ignored-model"},
			health:      map[string]config.ModelHealthState{},
			wantTracked: []string{"a", "b"},
			wantDue:     []string{"a", "b"},
		},
		{
			name:        "regex 忽略不进 tracked",
			local:       []string{"a", "internal-x", "internal-y"},
			ignored:     []string{"regex:^internal-"},
			health:      map[string]config.ModelHealthState{},
			wantTracked: []string{"a"},
			wantDue:     []string{"a"},
		},
		{
			name:        "pendingAdd 不进 tracked",
			local:       []string{"a", "b"},
			pendingAdd:  []string{"a"},
			health:      map[string]config.ModelHealthState{},
			wantTracked: []string{"b"},
			wantDue:     []string{"b"},
		},
		{
			name:        "pendingRemove 不进 tracked",
			local:       []string{"a", "b"},
			pendingRem:  []string{"b"},
			health:      map[string]config.ModelHealthState{},
			wantTracked: []string{"a"},
			wantDue:     []string{"a"},
		},
		{
			name:        "Vertex claude-* removal-protected 不进 tracked",
			local:       []string{"claude-opus-4-6", "gemini-2.5-pro"},
			channelType: common.ChannelTypeVertexAI,
			health:      map[string]config.ModelHealthState{},
			wantTracked: []string{"gemini-2.5-pro"},
			wantDue:     []string{"gemini-2.5-pro"},
		},
		{
			name:  "定型模型在 fast < elapsed < steady 时不到期",
			local: []string{"a"},
			health: map[string]config.ModelHealthState{
				"a": {LastProbe: now - 1800, Successes: 3}, // steady=3600s, 仅过1800s → 未到期
			},
			wantTracked: []string{"a"},
			wantDue:     []string{},
		},
		{
			name:  "due 按 LastProbe 升序",
			local: []string{"a", "b", "c"},
			health: map[string]config.ModelHealthState{
				"a": {LastProbe: 500},
				"b": {LastProbe: 100},
				"c": {LastProbe: 300},
			},
			wantTracked: []string{"a", "b", "c"},
			wantDue:     []string{"b", "c", "a"}, // 100 < 300 < 500
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTracked, gotDue := healthProbeCandidates(
				tt.local, tt.ignored, tt.pendingAdd, tt.pendingRem,
				tt.health, tt.channelType, now,
			)
			if !equalModelSets(gotTracked, tt.wantTracked) {
				t.Errorf("tracked = %v, want %v", gotTracked, tt.wantTracked)
			}
			// due 是有序的，用 slices 精确比较
			if len(gotDue) != len(tt.wantDue) {
				t.Fatalf("due len = %d, want %d; got %v", len(gotDue), len(tt.wantDue), gotDue)
			}
			for i := range gotDue {
				if gotDue[i] != tt.wantDue[i] {
					t.Errorf("due[%d] = %q, want %q; full: %v", i, gotDue[i], tt.wantDue[i], gotDue)
					break
				}
			}
		})
	}
}

func TestApplyHealthVerdicts(t *testing.T) {
	now := int64(1000)
	threshold := 3

	tests := []struct {
		name       string
		health     map[string]config.ModelHealthState
		verdicts   map[string]probeVerdict
		wantNext   map[string]config.ModelHealthState
		wantRemove []string
	}{
		{
			name:   "alive 加成功清失败",
			health: map[string]config.ModelHealthState{"a": {Fails: 2, Successes: 0, LastProbe: 500}},
			verdicts: map[string]probeVerdict{"a": verdictAlive},
			wantNext: map[string]config.ModelHealthState{"a": {Fails: 0, Successes: 1, LastProbe: now}},
		},
		{
			name:   "not_found 加失败清成功",
			health: map[string]config.ModelHealthState{"a": {Fails: 0, Successes: 5, LastProbe: 500}},
			verdicts: map[string]probeVerdict{"a": verdictNotFound},
			wantNext: map[string]config.ModelHealthState{"a": {Fails: 1, Successes: 0, LastProbe: now}},
		},
		{
			name:   "rate_limited 不动计数器",
			health: map[string]config.ModelHealthState{"a": {Fails: 1, Successes: 2, LastProbe: 500}},
			verdicts: map[string]probeVerdict{"a": verdictRateLimited},
			wantNext: map[string]config.ModelHealthState{"a": {Fails: 1, Successes: 2, LastProbe: now}},
		},
		{
			name:   "unavailable 不动计数器",
			health: map[string]config.ModelHealthState{"a": {Fails: 1, Successes: 2, LastProbe: 500}},
			verdicts: map[string]probeVerdict{"a": verdictUnavailable},
			wantNext: map[string]config.ModelHealthState{"a": {Fails: 1, Successes: 2, LastProbe: now}},
		},
		{
			name:   "inconclusive 不动计数器",
			health: map[string]config.ModelHealthState{"a": {Fails: 2, Successes: 0, LastProbe: 500}},
			verdicts: map[string]probeVerdict{"a": verdictInconclusive},
			wantNext: map[string]config.ModelHealthState{"a": {Fails: 2, Successes: 0, LastProbe: now}},
		},
		{
			name:   "skipped 不动计数器",
			health: map[string]config.ModelHealthState{"a": {Fails: 2, Successes: 0, LastProbe: 500}},
			verdicts: map[string]probeVerdict{"a": verdictSkipped},
			wantNext: map[string]config.ModelHealthState{"a": {Fails: 2, Successes: 0, LastProbe: now}},
		},
		{
			name:       "达阈值进 toRemove",
			health:     map[string]config.ModelHealthState{"a": {Fails: 2, LastProbe: 500}},
			verdicts:   map[string]probeVerdict{"a": verdictNotFound},
			wantNext:   map[string]config.ModelHealthState{"a": {Fails: 3, Successes: 0, LastProbe: now}},
			wantRemove: []string{"a"},
		},
		{
			name:   "F=2 后 alive 恢复，不进 toRemove",
			health: map[string]config.ModelHealthState{"a": {Fails: 2, LastProbe: 500}},
			verdicts: map[string]probeVerdict{"a": verdictAlive},
			wantNext: map[string]config.ModelHealthState{"a": {Fails: 0, Successes: 1, LastProbe: now}},
		},
		{
			name:   "verdicts 缺失的模型状态完全不变",
			health: map[string]config.ModelHealthState{"a": {Fails: 2, Successes: 0, LastProbe: 500}, "b": {Fails: 1, LastProbe: 400}},
			verdicts: map[string]probeVerdict{"a": verdictNotFound}, // b 不在 verdicts 里
			wantNext: map[string]config.ModelHealthState{
				"a": {Fails: 3, Successes: 0, LastProbe: now},
				"b": {Fails: 1, Successes: 0, LastProbe: 400}, // 一字节不变
			},
			wantRemove: []string{"a"},
		},
		{
			name:     "nil health 初始化新状态",
			health:   nil,
			verdicts: map[string]probeVerdict{"x": verdictAlive},
			wantNext: map[string]config.ModelHealthState{"x": {Fails: 0, Successes: 1, LastProbe: now}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotNext, gotRemove := applyHealthVerdicts(tt.health, tt.verdicts, now, threshold)

			// 验证 next
			if len(gotNext) != len(tt.wantNext) {
				t.Fatalf("next len = %d, want %d; got %v", len(gotNext), len(tt.wantNext), gotNext)
			}
			for k, want := range tt.wantNext {
				got, ok := gotNext[k]
				if !ok {
					t.Errorf("next missing key %q", k)
					continue
				}
				if got != want {
					t.Errorf("next[%q] = %+v, want %+v", k, got, want)
				}
			}

			// 验证 toRemove
			if len(gotRemove) != len(tt.wantRemove) {
				t.Fatalf("toRemove = %v, want %v", gotRemove, tt.wantRemove)
			}
			for i := range gotRemove {
				if gotRemove[i] != tt.wantRemove[i] {
					t.Errorf("toRemove[%d] = %q, want %q", i, gotRemove[i], tt.wantRemove[i])
				}
			}
		})
	}
}

func TestHealthChannelWideFault(t *testing.T) {
	origFast := config.UpstreamModelHealthProbeFastIntervalMinutes
	origThreshold := config.UpstreamModelHealthProbeFailThreshold
	t.Cleanup(func() {
		config.UpstreamModelHealthProbeFastIntervalMinutes = origFast
		config.UpstreamModelHealthProbeFailThreshold = origThreshold
	})
	config.UpstreamModelHealthProbeFastIntervalMinutes = 10
	config.UpstreamModelHealthProbeFailThreshold = 3

	now := int64(10000)
	threshold := 3

	tests := []struct {
		name        string
		tracked     []string
		health      map[string]config.ModelHealthState
		verdicts    map[string]probeVerdict
		wantFault   bool
		wantRemove  int // len(healthRemove) — fault 时为 0
	}{
		{
			name:    "tracked=3 全部 F=3 → fault",
			tracked: []string{"a", "b", "c"},
			health: map[string]config.ModelHealthState{
				"a": {Fails: 2}, "b": {Fails: 2}, "c": {Fails: 2},
			},
			verdicts:   map[string]probeVerdict{"a": verdictNotFound, "b": verdictNotFound, "c": verdictNotFound},
			wantFault:  true,
			wantRemove: 0,
		},
		{
			name:    "tracked=3 其中 2 个 F=3 → 不 fault，删 2",
			tracked: []string{"a", "b", "c"},
			health: map[string]config.ModelHealthState{
				"a": {Fails: 2}, "b": {Fails: 2}, "c": {Fails: 0},
			},
			verdicts:   map[string]probeVerdict{"a": verdictNotFound, "b": verdictNotFound, "c": verdictAlive},
			wantFault:  false,
			wantRemove: 2,
		},
		{
			name:    "tracked=1 单模型失败 → fault（禁用渠道优于删空）",
			tracked: []string{"a"},
			health: map[string]config.ModelHealthState{
				"a": {Fails: 2},
			},
			verdicts:   map[string]probeVerdict{"a": verdictNotFound},
			wantFault:  true,
			wantRemove: 0,
		},
		{
			name:       "tracked=0 → 不 fault",
			tracked:    []string{},
			health:     map[string]config.ModelHealthState{},
			verdicts:   map[string]probeVerdict{},
			wantFault:  false,
			wantRemove: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 直接测 applyHealthVerdicts + 全军覆没判定逻辑
			_, toRemove := applyHealthVerdicts(tt.health, tt.verdicts, now, threshold)

			isFault := len(toRemove) > 0 && len(toRemove) >= len(tt.tracked)
			if isFault != tt.wantFault {
				t.Errorf("fault = %v, want %v (tracked=%d toRemove=%d)",
					isFault, tt.wantFault, len(tt.tracked), len(toRemove))
			}

			if tt.wantFault {
				// fault 时 healthRemove 应为 nil（全军覆没分支清空）
				if tt.wantRemove != 0 {
					t.Fatal("test setup error: wantFault=true but wantRemove!=0")
				}
			} else {
				if len(toRemove) != tt.wantRemove {
					t.Errorf("toRemove len = %d, want %d", len(toRemove), tt.wantRemove)
				}
			}
		})
	}

	// 核心回归：tracked 是分母而非 localModels
	t.Run("tracked是分母：5本地2ignored剩3全失败→fault", func(t *testing.T) {
		localModels := []string{"a", "b", "c", "ign1", "ign2"}
		ignored := []string{"ign1", "ign2"}

		tracked, _ := healthProbeCandidates(
			localModels, ignored, nil, nil,
			map[string]config.ModelHealthState{}, 0, now,
		)
		if len(tracked) != 3 {
			t.Fatalf("tracked = %v, want len 3", tracked)
		}

		health := map[string]config.ModelHealthState{
			"a": {Fails: 2}, "b": {Fails: 2}, "c": {Fails: 2},
		}
		verdicts := map[string]probeVerdict{
			"a": verdictNotFound, "b": verdictNotFound, "c": verdictNotFound,
		}
		_, toRemove := applyHealthVerdicts(health, verdicts, now, threshold)
		isFault := len(toRemove) > 0 && len(toRemove) >= len(tracked)
		if !isFault {
			t.Errorf("expected fault: tracked=%d toRemove=%d", len(tracked), len(toRemove))
		}
	})
}

func TestHealthStatePruning(t *testing.T) {
	health := map[string]config.ModelHealthState{
		"a":       {Fails: 1, LastProbe: 100},
		"b":       {Fails: 0, Successes: 3, LastProbe: 200},
		"removed": {Fails: 2, LastProbe: 50},
	}
	localModels := []string{"a", "b"}

	got := pruneHealthState(health, localModels)

	if _, ok := got["removed"]; ok {
		t.Error("pruneHealthState should have removed key 'removed'")
	}
	if len(got) != 2 {
		t.Errorf("got len %d, want 2", len(got))
	}
	if got["a"].Fails != 1 || got["b"].Successes != 3 {
		t.Errorf("existing entries should be preserved: %+v", got)
	}

	// 全部被剪枝 → 返回 nil
	got2 := pruneHealthState(map[string]config.ModelHealthState{"x": {Fails: 1}}, []string{"a"})
	if got2 != nil {
		t.Errorf("expected nil when all keys pruned, got %v", got2)
	}
}
