package shipper

import (
	"context"
	"testing"
	"time"
)

func validRuntimeConfig() runtimeConfig {
	return runtimeConfig{
		queueURL:           "https://sqs.us-east-1.amazonaws.com/1/charge",
		region:             "us-east-1",
		siteID:             "site-1",
		bufferSize:         10000,
		batchSize:          10,
		batchWaitMS:        200,
		sendConcurrency:    8,
		sendTimeoutSeconds: 3,
		maxRetries:         3,
	}
}

func TestRuntimeConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*runtimeConfig)
		wantErr bool
	}{
		{name: "valid", mutate: func(*runtimeConfig) {}},
		{name: "zero retries allowed", mutate: func(c *runtimeConfig) { c.maxRetries = 0 }},
		{name: "missing queue URL", mutate: func(c *runtimeConfig) { c.queueURL = "" }, wantErr: true},
		{name: "missing region", mutate: func(c *runtimeConfig) { c.region = "" }, wantErr: true},
		{name: "whitespace site ID", mutate: func(c *runtimeConfig) { c.siteID = "  " }, wantErr: true},
		{name: "zero buffer", mutate: func(c *runtimeConfig) { c.bufferSize = 0 }, wantErr: true},
		{name: "zero batch size", mutate: func(c *runtimeConfig) { c.batchSize = 0 }, wantErr: true},
		{name: "batch above SQS limit", mutate: func(c *runtimeConfig) { c.batchSize = 11 }, wantErr: true},
		{name: "zero batch wait", mutate: func(c *runtimeConfig) { c.batchWaitMS = 0 }, wantErr: true},
		{name: "zero concurrency", mutate: func(c *runtimeConfig) { c.sendConcurrency = 0 }, wantErr: true},
		{name: "zero timeout", mutate: func(c *runtimeConfig) { c.sendTimeoutSeconds = 0 }, wantErr: true},
		{name: "negative retries", mutate: func(c *runtimeConfig) { c.maxRetries = -1 }, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validRuntimeConfig()
			tt.mutate(&cfg)
			err := cfg.validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRuntimeConfigSDKConfig(t *testing.T) {
	cfg := validRuntimeConfig()
	got := cfg.sdkConfig()

	if got.BufferSize != 10000 || got.BatchSize != 10 {
		t.Fatalf("size config = buffer %d batch %d", got.BufferSize, got.BatchSize)
	}
	if got.BatchWait != 200*time.Millisecond {
		t.Fatalf("BatchWait = %v, want 200ms", got.BatchWait)
	}
	if got.SendConcurrency != 8 {
		t.Fatalf("SendConcurrency = %d, want 8", got.SendConcurrency)
	}
	if got.SendTimeout != 3*time.Second {
		t.Fatalf("SendTimeout = %v, want 3s", got.SendTimeout)
	}
	if got.MaxRetries != 3 {
		t.Fatalf("MaxRetries = %d, want 3", got.MaxRetries)
	}

	cfg.maxRetries = 0
	if got := cfg.sdkConfig().MaxRetries; got >= 0 {
		t.Fatalf("zero retries must be encoded for SDK as a negative sentinel, got %d", got)
	}
}

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
