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
	Submit(&AuditRecord{})       // 不应阻塞
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

func TestShutdownWaitsForFlush(t *testing.T) {
	resetForTest()
	pkgConfig = &config{Enabled: true, ChannelSize: 10, BatchSize: 1000, FlushInterval: time.Hour, MaxBufferMB: 1024}
	var mu sync.Mutex
	var got int
	testDispatch = func(batch []*AuditRecord) {
		mu.Lock()
		got += len(batch)
		mu.Unlock()
	}
	recordChan = make(chan *AuditRecord, 10)
	ingestDone = make(chan struct{})
	go func() { ingestLoop(); close(ingestDone) }()
	Submit(&AuditRecord{})
	Submit(&AuditRecord{})
	Shutdown() // 应阻塞直到 ingestLoop flush 完成；返回即保证残余已 dispatch
	mu.Lock()
	defer mu.Unlock()
	if got != 2 {
		t.Errorf("Shutdown 应等待 flush 完成后再返回, got %d", got)
	}
}

func TestShutdownFlushesRemaining(t *testing.T) {
	resetForTest()
	pkgConfig = &config{Enabled: true, ChannelSize: 10, BatchSize: 1000, FlushInterval: time.Hour, MaxBufferMB: 1024}
	var mu sync.Mutex
	var got int
	testDispatch = func(batch []*AuditRecord) {
		mu.Lock()
		got += len(batch)
		mu.Unlock()
	}
	recordChan = make(chan *AuditRecord, 10)
	done := make(chan struct{})
	go func() { ingestLoop(); close(done) }()
	Submit(&AuditRecord{})
	Submit(&AuditRecord{})
	Shutdown() // 关闭 channel，ingestLoop 收尾 flush 残余并退出
	<-done
	mu.Lock()
	defer mu.Unlock()
	if got != 2 {
		t.Errorf("关停应 flush 残余 2 条, got %d", got)
	}
}

func TestSpillAndReplay(t *testing.T) {
	resetForTest()
	dir := t.TempDir()
	s := &spillStore{dir: dir, maxBytes: 100 * 1024 * 1024}

	original := []*AuditRecord{
		{
			EventTime:  time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC),
			XRequestID: "req-spill-1",
			UserID:     1,
			ChannelID:  3,
			OriginModel: "gpt-4",
			ActualModel: "gpt-4-0613",
			IsStream:   true,
			StatusCode: 200,
			DurationMS: 500,
			TruncatedFields: []string{"upstream_response"},
		},
		{
			EventTime:  time.Date(2026, 6, 22, 10, 0, 1, 0, time.UTC),
			XRequestID: "req-spill-2",
			UserID:     2,
			StatusCode: 400,
		},
	}

	spillBatchTo(s, original)

	files, err := s.scan()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("应有 1 个 spill 文件, got %d", len(files))
	}

	records, err := readSpillFile(files[0])
	if err != nil {
		t.Fatalf("readSpillFile: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("应读回 2 条记录, got %d", len(records))
	}
	if records[0].XRequestID != "req-spill-1" {
		t.Errorf("第一条 XRequestID 不匹配: %s", records[0].XRequestID)
	}
	if records[1].XRequestID != "req-spill-2" {
		t.Errorf("第二条 XRequestID 不匹配: %s", records[1].XRequestID)
	}
	if records[0].UserID != 1 || records[0].ChannelID != 3 {
		t.Errorf("字段值不匹配: UserID=%d, ChannelID=%d", records[0].UserID, records[0].ChannelID)
	}
	if !records[0].IsStream {
		t.Error("IsStream 应为 true")
	}
	if len(records[0].TruncatedFields) != 1 || records[0].TruncatedFields[0] != "upstream_response" {
		t.Errorf("TruncatedFields 不匹配: %v", records[0].TruncatedFields)
	}
}

func spillBatchTo(s *spillStore, batch []*AuditRecord) {
	oldSpill := spill
	spill = s
	defer func() { spill = oldSpill }()
	spillBatch(batch)
}
