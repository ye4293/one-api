package common

import (
	"context"
	"errors"
	"io"
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
