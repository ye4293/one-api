package controller

import (
	"slices"
	"testing"

	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/model"
)

// equalModelSets 比较两个模型名切片（忽略顺序）
func equalModelSets(got, want []string) bool {
	g := slices.Clone(got)
	w := slices.Clone(want)
	slices.Sort(g)
	slices.Sort(w)
	return slices.Equal(g, w)
}

func TestUpstreamCollectPendingChangesFromModels(t *testing.T) {
	tests := []struct {
		name       string
		local      []string
		upstream   []string
		ignored    []string
		mapping    map[string]string
		wantAdd    []string
		wantRemove []string
	}{
		{
			name:       "基础 diff",
			local:      []string{"gpt-4", "gpt-3.5-turbo"},
			upstream:   []string{"gpt-4", "gpt-4o"},
			wantAdd:    []string{"gpt-4o"},
			wantRemove: []string{"gpt-3.5-turbo"},
		},
		{
			name:       "完全一致则无变更",
			local:      []string{"gpt-4", "gpt-4o"},
			upstream:   []string{"gpt-4o", "gpt-4"},
			wantAdd:    []string{},
			wantRemove: []string{},
		},
		{
			name:       "去重与 trim",
			local:      []string{" gpt-4 ", "gpt-4", ""},
			upstream:   []string{"gpt-4", " gpt-4o ", "gpt-4o"},
			wantAdd:    []string{"gpt-4o"},
			wantRemove: []string{},
		},
		{
			name:       "本地为空则全部新增",
			local:      []string{},
			upstream:   []string{"gpt-4", "gpt-4o"},
			wantAdd:    []string{"gpt-4", "gpt-4o"},
			wantRemove: []string{},
		},
		{
			name:       "上游为空则全部待删",
			local:      []string{"gpt-4", "gpt-4o"},
			upstream:   []string{},
			wantAdd:    []string{},
			wantRemove: []string{"gpt-4", "gpt-4o"},
		},

		// ── ModelMapping ──
		{
			name:       "redirect target 算已覆盖，不进 pendingAdd",
			local:      []string{"my-gpt4"},
			upstream:   []string{"gpt-4"},
			mapping:    map[string]string{"my-gpt4": "gpt-4"},
			wantAdd:    []string{},
			wantRemove: []string{},
		},
		{
			name:       "redirect source 不因上游缺失而删除",
			local:      []string{"my-alias", "gpt-4"},
			upstream:   []string{"gpt-4"},
			mapping:    map[string]string{"my-alias": "gpt-4"},
			wantAdd:    []string{},
			wantRemove: []string{},
		},

		// ── IgnoredModels 拦新增（现有行为）──
		{
			name:       "忽略列表拦截 pendingAdd",
			local:      []string{"gpt-4"},
			upstream:   []string{"gpt-4", "gpt-4o", "text-embedding-3-small"},
			ignored:    []string{"text-embedding-3-small"},
			wantAdd:    []string{"gpt-4o"},
			wantRemove: []string{},
		},
		{
			name:       "regex 忽略规则拦截 pendingAdd",
			local:      []string{"gpt-4"},
			upstream:   []string{"gpt-4", "gpt-4o", "text-embedding-3-small", "text-embedding-3-large"},
			ignored:    []string{"regex:^text-embedding-"},
			wantAdd:    []string{"gpt-4o"},
			wantRemove: []string{},
		},

		// ── IgnoredModels 拦删除（改动 1：改动前这些用例必须 FAIL）──
		{
			name:       "忽略列表拦截 pendingRemove：手工维护的模型不被自动删除",
			local:      []string{"gpt-4", "my-custom-model"},
			upstream:   []string{"gpt-4"},
			ignored:    []string{"my-custom-model"},
			wantAdd:    []string{},
			wantRemove: []string{},
		},
		{
			name:       "regex 忽略规则拦截 pendingRemove",
			local:      []string{"gpt-4", "internal-a", "internal-b"},
			upstream:   []string{"gpt-4"},
			ignored:    []string{"regex:^internal-"},
			wantAdd:    []string{},
			wantRemove: []string{},
		},
		{
			name:       "忽略列表只保护命中的模型，其余照常删除",
			local:      []string{"gpt-4", "my-custom-model", "gpt-3.5-turbo"},
			upstream:   []string{"gpt-4"},
			ignored:    []string{"my-custom-model"},
			wantAdd:    []string{},
			wantRemove: []string{"gpt-3.5-turbo"},
		},

		// ── 健壮性 ──
		{
			name:       "非法 regex 规则不 panic，退化为不匹配",
			local:      []string{"gpt-4", "gpt-3.5-turbo"},
			upstream:   []string{"gpt-4"},
			ignored:    []string{"regex:[invalid("},
			wantAdd:    []string{},
			wantRemove: []string{"gpt-3.5-turbo"},
		},
		{
			name:       "忽略列表中的空白项被忽略",
			local:      []string{"gpt-4", "gpt-3.5-turbo"},
			upstream:   []string{"gpt-4"},
			ignored:    []string{"", "  "},
			wantAdd:    []string{},
			wantRemove: []string{"gpt-3.5-turbo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAdd, gotRemove := upstreamCollectPendingChangesFromModels(
				tt.local, tt.upstream, tt.ignored, tt.mapping,
			)
			if !equalModelSets(gotAdd, tt.wantAdd) {
				t.Errorf("pendingAdd = %v, want %v", gotAdd, tt.wantAdd)
			}
			if !equalModelSets(gotRemove, tt.wantRemove) {
				t.Errorf("pendingRemove = %v, want %v", gotRemove, tt.wantRemove)
			}
		})
	}
}

