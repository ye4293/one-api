package billship

import (
	"context"
	"testing"
)

func resetSingleton() { defShip.Store(nil) }

func TestInitValidates(t *testing.T) {
	resetSingleton()
	if err := Init(Config{Region: "us-east-1"}); err == nil {
		t.Error("Init should fail without QueueURL")
	}
	resetSingleton()
}

func TestPackageShipBeforeInitIsSafe(t *testing.T) {
	resetSingleton()
	// 未 Init：Ship / Shutdown / Stats 都不得 panic。
	Ship(Record{SiteID: "s", Model: "m", SourceType: "new-api", Body: []byte("{}")})
	if got := Stats(); got != (Snapshot{}) {
		t.Errorf("Stats before Init = %+v, want zero", got)
	}
	if err := Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown before Init = %v, want nil", err)
	}
}

func TestInitTwiceFails(t *testing.T) {
	resetSingleton()
	// 直接放一个假单例，模拟已初始化。
	cfg := Config{QueueURL: "q", Region: "r"}
	cfg.applyDefaults()
	sh := newShipper(cfg)
	sh.sendBatch = func([]Record) {}
	sh.start()
	defShip.Store(sh)

	if err := Init(Config{QueueURL: "q2", Region: "r2"}); err == nil {
		t.Error("second Init should fail")
	}
	_ = Shutdown(context.Background())
	resetSingleton()
}
