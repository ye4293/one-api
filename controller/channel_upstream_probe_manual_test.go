package controller

import (
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/model"
)

// TestSplitManualProbeTargets 钉死手动探针的成本/注入安全边界（约束 G）。
//
// 无论前端传什么模型名，只有落在 pendingAdd / pendingRemove 里的才会被探测；
// 其余一律进 rejected、不发任何上游请求。这是防止「以管理员身份向上游发起
// 任意模型名付费请求」的唯一防线。
func TestSplitManualProbeTargets(t *testing.T) {
	pendingAdd := []string{"gpt-4o", "gpt-4o-mini"}
	pendingRemove := []string{"dead-model-a", "dead-model-b"}

	cases := []struct {
		name       string
		models     []string
		wantAdd    []string
		wantRemove []string
		wantReject []string
	}{
		{
			name:       "全部命中 pending",
			models:     []string{"gpt-4o", "dead-model-a"},
			wantAdd:    []string{"gpt-4o"},
			wantRemove: []string{"dead-model-a"},
			wantReject: []string{},
		},
		{
			name:       "注入 pending 之外的模型名必须全被拒绝",
			models:     []string{"gpt-4o", "arbitrary-injected-model", "another-fake"},
			wantAdd:    []string{"gpt-4o"},
			wantRemove: []string{},
			wantReject: []string{"arbitrary-injected-model", "another-fake"},
		},
		{
			name:       "全部为注入 → 全部拒绝、不探任何模型",
			models:     []string{"x", "y", "z"},
			wantAdd:    []string{},
			wantRemove: []string{},
			wantReject: []string{"x", "y", "z"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			add, remove, reject := splitManualProbeTargets(tc.models, pendingAdd, pendingRemove)
			assertSameSet(t, "addBatch", add, tc.wantAdd)
			assertSameSet(t, "removeBatch", remove, tc.wantRemove)
			assertSameSet(t, "rejected", reject, tc.wantReject)
		})
	}
}

