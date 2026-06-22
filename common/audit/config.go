package audit

import (
	"strings"
	"time"

	"github.com/songquanpeng/one-api/common/env"
)

type config struct {
	Enabled           bool
	AWSRegion         string
	AWSAccessKey      string
	AWSSecretKey      string
	FirehoseStream    string
	AthenaDatabase    string
	AthenaTable       string
	AthenaWorkgroup   string
	S3OutputLocation  string
	S3DataLocation    string
	CompactionEnabled bool

	ChannelSize     int
	MaxBufferMB     int
	DiskBufferDir   string
	DiskBufferMaxGB int
	BatchSize       int
	FlushInterval   time.Duration
	MaxBodyKB       int
	MaxRespKB       int
	redactSet       map[string]struct{}
}

func loadConfig() *config {
	c := &config{
		Enabled:           env.Bool("AUDIT_ENABLED", false),
		AWSRegion:         env.String("AUDIT_AWS_REGION", ""),
		AWSAccessKey:      env.String("AUDIT_AWS_ACCESS_KEY", ""),
		AWSSecretKey:      env.String("AUDIT_AWS_SECRET_KEY", ""),
		FirehoseStream:    env.String("AUDIT_FIREHOSE_STREAM", ""),
		AthenaDatabase:    env.String("AUDIT_ATHENA_DATABASE", "audit"),
		AthenaTable:       env.String("AUDIT_ATHENA_TABLE", "request_logs"),
		AthenaWorkgroup:   env.String("AUDIT_ATHENA_WORKGROUP", "primary"),
		S3OutputLocation:  env.String("AUDIT_S3_OUTPUT_LOCATION", ""),
		S3DataLocation:    env.String("AUDIT_S3_DATA_LOCATION", ""),
		CompactionEnabled: env.Bool("AUDIT_COMPACTION_ENABLED", false),

		ChannelSize:     env.Int("AUDIT_CHANNEL_SIZE", 2000),
		MaxBufferMB:     env.Int("AUDIT_MAX_BUFFER_MB", 1024),
		DiskBufferDir:   env.String("AUDIT_DISK_BUFFER_DIR", "./data/audit_spill"),
		DiskBufferMaxGB: env.Int("AUDIT_DISK_BUFFER_MAX_GB", 40),
		BatchSize:       env.Int("AUDIT_BATCH_SIZE", 500),
		FlushInterval:   time.Duration(env.Int("AUDIT_FLUSH_INTERVAL_SEC", 10)) * time.Second,
		MaxBodyKB:       env.Int("AUDIT_MAX_BODY_KB", 10240),
		MaxRespKB:       env.Int("AUDIT_MAX_RESP_KB", 4096),
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
