// Package shipper 是 one-api 侧对 billship 计费投递 SDK（SQS 生产者）的适配层。
// 隔离外部 SDK：本包不 import model，故 model → common/shipper → billship 无 import 环。
package shipper

import (
	"context"
	"fmt"

	billship "github.com/ezlinkai/billship"

	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
)

// sourceType 标识消息来源，供计费侧区分网关类型。
const sourceType = "one-api"

// Init 按配置初始化 billship 单例。未启用时直接返回（Ship 后续为安全 no-op）。
// init 失败仅告警降级、绝不 crash 启动（对齐 audit 容错模式）。
func Init() {
	if !config.BillShipEnabled {
		logger.SysLog("bill shipper disabled")
		return
	}
	err := billship.Init(billship.Config{
		QueueURL:      config.BillShipQueueURL,
		Region:        config.BillShipRegion,
		SiteID:        config.BillShipSiteID,
		SourceType:    sourceType,
		Enabled:       true,
		LogFailedBody: config.BillShipLogFailedBody,
		Logger: func(level, msg string, kv ...any) {
			logger.SysError(fmt.Sprintf("[billship] %s %s %v", level, msg, kv))
		},
	})
	if err != nil {
		logger.SysError("bill shipper init failed, shipping disabled: " + err.Error())
		return
	}
	logger.SysLog("bill shipper initialized, site=" + config.BillShipSiteID)
}

// Ship 把一条 logs 行非阻塞投递到 SQS。未 Init / 禁用时为安全 no-op。
// 契约：调用后不得再修改 body（billship 异步持有该 slice）。调用方每次传入新分配的 body。
func Ship(logID int64, createdAt int64, modelName string, body []byte) {
	billship.Ship(billship.Record{
		SiteID:     config.BillShipSiteID,
		Model:      modelName,
		SourceType: sourceType,
		LogID:      logID,
		CreatedAt:  createdAt,
		Body:       body,
	})
}

// Shutdown 优雅停机，排空在途消息。未 Init 时返回 nil。
func Shutdown(ctx context.Context) {
	if err := billship.Shutdown(ctx); err != nil {
		logger.SysError("bill shipper shutdown: " + err.Error())
	}
}
