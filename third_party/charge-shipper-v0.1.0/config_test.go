package billship

import (
	"testing"
	"time"
)

func TestApplyDefaults(t *testing.T) {
	var c Config
	c.applyDefaults()
	if c.BufferSize != 10000 {
		t.Errorf("BufferSize = %d, want 10000", c.BufferSize)
	}
	if c.BatchSize != 10 {
		t.Errorf("BatchSize = %d, want 10", c.BatchSize)
	}
	if c.BatchWait != 200*time.Millisecond {
		t.Errorf("BatchWait = %v, want 200ms", c.BatchWait)
	}
	if c.SendConcurrency != 8 {
		t.Errorf("SendConcurrency = %d, want 8", c.SendConcurrency)
	}
	if c.SendTimeout != 3*time.Second {
		t.Errorf("SendTimeout = %v, want 3s", c.SendTimeout)
	}
	if c.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", c.MaxRetries)
	}
	if c.Logger == nil {
		t.Error("Logger should default to no-op, got nil")
	}
	c.Logger("info", "smoke") // no-op must not panic
}

func TestApplyDefaultsClampsBatchSize(t *testing.T) {
	c := Config{BatchSize: 50}
	c.applyDefaults()
	if c.BatchSize != 10 {
		t.Errorf("BatchSize = %d, want clamped to 10", c.BatchSize)
	}
}

func TestApplyDefaultsMaxRetries(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{"unset defaults to 3", 0, 3},
		{"explicit positive kept", 5, 5},
		{"negative means no retry", -1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Config{MaxRetries: tt.in}
			c.applyDefaults()
			if c.MaxRetries != tt.want {
				t.Errorf("MaxRetries = %d, want %d", c.MaxRetries, tt.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"ok", Config{QueueURL: "u", Region: "r"}, false},
		{"missing queue", Config{Region: "r"}, true},
		{"missing region", Config{QueueURL: "u"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.validate(); (err != nil) != tt.wantErr {
				t.Errorf("validate() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
