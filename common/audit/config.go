package audit

import (
	"encoding/json"
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
	MaxBodyKB          int
	MaxRespKB          int
	RetentionDays      int
	BodyS3Bucket       string
	BodyS3Prefix       string
	BodyS3ThresholdKB  int
	redactSet          map[string]struct{}
}

func loadConfig() *auditConfig {
	// 先从环境变量读取默认值（非 UI 配置项沿用此值）
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
		BodyS3ThresholdKB: 32, // 默认 32KB 以上走 S3
	}

	// 从 options 表的 auditConfig 字段覆盖（优先级高于环境变量）
	config.OptionMapRWMutex.RLock()
	raw, ok := config.OptionMap["auditConfig"]
	config.OptionMapRWMutex.RUnlock()

	if ok && raw != "" {
		var js struct {
			Enabled              bool   `json:"enabled"`
			AWSRegion            string `json:"awsRegion"`
			AWSAccessKeyId       string `json:"awsAccessKeyId"`
			AWSSecretAccessKey   string `json:"awsSecretAccessKey"`
			FirehoseStreamName   string `json:"firehoseStreamName"`
			AthenaDatabase       string `json:"athenaDatabase"`
			AthenaTable          string `json:"athenaTable"`
			AthenaWorkgroup      string `json:"athenaWorkgroup"`
			AthenaOutputLocation string `json:"athenaOutputLocation"`
			IcebergTableLocation string `json:"icebergTableLocation"`
			ChannelSize          int    `json:"channelSize"`
			MaxBufferMB          int    `json:"maxBufferMB"`
			DiskBufferDir        string `json:"diskBufferDir"`
			DiskBufferMaxGB      int    `json:"diskBufferMaxGB"`
			BatchSize            int    `json:"batchSize"`
			FlushIntervalSec     int    `json:"flushIntervalSec"`
			MaxBodyKB            int    `json:"maxBodyKB"`
			MaxRespKB            int    `json:"maxRespKB"`
			RetentionDays        int    `json:"retentionDays"`
			BodyS3Bucket         string `json:"bodyS3Bucket"`
			BodyS3Prefix         string `json:"bodyS3Prefix"`
			BodyS3ThresholdKB    int    `json:"bodyS3ThresholdKB"`
		}
		if err := json.Unmarshal([]byte(raw), &js); err == nil {
			c.Enabled = js.Enabled
			if js.AWSRegion != "" {
				c.AWSRegion = js.AWSRegion
			}
			if js.AWSAccessKeyId != "" {
				c.AWSAccessKey = js.AWSAccessKeyId
			}
			if js.AWSSecretAccessKey != "" {
				c.AWSSecretKey = js.AWSSecretAccessKey
			}
			if js.FirehoseStreamName != "" {
				c.FirehoseStream = js.FirehoseStreamName
			}
			if js.AthenaDatabase != "" {
				c.AthenaDatabase = js.AthenaDatabase
			}
			if js.AthenaTable != "" {
				c.AthenaTable = js.AthenaTable
			}
			if js.AthenaWorkgroup != "" {
				c.AthenaWorkgroup = js.AthenaWorkgroup
			}
			if js.AthenaOutputLocation != "" {
				c.S3OutputLocation = js.AthenaOutputLocation
			}
			if js.IcebergTableLocation != "" {
				c.S3DataLocation = js.IcebergTableLocation
			}
			if js.ChannelSize > 0 {
				c.ChannelSize = js.ChannelSize
			}
			if js.MaxBufferMB > 0 {
				c.MaxBufferMB = js.MaxBufferMB
			}
			if js.DiskBufferDir != "" {
				c.DiskBufferDir = js.DiskBufferDir
			}
			if js.DiskBufferMaxGB > 0 {
				c.DiskBufferMaxGB = js.DiskBufferMaxGB
			}
			if js.BatchSize > 0 {
				c.BatchSize = js.BatchSize
			}
			if js.FlushIntervalSec > 0 {
				c.FlushInterval = time.Duration(js.FlushIntervalSec) * time.Second
			}
			if js.MaxBodyKB >= 0 {
				c.MaxBodyKB = js.MaxBodyKB
			}
			if js.MaxRespKB >= 0 {
				c.MaxRespKB = js.MaxRespKB
			}
			if js.RetentionDays > 0 {
				c.RetentionDays = js.RetentionDays
			}
			if js.BodyS3Bucket != "" {
				c.BodyS3Bucket = js.BodyS3Bucket
			}
			if js.BodyS3Prefix != "" {
				c.BodyS3Prefix = js.BodyS3Prefix
			}
			if js.BodyS3ThresholdKB > 0 {
				c.BodyS3ThresholdKB = js.BodyS3ThresholdKB
			}
		}
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
