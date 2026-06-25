package util

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

type StreamEndReason string

const (
	StreamEndReasonNone        StreamEndReason = ""
	StreamEndReasonDone        StreamEndReason = "done"
	StreamEndReasonTimeout     StreamEndReason = "timeout"
	StreamEndReasonClientGone  StreamEndReason = "client_gone"
	StreamEndReasonScannerErr  StreamEndReason = "scanner_error"
	StreamEndReasonHandlerStop StreamEndReason = "handler_stop"
	StreamEndReasonEOF         StreamEndReason = "eof"
	StreamEndReasonPanic       StreamEndReason = "panic"
	StreamEndReasonPingFail    StreamEndReason = "ping_fail"
)

const maxStreamErrorEntries = 20

type StreamErrorEntry struct {
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

type StreamStatus struct {
	EndReason StreamEndReason
	EndError  error
	endOnce   sync.Once

	mu         sync.Mutex
	Errors     []StreamErrorEntry
	ErrorCount int
}

func NewStreamStatus() *StreamStatus {
	return &StreamStatus{}
}

func (s *StreamStatus) SetEndReason(reason StreamEndReason, err error) {
	if s == nil {
		return
	}
	s.endOnce.Do(func() {
		s.EndReason = reason
		s.EndError = err
	})
}

func (s *StreamStatus) RecordError(msg string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ErrorCount++
	if len(s.Errors) < maxStreamErrorEntries {
		s.Errors = append(s.Errors, StreamErrorEntry{
			Message:   msg,
			Timestamp: time.Now(),
		})
	}
}

func (s *StreamStatus) HasErrors() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ErrorCount > 0
}

func (s *StreamStatus) TotalErrorCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ErrorCount
}

// IsNormalEnd 判断流是否正常结束。done/eof/handler_stop 视为正常。
func (s *StreamStatus) IsNormalEnd() bool {
	if s == nil {
		return true
	}
	return s.EndReason == StreamEndReasonDone ||
		s.EndReason == StreamEndReasonEOF ||
		s.EndReason == StreamEndReasonHandlerStop
}

func (s *StreamStatus) Summary() string {
	if s == nil {
		return "StreamStatus<nil>"
	}
	b := &strings.Builder{}
	fmt.Fprintf(b, "reason=%s", s.EndReason)
	if s.EndError != nil {
		fmt.Fprintf(b, " end_error=%q", s.EndError.Error())
	}
	s.mu.Lock()
	if s.ErrorCount > 0 {
		fmt.Fprintf(b, " soft_errors=%d", s.ErrorCount)
	}
	s.mu.Unlock()
	return b.String()
}

// AppendStreamStatusOther 将 StreamStatus 序列化为 streamStatus:{...} 段，
// 以 ; 分隔追加到 otherInfo。StreamStatus 为 nil 时直接返回原 otherInfo。
func AppendStreamStatusOther(otherInfo string, ss *StreamStatus) string {
	if ss == nil {
		return otherInfo
	}

	status := "ok"
	if !ss.IsNormalEnd() || ss.HasErrors() {
		status = "error"
	}

	type streamStatusJSON struct {
		Status     string   `json:"status"`
		EndReason  string   `json:"end_reason"`
		EndError   string   `json:"end_error,omitempty"`
		ErrorCount int      `json:"error_count,omitempty"`
		Errors     []string `json:"errors,omitempty"`
	}

	data := streamStatusJSON{
		Status:    status,
		EndReason: string(ss.EndReason),
	}
	if ss.EndError != nil {
		data.EndError = ss.EndError.Error()
	}
	if ss.ErrorCount > 0 {
		data.ErrorCount = ss.ErrorCount
		ss.mu.Lock()
		msgs := make([]string, 0, len(ss.Errors))
		for _, e := range ss.Errors {
			msgs = append(msgs, e.Message)
		}
		ss.mu.Unlock()
		data.Errors = msgs
	}

	b, err := json.Marshal(data)
	if err != nil {
		return otherInfo
	}
	seg := "streamStatus:" + string(b)
	if otherInfo == "" {
		return seg
	}
	return otherInfo + ";" + seg
}
