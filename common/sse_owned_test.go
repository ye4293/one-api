package common

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestScanSSEOwnedStopsCallbacksAfterCancellation(t *testing.T) {
	reader, writer := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := io.WriteString(writer, "data: first\n\ndata: second\n\n")
		done <- err
		writer.Close()
	}()
	calls := 0
	err := ScanSSEOwned(ctx, reader, time.Second, 0, func(data string) bool { calls++; cancel(); return true }, nil)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("callbacks after cancellation: calls=%d err=%v", calls, err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reader worker did not stop")
	}
}

func TestScanSSEOwnedWithEventResetsEventName(t *testing.T) {
	body := io.NopCloser(strings.NewReader("event: response.created\ndata: first\n\ndata: second\n\nevent: error\ndata: third\n\n"))
	var events [][2]string
	err := ScanSSEOwnedWithEvent(context.Background(), body, time.Second, 0, func(eventType, data string) bool {
		events = append(events, [2]string{eventType, data})
		return true
	}, nil)
	want := [][2]string{{"response.created", "first"}, {"", "second"}, {"error", "third"}}
	if !errors.Is(err, io.EOF) || len(events) != len(want) {
		t.Fatalf("unexpected stream events: %v err=%v", events, err)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("event %d: got %v want %v", i, events[i], want[i])
		}
	}
}
