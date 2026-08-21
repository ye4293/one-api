package billship

import (
	"context"
	"sync"
	"testing"
	"time"
)

// newFakeShipper 用注入的 sendBatch 建 Shipper（不碰真 SQS）。
func newFakeShipper(t *testing.T, cfg Config, send func([]Record)) *Shipper {
	t.Helper()
	cfg.applyDefaults()
	s := newShipper(cfg)
	s.sendBatch = send
	s.start()
	return s
}

func TestShipDisabledIsNoop(t *testing.T) {
	var called bool
	s := newFakeShipper(t, Config{QueueURL: "q", Region: "r", Enabled: false},
		func([]Record) { called = true })
	s.Ship(Record{SiteID: "s", Model: "m", SourceType: "new-api", Body: []byte("{}")})
	_ = s.Shutdown(context.Background())
	if called {
		t.Error("Ship should be no-op when Enabled=false")
	}
	if got := s.snapshot().Enqueued; got != 0 {
		t.Errorf("Enqueued = %d, want 0", got)
	}
}

func TestShipInvalidCounts(t *testing.T) {
	s := newFakeShipper(t, Config{QueueURL: "q", Region: "r", Enabled: true}, func([]Record) {})
	s.Ship(Record{SiteID: "", Model: "m", SourceType: "new-api", Body: []byte("{}")}) // empty attr
	_ = s.Shutdown(context.Background())
	if got := s.snapshot().Invalid; got != 1 {
		t.Errorf("Invalid = %d, want 1", got)
	}
	if got := s.snapshot().Enqueued; got != 0 {
		t.Errorf("Enqueued = %d, want 0", got)
	}
}

func TestShipEnqueueAndFlushOnShutdown(t *testing.T) {
	var mu sync.Mutex
	var total int
	send := func(b []Record) { mu.Lock(); total += len(b); mu.Unlock() }
	s := newFakeShipper(t, Config{QueueURL: "q", Region: "r", Enabled: true, BatchSize: 5, BatchWait: time.Hour}, send)

	for i := 0; i < 23; i++ {
		s.Ship(Record{SiteID: "s", Model: "m", SourceType: "new-api", Body: []byte("{}")})
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown err = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if total != 23 {
		t.Errorf("delivered = %d, want 23 (flushed on shutdown)", total)
	}
	if got := s.snapshot().Enqueued; got != 23 {
		t.Errorf("Enqueued = %d, want 23", got)
	}
}

func TestShipDropsWhenBufferFull(t *testing.T) {
	// 用一个永不返回的 send 卡住 worker，让 buffer 打满。
	block := make(chan struct{})
	send := func([]Record) { <-block }
	s := newFakeShipper(t, Config{
		QueueURL: "q", Region: "r", Enabled: true,
		BufferSize: 2, BatchSize: 1, SendConcurrency: 1, BatchWait: time.Hour,
	}, send)

	dropped := 0
	for i := 0; i < 200; i++ {
		before := s.snapshot().Dropped
		s.Ship(Record{SiteID: "s", Model: "m", SourceType: "new-api", Body: []byte("{}")})
		if s.snapshot().Dropped > before {
			dropped++
		}
	}
	if dropped == 0 {
		t.Error("expected some drops when buffer is full")
	}
	close(block)
	// 停机（可能超时，容忍）。
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = s.Shutdown(ctx)
}

func TestShutdownTimeoutReturnsOnTime(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	send := func([]Record) { <-block } // 永不完成
	s := newFakeShipper(t, Config{
		QueueURL: "q", Region: "r", Enabled: true,
		BatchSize: 1, SendConcurrency: 1, BatchWait: time.Millisecond,
	}, send)
	s.Ship(Record{SiteID: "s", Model: "m", SourceType: "new-api", Body: []byte("{}")})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := s.Shutdown(ctx)
	if err == nil {
		t.Error("Shutdown should return ctx error on timeout")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("Shutdown took %v, should return promptly on ctx timeout", elapsed)
	}
}
