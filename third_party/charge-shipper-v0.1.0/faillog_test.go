package billship

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type logLine struct {
	level, msg string
	kv         map[string]any
}

// capture 收集 emit 调用，线程安全。
type capture struct {
	mu    sync.Mutex
	lines []logLine
}

func (c *capture) emit(level, msg string, kv ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	c.lines = append(c.lines, logLine{level, msg, m})
}

func (c *capture) snapshot() []logLine {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]logLine(nil), c.lines...)
}

func TestFailLogDetailFields(t *testing.T) {
	cap := &capture{}
	fl := newFailLogger(cap.emit, false)
	r := Record{SiteID: "s", Model: "gpt-4", SourceType: "new-api", LogID: 42, CreatedAt: 1700}
	fl.log(reasonSendFailed, r, 3, errors.New("Throttled"))

	lines := cap.snapshot()
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	l := lines[0]
	if l.level != "error" || l.msg != "billship ship failed" {
		t.Errorf("level/msg = %q/%q", l.level, l.msg)
	}
	for k, want := range map[string]any{
		"reason": reasonSendFailed, "source_type": "new-api", "site_id": "s",
		"model": "gpt-4", "log_id": int64(42), "created_at": int64(1700), "attempts": 3,
		"err": "Throttled",
	} {
		if l.kv[k] != want {
			t.Errorf("kv[%q] = %v, want %v", k, l.kv[k], want)
		}
	}
	if _, ok := l.kv["body"]; ok {
		t.Error("body should be absent when LogFailedBody=false")
	}
}

func TestFailLogBodyWhenEnabled(t *testing.T) {
	cap := &capture{}
	fl := newFailLogger(cap.emit, true)
	fl.log(reasonSendFailed, Record{SiteID: "s", Model: "m", SourceType: "new-api", Body: []byte("RAW")}, 1, nil)
	if cap.snapshot()[0].kv["body"] != "RAW" {
		t.Error("body should be present when LogFailedBody=true")
	}
}

func TestFailLogRateLimitAndAggregate(t *testing.T) {
	cap := &capture{}
	fl := newFailLogger(cap.emit, false)
	now := time.Unix(1000, 0)
	fl.now = func() time.Time { return now }

	r := Record{SiteID: "s", Model: "m", SourceType: "new-api"}
	// 同一秒内 100 条：只 1 条明细，其余抑制。
	for i := 0; i < 100; i++ {
		fl.log(reasonSendFailed, r, 1, nil)
	}
	details := 0
	for _, l := range cap.snapshot() {
		if l.msg == "billship ship failed" {
			details++
		}
	}
	if details != 1 {
		t.Errorf("detail lines = %d, want 1 (rate-limited)", details)
	}

	// 推进 10s 再打一条 → 触发聚合行 flush。
	now = now.Add(10 * time.Second)
	fl.log(reasonSendFailed, r, 1, nil)
	var summary *logLine
	for _, l := range cap.snapshot() {
		l := l
		if l.msg == "billship failures summary" {
			summary = &l
		}
	}
	if summary == nil {
		t.Fatal("expected an aggregate summary line after 10s")
	}
	if summary.kv["count"].(int64) < 99 {
		t.Errorf("summary count = %v, want >=99", summary.kv["count"])
	}
}
