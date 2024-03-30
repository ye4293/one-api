package controller

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/middleware"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/monitor"
	"github.com/songquanpeng/one-api/relay/constant"
	"github.com/songquanpeng/one-api/relay/controller"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

// https://platform.openai.com/docs/api-reference/chat

func relay(c *gin.Context, relayMode int) *model.ErrorWithStatusCode {
	var err *model.ErrorWithStatusCode
	switch relayMode {
	case constant.RelayModeImagesGenerations:
		err = controller.RelayImageHelper(c, relayMode)
	case constant.RelayModeAudioSpeech:
		fallthrough
	case constant.RelayModeAudioTranslation:
		fallthrough
	case constant.RelayModeAudioTranscription:
		err = controller.RelayAudioHelper(c, relayMode)
	default:
		err = controller.RelayTextHelper(c)
	}
	return err
}
func Relay(c *gin.Context) {
	ctx := c.Request.Context()
	relayMode := constant.Path2RelayMode(c.Request.URL.Path)
	if config.DebugEnabled {
		requestBody, _ := common.GetRequestBody(c)
		logger.Debugf(ctx, "request body: %s", string(requestBody))
	}
	channelId := c.GetInt("channel_id")
	bizErr := relay(c, relayMode)
	requestId := c.GetString(logger.RequestIdKey) // 确保在函数开始就获取requestId

	if bizErr == nil {
		monitor.Emit(channelId, true)
		return
	}
	lastFailedChannelId := channelId
	channelName := c.GetString("channel_name")
	group := c.GetString("group")
	originalModel := c.GetString("original_model")
	go processChannelRelayError(ctx, channelId, channelName, bizErr)

	retryTimes := config.RetryTimes
	if !shouldRetry(c, bizErr.StatusCode) {
		logger.Errorf(ctx, "Relay error happen, status code is %d, won't retry in this case", bizErr.StatusCode)
		retryTimes = 0
	}

	for i := retryTimes; i > 0; i-- {
		channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, originalModel, i != retryTimes)
		if err != nil {
			logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed: %w", err)
			break
		}
		if channel.Id == lastFailedChannelId {
			continue
		}
		logger.Infof(ctx, "Using channel #%d to retry (remain times %d)", channel.Id, i)

		middleware.SetupContextForSelectedChannel(c, channel, originalModel)
		requestBody, err := common.GetRequestBody(c)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		bizErr = relay(c, relayMode)
		if bizErr == nil {
			return
		}

		channelId = c.GetInt("channel_id")
		lastFailedChannelId = channelId
		channelName = c.GetString("channel_name")
		go processChannelRelayError(ctx, channelId, channelName, bizErr)
	}

	// 如果所有尝试都失败，不处理耗时记录
	if bizErr != nil {
		if bizErr.StatusCode == http.StatusTooManyRequests {
			bizErr.Error.Message = "The current group upstream load is saturated, please try again later."
		}
		bizErr.Error.Message = helper.MessageWithRequestId(bizErr.Error.Message, requestId)
		c.JSON(bizErr.StatusCode, gin.H{
			"error": bizErr.Error,
		})
	}
}

func shouldRetry(c *gin.Context, statusCode int) bool {
	if _, ok := c.Get("specific_channel_id"); ok {
		return false
	}
	if statusCode == http.StatusTooManyRequests {
		return true
	}
	if statusCode/100 == 5 {
		return true
	}
	if statusCode == http.StatusBadRequest {
		return false
	}
	if statusCode/100 == 2 {
		return false
	}
	return true
}

func processChannelRelayError(ctx context.Context, channelId int, channelName string, err *model.ErrorWithStatusCode) {
	logger.Errorf(ctx, "relay error (channel #%d): %s", channelId, err.Message)
	// https://platform.openai.com/docs/guides/error-codes/api-errors
	if util.ShouldDisableChannel(&err.Error, err.StatusCode) {
		monitor.DisableChannel(channelId, channelName, err.Message)
	} else {
		monitor.Emit(channelId, false)
	}
}

func RelayMidjourney(c *gin.Context) {

}

func RelayNotImplemented(c *gin.Context) {
	err := model.Error{
		Message: "API not implemented",
		Type:    "api_error",
		Param:   "",
		Code:    "api_not_implemented",
	}
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": err,
	})
}

func RelayNotFound(c *gin.Context) {
	err := model.Error{
		Message: fmt.Sprintf("Invalid URL (%s %s)", c.Request.Method, c.Request.URL.Path),
		Type:    "invalid_request_error",
		Param:   "",
		Code:    "",
	}
	c.JSON(http.StatusNotFound, gin.H{
		"error": err,
	})
}
