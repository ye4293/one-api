package common

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"time"
)

var ErrSSETimeout = errors.New("SSE stream timed out")

type ownedSSEEvent struct {
	eventType string
	data      string
	err       error
}

// ScanSSEOwned runs callbacks on the calling request goroutine. The background
// reader retains only the body and context, never a Gin Context or a writer.
func ScanSSEOwned(ctx context.Context, body io.ReadCloser, timeout, pingInterval time.Duration, handler func(string) bool, ping func() error) error {
	return ScanSSEOwnedWithEvent(ctx, body, timeout, pingInterval, func(_ string, data string) bool {
		return handler(data)
	}, ping)
}

// ScanSSEOwnedWithEvent also preserves the SSE event name without moving
// callbacks off the owning request goroutine.
func ScanSSEOwnedWithEvent(ctx context.Context, body io.ReadCloser, timeout, pingInterval time.Duration, handler func(string, string) bool, ping func() error) error {
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	events := make(chan ownedSSEEvent)
	stop := make(chan struct{})
	defer close(stop)
	defer body.Close()
	go func() {
		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 64<<10), 50<<20)
		send := func(e ownedSSEEvent) bool {
			select {
			case events <- e:
				return true
			case <-ctx.Done():
				return false
			case <-stop:
				return false
			}
		}
		var eventType string
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				eventType = ""
				continue
			}
			if strings.HasPrefix(line, "event:") {
				eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
				continue
			}
			if strings.HasPrefix(line, "data:") {
				if !send(ownedSSEEvent{eventType: eventType, data: strings.TrimSpace(strings.TrimPrefix(line, "data:"))}) {
					return
				}
			}
		}
		err := scanner.Err()
		if err == nil {
			err = io.EOF
		}
		send(ownedSSEEvent{err: err})
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var pingC <-chan time.Time
	if pingInterval > 0 && ping != nil {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		pingC = ticker.C
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return ErrSSETimeout
		case <-pingC:
			if err := ping(); err != nil {
				return err
			}
		case event := <-events:
			if err := ctx.Err(); err != nil {
				return err
			}
			if event.err != nil {
				return event.err
			}
			timer.Reset(timeout)
			if !handler(event.eventType, event.data) {
				return nil
			}
		}
	}
}
