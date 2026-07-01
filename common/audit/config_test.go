package audit

import (
	"os"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	os.Clearenv()
	cfg := loadConfig()
	if cfg.Enabled {
		t.Errorf("Enabled 默认应为 false")
	}
	if cfg.ChannelSize != 2000 {
		t.Errorf("ChannelSize 默认应为 2000, got %d", cfg.ChannelSize)
	}
	if cfg.MaxBufferMB != 1024 {
		t.Errorf("MaxBufferMB 默认应为 1024, got %d", cfg.MaxBufferMB)
	}
	if cfg.DiskBufferMaxGB != 40 {
		t.Errorf("DiskBufferMaxGB 默认应为 40, got %d", cfg.DiskBufferMaxGB)
	}
	if cfg.BatchSize != 500 {
		t.Errorf("BatchSize 默认应为 500, got %d", cfg.BatchSize)
	}
	if cfg.MaxBodyKB != 10240 {
		t.Errorf("MaxBodyKB 默认应为 10240, got %d", cfg.MaxBodyKB)
	}
	if cfg.MaxRespKB != 4096 {
		t.Errorf("MaxRespKB 默认应为 4096, got %d", cfg.MaxRespKB)
	}
}

func TestLoadConfigRedactHeadersLowercased(t *testing.T) {
	os.Clearenv()
	cfg := loadConfig()
	if _, ok := cfg.redactSet["authorization"]; !ok {
		t.Errorf("默认脱敏头应包含小写 authorization")
	}
	if _, ok := cfg.redactSet["x-api-key"]; !ok {
		t.Errorf("默认脱敏头应包含小写 x-api-key")
	}
}
