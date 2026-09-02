package billship

import (
	"sync"
	"testing"
	"time"
)

func drainBatches(out <-chan []Record) [][]Record {
	var got [][]Record
	for b := range out {
		got = append(got, b)
	}
	return got
}

func TestBatcherFlushesOnSize(t *testing.T) {
	in := make(chan Record, 100)
	out := make(chan []Record, 100)
	stop := make(chan struct{})
	b := &batcher{in: in, out: out, batchSize: 3, wait: time.Hour, stop: stop}
	go b.run()

	for i := 0; i < 7; i++ {
		in <- Record{SiteID: "s", Model: "m", SourceType: "new-api", Body: []byte("x")}
	}
	// 关 stop → drain 剩余 + close(out)
	close(stop)
	got := drainBatches(out)

	total := 0
	for _, batch := range got {
		if len(batch) > 3 {
			t.Errorf("batch size %d exceeds 3", len(batch))
		}
		total += len(batch)
	}
	if total != 7 {
		t.Errorf("total records = %d, want 7", total)
	}
}

func TestBatcherFlushesOnWait(t *testing.T) {
	in := make(chan Record, 10)
	out := make(chan []Record, 10)
	stop := make(chan struct{})
	b := &batcher{in: in, out: out, batchSize: 100, wait: 30 * time.Millisecond, stop: stop}
	go b.run()

	in <- Record{SiteID: "s", Model: "m", SourceType: "new-api", Body: []byte("x")}
	// 不关 stop，等 wait 触发。
	select {
	case batch := <-out:
		if len(batch) != 1 {
			t.Errorf("batch size = %d, want 1", len(batch))
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timer flush did not fire")
	}
	close(stop)
	drainBatches(out)
}

func TestBatcherOnDiscardOnAbort(t *testing.T) {
	in := make(chan Record, 10)
	out := make(chan []Record) // 无缓冲且无接收者 → flush 阻塞，模拟 worker 卡死
	stop := make(chan struct{})
	abort := make(chan struct{})
	var mu sync.Mutex
	var discarded []Record
	b := &batcher{
		in: in, out: out, batchSize: 1, wait: time.Hour, stop: stop, abort: abort,
		onDiscard: func(rs []Record) { mu.Lock(); discarded = append(discarded, rs...); mu.Unlock() },
	}
	done := make(chan struct{})
	go func() { b.run(); close(done) }()

	in <- Record{SiteID: "s", Model: "m", SourceType: "new-api", Body: []byte("x")}
	time.Sleep(20 * time.Millisecond) // 等 batcher 卡在 flush 上
	close(abort)                      // 硬停机

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("batcher did not return after abort")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(discarded) != 1 {
		t.Errorf("discarded = %d, want 1 (in-hand batch not silently dropped)", len(discarded))
	}
}

func TestBatcherSplitsOn256KB(t *testing.T) {
	in := make(chan Record, 10)
	out := make(chan []Record, 10)
	stop := make(chan struct{})
	b := &batcher{in: in, out: out, batchSize: 100, wait: time.Hour, stop: stop}
	go b.run()

	big := make([]byte, 200*1024) // 每条 ~200KB，两条聚合超 256KB → 必须切
	in <- Record{SiteID: "s", Model: "m", SourceType: "new-api", Body: big}
	in <- Record{SiteID: "s", Model: "m", SourceType: "new-api", Body: big}
	close(stop)
	got := drainBatches(out)

	if len(got) != 2 {
		t.Fatalf("got %d batches, want 2 (256KB split)", len(got))
	}
	for _, batch := range got {
		if len(batch) != 1 {
			t.Errorf("batch size = %d, want 1 each", len(batch))
		}
	}
}
