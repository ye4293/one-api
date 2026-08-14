package shipper

import (
	"context"
	"testing"
	"time"
)

// 未 Init（禁用）时，Ship 必须是安全 no-op，绝不 panic —— 这是"不影响原业务"的底线。
func TestShipNoopWhenNotInitialized(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Ship panicked when not initialized: %v", r)
		}
	}()
	Ship(1, time.Now().Unix(), "gpt-4o", []byte(`{"id":1,"model_name":"gpt-4o"}`))
}

// 未 Init 时 Shutdown 也应安全返回。
func TestShutdownNoopWhenNotInitialized(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	Shutdown(ctx) // 不 panic 即通过
}
