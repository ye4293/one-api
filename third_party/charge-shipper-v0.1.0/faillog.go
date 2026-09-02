package billship

import (
	"sync"
	"time"
)

const (
	reasonSendFailed = "send_failed"
	reasonInvalid    = "invalid"
	reasonDropped    = "dropped"

	detailInterval  = time.Second      // 全局明细节流窗口
	summaryInterval = 10 * time.Second // 聚合行输出周期
)

// failLogger 打印终态失败明细并做洪泛限流：全局 ≤1 条明细/秒，
// 被抑制的按 reason 累计，每 summaryInterval 输出一条聚合行。
type failLogger struct {
	emit    func(level, msg string, kv ...any)
	logBody bool
	now     func() time.Time

	mu          sync.Mutex
	windowStart time.Time
	lastDetail  time.Time
	suppressed  map[string]int64
}

func newFailLogger(emit func(level, msg string, kv ...any), logBody bool) *failLogger {
	return &failLogger{
		emit:       emit,
		logBody:    logBody,
		now:        time.Now,
		suppressed: map[string]int64{},
	}
}

// pendingEmit holds arguments for a single deferred f.emit call.
type pendingEmit struct {
	level, msg string
	kv         []any
}

func (f *failLogger) log(reason string, r Record, attempts int, cause error) {
	// Gather all pending emissions under the lock, then emit after releasing.
	var pending []pendingEmit

	f.mu.Lock()
	now := f.now()

	if f.windowStart.IsZero() {
		f.windowStart = now
	} else if now.Sub(f.windowStart) >= summaryInterval {
		pending = append(pending, f.collectSummaryLocked(now)...)
	}

	if now.Sub(f.lastDetail) >= detailInterval {
		f.lastDetail = now
		kv := []any{
			"reason", reason,
			"source_type", r.SourceType,
			"site_id", r.SiteID,
			"model", r.Model,
			"log_id", r.LogID,
			"created_at", r.CreatedAt,
			"attempts", attempts,
		}
		if cause != nil {
			kv = append(kv, "err", cause.Error())
		}
		if f.logBody {
			kv = append(kv, "body", string(r.Body))
		}
		pending = append(pending, pendingEmit{"error", "billship ship failed", kv})
	} else {
		f.suppressed[reason]++
	}
	f.mu.Unlock()

	// All f.emit calls happen outside the lock so a slow logger never blocks Ship.
	for _, p := range pending {
		f.emit(p.level, p.msg, p.kv...)
	}
}

// flush 立即输出当前窗口内被抑制的失败聚合行（停机收尾用），
// 避免失败停止/停机后最后一段抑制计数只留在 Stats、日志里少报补数据规模。
func (f *failLogger) flush() {
	f.mu.Lock()
	pending := f.collectSummaryLocked(f.now())
	f.mu.Unlock()
	for _, p := range pending {
		f.emit(p.level, p.msg, p.kv...)
	}
}

// collectSummaryLocked gathers summary lines for all suppressed reasons and resets
// the window. Caller must hold f.mu. Returns pending emissions (emitted after unlock).
func (f *failLogger) collectSummaryLocked(now time.Time) []pendingEmit {
	var out []pendingEmit
	for reason, n := range f.suppressed {
		if n > 0 {
			out = append(out, pendingEmit{
				"error", "billship failures summary",
				[]any{"reason", reason, "count", n, "window", summaryInterval.String()},
			})
		}
		delete(f.suppressed, reason)
	}
	f.windowStart = now
	return out
}
