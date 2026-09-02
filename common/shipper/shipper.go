// Package shipper 是 one-api 侧对 billship 计费投递 SDK（SQS 生产者）的适配层。
// 隔离外部 SDK：本包不 import model，故 model → common/shipper → billship 无 import 环。
package shipper

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	billship "github.com/changshiaos/charge/server/shipper"

	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
)

// sourceType 标识消息来源，供计费侧区分网关类型。
const sourceType = "one-api"

var (
	shipEnabled atomic.Bool
	shipSiteID  atomic.Pointer[string]
)

type runtimeConfig struct {
	queueURL           string
	region             string
	siteID             string
	logFailedBody      bool
	bufferSize         int
	batchSize          int
	batchWaitMS        int
	sendConcurrency    int
	sendTimeoutSeconds int
	maxRetries         int
}

func loadRuntimeConfig() runtimeConfig {
	return runtimeConfig{
		queueURL:           config.BillShipQueueURL,
		region:             config.BillShipRegion,
		siteID:             config.BillShipSiteID,
		logFailedBody:      config.BillShipLogFailedBody,
		bufferSize:         config.BillShipBufferSize,
		batchSize:          config.BillShipBatchSize,
		batchWaitMS:        config.BillShipBatchWaitMS,
		sendConcurrency:    config.BillShipSendConcurrency,
		sendTimeoutSeconds: config.BillShipSendTimeoutSeconds,
		maxRetries:         config.BillShipMaxRetries,
	}
}

// Init 按配置初始化 billship 单例。未启用时直接返回（Ship 后续为安全 no-op）。
// init 失败仅告警降级、绝不 crash 启动（对齐 audit 容错模式）。
func Init() {
	if !config.BillShipEnabled {
		logger.SysLog("bill shipper disabled")
		return
	}
	runtimeCfg := loadRuntimeConfig()
	if err := runtimeCfg.validate(); err != nil {
		logger.SysError("bill shipper config invalid, shipping disabled: " + err.Error())
		return
	}
	err := billship.Init(runtimeCfg.sdkConfig())
	if err != nil {
		logger.SysError("bill shipper init failed, shipping disabled: " + err.Error())
		return
	}
	siteID := runtimeCfg.siteID
	shipSiteID.Store(&siteID)
	shipEnabled.Store(true)
	logger.SysLog(fmt.Sprintf(
		"bill shipper initialized, site=%s buffer_size=%d batch_size=%d batch_wait=%s send_concurrency=%d send_timeout=%s max_retries=%d",
		siteID,
		runtimeCfg.bufferSize,
		runtimeCfg.batchSize,
		time.Duration(runtimeCfg.batchWaitMS)*time.Millisecond,
		runtimeCfg.sendConcurrency,
		time.Duration(runtimeCfg.sendTimeoutSeconds)*time.Second,
		runtimeCfg.maxRetries,
	))
}

func (c runtimeConfig) validate() error {
	if strings.TrimSpace(c.queueURL) == "" {
		return errors.New("BILL_SHIP_QUEUE_URL is required")
	}
	if strings.TrimSpace(c.region) == "" {
		return errors.New("BILL_SHIP_REGION is required")
	}
	if strings.TrimSpace(c.siteID) == "" {
		return errors.New("BILL_SHIP_SITE_ID is required")
	}
	if c.bufferSize <= 0 {
		return errors.New("BILL_SHIP_BUFFER_SIZE must be greater than 0")
	}
	if c.batchSize < 1 || c.batchSize > 10 {
		return errors.New("BILL_SHIP_BATCH_SIZE must be between 1 and 10")
	}
	if c.batchWaitMS <= 0 {
		return errors.New("BILL_SHIP_BATCH_WAIT_MS must be greater than 0")
	}
	if c.sendConcurrency <= 0 {
		return errors.New("BILL_SHIP_SEND_CONCURRENCY must be greater than 0")
	}
	if c.sendTimeoutSeconds <= 0 {
		return errors.New("BILL_SHIP_SEND_TIMEOUT_SECONDS must be greater than 0")
	}
	if c.maxRetries < 0 {
		return errors.New("BILL_SHIP_MAX_RETRIES must be greater than or equal to 0")
	}
	return nil
}

func (c runtimeConfig) sdkConfig() billship.Config {
	maxRetries := c.maxRetries
	if maxRetries == 0 {
		// billship 把 Config.MaxRetries=0 解释为“使用默认值 3”；负值才表示不重试。
		maxRetries = -1
	}
	return billship.Config{
		QueueURL:        c.queueURL,
		Region:          c.region,
		SiteID:          c.siteID,
		SourceType:      sourceType,
		BufferSize:      c.bufferSize,
		BatchSize:       c.batchSize,
		BatchWait:       time.Duration(c.batchWaitMS) * time.Millisecond,
		SendConcurrency: c.sendConcurrency,
		SendTimeout:     time.Duration(c.sendTimeoutSeconds) * time.Second,
		MaxRetries:      maxRetries,
		Enabled:         true,
		LogFailedBody:   c.logFailedBody,
		Logger: func(level, msg string, kv ...any) {
			logger.SysError(fmt.Sprintf("[billship] %s %s %v", level, msg, kv))
		},
	}
}

// Enabled 返回适配层是否已成功初始化并可投递。
// 与配置开关相比，这还能覆盖启用配置但 SDK 初始化失败的降级状态。
func Enabled() bool {
	return shipEnabled.Load()
}

// Ship 把一条 logs 行非阻塞投递到 SQS。未 Init / 禁用时为安全 no-op。
// 契约：调用后不得再修改 body（billship 异步持有该 slice）。调用方每次传入新分配的 body。
func Ship(logID int64, createdAt int64, modelName string, body []byte) {
	if !shipEnabled.Load() {
		return
	}
	siteID := shipSiteID.Load()
	if siteID == nil {
		return
	}
	billship.Ship(billship.Record{
		SiteID:     *siteID,
		Model:      modelName,
		SourceType: sourceType,
		LogID:      logID,
		CreatedAt:  createdAt,
		Body:       body,
	})
}

// Shutdown 优雅停机，排空在途消息。未 Init 时返回 nil。
func Shutdown(ctx context.Context) {
	if !shipEnabled.Swap(false) {
		return
	}
	if err := billship.Shutdown(ctx); err != nil {
		logger.SysError("bill shipper shutdown: " + err.Error())
	}
}
