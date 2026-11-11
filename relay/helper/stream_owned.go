package helper

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/relay/util"
	"io"
	"net/http"
	"time"
)

// Unlike the legacy scanner, callbacks cannot survive the owning request.
func StreamScannerHandlerOwned(c *gin.Context, resp *http.Response, info *util.RelayMeta, handler func(string) bool) {
	if handler == nil {
		return
	}
	StreamScannerHandlerOwnedWithEvent(c, resp, info, func(_ string, data string) bool {
		return handler(data)
	})
}

// Preserve event-only response types while keeping Gin access on the request.
func StreamScannerHandlerOwnedWithEvent(c *gin.Context, resp *http.Response, info *util.RelayMeta, handler func(string, string) bool) {
	if resp == nil || handler == nil || info == nil {
		return
	}
	info.StreamStatus = util.NewStreamStatus()
	common.SetEventStreamHeaders(c)
	timeout := time.Duration(config.StreamingTimeout) * time.Second
	pingInterval := time.Duration(0)
	if config.PingIntervalEnabled && !info.DisablePing {
		pingInterval = time.Duration(config.PingIntervalSeconds) * time.Second
		if pingInterval <= 0 {
			pingInterval = DefaultPingInterval
		}
	}
	err := common.ScanSSEOwnedWithEvent(c.Request.Context(), resp.Body, timeout, pingInterval, func(eventType, data string) bool {
		if data == "[DONE]" {
			info.StreamStatus.SetEndReason(util.StreamEndReasonDone, nil)
			return false
		}
		info.SetFirstResponseTime()
		if !handler(eventType, data) {
			info.StreamStatus.SetEndReason(util.StreamEndReasonHandlerStop, nil)
			return false
		}
		return true
	}, func() error { return PingData(c) })
	switch {
	case err == nil:
	case errors.Is(err, io.EOF):
		info.StreamStatus.SetEndReason(util.StreamEndReasonEOF, nil)
	case c.Request.Context().Err() != nil:
		info.StreamStatus.SetEndReason(util.StreamEndReasonClientGone, err)
	case errors.Is(err, common.ErrSSETimeout):
		info.StreamStatus.SetEndReason(util.StreamEndReasonTimeout, err)
	default:
		info.StreamStatus.SetEndReason(util.StreamEndReasonScannerErr, err)
		info.StreamStatus.RecordError(err.Error())
	}
}
