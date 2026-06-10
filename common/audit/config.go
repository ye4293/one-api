package audit

import (
	"strings"
	"time"

	"github.com/songquanpeng/one-api/common/env"
)

type config struct {
	Enabled             bool
	GCPProject          string
	BQDataset           string
	BQTable             string
	GCSBucket           string
	CredentialsFile     string
	ChannelSize         int
	MaxBufferMB         int
	DiskBufferDir       string
	DiskBufferMaxGB     int
	BatchSize           int
	FlushInterval       time.Duration
	MaxBodyKB           int
	MaxRespKB           int
	PartitionExpireDays int
	redactSet           map[string]struct{}
}

func loadConfig() *config {
	c := &config{
		Enabled:             env.Bool("AUDIT_ENABLED", false),
		GCPProject:          env.String("AUDIT_GCP_PROJECT", ""),
		BQDataset:           env.String("AUDIT_BQ_DATASET", "audit"),
		BQTable:             env.String("AUDIT_BQ_TABLE", "request_logs"),
		GCSBucket:           env.String("AUDIT_GCS_BUCKET", ""),
		CredentialsFile:     env.String("AUDIT_CREDENTIALS_FILE", ""),
		ChannelSize:         env.Int("AUDIT_CHANNEL_SIZE", 2000),
		MaxBufferMB:         env.Int("AUDIT_MAX_BUFFER_MB", 1024),
		DiskBufferDir:       env.String("AUDIT_DISK_BUFFER_DIR", "./data/audit_spill"),
		DiskBufferMaxGB:     env.Int("AUDIT_DISK_BUFFER_MAX_GB", 40),
		BatchSize:           env.Int("AUDIT_BATCH_SIZE", 500),
		FlushInterval:       time.Duration(env.Int("AUDIT_FLUSH_INTERVAL_SEC", 10)) * time.Second,
		MaxBodyKB:           env.Int("AUDIT_MAX_BODY_KB", 10240),
		MaxRespKB:           env.Int("AUDIT_MAX_RESP_KB", 4096),
		PartitionExpireDays: env.Int("AUDIT_PARTITION_EXPIRE_DAYS", 0),
	}
	raw := env.String("AUDIT_REDACT_HEADERS", "Authorization,Api-Key,X-Api-Key,Cookie,Set-Cookie")
	c.redactSet = make(map[string]struct{})
	for _, h := range strings.Split(raw, ",") {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			c.redactSet[h] = struct{}{}
		}
	}
	return c
}
