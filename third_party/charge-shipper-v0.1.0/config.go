// Package billship 是 charge 计费系统的生产者发送 SDK（P0 实时通道）。
// 网关只需 Init 一次、热路径调 Ship、停机调 Shutdown。设计见
// docs/superpowers/specs/2026-08-07-bill-shipper-sdk-design.md。
package billship

import (
	"errors"
	"time"
)

// Config 是网关初始化 SDK 的全部旋钮。缺省项由 applyDefaults 填充。
type Config struct {
	QueueURL   string
	Region     string
	SiteID     string
	SourceType string

	BufferSize      int           // 有界 chan 容量（默认 10000）
	BatchSize       int           // 攒批条数（≤10，默认 10；>10 收敛到 10）
	BatchWait       time.Duration // 攒批最长等待（默认 200ms）
	SendConcurrency int           // 并发 batch worker 数（默认 8）
	SendTimeout     time.Duration // 单次 SendMessageBatch 的 ctx 超时（默认 3s）
	MaxRetries      int           // 单批/失败子集最大重试次数（默认 3；设负数 = 不重试）

	Enabled       bool // 总开关（灰度可一键关）
	LogFailedBody bool // 真失败时是否把 Body 一并打进日志（默认 false）

	// Logger 可选；nil 时用 no-op。字段名对齐 charge logx 习惯便于 grep。
	Logger func(level, msg string, kv ...any)

	// CursorSaveFn 预留给 P3 水位，P0 忽略。
	CursorSaveFn func(sourceID string, watermarkTs int64) error
}

func (c *Config) applyDefaults() {
	if c.BufferSize <= 0 {
		c.BufferSize = 10000
	}
	if c.BatchSize <= 0 {
		c.BatchSize = 10
	}
	if c.BatchSize > 10 {
		c.BatchSize = 10 // SQS SendMessageBatch 硬上限
	}
	if c.BatchWait <= 0 {
		c.BatchWait = 200 * time.Millisecond
	}
	if c.SendConcurrency <= 0 {
		c.SendConcurrency = 8
	}
	if c.SendTimeout <= 0 {
		c.SendTimeout = 3 * time.Second
	}
	// MaxRetries：未设(0) 走默认 3；显式设负数 = 不重试（0 次），事故时快速失败到失败日志。
	if c.MaxRetries == 0 {
		c.MaxRetries = 3
	} else if c.MaxRetries < 0 {
		c.MaxRetries = 0
	}
	if c.Logger == nil {
		c.Logger = func(string, string, ...any) {}
	}
}

func (c Config) validate() error {
	if c.QueueURL == "" {
		return errors.New("billship: Config.QueueURL is required")
	}
	if c.Region == "" {
		return errors.New("billship: Config.Region is required")
	}
	return nil
}
