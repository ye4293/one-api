package gemini

import (
	"bufio"
	"context"
	"io"
	"strings"
)

// Match the native Gemini stream limit so inline image data does not stop
// response ID extraction at bufio.Scanner's default 64 KiB limit.
const maxGeminiStreamLineSize = 50 << 20

type geminiStreamEvent struct {
	data string
	err  error
}

func scanGeminiStream(ctx context.Context, body io.Reader) <-chan geminiStreamEvent {
	events := make(chan geminiStreamEvent)
	go func() {
		defer close(events)
		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 64<<10), maxGeminiStreamLineSize)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" {
				continue
			}
			select {
			case events <- geminiStreamEvent{data: data}:
			case <-ctx.Done():
				return
			}
			if data == "[DONE]" {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case events <- geminiStreamEvent{err: err}:
			case <-ctx.Done():
			}
		}
	}()
	return events
}
