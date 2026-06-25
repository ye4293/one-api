package util

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestSetEndReason_FirstWins(t *testing.T) {
	s := NewStreamStatus()
	s.SetEndReason(StreamEndReasonDone, nil)
	s.SetEndReason(StreamEndReasonTimeout, nil)
	s.mu.Lock()
	got := s.EndReason
	s.mu.Unlock()
	if got != StreamEndReasonDone {
		t.Fatalf("expected done, got %s", got)
	}
}

func TestSetEndReason_Idempotent(t *testing.T) {
	s := NewStreamStatus()
	s.SetEndReason(StreamEndReasonDone, nil)
	s.SetEndReason(StreamEndReasonDone, nil) // must not panic
}

func TestRecordError_Limit(t *testing.T) {
	s := NewStreamStatus()
	for i := 0; i < 25; i++ {
		s.RecordError("err")
	}
	if s.TotalErrorCount() != 25 {
		t.Fatalf("expected ErrorCount=25, got %d", s.TotalErrorCount())
	}
	s.mu.Lock()
	n := len(s.Errors)
	s.mu.Unlock()
	if n != maxStreamErrorEntries {
		t.Fatalf("expected len(Errors)=%d, got %d", maxStreamErrorEntries, n)
	}
}

func TestHasErrors(t *testing.T) {
	s := NewStreamStatus()
	if s.HasErrors() {
		t.Fatal("expected no errors initially")
	}
	s.RecordError("oops")
	if !s.HasErrors() {
		t.Fatal("expected HasErrors after RecordError")
	}
}

func TestIsNormalEnd(t *testing.T) {
	cases := []struct {
		reason StreamEndReason
		want   bool
	}{
		{StreamEndReasonDone, true},
		{StreamEndReasonEOF, true},
		{StreamEndReasonHandlerStop, true},
		{StreamEndReasonTimeout, false},
		{StreamEndReasonClientGone, false},
		{StreamEndReasonScannerErr, false},
		{StreamEndReasonPanic, false},
		{StreamEndReasonPingFail, false},
	}
	for _, tc := range cases {
		s := NewStreamStatus()
		s.SetEndReason(tc.reason, nil)
		if got := s.IsNormalEnd(); got != tc.want {
			t.Errorf("IsNormalEnd(%s) = %v, want %v", tc.reason, got, tc.want)
		}
	}
}

func TestNilSafe(t *testing.T) {
	var s *StreamStatus
	s.SetEndReason(StreamEndReasonDone, nil) // must not panic
	s.RecordError("msg")                     // must not panic
	_ = s.HasErrors()
	_ = s.TotalErrorCount()
	_ = s.IsNormalEnd()
	_ = s.Summary()
}

func TestConcurrent(t *testing.T) {
	s := NewStreamStatus()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			s.SetEndReason(StreamEndReasonDone, nil)
		}()
		go func() {
			defer wg.Done()
			s.RecordError("concurrent error")
		}()
	}
	wg.Wait()
	s.mu.Lock()
	got := s.EndReason
	s.mu.Unlock()
	if got != StreamEndReasonDone {
		t.Fatalf("expected done after concurrent sets, got %s", got)
	}
}

func TestAppendStreamStatusOther_NilReturnsOriginal(t *testing.T) {
	result := AppendStreamStatusOther("existing", nil)
	if result != "existing" {
		t.Fatalf("expected 'existing', got %q", result)
	}
}

func TestAppendStreamStatusOther_NormalEnd(t *testing.T) {
	s := NewStreamStatus()
	s.SetEndReason(StreamEndReasonDone, nil)
	result := AppendStreamStatusOther("", s)
	want := `streamStatus:{"status":"ok","end_reason":"done"}`
	if result != want {
		t.Fatalf("expected %q, got %q", want, result)
	}
}

func TestAppendStreamStatusOther_ClientGone(t *testing.T) {
	s := NewStreamStatus()
	s.SetEndReason(StreamEndReasonClientGone, fmt.Errorf("context canceled"))
	result := AppendStreamStatusOther("billingDetails:{}", s)
	if !strings.Contains(result, "client_gone") {
		t.Fatalf("expected client_gone in %q", result)
	}
	if !strings.Contains(result, "billingDetails:{}") {
		t.Fatalf("expected original prefix preserved in %q", result)
	}
}
