package billship

import (
	"sync"
	"testing"
)

func TestStatsSnapshot(t *testing.T) {
	var s stats
	s.enqueued.Add(3)
	s.dropped.Add(1)
	s.sent.Add(2)
	s.sendFailures.Add(4)
	s.retries.Add(5)
	s.invalid.Add(6)
	s.inFlight.Store(7)
	s.batchesSent.Add(8)

	snap := s.snapshot()
	want := Snapshot{Enqueued: 3, Dropped: 1, Sent: 2, SendFailures: 4, Retries: 5, Invalid: 6, InFlight: 7, BatchesSent: 8}
	if snap != want {
		t.Errorf("snapshot() = %+v, want %+v", snap, want)
	}
}

func TestStatsConcurrent(t *testing.T) {
	var s stats
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.enqueued.Add(1) }()
	}
	wg.Wait()
	if got := s.snapshot().Enqueued; got != 100 {
		t.Errorf("Enqueued = %d, want 100", got)
	}
}
