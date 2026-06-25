package audit

import (
	"regexp"
	"strings"
	"time"

	"github.com/songquanpeng/one-api/common/config"
)

var reIdentifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,127}$`)

type auditConfig struct {
	Enabled          bool
	AWSRegion        string
	AWSAccessKey     string
	AWSSecretKey     string
	FirehoseStream   string
	AthenaDatabase   string
	AthenaTable      string
	AthenaWorkgroup  string
	S3OutputLocation string
	S3DataLocation   string
	ChannelSize      int
	MaxBufferMB      int
	DiskBufferDir    string
	DiskBufferMaxGB  int
	BatchSize        int
	FlushInterval    time.Duration
	MaxBodyKB        int
	MaxRespKB        int
	RetentionDays    int
	redactSet        map[string]struct{}
}

func loadConfig() *auditConfig {
	c := &auditConfig{
		Enabled:          config.AuditEnabled,
		AWSRegion:        config.AuditAWSRegion,
		AWSAccessKey:     config.AuditAWSAccessKey,
		AWSSecretKey:     config.AuditAWSSecretKey,
		FirehoseStream:   config.AuditFirehoseStream,
		AthenaDatabase:   config.AuditAthenaDatabase,
		AthenaTable:      config.AuditAthenaTable,
		AthenaWorkgroup:  config.AuditAthenaWorkgroup,
		S3OutputLocation: config.AuditS3OutputLocation,
		S3DataLocation:   config.AuditS3DataLocation,
		ChannelSize:      config.AuditChannelSize,
		MaxBufferMB:      config.AuditMaxBufferMB,
		DiskBufferDir:    config.AuditDiskBufferDir,
		DiskBufferMaxGB:  config.AuditDiskBufferMaxGB,
		BatchSize:        config.AuditBatchSize,
		FlushInterval:    time.Duration(config.AuditFlushIntervalSec) * time.Second,
		MaxBodyKB:        config.AuditMaxBodyKB,
		MaxRespKB:        config.AuditMaxRespKB,
		RetentionDays:    config.AuditRetentionDays,
	}
	c.redactSet = make(map[string]struct{})
	for _, h := range strings.Split(config.AuditRedactHeaders, ",") {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			c.redactSet[h] = struct{}{}
		}
	}
	if c.Enabled {
		if !reIdentifier.MatchString(c.AthenaDatabase) {
			c.Enabled = false
		}
		if !reIdentifier.MatchString(c.AthenaTable) {
			c.Enabled = false
		}
	}
	return c
}
