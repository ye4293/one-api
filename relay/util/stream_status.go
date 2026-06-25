package util

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
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

// StreamErrorEntry 记录一条软错误消息。
type StreamErrorEntry struct {
	Message string `json:"message"`
}

// streamStatusPayload 是写入 Log.Other 的 JSON 结构，定义在包级别避免每次调用重建类型。
type streamStatusPayload struct {
	Status     string   `json:"status"`
	EndReason  string   `json:"end_reason"`
	EndError   string   `json:"end_error,omitempty"`
	ErrorCount int      `json:"error_count,omitempty"`
	Errors     []string `json:"errors,omitempty"`
}

type StreamStatus struct {
	EndReason StreamEndReason
	EndError  error
	endSet    bool

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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.endSet {
		return
	}
	s.endSet = true
	s.EndReason = reason
	s.EndError = err
}

func (s *StreamStatus) RecordError(msg string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ErrorCount++
	if len(s.Errors) < maxStreamErrorEntries {
		s.Errors = append(s.Errors, StreamErrorEntry{Message: msg})
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
	s.mu.Lock()
	defer s.mu.Unlock()
	return isNormalEndReason(s.EndReason)
}

// isNormalEndReason 判断给定的结束原因是否属于正常结束（不加锁，供内部复用）。
func isNormalEndReason(r StreamEndReason) bool {
	return r == StreamEndReasonDone ||
		r == StreamEndReasonEOF ||
		r == StreamEndReasonHandlerStop
}

func (s *StreamStatus) Summary() string {
	if s == nil {
		return "StreamStatus<nil>"
	}
	s.mu.Lock()
	reason := s.EndReason
	endErr := s.EndError
	errCount := s.ErrorCount
	s.mu.Unlock()
	b := &strings.Builder{}
	fmt.Fprintf(b, "reason=%s", reason)
	if endErr != nil {
		fmt.Fprintf(b, " end_error=%q", endErr.Error())
	}
	if errCount > 0 {
		fmt.Fprintf(b, " soft_errors=%d", errCount)
	}
	return b.String()
}

// AppendStreamStatusOther 将 StreamStatus 序列化为 streamStatus:{...} 段，
// 以 ; 分隔追加到 otherInfo。StreamStatus 为 nil 时直接返回原 otherInfo。
func AppendStreamStatusOther(otherInfo string, ss *StreamStatus) string {
	if ss == nil {
		return otherInfo
	}

	ss.mu.Lock()
	endReason := ss.EndReason
	endErr := ss.EndError
	errCount := ss.ErrorCount
	var msgs []string
	if errCount > 0 {
		msgs = make([]string, 0, len(ss.Errors))
		for _, e := range ss.Errors {
			msgs = append(msgs, e.Message)
		}
	}
	ss.mu.Unlock()

	if endReason == StreamEndReasonNone {
		return otherInfo
	}

	isNormal := isNormalEndReason(endReason)
	status := "ok"
	if !isNormal || errCount > 0 {
		status = "error"
	}

	data := streamStatusPayload{
		Status:    status,
		EndReason: string(endReason),
	}
	if endErr != nil {
		data.EndError = endErr.Error()
	}
	if errCount > 0 {
		data.ErrorCount = errCount
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