func assertSameSet(t *testing.T, label string, got, want []string) {
	t.Helper()
	g := append([]string{}, got...)
	w := append([]string{}, want...)
	sort.Strings(g)
	sort.Strings(w)
	if len(g) == 0 && len(w) == 0 {
		return
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}

// TestManualBudgetDoesNotTouchRoundBudget 钉死约束 A：手动探针的预算与定时任务的
// 全局每轮余额完全隔离。manualBudget.take() 无论调用多少次，都不得改变
// upstreamProbeRoundBudget。
func TestManualBudgetDoesNotTouchRoundBudget(t *testing.T) {
	upstreamProbeRoundBudget.Store(200)
	b := &manualBudget{remaining: 100}
	for i := 0; i < 100; i++ {
		if !b.take() {
			t.Fatalf("第 %d 次 take() 意外返回 false", i)
		}
	}
	if b.take() {
		t.Error("预算耗尽后 take() 应返回 false")
	}
	if got := upstreamProbeRoundBudget.Load(); got != 200 {
		t.Errorf("upstreamProbeRoundBudget 被手动预算改动了: got=%d, want 200", got)
	}
}

// TestProbeBudgetTouchesRoundBudget 回归保护：定时任务的 probeBudget.take() 必须
// 仍然扣减全局每轮余额（这是它与 manualBudget 的本质区别，不能在重构里被改掉）。
func TestProbeBudgetTouchesRoundBudget(t *testing.T) {
	upstreamProbeRoundBudget.Store(5)
	b := &probeBudget{channelRemaining: 100, channelDeadline: farFuture()}
	if !b.take() {
		t.Fatal("take() 意外返回 false")
	}
	if got := upstreamProbeRoundBudget.Load(); got != 4 {
		t.Errorf("probeBudget.take() 未扣减全局余额: got=%d, want 4", got)
	}
}

// TestProbeStatSinkNilSafe 钉死约束 B 的基础：手动路径传 nil stats，record/reset
// 必须 nil 安全，绝不 panic。
func TestProbeStatSinkNilSafe(t *testing.T) {
	var s *probeStatSink // nil
	s.record(verdictAlive)
	s.record(verdictNotFound)
	s.reset()
	// 不 panic 即通过
}

// TestProbeStatSinkRecord 验证每个 verdict 落到正确的计数器。
func TestProbeStatSinkRecord(t *testing.T) {
	s := &probeStatSink{}
	s.record(verdictAlive)
	s.record(verdictAlive)
	s.record(verdictNotFound)
	s.record(verdictUnavailable)
	s.record(verdictRateLimited)
	s.record(verdictSkipped)
	s.record(verdictInconclusive)
	if s.alive.Load() != 2 {
		t.Errorf("alive = %d, want 2", s.alive.Load())
	}
	if s.notFound.Load() != 1 || s.unavailable.Load() != 1 || s.rateLimited.Load() != 1 ||
		s.skipped.Load() != 1 || s.inconclusive.Load() != 1 {
		t.Errorf("计数错误: %+v", s)
	}
	s.reset()
	if s.alive.Load() != 0 || s.inconclusive.Load() != 0 {
		t.Error("reset 后计数未清零")
	}
}

// TestBuildProbeLogOther 钉死日志 Other 字段格式：必须带 probe_source 且区分 task/manual。
// 下游可能按这些字段 grep，格式不能随意改。
func TestBuildProbeLogOther(t *testing.T) {
	res := probeResult{Verdict: verdictAlive, StatusCode: 200}
	manual := buildProbeLogOther(res, probeScenePendingAdd, probeSourceManual)
	if !strings.Contains(manual, "probe_source:manual") {
		t.Errorf("缺少 probe_source:manual: %s", manual)
	}
	if !strings.Contains(manual, "probe_verdict:alive") ||
		!strings.Contains(manual, "probe_scene:pending_add") ||
		!strings.Contains(manual, "probe_status:200") {
		t.Errorf("Other 字段格式变化: %s", manual)
	}
	task := buildProbeLogOther(res, probeScenePendingRemove, probeSourceTask)
	if !strings.Contains(task, "probe_source:task") {
		t.Errorf("缺少 probe_source:task: %s", task)
	}
}

// TestProbeChannelUnsupportedReason 钉死约束 E 的判定：渠道类型级短路。
func TestProbeChannelUnsupportedReason(t *testing.T) {
	// 支持的类型（OpenAI）→ 空
	if r := probeChannelUnsupportedReason(&model.Channel{Type: common.ChannelTypeOpenAI}); r != "" {
		t.Errorf("OpenAI 渠道应可探测，却返回: %q", r)
	}
	// 不支持的类型（Flux 是纯视频/图像）→ 非空
	if r := probeChannelUnsupportedReason(&model.Channel{Type: common.ChannelTypeFlux}); r == "" {
		t.Error("Flux 渠道应返回不支持原因，却返回空")
	}
}

// TestManualProbeActiveGracefulWithoutRedis 验证 Redis 未启用时的降级：
// 不阻塞、不 panic（单实例部署下手动探针仍可用）。测试环境 Redis 通常未启用。
func TestManualProbeActiveGracefulWithoutRedis(t *testing.T) {
	if common.RedisEnabled {
		t.Skip("Redis 已启用，跳过降级路径测试")
	}
	uid, ok := manualProbeActive(99999)
	if ok || uid != 0 {
		t.Errorf("Redis 未启用时应返回 (0,false)，得到 (%d,%v)", uid, ok)
	}
	manualProbeRefresh(99999, 1) // 不得 panic
}

func farFuture() time.Time {
	return time.Now().Add(time.Hour)
}

// TestManualBudgetConcurrentTake 钉死 manualBudget 在并发下的正确性（-race）。
// 渠道内模型并行后多个 goroutine 会同时 take()，remaining-- 必须加锁，
// 且成功次数必须恰好等于初始额度（不多不少）。
func TestManualBudgetConcurrentTake(t *testing.T) {
	const limit = 100
	b := &manualBudget{remaining: limit}
	var success atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 300; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if b.take() {
				success.Add(1)
			}
		}()
	}
	wg.Wait()
	if success.Load() != limit {
		t.Errorf("并发 take() 成功次数 = %d，必须恰好 = %d（多=超发，少=丢失）", success.Load(), limit)
	}
}

// TestProbeBudgetConcurrentTake 钉死 probeBudget 在并发下不超发全局每轮余额。
func TestProbeBudgetConcurrentTake(t *testing.T) {
	const roundLimit = 50
	upstreamProbeRoundBudget.Store(roundLimit)
	b := &probeBudget{channelRemaining: 1000, channelDeadline: farFuture()}
	var success atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 300; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if b.take() {
				success.Add(1)
			}
		}()
	}
	wg.Wait()
	if success.Load() != roundLimit {
		t.Errorf("并发 take() 成功 = %d，必须恰好 = 全局余额 %d", success.Load(), roundLimit)
	}
	if upstreamProbeRoundBudget.Load() < 0 {
		t.Errorf("全局余额被扣成负数: %d", upstreamProbeRoundBudget.Load())
	}
}

