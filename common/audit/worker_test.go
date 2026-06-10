package audit

import (
	"sync"
	"testing"
	"time"
)

func TestSubmitNonBlockingDropsWhenChanFull(t *testing.T) {
	resetForTest()
	pkgConfig = &config{Enabled: true, ChannelSize: 1}
	recordChan = make(chan *AuditRecord, 1)
	recordChan <- &AuditRecord{} // 占满
	Submit(&AuditRecord{})        // 不应阻塞
	if Dropped() != 1 {
		t.Errorf("channel 满时应丢弃并计数, dropped=%d", Dropped())
	}
}

func TestIngestFlushOnBatchSize(t *testing.T) {
	resetForTest()
	pkgConfig = &config{Enabled: true, ChannelSize: 10, BatchSize: 2, FlushInterval: time.Hour, MaxBufferMB: 1024}
	var mu sync.Mutex
	var dispatched [][]*AuditRecord
	testDispatch = func(batch []*AuditRecord) {
		mu.Lock()
		dispatched = append(dispatched, batch)
		mu.Unlock()
	}
	recordChan = make(chan *AuditRecord, 10)
	done := make(chan struct{})
	go func() { ingestLoop(); close(done) }()
	Submit(&AuditRecord{XRequestID: "a"})
	Submit(&AuditRecord{XRequestID: "b"}) // 达到 BatchSize=2 → flush
	time.Sleep(50 * time.Millisecond)
	close(recordChan)
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(dispatched) == 0 || len(dispatched[0]) != 2 {
		t.Errorf("应在 batch 满 2 条时 flush, got %v", dispatched)
	}
}

func TestShutdownFlushesRemaining(t *testing.T) {
	resetForTest()
	pkgConfig = &config{Enabled: true, ChannelSize: 10, BatchSize: 1000, FlushInterval: time.Hour, MaxBufferMB: 1024}
	var got int
	testDispatch = func(batch []*AuditRecord) { got += len(batch) }
	recordChan = make(chan *AuditRecord, 10)
	go ingestLoop()
	Submit(&AuditRecord{})
	Submit(&AuditRecord{})
	Shutdown() // 关闭 channel，ingestLoop 收尾 flush 残余
	time.Sleep(50 * time.Millisecond)
	if got != 2 {
		t.Errorf("关停应 flush 残余 2 条, got %d", got)
	}
}