func TestUpstreamNormalizeModelNames(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"去空白", []string{" a ", "b "}, []string{"a", "b"}},
		{"去重", []string{"a", "a", "b"}, []string{"a", "b"}},
		{"丢弃空串", []string{"a", "", "  "}, []string{"a"}},
		{"nil 输入", nil, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := upstreamNormalizeModelNames(tt.in); !equalModelSets(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUpstreamMergeSubtractIntersect(t *testing.T) {
	t.Run("merge 去重且保留基准顺序", func(t *testing.T) {
		got := upstreamMergeModelNames([]string{"a", "b"}, []string{"b", "c"})
		if !slices.Equal(got, []string{"a", "b", "c"}) {
			t.Errorf("got %v, want [a b c]", got)
		}
	})
	t.Run("subtract", func(t *testing.T) {
		got := upstreamSubtractModelNames([]string{"a", "b", "c"}, []string{"b"})
		if !slices.Equal(got, []string{"a", "c"}) {
			t.Errorf("got %v, want [a c]", got)
		}
	})
	t.Run("subtract 移除不存在的项是安全的", func(t *testing.T) {
		got := upstreamSubtractModelNames([]string{"a"}, []string{"zzz"})
		if !slices.Equal(got, []string{"a"}) {
			t.Errorf("got %v, want [a]", got)
		}
	})
	t.Run("intersect", func(t *testing.T) {
		got := upstreamIntersectModelNames([]string{"a", "b", "c"}, []string{"b", "c", "d"})
		if !slices.Equal(got, []string{"b", "c"}) {
			t.Errorf("got %v, want [b c]", got)
		}
	})
}

func TestUpstreamApplySelectedModelChanges(t *testing.T) {
	tests := []struct {
		name   string
		origin []string
		add    []string
		remove []string
		want   []string
	}{
		{"只新增", []string{"a"}, []string{"b"}, nil, []string{"a", "b"}},
		{"只删除", []string{"a", "b"}, nil, []string{"b"}, []string{"a"}},
		{"同时增删", []string{"a", "b"}, []string{"c"}, []string{"a"}, []string{"b", "c"}},
		{"add 与 remove 冲突时 add 优先", []string{"a"}, []string{"b"}, []string{"b"}, []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := upstreamApplySelectedModelChanges(tt.origin, tt.add, tt.remove)
			if !equalModelSets(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldBlockBulkRemove(t *testing.T) {
	origPercent := config.UpstreamRemoveGuardPercent
	origMinLocal := config.UpstreamRemoveGuardMinLocalModels
	t.Cleanup(func() {
		config.UpstreamRemoveGuardPercent = origPercent
		config.UpstreamRemoveGuardMinLocalModels = origMinLocal
	})

	tests := []struct {
		name     string
		percent  int
		minLocal int
		local    int
		remove   int
		want     bool
	}{
		// ── 默认配置：50% / 下限 5 ──
		{"超过阈值则拦下", 50, 5, 10, 6, true},
		{"恰好等于阈值不拦（严格大于）", 50, 5, 10, 5, false},
		{"低于阈值放行", 50, 5, 10, 4, false},
		{"全部删除且达到下限则拦下", 50, 5, 5, 5, true},

		// ── 约束 C 回归：小渠道必须放行，否则「模型全删 → 自动禁用渠道」链路失效 ──
		{"本地 3 个模型全删：低于下限，放行", 50, 5, 3, 3, false},
		{"本地 4 个模型全删：低于下限，放行", 50, 5, 4, 4, false},
		{"本地 1 个模型全删：低于下限，放行", 50, 5, 1, 1, false},
		{"本地恰好等于下限：启用保护", 50, 5, 5, 4, true},

		// ── 关闭与边界 ──
		{"percent=0 表示关闭保护", 0, 5, 10, 10, false},
		{"percent 负数视为关闭", -1, 5, 10, 10, false},
		{"remove=0 放行", 50, 5, 10, 0, false},
		{"local=0 放行", 50, 5, 0, 3, false},
		{"下限为 0 时小渠道也启用保护", 50, 0, 2, 2, true},
		{"percent=100 仅在超过全量时触发（实际永不）", 100, 5, 10, 10, false},
		{"percent=1 几乎总是触发", 1, 5, 100, 2, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config.UpstreamRemoveGuardPercent = tt.percent
			config.UpstreamRemoveGuardMinLocalModels = tt.minLocal
			if got := shouldBlockBulkRemove(tt.local, tt.remove); got != tt.want {
				t.Errorf("shouldBlockBulkRemove(local=%d, remove=%d) with percent=%d minLocal=%d = %v, want %v",
					tt.local, tt.remove, tt.percent, tt.minLocal, got, tt.want)
			}
		})
	}
}

func TestUpstreamNormalizeChannelModelMapping(t *testing.T) {
	strPtr := func(s string) *string { return &s }
	tests := []struct {
		name    string
		mapping *string
		want    map[string]string
	}{
		{"nil", nil, nil},
		{"空串", strPtr(""), nil},
		{"空对象", strPtr("{}"), nil},
		{"非法 JSON", strPtr("{oops"), nil},
		{"正常映射", strPtr(`{"a":"b"}`), map[string]string{"a": "b"}},
		{"trim 键值", strPtr(`{" a ":" b "}`), map[string]string{"a": "b"}},
		{"丢弃空键值", strPtr(`{"":"b","c":""}`), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := &model.Channel{ModelMapping: tt.mapping}
			got := upstreamNormalizeChannelModelMapping(ch)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("key %q: got %q, want %q", k, got[k], v)
				}
			}
		})
	}
}
