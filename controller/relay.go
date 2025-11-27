package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/middleware"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/monitor"
	"github.com/songquanpeng/one-api/relay/channel/midjourney"
	relayconstant "github.com/songquanpeng/one-api/relay/constant"
	controller "github.com/songquanpeng/one-api/relay/controller"
	"github.com/songquanpeng/one-api/relay/model"

	"github.com/songquanpeng/one-api/relay/util"
)

// https://platform.openai.com/docs/api-reference/chat

func relayHelper(c *gin.Context, relayMode int) *model.ErrorWithStatusCode {
	ctx := c.Request.Context()
	var err *model.ErrorWithStatusCode

	logger.Infof(ctx, "relayHelper: relayMode=%d, path=%s", relayMode, c.Request.URL.Path)

	switch relayMode {
	case relayconstant.RelayModeImagesGenerations:
		logger.Infof(ctx, "relayHelper: calling RelayImageHelper for images/generations")

		// 检查调用前的上下文状态
		if channelHistoryInterface, exists := c.Get("admin_channel_history"); exists {
			logger.Infof(ctx, "relayHelper: BEFORE RelayImageHelper - admin_channel_history exists: %v", channelHistoryInterface)
		} else {
			logger.Warnf(ctx, "relayHelper: BEFORE RelayImageHelper - admin_channel_history NOT found")
		}

		err = controller.RelayImageHelper(c, relayMode)

		// 检查调用后的上下文状态
		if channelHistoryInterface, exists := c.Get("admin_channel_history"); exists {
			logger.Infof(ctx, "relayHelper: AFTER RelayImageHelper - admin_channel_history exists: %v", channelHistoryInterface)
		} else {
			logger.Warnf(ctx, "relayHelper: AFTER RelayImageHelper - admin_channel_history NOT found")
		}

		logger.Infof(ctx, "relayHelper: RelayImageHelper returned error: %v", err)

	case relayconstant.RelayModeAudioSpeech:
		fallthrough
	case relayconstant.RelayModeAudioTranslation:
		fallthrough
	case relayconstant.RelayModeAudioTranscription:
		err = controller.RelayAudioHelper(c, relayMode)
	default:
		err = controller.RelayTextHelper(c)
	}
	return err
}

func Relay(c *gin.Context) {
	ctx := c.Request.Context()
	relayMode := relayconstant.Path2RelayMode(c.Request.URL.Path)
	requestID := c.GetHeader("X-Request-ID")
	c.Set("X-Request-ID", requestID)

	channelId := c.GetInt("channel_id")
	userId := c.GetInt("id")

	logger.Infof(ctx, "Relay START: path=%s, relayMode=%d, channelId=%d, userId=%d",
		c.Request.URL.Path, relayMode, channelId, userId)

	if config.DebugEnabled {
		requestBody, _ := common.GetRequestBody(c)
		logger.Debugf(ctx, "request body: %s", string(requestBody))
	}

	// 为第一次调用设置渠道历史
	var firstChannelHistory []int
	firstChannelHistory = append(firstChannelHistory, channelId)
	c.Set("admin_channel_history", firstChannelHistory)

	bizErr := relayHelper(c, relayMode)

	if bizErr == nil {
		// 第一次成功，需要补充设置渠道历史（如果还没有的话）
		if channelHistoryInterface, exists := c.Get("admin_channel_history"); !exists {
			var channelHistory []int
			channelHistory = append(channelHistory, channelId)
			c.Set("admin_channel_history", channelHistory)
			logger.Infof(ctx, "Relay SUCCESS - setting admin_channel_history for first success: %v, path: %s, channelId: %d",
				channelHistory, c.Request.URL.Path, channelId)
		} else {
			logger.Infof(ctx, "Relay SUCCESS - admin_channel_history already exists: %v, path: %s",
				channelHistoryInterface, c.Request.URL.Path)
		}

		monitor.Emit(channelId, true)
		return
	}

	channelName := c.GetString("channel_name")
	group := c.GetString("group")
	originalModel := c.GetString("original_model")
	keyIndex := c.GetInt("key_index") // 在异步调用前获取keyIndex
	go processChannelRelayError(ctx, userId, channelId, channelName, keyIndex, bizErr, originalModel)

	retryTimes := config.RetryTimes
	if !shouldRetry(c, bizErr.StatusCode, bizErr.Error.Message) {
		logger.Errorf(ctx, "Relay error happen, status code is %d, won't retry in this case", bizErr.StatusCode)
		retryTimes = 0
	}

	// 记录使用的渠道历史，用于添加到日志中
	var channelHistory []int
	// 添加初始失败的渠道
	channelHistory = append(channelHistory, channelId)

	for i := retryTimes; i > 0; i-- {
		// 每次重试都选择新渠道（多Key和单Key渠道统一处理）
		// 计算应该跳过的优先级数量：第1次重试仍使用最高优先级，第2次重试使用第2个优先级，以此类推
		skipPriorityLevels := retryTimes - i
		channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, originalModel, skipPriorityLevels)
		if err != nil {
			logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed: %v", err)
			break
		}

		// 获取重试原因 - 直接使用原始错误消息
		retryReason := bizErr.Error.Message

		// 获取新渠道的key信息
		newKeyIndex := 0
		isMultiKey := false
		if channel.MultiKeyInfo.IsMultiKey {
			isMultiKey = true
			// 获取下一个可用key的索引
			_, newKeyIndex, _ = channel.GetNextAvailableKey()
		}

		// 生成详细的重试日志
		retryLog := formatRetryLog(ctx, channelId, channelName, keyIndex,
			channel.Id, channel.Name, newKeyIndex, originalModel, retryReason,
			retryTimes-i+1, retryTimes, isMultiKey, userId, requestID)

		logger.Infof(ctx, retryLog)

		// 记录重试使用的渠道
		channelHistory = append(channelHistory, channel.Id)

		middleware.SetupContextForSelectedChannel(c, channel, originalModel)
		// 重新构建请求体 - 修复Content-Length错误
		requestBody, err := common.GetRequestBody(c)
		if err != nil {
			logger.Errorf(ctx, "GetRequestBody failed: %v", err)
			break
		}
		// 重要：重建请求体，确保重试时有正确的内容
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		logger.Debugf(ctx, "Rebuilt request body for retry, size: %d bytes", len(requestBody))

		// 在调用relayHelper之前设置渠道历史，这样RelayImageHelper就能获取到了
		c.Set("admin_channel_history", channelHistory)

		bizErr = relayHelper(c, relayMode)
		if bizErr == nil {
			monitor.Emit(channel.Id, true)
			return
		}

		channelId = c.GetInt("channel_id")
		channelName = c.GetString("channel_name")
		keyIndex = c.GetInt("key_index") // 在异步调用前获取keyIndex

		go processChannelRelayError(ctx, userId, channelId, channelName, keyIndex, bizErr, originalModel)
	}

	// 如果所有尝试都失败，记录渠道历史到上下文中
	if bizErr != nil {
		// 记录渠道历史到上下文中，供后续日志记录使用
		c.Set("admin_channel_history", channelHistory)

		// 检查是否是xAI内容违规错误，如果是则记录费用
		if isXAIContentViolation(bizErr.StatusCode, bizErr.Error.Message) {
			recordXAIContentViolationCharge(ctx, c, channelHistory)
		} else {
			// 记录失败请求的日志（不消费quota）
			recordFailedRequestLog(ctx, c, bizErr, channelHistory)
		}

		if bizErr.StatusCode == http.StatusTooManyRequests {
			bizErr.Error.Message = "The current group upstream load is saturated, please try again later."
		}
		c.JSON(bizErr.StatusCode, gin.H{
			"error": gin.H{
				"message": bizErr.Error.Message,
				"type":    "api_error",
				"param":   "",
				"code":    bizErr.Error.Code,
			},
		})
	}
}

// recordFailedRequestLog 记录失败请求的日志
func recordFailedRequestLog(ctx context.Context, c *gin.Context, bizErr *model.ErrorWithStatusCode, channelHistory []int) {
	userId := c.GetInt("id")
	originalModel := c.GetString("original_model")
	tokenName := c.GetString("token_name")
	requestID := c.GetHeader("X-Request-ID")

	// 获取渠道历史信息
	var otherInfo string
	if len(channelHistory) > 0 {
		if channelHistoryBytes, err := json.Marshal(channelHistory); err == nil {
			otherInfo = fmt.Sprintf("adminInfo:%s", string(channelHistoryBytes))
		}
	}

	// 构建失败日志内容
	logContent := fmt.Sprintf("请求失败: %s", bizErr.Error.Message)
	if requestID != "" {
		logContent = fmt.Sprintf("请求失败 [%s]: %s", requestID, bizErr.Error.Message)
	}

	// 获取最后使用的渠道ID
	channelId := 0
	if len(channelHistory) > 0 {
		channelId = channelHistory[len(channelHistory)-1]
	}

	// 记录失败日志，quota为0
	dbmodel.RecordConsumeLogWithOtherAndRequestID(
		ctx,
		userId,
		channelId,
		0, // promptTokens
		0, // completionTokens
		originalModel,
		tokenName,
		0, // quota - 失败请求不消费
		logContent,
		0.0,   // duration
		"",    // title
		"",    // httpReferer
		false, // isStream
		0.0,   // firstWordLatency
		otherInfo,
		requestID,
	)

	logger.Infof(ctx, "Recorded failed request log: userId=%d, model=%s, error=%s, channels=%v",
		userId, originalModel, bizErr.Error.Message, channelHistory)
}

// recordMidjourneyFailedLog 记录Midjourney失败请求的日志
func recordMidjourneyFailedLog(ctx context.Context, c *gin.Context, mjErr *midjourney.MidjourneyResponseWithStatusCode, channelHistory []int) {
	userId := c.GetInt("id")
	originalModel := c.GetString("original_model")
	tokenName := c.GetString("token_name")
	requestID := c.GetHeader("X-Request-ID")

	// 获取渠道历史信息
	var otherInfo string
	if len(channelHistory) > 0 {
		if channelHistoryBytes, err := json.Marshal(channelHistory); err == nil {
			otherInfo = fmt.Sprintf("adminInfo:%s", string(channelHistoryBytes))
		}
	}

	// 构建失败日志内容
	errorMsg := fmt.Sprintf("%s %s", mjErr.Response.Description, mjErr.Response.Result)
	logContent := fmt.Sprintf("Midjourney请求失败: %s", errorMsg)
	if requestID != "" {
		logContent = fmt.Sprintf("Midjourney请求失败 [%s]: %s", requestID, errorMsg)
	}

	// 获取最后使用的渠道ID
	channelId := 0
	if len(channelHistory) > 0 {
		channelId = channelHistory[len(channelHistory)-1]
	}

	// 记录失败日志，quota为0
	dbmodel.RecordConsumeLogWithOtherAndRequestID(
		ctx,
		userId,
		channelId,
		0, // promptTokens
		0, // completionTokens
		originalModel,
		tokenName,
		0, // quota - 失败请求不消费
		logContent,
		0.0,   // duration
		"",    // title
		"",    // httpReferer
		false, // isStream
		0.0,   // firstWordLatency
		otherInfo,
		requestID,
	)

	logger.Infof(ctx, "Recorded Midjourney failed request log: userId=%d, model=%s, error=%s, channels=%v",
		userId, originalModel, errorMsg, channelHistory)
}

// recordRunwayFailedLog 记录Runway失败请求的日志
func recordRunwayFailedLog(ctx context.Context, c *gin.Context, statusCode int, channelHistory []int) {
	userId := c.GetInt("id")
	originalModel := c.GetString("original_model")
	tokenName := c.GetString("token_name")
	requestID := c.GetHeader("X-Request-ID")

	// 获取渠道历史信息
	var otherInfo string
	if len(channelHistory) > 0 {
		if channelHistoryBytes, err := json.Marshal(channelHistory); err == nil {
			otherInfo = fmt.Sprintf("adminInfo:%s", string(channelHistoryBytes))
		}
	}

	// 构建失败日志内容
	errorMsg := fmt.Sprintf("HTTP状态码: %d", statusCode)
	logContent := fmt.Sprintf("Runway请求失败: %s", errorMsg)
	if requestID != "" {
		logContent = fmt.Sprintf("Runway请求失败 [%s]: %s", requestID, errorMsg)
	}

	// 获取最后使用的渠道ID
	channelId := 0
	if len(channelHistory) > 0 {
		channelId = channelHistory[len(channelHistory)-1]
	}

	// 记录失败日志，quota为0
	dbmodel.RecordConsumeLogWithOtherAndRequestID(
		ctx,
		userId,
		channelId,
		0, // promptTokens
		0, // completionTokens
		originalModel,
		tokenName,
		0, // quota - 失败请求不消费
		logContent,
		0.0,   // duration
		"",    // title
		"",    // httpReferer
		false, // isStream
		0.0,   // firstWordLatency
		otherInfo,
		requestID,
	)

	logger.Infof(ctx, "Recorded Runway failed request log: userId=%d, model=%s, error=%s, channels=%v",
		userId, originalModel, errorMsg, channelHistory)
}

// formatRetryLog 格式化重试日志信息
func formatRetryLog(ctx context.Context, originalChannelId int, originalChannelName string, originalKeyIndex int,
	newChannelId int, newChannelName string, newKeyIndex int, model string, retryReason string,
	retryAttempt int, totalRetries int, isMultiKey bool, userId int, requestID string) string {

	// 构建基础重试信息
	retryInfo := fmt.Sprintf("Retry: 模型=%s, 原因=%s, 尝试=%d/%d",
		model, retryReason, retryAttempt, totalRetries)

	// 添加用户和请求信息
	userInfo := fmt.Sprintf(", 用户ID=%d", userId)
	if requestID != "" {
		userInfo += fmt.Sprintf(", 请求ID=%s", requestID)
	}

	// 添加渠道切换信息
	channelSwitch := fmt.Sprintf(", 渠道切换: #%d(%s) -> #%d(%s)",
		originalChannelId, originalChannelName, newChannelId, newChannelName)

	// 如果是多key渠道，添加key信息
	keyInfo := ""
	if isMultiKey {
		keyInfo = fmt.Sprintf(", Key切换: index %d -> %d", originalKeyIndex, newKeyIndex)
	}

	return retryInfo + userInfo + channelSwitch + keyInfo
}

// retryableErrorPatterns 定义可重试的错误模式（全局常量，避免重复创建）
var retryableErrorPatterns = []string{
	// API key相关错误
	"api key not valid", "invalid_api_key", "authentication_error",
	"api key not found", "invalid api key",
	// Billing限制错误
	"billing_hard_limit_reached", "billing hard limit has been reached",
	"billing limit has been reached", "hard limit has been reached",
	// AWS封号错误
	"operation not allowed",
}

func shouldRetry(c *gin.Context, statusCode int, message string) bool {
	// 如果指定了特定渠道，不允许重试
	if _, ok := c.Get("specific_channel_id"); ok {
		return false
	}

	// 2xx成功状态码不重试
	if statusCode/100 == 2 {
		return false
	}

	// 5xx服务器错误和429限流错误直接重试
	if statusCode/100 == 5 || statusCode == http.StatusTooManyRequests {
		return true
	}

	// 400错误需要根据具体错误内容判断
	if statusCode == http.StatusBadRequest {
		return shouldRetryBadRequest(c, message)
	}

	// 403错误需要根据具体错误内容判断
	if statusCode == http.StatusForbidden {
		return shouldRetryForbidden(c, message)
	}

	// 其他4xx错误（除400、403外）默认重试
	if statusCode/100 == 4 {
		return true
	}

	// 其他未知错误默认重试
	return true
}

// shouldRetryBadRequest 专门处理400错误的重试逻辑
func shouldRetryBadRequest(c *gin.Context, message string) bool {
	if message == "" {
		return false
	}

	// 转换为小写进行比较，提高匹配效率
	messageLower := strings.ToLower(message)

	// 检查x.ai的特殊情况（保持原有逻辑）
	if strings.Contains(message, "Incorrect API key provided") && strings.Contains(message, "console.x.ai") {
		logger.Warnf(c.Request.Context(), "X.AI API key error detected, will retry with other channels")
		return true
	}

	// 检查通用的可重试错误模式
	for _, errPattern := range retryableErrorPatterns {
		if strings.Contains(messageLower, errPattern) {
			logger.Warnf(c.Request.Context(), "Retryable error detected (%s), will retry with other channels", errPattern)
			return true
		}
	}

	return false
}

// shouldRetryForbidden 专门处理403错误的重试逻辑
func shouldRetryForbidden(c *gin.Context, message string) bool {
	if message == "" {
		// 没有错误消息时，默认重试
		return true
	}

	// 转换为小写进行比较
	messageLower := strings.ToLower(message)

	// 检查xai的内容违规错误（不应重试）
	contentViolationPatterns := []string{
		"content violates usage guidelines",
		"violates usage guidelines",
		"safety_check_type",
	}

	for _, pattern := range contentViolationPatterns {
		if strings.Contains(messageLower, pattern) {
			logger.Warnf(c.Request.Context(), "Content violation error detected (%s), will NOT retry", pattern)
			return false
		}
	}

	// 检查通用的可重试错误模式
	for _, errPattern := range retryableErrorPatterns {
		if strings.Contains(messageLower, errPattern) {
			logger.Warnf(c.Request.Context(), "Retryable 403 error detected (%s), will retry with other channels", errPattern)
			return true
		}
	}

	// 其他403错误默认重试（可能是认证或权限问题）
	return true
}

// isXAIContentViolation 检测是否是xAI的内容违规错误
func isXAIContentViolation(statusCode int, message string) bool {
	if statusCode != http.StatusForbidden {
		return false
	}

	messageLower := strings.ToLower(message)
	contentViolationPatterns := []string{
		"content violates usage guidelines",
		"violates usage guidelines",
		"safety_check_type",
	}

	for _, pattern := range contentViolationPatterns {
		if strings.Contains(messageLower, pattern) {
			return true
		}
	}

	return false
}

// recordXAIContentViolationCharge 记录xAI内容违规产生的费用（0.05美金）
func recordXAIContentViolationCharge(ctx context.Context, c *gin.Context, channelHistory []int) {
	userId := c.GetInt("id")
	channelId := c.GetInt("channel_id")
	originalModel := c.GetString("original_model")
	tokenName := c.GetString("token_name")
	requestID := c.GetHeader("X-Request-ID")

	// xAI对内容违规请求收费0.05美金
	// quota计算: 0.05 * 500000 = 25000
	quota := int64(25000)

	// 获取渠道历史信息
	var otherInfo string
	if len(channelHistory) > 0 {
		if channelHistoryBytes, err := json.Marshal(channelHistory); err == nil {
			otherInfo = fmt.Sprintf("adminInfo:%s", string(channelHistoryBytes))
		}
	}

	// 构建日志内容
	logContent := "xAI内容违规检查失败（已扣费$0.05）"
	if requestID != "" {
		logContent = fmt.Sprintf("xAI内容违规检查失败 [%s]（已扣费$0.05）", requestID)
	}

	// 记录消费日志
	dbmodel.RecordConsumeLogWithOtherAndRequestID(
		ctx,
		userId,
		channelId,
		0, // promptTokens
		0, // completionTokens
		originalModel,
		tokenName,
		quota, // 扣费25000 quota（对应0.05美金）
		logContent,
		0.0,   // duration
		"",    // title
		"",    // httpReferer
		false, // isStream
		0.0,   // firstWordLatency
		otherInfo,
		requestID,
	)

	// 更新用户和渠道quota
	err := dbmodel.PostConsumeTokenQuota(c.GetInt("token_id"), quota)
	if err != nil {
		logger.SysError("error consuming token remain quota: " + err.Error())
	}

	err = dbmodel.CacheUpdateUserQuota(ctx, userId)
	if err != nil {
		logger.SysError("error update user quota cache: " + err.Error())
	}

	dbmodel.UpdateUserUsedQuotaAndRequestCount(userId, quota)
	dbmodel.UpdateChannelUsedQuota(channelId, quota)

	logger.Infof(ctx, "Recorded xAI content violation charge: userId=%d, channelId=%d, model=%s, quota=%d ($0.05)",
		userId, channelId, originalModel, quota)
}

func processChannelRelayError(ctx context.Context, userId int, channelId int, channelName string, keyIndex int, err *model.ErrorWithStatusCode, modelName string) {
	logger.Errorf(ctx, "relay error (userId #%d,channel #%d): %s", userId, channelId, err.Error.Message)

	// 获取渠道信息
	channel, getErr := dbmodel.GetChannelById(channelId, true)
	if getErr != nil {
		logger.Errorf(ctx, "failed to get channel %d: %s", channelId, getErr.Error())
		monitor.Emit(channelId, false)
		return
	}

	// 处理多Key渠道的错误
	if channel.MultiKeyInfo.IsMultiKey {
		processMultiKeyChannelError(ctx, channel, keyIndex, err, modelName)
	} else {
		// 单Key渠道的原有逻辑（不使用keyIndex参数）
		// 添加保护检查：如果KeyCount > 1但IsMultiKey=false，可能存在逻辑错误
		if channel.MultiKeyInfo.KeyCount > 1 {
			logger.SysLog(fmt.Sprintf("WARNING: Channel %d has KeyCount=%d but IsMultiKey=false, this may indicate a logic error. Using multi-key logic instead.",
				channelId, channel.MultiKeyInfo.KeyCount))
			processMultiKeyChannelError(ctx, channel, keyIndex, err, modelName)
			return
		}

		if util.ShouldDisableChannel(&err.Error, err.StatusCode) {
			if channel.AutoDisabled {
				monitor.DisableChannelWithStatusCode(channelId, channelName, err.Error.Message, modelName, err.StatusCode)
			} else {
				logger.Infof(ctx, "channel #%d (%s) should be disabled but auto-disable is turned off", channelId, channelName)
				monitor.Emit(channelId, false)
			}
		} else {
			monitor.Emit(channelId, false)
		}
	}
}

// processMultiKeyChannelError 处理多Key渠道的错误
func processMultiKeyChannelError(ctx context.Context, channel *dbmodel.Channel, keyIndex int, err *model.ErrorWithStatusCode, modelName string) {
	// 直接使用传入的keyIndex，不再从context中获取

	var ginCtx *gin.Context
	// 尝试从context中获取gin.Context（用于添加排除列表）
	if ginCtxValue := ctx.Value(gin.ContextKey); ginCtxValue != nil {
		if gc, ok := ginCtxValue.(*gin.Context); ok {
			ginCtx = gc
		}
	}

	// 处理特定Key的错误
	if util.ShouldDisableChannel(&err.Error, err.StatusCode) {
		keyErr := channel.HandleKeyError(keyIndex, err.Error.Message, err.StatusCode, modelName)
		if keyErr != nil {
			logger.Errorf(ctx, "failed to handle key error for channel %d, key %d: %s",
				channel.Id, keyIndex, keyErr.Error())
		}

		// 如果有gin.Context，将失败的Key索引添加到排除列表中，以便重试时跳过
		if ginCtx != nil {
			addExcludedKeyIndexToContext(ginCtx, keyIndex)
		}
	}

	// 发送监控事件
	monitor.Emit(channel.Id, false)
}

// addExcludedKeyIndexToContext 添加一个需要排除的Key索引到gin.Context中
func addExcludedKeyIndexToContext(c *gin.Context, keyIndex int) {
	var excludedKeys []int
	if excludedKeysInterface, exists := c.Get("excluded_key_indices"); exists {
		if excludedKeysSlice, ok := excludedKeysInterface.([]int); ok {
			excludedKeys = excludedKeysSlice
		}
	}

	// 检查是否已经存在
	for _, existingIndex := range excludedKeys {
		if existingIndex == keyIndex {
			return
		}
	}

	// 添加新的索引
	excludedKeys = append(excludedKeys, keyIndex)
	c.Set("excluded_key_indices", excludedKeys)
}

// getExcludedKeyIndicesFromContext 获取排除的Key索引列表
func getExcludedKeyIndicesFromContext(c *gin.Context) []int {
	if excludedKeysInterface, exists := c.Get("excluded_key_indices"); exists {
		if excludedKeys, ok := excludedKeysInterface.([]int); ok {
			return excludedKeys
		}
	}
	return []int{}
}

func relayMidjourney(c *gin.Context, relayMode int) *midjourney.MidjourneyResponseWithStatusCode {
	var err *midjourney.MidjourneyResponseWithStatusCode
	switch relayMode {
	case relayconstant.RelayModeMidjourneyNotify:
		err = controller.RelayMidjourneyNotify(c)
	case relayconstant.RelayModeMidjourneyTaskFetch, relayconstant.RelayModeMidjourneyTaskFetchByCondition:
		err = controller.RelayMidjourneyTask(c, relayMode)
	case relayconstant.RelayModeMidjourneyTaskImageSeed:
		err = controller.RelayMidjourneyTaskImageSeed(c)
	case relayconstant.RelayModeSwapFace:
		err = controller.RelaySwapFace(c)
	default:
		err = controller.RelayMidjourneySubmit(c, relayMode)
	}
	return err
}

func RelayMidjourney(c *gin.Context) {
	ctx := c.Request.Context()
	relayMode := c.GetInt("relay_mode")

	var MjErr *midjourney.MidjourneyResponseWithStatusCode
	MjErr = relayMidjourney(c, relayMode)
	retryTimes := config.RetryTimes
	if MjErr == nil {
		// 第一次成功，记录使用的渠道到上下文中
		var channelHistory []int
		channelId := c.GetInt("channel_id")
		if channelId > 0 {
			channelHistory = append(channelHistory, channelId)
			c.Set("admin_channel_history", channelHistory)
		}
		return
	}
	// channelId := c.GetInt("channel_id")
	// channelName := c.GetString("channel_name")
	group := c.GetString("group")
	originalModel := c.GetString("original_model")

	// if originalModel != "" {
	// 	ShouldDisabelMidjourneyChannel(channelId, channelName, MjErr)
	// }
	if !MidjourneyShouldRetryByCode(MjErr) { //返回false就不执行重试
		retryTimes = 0
		logger.SysLog("no retry!!!")
	}

	// 记录使用的渠道历史，用于添加到日志中
	var channelHistory []int
	// 添加初始失败的渠道
	channelId := c.GetInt("channel_id")
	if channelId > 0 {
		channelHistory = append(channelHistory, channelId)
	}

	for i := retryTimes; i > 0; i-- {
		if originalModel != "" {
			// 计算应该跳过的优先级数量：第1次重试仍使用最高优先级
			skipPriorityLevels := retryTimes - i
			channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, originalModel, skipPriorityLevels)
			if err != nil {
				logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed: %+v", err)
				break
			}
			logger.Infof(ctx, "Using channel #%d to retry (remain times %d)", channel.Id, i)

			// 记录重试使用的渠道
			channelHistory = append(channelHistory, channel.Id)

			middleware.SetupContextForSelectedChannel(c, channel, originalModel)
			requestBody, err := common.GetRequestBody(c)
			if err != nil {
				return
			}
			c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
			MjErr := relayMidjourney(c, relayMode)
			if MjErr == nil {
				// 成功时记录渠道历史到上下文中
				c.Set("admin_channel_history", channelHistory)
				return
			}
			// ShouldDisabelMidjourneyChannel(channelId, channelName, MjErr)
		} else {
			requestBody, err := common.GetRequestBody(c)
			if err == nil {
				return
			}
			c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
			MjErr = relayMidjourney(c, relayMode)
			if MjErr == nil {
				return
			}
			logger.SysLog(fmt.Sprintf("relayMode:%+v;retry:%d\n", relayMode, i))
		}
	}
	if MjErr != nil {
		// 失败时记录渠道历史到上下文中
		c.Set("admin_channel_history", channelHistory)

		// 记录Midjourney失败请求的日志
		recordMidjourneyFailedLog(ctx, c, MjErr, channelHistory)

		statusCode := http.StatusBadRequest
		if MjErr.Response.Code == 30 {
			MjErr.Response.Result = "The current group load is saturated, please try again later, or upgrade your account to improve service quality."
			statusCode = http.StatusTooManyRequests
		}
		c.JSON(statusCode, gin.H{
			"description": util.ProcessString(fmt.Sprintf("%s %s", MjErr.Response.Description, MjErr.Response.Result)),
			"type":        "upstream_error",
			"code":        MjErr.Response.Code,
		})
		channelId := c.GetInt("channel_id")
		logger.SysError(fmt.Sprintf("relay error (channel #%d): %s", channelId, fmt.Sprintf("%s %s", MjErr.Response.Description, MjErr.Response.Result)))
	}
}

func MidjourneyShouldRetryByCode(MjErr *midjourney.MidjourneyResponseWithStatusCode) bool {
	// if MjErr.Response.Code == 23 { //当前渠道已满
	// 	return true
	// }
	// if MjErr.Response.Code == 24 {
	// 	return false
	// }
	// if MjErr.Response.Code != 1 && MjErr.Response.Code != 21 && MjErr.Response.Code != 22 && MjErr.Response.Code != 4 {
	// 	return true
	// }

	return false
}

// func ShouldDisabelMidjourneyChannel(channelId int, channelName string, MjErr *midjourney.MidjourneyResponseWithStatusCode) {
// 	if MjErr.Response.Code == 3 {
// 		monitor.DisableChannel(channelId, channelName, MjErr.Response.Description)
// 	}

// 	if MjErr.StatusCode == 403 || MjErr.StatusCode == 401 || MjErr.StatusCode == 404 {
// 		monitor.DisableChannel(channelId, channelName, MjErr.Response.Description)
// 	}

// }

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

func relaySd(c *gin.Context, relayMode int) *model.ErrorWithStatusCode {
	var err *model.ErrorWithStatusCode
	if relayMode == relayconstant.RelayModeUpscaleCreativeResult || relayMode == relayconstant.RelayModeVideoResult {
		err = controller.GetUpscaleResults(c)
	} else {
		err = controller.RelaySdGenerate(c, relayMode)
	}
	return err
}

func RelaySd(c *gin.Context) {
	ctx := c.Request.Context()
	relayMode := c.GetInt("relay_mode")
	channelId := c.GetInt("channel_id")
	userId := c.GetInt("id")

	SdErr := relaySd(c, relayMode)
	if SdErr == nil {
		// 第一次成功，记录使用的渠道到上下文中
		var channelHistory []int
		channelHistory = append(channelHistory, channelId)
		c.Set("admin_channel_history", channelHistory)
		return
	}

	group := c.GetString("group")
	originalModel := c.GetString("original_model")
	retryTimes := config.RetryTimes
	if !shouldRetry(c, SdErr.StatusCode, SdErr.Error.Message) {
		logger.Errorf(ctx, "Relay error happen, status code is %d, won't retry in this case", SdErr.StatusCode)
		retryTimes = 0
	}

	// 获取初始渠道信息用于重试日志
	originalChannelId := channelId
	originalChannelName := c.GetString("channel_name")
	originalKeyIndex := c.GetInt("key_index")
	requestID := c.GetHeader("X-Request-ID")

	// 记录使用的渠道历史，用于添加到日志中
	var channelHistory []int
	// 添加初始失败的渠道
	channelHistory = append(channelHistory, originalChannelId)

	for i := retryTimes; i > 0; i-- {
		// 计算应该跳过的优先级数量：第1次重试仍使用最高优先级
		skipPriorityLevels := retryTimes - i
		channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, originalModel, skipPriorityLevels)
		if err != nil {
			logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed: %v", err)
			break
		}

		// 获取重试原因 - 直接使用原始错误消息
		retryReason := SdErr.Error.Message

		// 获取新渠道的key信息
		newKeyIndex := 0
		isMultiKey := false
		if channel.MultiKeyInfo.IsMultiKey {
			isMultiKey = true
			// 获取下一个可用key的索引
			_, newKeyIndex, _ = channel.GetNextAvailableKey()
		}

		// 生成详细的重试日志
		retryLog := formatRetryLog(ctx, originalChannelId, originalChannelName, originalKeyIndex,
			channel.Id, channel.Name, newKeyIndex, originalModel, retryReason,
			retryTimes-i+1, retryTimes, isMultiKey, userId, requestID)

		logger.Infof(ctx, retryLog)

		// 记录重试使用的渠道
		channelHistory = append(channelHistory, channel.Id)

		middleware.SetupContextForSelectedChannel(c, channel, originalModel)
		requestBody, err := common.GetRequestBody(c)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		SdErr = relaySd(c, relayMode)
		if SdErr == nil {
			// 成功时记录渠道历史到上下文中
			c.Set("admin_channel_history", channelHistory)
			return
		}

		channelId = c.GetInt("channel_id")
		keyIndex := c.GetInt("key_index")

		channelName := c.GetString("channel_name")
		go processChannelRelayError(ctx, userId, channelId, channelName, keyIndex, SdErr, originalModel)
	}
	if SdErr != nil {
		// 失败时记录渠道历史到上下文中
		c.Set("admin_channel_history", channelHistory)

		// 记录SD失败请求的日志
		recordFailedRequestLog(ctx, c, SdErr, channelHistory)

		c.JSON(http.StatusBadRequest, gin.H{
			"error": SdErr.Code,
		})
	}

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

func RelayVideoGenerate(c *gin.Context) {
	ctx := c.Request.Context()
	requestID := c.GetHeader("X-Request-ID")
	c.Set("X-Request-ID", requestID)

	channelId := c.GetInt("channel_id")
	userId := c.GetInt("id")
	modelName := c.GetString("original_model")

	bizErr := controller.DoVideoRequest(c, modelName)

	if bizErr == nil {
		// 第一次成功，记录使用的渠道到上下文中
		var channelHistory []int
		channelHistory = append(channelHistory, channelId)
		c.Set("admin_channel_history", channelHistory)
		return
	}

	channelName := c.GetString("channel_name")
	group := c.GetString("group")
	keyIndex := c.GetInt("key_index")

	go processChannelRelayError(ctx, userId, channelId, channelName, keyIndex, bizErr, modelName)

	retryTimes := config.RetryTimes
	if !shouldRetry(c, bizErr.StatusCode, bizErr.Error.Message) {
		logger.Errorf(ctx, "Video generation error happen, status code is %d, won't retry in this case", bizErr.StatusCode)
		retryTimes = 0
	}

	// 获取初始渠道信息用于重试日志
	originalChannelId := channelId
	originalChannelName := channelName
	originalKeyIndex := keyIndex

	// 记录使用的渠道历史，用于添加到日志中
	var channelHistory []int
	// 添加初始失败的渠道
	channelHistory = append(channelHistory, originalChannelId)

	for i := retryTimes; i > 0; i-- {
		// 计算应该跳过的优先级数量：第1次重试仍使用最高优先级
		skipPriorityLevels := retryTimes - i
		channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, modelName, skipPriorityLevels)
		if err != nil {
			logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed: %v", err)
			break
		}

		// 获取重试原因 - 直接使用原始错误消息
		retryReason := bizErr.Error.Message

		// 获取新渠道的key信息
		newKeyIndex := 0
		isMultiKey := false
		if channel.MultiKeyInfo.IsMultiKey {
			isMultiKey = true
			// 获取下一个可用key的索引
			_, newKeyIndex, _ = channel.GetNextAvailableKey()
		}

		// 生成详细的重试日志
		retryLog := formatRetryLog(ctx, originalChannelId, originalChannelName, originalKeyIndex,
			channel.Id, channel.Name, newKeyIndex, modelName, retryReason,
			retryTimes-i+1, retryTimes, isMultiKey, userId, requestID)

		logger.Infof(ctx, retryLog)

		// 记录重试使用的渠道
		channelHistory = append(channelHistory, channel.Id)

		// 使用新通道的配置更新上下文
		middleware.SetupContextForSelectedChannel(c, channel, modelName)
		requestBody, err := common.GetRequestBody(c)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))

		bizErr = controller.DoVideoRequest(c, modelName)
		if bizErr == nil {
			// 成功时记录渠道历史到上下文中
			c.Set("admin_channel_history", channelHistory)
			return
		}

		// 记录失败渠道
		channelId = c.GetInt("channel_id")

		channelName = c.GetString("channel_name")
		keyIndex := c.GetInt("key_index")
		go processChannelRelayError(ctx, userId, channelId, channelName, keyIndex, bizErr, modelName)
	}

	// 所有重试都失败后的处理
	if bizErr != nil {
		// 失败时记录渠道历史到上下文中
		c.Set("admin_channel_history", channelHistory)

		// 记录视频生成失败请求的日志
		recordFailedRequestLog(ctx, c, bizErr, channelHistory)

		if bizErr.StatusCode == http.StatusTooManyRequests {
			bizErr.Error.Message = "The current group upstream load is saturated, please try again later."
		}
		c.JSON(bizErr.StatusCode, gin.H{
			"error": gin.H{
				"message": util.ProcessString(bizErr.Error.Message),
				"type":    bizErr.Error.Type,
				"param":   bizErr.Error.Param,
				"code":    bizErr.Error.Code,
			},
		})
	}
}

func RelayVideoResult(c *gin.Context) {
	taskId := c.Query("taskid")
	responseFormat := c.Query("response_format")
	c.Set("response_format", responseFormat)
	bizErr := controller.GetVideoResult(c, taskId)
	if bizErr != nil {
		if bizErr.StatusCode == http.StatusTooManyRequests {
			bizErr.Error.Message = "The current group upstream load is saturated, please try again later."
		}
		c.JSON(bizErr.StatusCode, gin.H{
			"error": util.ProcessString(bizErr.Error.Message),
		})
	}
}

// RelayVideoResultById 通过Query String参数获取视频生成结果
// 完全匹配豆包原始API格式 /api/v3/contents/generations/tasks?id=task_id
func RelayDouBaoVideoResultById(c *gin.Context) {
	taskId := c.Param("taskid")
	logger.SysLog(fmt.Sprintf("doubao-task-id: %s", taskId))
	if taskId == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": "Task ID is required in query parameter 'id'",
				"type":    "invalid_request_error",
				"code":    "missing_required_parameter",
			},
		})
		return
	}
	bizErr := controller.GetVideoResult(c, taskId)
	if bizErr != nil {
		if bizErr.StatusCode == http.StatusTooManyRequests {
			bizErr.Error.Message = "The current group upstream load is saturated, please try again later."
		}
		c.JSON(bizErr.StatusCode, gin.H{
			"error": util.ProcessString(bizErr.Error.Message),
		})
	}
}

func RelayDirectFlux(c *gin.Context) {
	ctx := c.Request.Context()
	requestID := c.GetHeader("X-Request-ID")

	// Get the full path
	fullPath := c.Request.URL.Path
	logger.Debugf(ctx, "[%s] RelayDirectFlux called with path: %s, method: %s", requestID, fullPath, c.Request.Method)

	// Extract the model name (last part of the path)
	// For a path like "/v1/flux-pro-1.1", this will extract "flux-pro-1.1"
	pathParts := strings.Split(fullPath, "/")
	modelName := pathParts[len(pathParts)-1]
	logger.Debugf(ctx, "[%s] Extracted model name: %s", requestID, modelName)

	// You can now use modelName ("flux-pro-1.1") for further processing
	// For example, you might want to set it in the context for use downstream
	c.Set("model_name", modelName)

	userId := c.GetInt("id")
	logger.Debugf(ctx, "[%s] User ID: %d", requestID, userId)

	userGroup, err := dbmodel.CacheGetUserGroup(userId)
	if err != nil {
		logger.Errorf(ctx, "[%s] Failed to get user group: %v", requestID, err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": helper.MessageWithRequestId("Failed to get user group", requestID),
				"type":    "api_error",
			},
		})
		return
	}
	logger.Debugf(ctx, "[%s] User group: %s", requestID, userGroup)
	c.Set("group", userGroup)

	var fullRequestUrl string

	logger.Debugf(ctx, "[%s] Looking for channel with model: %s, group: %s", requestID, modelName, userGroup)
	channel, err := dbmodel.CacheGetRandomSatisfiedChannel(userGroup, modelName, 0)
	if err != nil {
		message := fmt.Sprintf("There are no channels available for model %s under the current group %s", modelName, userGroup)
		if channel != nil {
			logger.SysError(fmt.Sprintf("Channel does not exist：%d", channel.Id))
			message = "Database consistency has been violated, please contact the administrator"
		}
		logger.Errorf(ctx, "[%s] %s: %v", requestID, message, err)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{
				"message": helper.MessageWithRequestId(message, requestID),
				"type":    "api_error",
			},
		})
		c.Abort()
		return
	}
	logger.Debugf(ctx, "[%s] Found channel ID: %d, type: %d, base URL: %s", requestID, channel.Id, channel.Type, *channel.BaseURL)
	middleware.SetupContextForSelectedChannel(c, channel, modelName)

	if channel.Type == 46 {
		fullRequestUrl = *channel.BaseURL + fullPath
	} else {
		fullRequestUrl = *channel.BaseURL + "/flux" + fullPath
	}
	logger.Debugf(ctx, "[%s] Full request URL: %s", requestID, fullRequestUrl)

	// Read and log request body for debugging
	requestBodyBytes, err := common.GetRequestBody(c)
	if err != nil {
		logger.Errorf(ctx, "[%s] Failed to read request body: %v", requestID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read request body"})
		return
	}
	if len(requestBodyBytes) > 0 {
		// Only log a portion of the body if it's large
		bodyPreview := string(requestBodyBytes)
		if len(bodyPreview) > 200 {
			bodyPreview = bodyPreview[:200] + "... (truncated)"
		}
		logger.Debugf(ctx, "[%s] Request body: %s", requestID, bodyPreview)
	} else {
		logger.Debugf(ctx, "[%s] Request body is empty", requestID)
	}

	// Create the request to forward
	request, err := http.NewRequest(c.Request.Method, fullRequestUrl, bytes.NewBuffer(requestBodyBytes))
	if err != nil {
		logger.Errorf(ctx, "[%s] Failed to create request: %v", requestID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request"})
		return
	}

	// Get Content-Type from original request or use default
	contentType := c.Request.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	request.Header.Set("Content-Type", contentType)

	// Set authorization header
	request.Header.Set("Authorization", "Bearer "+channel.Key)
	logger.Debugf(ctx, "[%s] Request headers set, sending request", requestID)

	client := &http.Client{
		Timeout: 60 * time.Second,
	}
	response, err := client.Do(request)
	if err != nil {
		logger.Errorf(ctx, "[%s] Failed to send request: %v", requestID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to send request to provider"})
		return
	}
	defer response.Body.Close()
	logger.Debugf(ctx, "[%s] Received response with status code: %d", requestID, response.StatusCode)

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		logger.Errorf(ctx, "[%s] Failed to read provider response: %v", requestID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read provider response"})
		return
	}

	// Log response body preview
	responsePreview := string(responseBody)
	if len(responsePreview) > 200 {
		responsePreview = responsePreview[:200] + "... (truncated)"
	}
	logger.Debugf(ctx, "[%s] Response body: %s", requestID, responsePreview)

	// 处理不同状态码的响应
	if response.StatusCode != 200 {
		// 如果是422错误，直接返回原始错误信息
		var errorResponse map[string]interface{}
		if err := json.Unmarshal(responseBody, &errorResponse); err != nil {
			logger.Errorf(ctx, "[%s] Failed to parse error response: %v", requestID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse error response"})
			return
		}
		logger.Debugf(ctx, "[%s] Returning error response with status code: %d", requestID, response.StatusCode)
		c.JSON(response.StatusCode, errorResponse)
		return
	} else if response.StatusCode == 200 {
		// 如果是200成功，解析JSON并获取polling_url
		var successResponse map[string]interface{}
		if err := json.Unmarshal(responseBody, &successResponse); err != nil {
			logger.Errorf(ctx, "[%s] Failed to parse success response: %v", requestID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse success response"})
			return
		}

		// 获取polling_url并发起第二次请求
		pollingURL, ok := successResponse["polling_url"].(string)
		if !ok {
			logger.Errorf(ctx, "[%s] polling_url not found or not a string in response: %v", requestID, successResponse)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid response format, polling_url not found"})
			return
		}
		logger.Debugf(ctx, "[%s] Found polling URL: %s", requestID, pollingURL)

		// 创建并发送polling请求
		pollingRequest, err := http.NewRequest("GET", pollingURL, nil)
		if err != nil {
			logger.Errorf(ctx, "[%s] Failed to create polling request: %v", requestID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create polling request"})
			return
		}

		// 设置polling请求的头信息
		pollingRequest.Header.Set("x-key", channel.Key)
		logger.Debugf(ctx, "[%s] Sending polling request", requestID)

		// 执行polling请求
		pollingResponse, err := client.Do(pollingRequest)
		if err != nil {
			logger.Errorf(ctx, "[%s] Failed to execute polling request: %v", requestID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to execute polling request"})
			return
		}
		defer pollingResponse.Body.Close()
		logger.Debugf(ctx, "[%s] Received polling response with status code: %d", requestID, pollingResponse.StatusCode)

		// 读取并返回polling响应
		pollingBody, err := io.ReadAll(pollingResponse.Body)
		if err != nil {
			logger.Errorf(ctx, "[%s] Failed to read polling response: %v", requestID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read polling response"})
			return
		}

		// Log polling response preview
		pollingPreview := string(pollingBody)
		if len(pollingPreview) > 200 {
			pollingPreview = pollingPreview[:200] + "... (truncated)"
		}
		logger.Debugf(ctx, "[%s] Polling response body: %s", requestID, pollingPreview)

		logger.Debugf(ctx, "[%s] Returning polling response to client", requestID)
		c.Writer.WriteHeader(pollingResponse.StatusCode)
		c.Writer.Write(pollingBody)
	} else {
		// 处理其他状态码
		logger.Debugf(ctx, "[%s] Returning original response with status code: %d", requestID, response.StatusCode)
		c.Writer.WriteHeader(response.StatusCode)
		c.Writer.Write(responseBody)
	}
	logger.Debugf(ctx, "[%s] RelayDirectFlux completed", requestID)
}

func RelayOcr(c *gin.Context) {
	ctx := c.Request.Context()
	channelId := c.GetInt("channel_id")
	channel, err := dbmodel.GetChannelById(channelId, true)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("Failed to get channel: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get channel"})
		return
	}

	userId := c.GetInt("id")
	startTime := time.Now()

	fullRequestUrl := "https://api.mistral.ai/v1/ocr"
	requestBody, err := common.GetRequestBody(c)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("Failed to read request body: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read request body"})
		return
	}

	// Create the request to forward to Mistral API
	request, err := http.NewRequest(c.Request.Method, fullRequestUrl, bytes.NewBuffer(requestBody))
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("Failed to create request: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request"})
		return
	}

	// Set necessary headers
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+channel.Key)

	// Send the request
	client := &http.Client{}
	response, err := client.Do(request)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("Failed to send request: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to send request to provider"})
		return
	}
	defer response.Body.Close()

	// Read response body
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("Failed to read provider response: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read provider response"})
		return
	}

	// Calculate duration
	duration := float64(time.Since(startTime).Milliseconds()) / 1000.0

	// Process the response based on status code
	if response.StatusCode == http.StatusOK {
		// For successful responses, extract usage info
		var ocrResponse map[string]interface{}
		if err := json.Unmarshal(responseBody, &ocrResponse); err != nil {
			logger.Error(ctx, fmt.Sprintf("Failed to parse OCR response: %v", err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse OCR response"})
			return
		}

		// Extract usage info if available
		if usageInfo, ok := ocrResponse["usage_info"].(map[string]interface{}); ok {
			pagesProcessed := 0
			docSizeBytes := 0

			if pp, ok := usageInfo["pages_processed"].(float64); ok {
				pagesProcessed = int(pp)
			}
			if dsb, ok := usageInfo["doc_size_bytes"].(float64); ok {
				docSizeBytes = int(dsb)
			}

			// Log usage info
			logger.Infof(ctx, "OCR usage info - channel #%d: pages processed: %d, doc size: %d bytes",
				channelId, pagesProcessed, docSizeBytes)

			// Define model name and token name
			modelName := "mistral-ocr-latest"
			tokenName := c.GetString("token_name")

			// Calculate quota: pages_processed * 1/1000 * 500000
			quota := float64(pagesProcessed) * 0.001 * 500000

			// Record consumption log with all required parameters
			logContent := fmt.Sprintf("OCR pages processed: %d, doc size: %d bytes", pagesProcessed, docSizeBytes)
			title := ""
			httpReferer := ""

			dbmodel.RecordConsumeLog(ctx, userId, channelId, pagesProcessed, docSizeBytes, modelName, tokenName, int64(quota), logContent, duration, title, httpReferer, false, 0.0)

			// Update user and channel quota
			err := dbmodel.PostConsumeTokenQuota(c.GetInt("token_id"), int64(quota))
			if err != nil {
				logger.SysError("error consuming token remain quota: " + err.Error())
			}

			err = dbmodel.CacheUpdateUserQuota(ctx, userId)
			if err != nil {
				logger.SysError("error update user quota cache: " + err.Error())
			}

			dbmodel.UpdateUserUsedQuotaAndRequestCount(userId, int64(quota))
			dbmodel.UpdateChannelUsedQuota(channelId, int64(quota))
		}

		// Return the original response to the client
		c.Data(http.StatusOK, "application/json", responseBody)
	} else {
		// For error responses, just forward the error
		c.Data(response.StatusCode, "application/json", responseBody)
	}
}

func RelayRecraft(c *gin.Context) {
	ctx := c.Request.Context()
	requestID := c.GetHeader("X-Request-ID")
	c.Set("X-Request-ID", requestID)

	channelId := c.GetInt("channel_id")
	userId := c.GetInt("id")
	modelName := c.GetString("model")

	logger.Infof(ctx, "RelayRecraft START: channelId=%d, userId=%d, modelName=%s",
		channelId, userId, modelName)

	// 为第一次调用设置渠道历史
	var firstChannelHistory []int
	firstChannelHistory = append(firstChannelHistory, channelId)
	c.Set("admin_channel_history", firstChannelHistory)
	logger.Infof(ctx, "RelayRecraft: Setting admin_channel_history for FIRST call: %v", firstChannelHistory)

	// 尝试第一次请求
	bizErr := relayRecraftHelper(c)
	if bizErr == nil {
		logger.Infof(ctx, "RelayRecraft: First call SUCCESS with channelHistory: %v", firstChannelHistory)
		return
	}

	// 第一次失败，开始重试逻辑
	channelName := c.GetString("channel_name")
	group := c.GetString("group")
	keyIndex := c.GetInt("key_index")
	go processChannelRelayError(ctx, userId, channelId, channelName, keyIndex, bizErr, modelName)

	retryTimes := config.RetryTimes
	if !shouldRetry(c, bizErr.StatusCode, bizErr.Error.Message) {
		logger.Errorf(ctx, "Recraft relay error happen, status code is %d, won't retry in this case", bizErr.StatusCode)
		retryTimes = 0
	}

	// 记录使用的渠道历史，用于添加到日志中
	var channelHistory []int
	// 添加初始失败的渠道
	channelHistory = append(channelHistory, channelId)

	for i := retryTimes; i > 0; i-- {
		// 计算应该跳过的优先级数量：第1次重试仍使用最高优先级
		skipPriorityLevels := retryTimes - i
		channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, modelName, skipPriorityLevels)
		if err != nil {
			logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed: %v", err)
			break
		}
		if channel.Id == channelId {
			continue
		}
		logger.Infof(ctx, "Recraft retry: 模型=%s, 尝试=%d/%d, 用户ID=%d, 渠道切换: #%d(%s) -> #%d(%s)",
			modelName, retryTimes-i+1, retryTimes, userId, channelId, channelName, channel.Id, channel.Name)

		middleware.SetupContextForSelectedChannel(c, channel, modelName)
		channelHistory = append(channelHistory, channel.Id)

		// 重新构建请求体
		requestBody, err := common.GetRequestBody(c)
		if err == nil {
			c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		}

		// 在调用relayRecraftHelper之前设置渠道历史
		c.Set("admin_channel_history", channelHistory)
		logger.Infof(ctx, "RelayRecraft: Setting admin_channel_history before retry: %v", channelHistory)

		bizErr = relayRecraftHelper(c)
		if bizErr == nil {
			logger.Infof(ctx, "RelayRecraft: Retry SUCCESS with channelHistory: %v", channelHistory)
			return
		}

		channelId = channel.Id
		channelName = channel.Name
		keyIndex = c.GetInt("key_index")
		go processChannelRelayError(ctx, userId, channelId, channelName, keyIndex, bizErr, modelName)
	}

	// 所有重试都失败后的处理
	c.Set("admin_channel_history", channelHistory)

	// 记录Recraft失败请求的日志
	recordFailedRequestLog(ctx, c, bizErr, channelHistory)

	if bizErr.StatusCode == http.StatusTooManyRequests {
		bizErr.Error.Message = "The current group upstream load is saturated, please try again later."
	}
	c.JSON(bizErr.StatusCode, gin.H{
		"error": gin.H{
			"message": util.ProcessString(bizErr.Error.Message),
			"type":    bizErr.Error.Type,
			"param":   bizErr.Error.Param,
			"code":    bizErr.Error.Code,
		},
	})
}

func relayRecraftHelper(c *gin.Context) *model.ErrorWithStatusCode {
	ctx := c.Request.Context()
	requestID := c.GetHeader("X-Request-ID")

	channelId := c.GetInt("channel_id")
	userId := c.GetInt("id")
	startTime := time.Now()
	modelName := c.GetString("model")

	channel, err := dbmodel.GetChannelById(channelId, true)
	if err != nil {
		logger.Errorf(ctx, "[%s] Failed to get channel: %v", requestID, err)
		return &model.ErrorWithStatusCode{
			StatusCode: http.StatusInternalServerError,
			Error:      model.Error{Message: "Failed to get channel"},
		}
	}

	fullPath := c.Request.URL.Path
	var fullRequestUrl string
	if channel.Type == 43 {
		fullRequestUrl = *channel.BaseURL + fullPath
	} else {
		fullRequestUrl = *channel.BaseURL + "/recraft" + fullPath
	}
	logger.Debugf(ctx, "[%s] Full request URL: %s", requestID, fullRequestUrl)

	// Read request body
	requestBodyBytes, err := common.GetRequestBody(c)
	if err != nil {
		logger.Errorf(ctx, "[%s] Failed to read request body: %v", requestID, err)
		return &model.ErrorWithStatusCode{
			StatusCode: http.StatusInternalServerError,
			Error:      model.Error{Message: "Failed to read request body"},
		}
	}

	// Parse request to check for normalizeResponseFormat parameter
	var requestData map[string]interface{}
	normalizeFormat := false
	if len(requestBodyBytes) > 0 {
		if err := json.Unmarshal(requestBodyBytes, &requestData); err == nil {
			// Check if normalizeResponseFormat is present and true
			if format, ok := requestData["normalizeResponseFormat"].(bool); ok && format {
				normalizeFormat = true
				logger.Debugf(ctx, "[%s] Response format will be normalized to OpenAI format", requestID)

				// Remove the parameter before forwarding to Recraft
				delete(requestData, "normalizeResponseFormat")

				// Re-encode the modified request
				modifiedRequestBytes, err := json.Marshal(requestData)
				if err == nil {
					requestBodyBytes = modifiedRequestBytes
				} else {
					logger.Warnf(ctx, "[%s] Failed to re-encode modified request: %v", requestID, err)
				}
			} else {
				logger.Debugf(ctx, "[%s] Using native Recraft response format", requestID)
			}
		}
	}

	// Create the request to forward
	request, err := http.NewRequest(c.Request.Method, fullRequestUrl, bytes.NewBuffer(requestBodyBytes))
	if err != nil {
		logger.Errorf(ctx, "[%s] Failed to create request: %v", requestID, err)
		return &model.ErrorWithStatusCode{
			StatusCode: http.StatusInternalServerError,
			Error:      model.Error{Message: "Failed to create request"},
		}
	}

	// Get Content-Type from original request or use default
	contentType := c.Request.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	request.Header.Set("Content-Type", contentType)

	// Set authorization header
	request.Header.Set("Authorization", "Bearer "+channel.Key)

	// Send the request
	client := &http.Client{
		Timeout: 120 * time.Second,
	}
	response, err := client.Do(request)
	if err != nil {
		logger.Errorf(ctx, "[%s] Failed to send request: %v", requestID, err)
		return &model.ErrorWithStatusCode{
			StatusCode: http.StatusInternalServerError,
			Error:      model.Error{Message: "Failed to send request to provider"},
		}
	}
	defer response.Body.Close()

	// Read response body
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		logger.Errorf(ctx, "[%s] Failed to read provider response: %v", requestID, err)
		return &model.ErrorWithStatusCode{
			StatusCode: http.StatusInternalServerError,
			Error:      model.Error{Message: "Failed to read provider response"},
		}
	}

	// Calculate duration
	duration := float64(time.Since(startTime).Milliseconds()) / 1000.0

	// Process the response based on status code
	if response.StatusCode == http.StatusOK {
		// For successful responses, deduct quota

		// Define default quota based on endpoint
		var modelPrice float64
		defaultPrice, ok := common.DefaultModelPrice[modelName]
		if !ok {
			modelPrice = 0.1
		} else {
			modelPrice = defaultPrice
		}
		quota := int64(modelPrice * 500000)

		// Record consumption log
		tokenName := c.GetString("token_name")
		xRequestID := c.GetString("X-Request-ID")
		logContent := fmt.Sprintf("Recraft API call: %s", modelName)
		title := ""
		httpReferer := ""

		// Use placeholder values for input/output tokens since we don't have actual token counts
		inputTokens := 0
		outputTokens := 0

		// 获取渠道历史信息并记录日志
		var otherInfo string
		if channelHistoryInterface, exists := c.Get("admin_channel_history"); exists {
			if channelHistory, ok := channelHistoryInterface.([]int); ok && len(channelHistory) > 0 {
				if channelHistoryBytes, err := json.Marshal(channelHistory); err == nil {
					otherInfo = fmt.Sprintf("adminInfo:%s", string(channelHistoryBytes))
				}
			}
		}

		if otherInfo != "" {
			dbmodel.RecordConsumeLogWithOtherAndRequestID(ctx, userId, channelId, inputTokens, outputTokens,
				modelName, tokenName, quota, logContent, duration, title, httpReferer, false, 0.0, otherInfo, xRequestID)
		} else {
			dbmodel.RecordConsumeLogWithRequestID(ctx, userId, channelId, inputTokens, outputTokens,
				modelName, tokenName, quota, logContent, duration, title, httpReferer, false, 0.0, xRequestID)
		}

		// Update user and channel quota
		err := dbmodel.PostConsumeTokenQuota(c.GetInt("token_id"), quota)
		if err != nil {
			logger.SysError("error consuming token remain quota: " + err.Error())
		}

		err = dbmodel.CacheUpdateUserQuota(ctx, userId)
		if err != nil {
			logger.SysError("error update user quota cache: " + err.Error())
		}

		dbmodel.UpdateUserUsedQuotaAndRequestCount(userId, quota)
		dbmodel.UpdateChannelUsedQuota(channelId, quota)

		logger.Infof(ctx, "[%s] Recraft API call completed - model: %s, channel #%d, quota: %d",
			requestID, modelName, channelId, quota)

		// Handle response format normalization if requested
		if normalizeFormat {
			var recraftResponse map[string]interface{}
			if err := json.Unmarshal(responseBody, &recraftResponse); err == nil {
				// Convert Recraft response to OpenAI DALL-E format
				openaiResponse := map[string]interface{}{
					"created": time.Now().Unix(),
					"data": []map[string]interface{}{
						{
							"url":            "",
							"revised_prompt": "",
						},
					},
				}

				// Extract image URL from Recraft response
				if image, ok := recraftResponse["image"].(map[string]interface{}); ok {
					if url, ok := image["url"].(string); ok {
						openaiResponse["data"].([]map[string]interface{})[0]["url"] = url
					}
				}

				// Convert to JSON
				normalizedResponse, err := json.Marshal(openaiResponse)
				if err == nil {
					responseBody = normalizedResponse
					logger.Debugf(ctx, "[%s] Response normalized to OpenAI format", requestID)
				} else {
					logger.Warnf(ctx, "[%s] Failed to normalize response: %v", requestID, err)
				}
			} else {
				logger.Warnf(ctx, "[%s] Failed to parse Recraft response for normalization: %v", requestID, err)
			}
		} else {
			// When normalizeResponseFormat is false or not present, use native Recraft format
			logger.Debugf(ctx, "[%s] Using native Recraft response format", requestID)
			// responseBody is already the native format, no processing needed
		}
	} else {
		// Log error responses
		logger.Errorf(ctx, "[%s] Recraft API error: %d - %s", requestID, response.StatusCode, string(responseBody))

		// Check if we should disable the channel
		if response.StatusCode == 401 || response.StatusCode == 403 {
			monitor.DisableChannelSafelyWithStatusCode(channelId, channel.Name, "Authentication error with Recraft API", "N/A (Recraft Auth)", response.StatusCode)
		}

		// Handle error format normalization if requested
		if normalizeFormat {
			var recraftError map[string]interface{}
			if err := json.Unmarshal(responseBody, &recraftError); err == nil {
				// Convert Recraft error to OpenAI error format
				openaiError := map[string]interface{}{
					"error": map[string]interface{}{
						"message": "An error occurred",
						"type":    "api_error",
						"code":    "recraft_error",
					},
				}

				// Extract error message and code from Recraft response
				if message, ok := recraftError["message"].(string); ok {
					openaiError["error"].(map[string]interface{})["message"] = message
				}
				if code, ok := recraftError["code"].(string); ok {
					openaiError["error"].(map[string]interface{})["code"] = code
				}

				// Convert to JSON
				normalizedError, err := json.Marshal(openaiError)
				if err == nil {
					responseBody = normalizedError
					logger.Debugf(ctx, "[%s] Error response normalized to OpenAI format", requestID)
				} else {
					logger.Warnf(ctx, "[%s] Failed to normalize error response: %v", requestID, err)
				}
			} else {
				logger.Warnf(ctx, "[%s] Failed to parse Recraft error for normalization: %v", requestID, err)
			}
		} else {
			// When normalizeResponseFormat is false or not present, use native Recraft error format
			logger.Debugf(ctx, "[%s] Using native Recraft error format", requestID)
			// responseBody is already the native format, no processing needed
		}
	}

	// Pass through the response regardless of status code
	c.Data(response.StatusCode, response.Header.Get("Content-Type"), responseBody)
	return nil
}

func RelayImageGenerateAsync(c *gin.Context) {
	ctx := c.Request.Context()
	requestID := c.GetHeader("X-Request-ID")
	c.Set("X-Request-ID", requestID)

	channelId := c.GetInt("channel_id")
	userId := c.GetInt("id")
	modelName := c.GetString("original_model")

	// 为第一次调用设置渠道历史
	var firstChannelHistory []int
	firstChannelHistory = append(firstChannelHistory, channelId)
	c.Set("admin_channel_history", firstChannelHistory)

	// 尝试第一次请求
	bizErr := controller.DoImageRequest(c, modelName)
	if bizErr == nil {
		return
	}

	// 第一次失败，开始重试逻辑
	channelName := c.GetString("channel_name")
	group := c.GetString("group")
	keyIndex := c.GetInt("key_index")
	go processChannelRelayError(ctx, userId, channelId, channelName, keyIndex, bizErr, modelName)

	retryTimes := config.RetryTimes
	if !shouldRetry(c, bizErr.StatusCode, bizErr.Error.Message) {
		logger.Errorf(ctx, "Image relay error happen, status code is %d, won't retry in this case", bizErr.StatusCode)
		retryTimes = 0
	}

	// 记录使用的渠道历史，用于添加到日志中
	var channelHistory []int
	// 添加初始失败的渠道
	channelHistory = append(channelHistory, channelId)

	for i := retryTimes; i > 0; i-- {
		// 计算应该跳过的优先级数量：第1次重试仍使用最高优先级
		skipPriorityLevels := retryTimes - i
		channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, modelName, skipPriorityLevels)
		if err != nil {
			logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed: %v", err)
			break
		}
		if channel.Id == channelId {
			continue
		}
		logger.Infof(ctx, "Image retry: 模型=%s, 尝试=%d/%d, 用户ID=%d, 渠道切换: #%d(%s) -> #%d(%s)",
			modelName, retryTimes-i+1, retryTimes, userId, channelId, channelName, channel.Id, channel.Name)

		middleware.SetupContextForSelectedChannel(c, channel, modelName)
		channelHistory = append(channelHistory, channel.Id)

		// 重新构建请求体
		requestBody, err := common.GetRequestBody(c)
		if err == nil {
			c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		}

		// 在调用DoImageRequest之前设置渠道历史
		c.Set("admin_channel_history", channelHistory)
		logger.Infof(ctx, "RelayImageGenerateAsync: Setting admin_channel_history before retry: %v", channelHistory)

		bizErr = controller.DoImageRequest(c, modelName)
		if bizErr == nil {
			logger.Infof(ctx, "RelayImageGenerateAsync: Retry SUCCESS with channelHistory: %v", channelHistory)
			return
		}

		channelId = channel.Id
		channelName = channel.Name
		keyIndex = c.GetInt("key_index")
		go processChannelRelayError(ctx, userId, channelId, channelName, keyIndex, bizErr, modelName)
	}

	// 所有重试都失败后的处理
	c.Set("admin_channel_history", channelHistory)

	// 记录图片生成失败请求的日志
	recordFailedRequestLog(ctx, c, bizErr, channelHistory)

	if bizErr.StatusCode == http.StatusTooManyRequests {
		bizErr.Error.Message = "The current group upstream load is saturated, please try again later."
	}
	c.JSON(bizErr.StatusCode, gin.H{
		"error": gin.H{
			"message": util.ProcessString(bizErr.Error.Message),
			"type":    bizErr.Error.Type,
			"param":   bizErr.Error.Param,
			"code":    bizErr.Error.Code,
		},
	})
}

func RelayImageResult(c *gin.Context) {
	taskId := c.Query("taskid")
	bizErr := controller.GetImageResult(c, taskId)
	if bizErr != nil {
		if bizErr.StatusCode == http.StatusTooManyRequests {
			bizErr.Error.Message = "The current group upstream load is saturated, please try again later."
		}
		c.JSON(bizErr.StatusCode, gin.H{
			"error": gin.H{
				"message": util.ProcessString(bizErr.Error.Message),
				"type":    bizErr.Error.Type,
				"param":   bizErr.Error.Param,
				"code":    bizErr.Error.Code,
			},
		})
	}

}

func RelayRunway(c *gin.Context) {
	ctx := c.Request.Context()
	requestID := c.GetHeader("X-Request-ID")
	c.Set("X-Request-ID", requestID)

	channelId := c.GetInt("channel_id")
	userId := c.GetInt("id")
	modelName := c.GetString("original_model")

	logger.Infof(ctx, "RelayRunway start - userId: %d, channelId: %d, model: %s, requestID: %s",
		userId, channelId, modelName, requestID)

	// 尝试第一次请求
	success, statusCode := tryRunwayRequest(c)
	if success {
		// 第一次成功，记录使用的渠道到上下文中
		var channelHistory []int
		channelHistory = append(channelHistory, channelId)
		c.Set("admin_channel_history", channelHistory)

		logger.Infof(ctx, "RelayRunway success on first try - userId: %d, channelId: %d", userId, channelId)
		return
	}

	// 第一次失败，处理错误和重试

	channelName := c.GetString("channel_name")
	group := c.GetString("group")

	logger.Errorf(ctx, "RelayRunway first attempt failed - userId: %d, channelId: %d (%s), statusCode: %d",
		userId, channelId, channelName, statusCode)

	// 使用空的错误对象调用 processChannelRelayError，让它自己处理
	keyIndex := c.GetInt("key_index")
	go processChannelRelayError(ctx, userId, channelId, channelName, keyIndex, &model.ErrorWithStatusCode{
		StatusCode: statusCode,
		Error:      model.Error{Message: "Request failed"},
	}, modelName)

	retryTimes := config.RetryTimes
	if !shouldRetry(c, statusCode, "") {
		logger.Errorf(ctx, "Runway request error happen, status code is %d, won't retry in this case", statusCode)
		// 不重试时，记录失败日志并写入响应
		var channelHistory []int
		channelHistory = append(channelHistory, channelId)
		c.Set("admin_channel_history", channelHistory)
		recordRunwayFailedLog(ctx, c, statusCode, channelHistory)
		writeLastFailureResponse(c, statusCode)
		return
	}

	logger.Infof(ctx, "RelayRunway will retry %d times - status code: %d", retryTimes, statusCode)

	// 获取初始渠道信息用于重试日志
	originalChannelId := channelId
	originalChannelName := channelName
	originalKeyIndex := keyIndex

	// 记录使用的渠道历史，用于添加到日志中
	var channelHistory []int
	// 添加初始失败的渠道
	channelHistory = append(channelHistory, originalChannelId)

	for i := retryTimes; i > 0; i-- {
		logger.Infof(ctx, "RelayRunway retry attempt %d/%d - looking for new channel", retryTimes-i+1, retryTimes)

		// 计算应该跳过的优先级数量：第1次重试仍使用最高优先级
		skipPriorityLevels := retryTimes - i
		channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, modelName, skipPriorityLevels)
		if err != nil {
			logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed on retry %d/%d: %v", retryTimes-i+1, retryTimes, err)
			break
		}

		// 获取重试原因 - 直接使用状态码
		retryReason := fmt.Sprintf("HTTP状态码: %d", statusCode)

		// 获取新渠道的key信息
		newKeyIndex := 0
		isMultiKey := false
		if channel.MultiKeyInfo.IsMultiKey {
			isMultiKey = true
			// 获取下一个可用key的索引
			_, newKeyIndex, _ = channel.GetNextAvailableKey()
		}

		// 生成详细的重试日志
		retryLog := formatRetryLog(ctx, originalChannelId, originalChannelName, originalKeyIndex,
			channel.Id, channel.Name, newKeyIndex, modelName, retryReason,
			retryTimes-i+1, retryTimes, isMultiKey, userId, requestID)

		logger.Infof(ctx, retryLog)

		// 记录重试使用的渠道
		channelHistory = append(channelHistory, channel.Id)

		// 使用新通道的配置更新上下文
		middleware.SetupContextForSelectedChannel(c, channel, modelName)
		requestBody, err := common.GetRequestBody(c)
		if err != nil {
			logger.Errorf(ctx, "Failed to get request body for retry %d/%d: %v", retryTimes-i+1, retryTimes, err)
			break
		}
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))

		logger.Infof(ctx, "Sending retry request %d/%d to channel #%d", retryTimes-i+1, retryTimes, channel.Id)
		success, statusCode = tryRunwayRequest(c)
		if success {
			logger.Infof(ctx, "RelayRunway retry %d/%d SUCCESS on channel #%d", retryTimes-i+1, retryTimes, channel.Id)
			// 成功时记录渠道历史到上下文中
			c.Set("admin_channel_history", channelHistory)
			return
		}

		channelId = c.GetInt("channel_id")

		channelName = c.GetString("channel_name")
		logger.Errorf(ctx, "RelayRunway retry %d/%d FAILED on channel #%d (%s) - statusCode: %d",
			retryTimes-i+1, retryTimes, channelId, channelName, statusCode)

		keyIndex := c.GetInt("key_index")
		go processChannelRelayError(ctx, userId, channelId, channelName, keyIndex, &model.ErrorWithStatusCode{
			StatusCode: statusCode,
			Error:      model.Error{Message: "Retry failed"},
		}, modelName)

		// 检查这次失败是否还应该继续重试
		if !shouldRetry(c, statusCode, "") {
			logger.Errorf(ctx, "Retry encountered non-retryable error, status code is %d, stopping retries", statusCode)
			writeLastFailureResponse(c, statusCode)
			return
		}
	}

	// 所有重试都失败后，写入最后一次失败的响应
	logger.Errorf(ctx, "RelayRunway ALL RETRIES FAILED - userId: %d, final statusCode: %d", userId, statusCode)
	// 失败时记录渠道历史到上下文中
	c.Set("admin_channel_history", channelHistory)

	// 记录Runway失败请求的日志
	recordRunwayFailedLog(ctx, c, statusCode, channelHistory)

	writeLastFailureResponse(c, statusCode)
}

// tryRunwayRequest 尝试执行 Runway 请求，返回是否成功和状态码
func tryRunwayRequest(c *gin.Context) (success bool, statusCode int) {
	ctx := c.Request.Context()
	meta := util.GetRelayMeta(c)
	channelId := c.GetInt("channel_id")

	logger.Debugf(ctx, "tryRunwayRequest start - channelId: %d", channelId)

	// 保存原始的 ResponseWriter
	originalWriter := c.Writer

	// 创建一个缓冲的 ResponseWriter 来捕获响应
	rec := &responseRecorder{
		ResponseWriter: originalWriter,
		statusCode:     200,
		body:           bytes.NewBuffer(nil),
	}
	c.Writer = rec

	// 调用原始函数
	controller.DirectRelayRunway(c, meta)

	// 恢复原始的 ResponseWriter
	c.Writer = originalWriter

	logger.Debugf(ctx, "tryRunwayRequest response - channelId: %d, statusCode: %d", channelId, rec.statusCode)

	// 检查响应状态码
	if rec.statusCode >= 400 {
		logger.Debugf(ctx, "tryRunwayRequest FAILED - channelId: %d, statusCode: %d", channelId, rec.statusCode)
		// 失败时不写入响应，让重试逻辑处理
		return false, rec.statusCode
	}

	// 成功时，将响应写入原始的 ResponseWriter
	logger.Debugf(ctx, "tryRunwayRequest SUCCESS - channelId: %d, statusCode: %d", channelId, rec.statusCode)
	originalWriter.WriteHeader(rec.statusCode)
	for k, v := range rec.Header() {
		originalWriter.Header()[k] = v
	}
	originalWriter.Write(rec.body.Bytes())

	return true, rec.statusCode
}

// writeLastFailureResponse 写入最后失败的响应到客户端
func writeLastFailureResponse(c *gin.Context, statusCode int) {
	ctx := c.Request.Context()
	meta := util.GetRelayMeta(c)
	channelId := c.GetInt("channel_id")

	logger.Debugf(ctx, "writeLastFailureResponse - channelId: %d, statusCode: %d", channelId, statusCode)

	// 重新获取请求体
	requestBody, err := common.GetRequestBody(c)
	if err != nil {
		logger.Errorf(ctx, "Failed to get request body for final response: %v", err)
		c.JSON(statusCode, gin.H{"error": "Request failed"})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))

	// 直接调用 DirectRelayRunway 让它写入错误响应
	controller.DirectRelayRunway(c, meta)
}

// responseRecorder 用于捕获响应
type responseRecorder struct {
	gin.ResponseWriter
	statusCode int
	body       *bytes.Buffer
}

func (r *responseRecorder) WriteHeader(code int) {
	r.statusCode = code
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	return r.body.Write(data)
}

func RelayRunwayResult(c *gin.Context) {
	taskId := c.Param("taskId")
	if taskId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "taskId is required"})
		return
	}
	controller.GetRunwayResult(c, taskId)
}

func RelaySoraVideoResult(c *gin.Context) {
	videoId := c.Param("videoId")
	if videoId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "videoId is required"})
		return
	}
	controller.GetSoraVideoResult(c, videoId)
}

func RelaySoraVideoContent(c *gin.Context) {
	videoId := c.Param("videoId")
	if videoId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "videoId is required"})
		return
	}
	controller.GetSoraVideoContent(c, videoId)
}

func RelaySoraVideoRemix(c *gin.Context) {
	videoId := c.Param("videoId")
	if videoId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "videoId is required"})
		return
	}
	controller.DirectRelaySoraVideoRemix(c, videoId)
}

func RelaySoraVideo(c *gin.Context) {
	ctx := c.Request.Context()
	requestID := c.GetHeader("X-Request-ID")
	c.Set("X-Request-ID", requestID)

	channelId := c.GetInt("channel_id")
	userId := c.GetInt("id")
	modelName := c.GetString("original_model")

	logger.Infof(ctx, "RelaySoraVideo start - userId: %d, channelId: %d, model: %s, requestID: %s",
		userId, channelId, modelName, requestID)

	// 尝试第一次请求
	success, statusCode, errorMessage := trySoraRequest(c)
	if success {
		// 第一次成功，记录使用的渠道到上下文中
		var channelHistory []int
		channelHistory = append(channelHistory, channelId)
		c.Set("admin_channel_history", channelHistory)

		logger.Infof(ctx, "RelaySoraVideo success on first try - userId: %d, channelId: %d", userId, channelId)
		return
	}

	// 第一次失败，处理错误和重试
	channelName := c.GetString("channel_name")
	group := c.GetString("group")

	logger.Errorf(ctx, "RelaySoraVideo first attempt failed - userId: %d, channelId: %d (%s), statusCode: %d, error: %s",
		userId, channelId, channelName, statusCode, errorMessage)

	// 使用具体的错误消息调用 processChannelRelayError
	keyIndex := c.GetInt("key_index")
	go processChannelRelayError(ctx, userId, channelId, channelName, keyIndex, &model.ErrorWithStatusCode{
		StatusCode: statusCode,
		Error:      model.Error{Message: errorMessage},
	}, modelName)

	retryTimes := config.RetryTimes
	if !shouldRetry(c, statusCode, errorMessage) {
		logger.Errorf(ctx, "Sora request error happen, status code is %d, won't retry in this case", statusCode)
		// 不重试时，记录失败日志并写入响应
		var channelHistory []int
		channelHistory = append(channelHistory, channelId)
		c.Set("admin_channel_history", channelHistory)
		recordSoraFailedLog(ctx, c, statusCode, channelHistory)
		writeLastSoraFailureResponse(c, statusCode)
		return
	}

	logger.Infof(ctx, "RelaySoraVideo will retry %d times - status code: %d", retryTimes, statusCode)

	// 获取初始渠道信息用于重试日志
	originalChannelId := channelId
	originalChannelName := channelName
	originalKeyIndex := keyIndex

	// 记录使用的渠道历史，用于添加到日志中
	var channelHistory []int
	// 添加初始失败的渠道
	channelHistory = append(channelHistory, originalChannelId)

	for i := retryTimes; i > 0; i-- {
		logger.Infof(ctx, "RelaySoraVideo retry attempt %d/%d - looking for new channel", retryTimes-i+1, retryTimes)

		// 计算应该跳过的优先级数量：第1次重试仍使用最高优先级
		skipPriorityLevels := retryTimes - i
		channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, modelName, skipPriorityLevels)
		if err != nil {
			logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed on retry %d/%d: %v", retryTimes-i+1, retryTimes, err)
			break
		}

		// 获取重试原因 - 直接使用状态码
		retryReason := fmt.Sprintf("HTTP状态码: %d", statusCode)

		// 获取新渠道的key信息
		newKeyIndex := 0
		isMultiKey := false
		if channel.MultiKeyInfo.IsMultiKey {
			isMultiKey = true
			// 获取下一个可用key的索引
			_, newKeyIndex, _ = channel.GetNextAvailableKey()
		}

		// 生成详细的重试日志
		retryLog := formatRetryLog(ctx, originalChannelId, originalChannelName, originalKeyIndex,
			channel.Id, channel.Name, newKeyIndex, modelName, retryReason,
			retryTimes-i+1, retryTimes, isMultiKey, userId, requestID)

		logger.Infof(ctx, retryLog)

		// 记录重试使用的渠道
		channelHistory = append(channelHistory, channel.Id)

		// 使用新通道的配置更新上下文
		middleware.SetupContextForSelectedChannel(c, channel, modelName)
		requestBody, err := common.GetRequestBody(c)
		if err != nil {
			logger.Errorf(ctx, "Failed to get request body for retry %d/%d: %v", retryTimes-i+1, retryTimes, err)
			break
		}
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))

		logger.Infof(ctx, "Sending retry request %d/%d to channel #%d", retryTimes-i+1, retryTimes, channel.Id)
		success, statusCode, retryErrorMessage := trySoraRequest(c)
		if success {
			logger.Infof(ctx, "RelaySoraVideo retry %d/%d SUCCESS on channel #%d", retryTimes-i+1, retryTimes, channel.Id)
			// 成功时记录渠道历史到上下文中
			c.Set("admin_channel_history", channelHistory)
			return
		}

		channelId = c.GetInt("channel_id")

		channelName = c.GetString("channel_name")
		logger.Errorf(ctx, "RelaySoraVideo retry %d/%d FAILED on channel #%d (%s) - statusCode: %d, error: %s",
			retryTimes-i+1, retryTimes, channelId, channelName, statusCode, retryErrorMessage)

		keyIndex := c.GetInt("key_index")
		go processChannelRelayError(ctx, userId, channelId, channelName, keyIndex, &model.ErrorWithStatusCode{
			StatusCode: statusCode,
			Error:      model.Error{Message: retryErrorMessage},
		}, modelName)

		// 检查这次失败是否还应该继续重试
		if !shouldRetry(c, statusCode, retryErrorMessage) {
			logger.Errorf(ctx, "Retry encountered non-retryable error, status code is %d, stopping retries", statusCode)
			writeLastSoraFailureResponse(c, statusCode)
			return
		}
	}

	// 所有重试都失败后，写入最后一次失败的响应
	logger.Errorf(ctx, "RelaySoraVideo ALL RETRIES FAILED - userId: %d, final statusCode: %d", userId, statusCode)
	// 失败时记录渠道历史到上下文中
	c.Set("admin_channel_history", channelHistory)

	// 记录Sora失败请求的日志
	recordSoraFailedLog(ctx, c, statusCode, channelHistory)

	writeLastSoraFailureResponse(c, statusCode)
}

// trySoraRequest 尝试执行 Sora 请求，返回是否成功、状态码和错误消息
func trySoraRequest(c *gin.Context) (success bool, statusCode int, errorMessage string) {
	ctx := c.Request.Context()
	channelId := c.GetInt("channel_id")

	// 记录请求开始
	logger.Debugf(ctx, "trySoraRequest start - channelId: %d", channelId)

	// 获取meta信息，进行空值检查
	meta := util.GetRelayMeta(c)
	if meta == nil {
		logger.Errorf(ctx, "trySoraRequest: failed to get relay meta for channelId: %d", channelId)
		return false, http.StatusInternalServerError, "Internal server error: missing relay meta"
	}

	// 保存和替换ResponseWriter
	originalWriter := c.Writer
	rec := newResponseRecorder(originalWriter)
	c.Writer = rec

	// 使用defer确保ResponseWriter始终被恢复
	defer func() {
		c.Writer = originalWriter
	}()

	// 调用原始函数，捕获可能的panic
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf(ctx, "trySoraRequest panic recovered - channelId: %d, error: %v", channelId, r)
			statusCode = http.StatusInternalServerError
			errorMessage = "Internal server error: request panic"
			success = false
		}
	}()

	controller.DirectRelaySoraVideo(c, meta)

	// 记录响应信息
	statusCode = rec.statusCode
	logger.Debugf(ctx, "trySoraRequest response - channelId: %d, statusCode: %d, bodySize: %d",
		channelId, statusCode, rec.body.Len())

	// 检查响应状态码
	if statusCode >= 400 {
		// 提取错误消息
		responseBody := rec.body.String()
		errorMessage = extractErrorMessage(responseBody)

		logger.Debugf(ctx, "trySoraRequest FAILED - channelId: %d, statusCode: %d, error: %s",
			channelId, statusCode, truncateString(errorMessage, 100))
		return false, statusCode, errorMessage
	}

	// 成功时写入响应
	if err := writeSuccessResponse(originalWriter, rec); err != nil {
		logger.Errorf(ctx, "trySoraRequest: failed to write success response - channelId: %d, error: %v", channelId, err)
		return false, http.StatusInternalServerError, "Failed to write response"
	}

	logger.Debugf(ctx, "trySoraRequest SUCCESS - channelId: %d, statusCode: %d", channelId, statusCode)
	return true, statusCode, ""
}

// newResponseRecorder 创建新的响应记录器
func newResponseRecorder(original gin.ResponseWriter) *responseRecorder {
	return &responseRecorder{
		ResponseWriter: original,
		statusCode:     200,
		body:           bytes.NewBuffer(nil),
	}
}

// writeSuccessResponse 写入成功响应到原始ResponseWriter
func writeSuccessResponse(writer gin.ResponseWriter, rec *responseRecorder) error {
	// 设置状态码
	writer.WriteHeader(rec.statusCode)

	// 复制响应头
	for k, v := range rec.Header() {
		writer.Header()[k] = v
	}

	// 写入响应体
	if _, err := writer.Write(rec.body.Bytes()); err != nil {
		return fmt.Errorf("failed to write response body: %w", err)
	}

	return nil
}

// extractErrorMessage 从响应体中提取错误消息
func extractErrorMessage(responseBody string) string {
	if responseBody == "" {
		return "Request failed"
	}

	// 防止过长的响应体造成内存问题，限制解析长度
	const maxParseLength = 2048
	parseBody := responseBody
	if len(responseBody) > maxParseLength {
		parseBody = responseBody[:maxParseLength]
	}

	// 快速检查是否可能是JSON格式
	trimmed := strings.TrimSpace(parseBody)
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return truncateString(responseBody, 200)
	}

	// 尝试解析JSON响应
	var errorResponse map[string]interface{}
	if err := json.Unmarshal([]byte(parseBody), &errorResponse); err != nil {
		// JSON解析失败，返回截取的原始内容
		return truncateString(responseBody, 200)
	}

	// 尝试提取错误消息，按优先级排序
	if msg := extractFromErrorObject(errorResponse); msg != "" {
		return msg
	}

	if msg := extractFromDirectFields(errorResponse); msg != "" {
		return msg
	}

	// 如果都没找到，返回截取的原始内容
	return truncateString(responseBody, 200)
}

// extractFromErrorObject 从标准的error对象中提取消息
func extractFromErrorObject(errorResponse map[string]interface{}) string {
	errorObj, ok := errorResponse["error"].(map[string]interface{})
	if !ok {
		return ""
	}

	// 优先返回message字段
	if message, ok := errorObj["message"].(string); ok && message != "" {
		return sanitizeErrorMessage(message)
	}

	// 其次返回code字段
	if code, ok := errorObj["code"].(string); ok && code != "" {
		return sanitizeErrorMessage(code)
	}

	// 检查type字段
	if errorType, ok := errorObj["type"].(string); ok && errorType != "" {
		return sanitizeErrorMessage(errorType)
	}

	return ""
}

// extractFromDirectFields 从响应对象的直接字段中提取消息
func extractFromDirectFields(errorResponse map[string]interface{}) string {
	// 检查常见的错误字段
	fields := []string{"message", "detail", "error_description", "description"}

	for _, field := range fields {
		if value, ok := errorResponse[field].(string); ok && value != "" {
			return sanitizeErrorMessage(value)
		}
	}

	return ""
}

// sanitizeErrorMessage 清理错误消息，防止恶意内容
func sanitizeErrorMessage(message string) string {
	// 限制消息长度，防止日志攻击
	const maxMessageLength = 500
	if len(message) > maxMessageLength {
		message = message[:maxMessageLength] + "..."
	}

	// 移除控制字符，防止日志注入
	message = strings.ReplaceAll(message, "\n", " ")
	message = strings.ReplaceAll(message, "\r", " ")
	message = strings.ReplaceAll(message, "\t", " ")

	return strings.TrimSpace(message)
}

// truncateString 截取字符串到指定长度
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// writeLastSoraFailureResponse 写入最后失败的响应到客户端
func writeLastSoraFailureResponse(c *gin.Context, statusCode int) {
	ctx := c.Request.Context()
	meta := util.GetRelayMeta(c)
	channelId := c.GetInt("channel_id")

	logger.Debugf(ctx, "writeLastSoraFailureResponse - channelId: %d, statusCode: %d", channelId, statusCode)

	// 重新获取请求体
	requestBody, err := common.GetRequestBody(c)
	if err != nil {
		logger.Errorf(ctx, "Failed to get request body for final response: %v", err)
		c.JSON(statusCode, gin.H{"error": "Request failed"})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))

	// 直接调用 DirectRelaySoraVideo 让它写入错误响应
	controller.DirectRelaySoraVideo(c, meta)
}

// recordSoraFailedLog 记录Sora失败请求的日志
func recordSoraFailedLog(ctx context.Context, c *gin.Context, statusCode int, channelHistory []int) {
	// 这里可以添加特定的日志记录逻辑
	userId := c.GetInt("id")
	modelName := c.GetString("original_model")
	logger.Errorf(ctx, "Sora request failed - userId: %d, model: %s, statusCode: %d, channelHistory: %v",
		userId, modelName, statusCode, channelHistory)
}

func relayGeminiHelper(c *gin.Context, relayMode int) *model.ErrorWithStatusCode {
	if relayMode == relayconstant.RelayModeGeminiGenerateContent || relayMode == relayconstant.RelayModeGeminiStreamGenerateContent {
		return controller.RelayGeminiNative(c)
	}
	return nil
}
func RelayGemini(c *gin.Context) {
	ctx := c.Request.Context()
	channelId := c.GetInt("channel_id")
	userId := c.GetInt("id")
	relayMode := c.GetInt("relay_mode")
	geminiErr := relayGeminiHelper(c, relayMode)

	if geminiErr == nil {
		return
	}

	lastFailedChannelId := channelId
	channelName := c.GetString("channel_name")
	group := c.GetString("group")
	retryTimes := config.RetryTimes
	if !shouldRetry(c, geminiErr.StatusCode, geminiErr.Error.Message) {
		logger.Errorf(ctx, "Gemini relay error happen, status code is %d, won't retry in this case", geminiErr.StatusCode)
		retryTimes = 0
	}

	for i := retryTimes; i > 0; i-- {
		channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, c.GetString("original_model"), i != retryTimes)
		if err != nil {
			logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed: %s", err.Error())
			break
		}
		if channel.Id == lastFailedChannelId {
			continue
		}
		logger.Infof(ctx, "Using channel #%d to retry Gemini request (remain times %d)", channel.Id, i)

		middleware.SetupContextForSelectedChannel(c, channel, c.GetString("original_model"))
		requestBody, err := common.GetRequestBody(c)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		geminiErr = relayGeminiHelper(c, relayMode)
		if geminiErr == nil {
			return
		}

		channelId = c.GetInt("channel_id")
		lastFailedChannelId = channelId
		channelName = c.GetString("channel_name")
		go processChannelRelayError(ctx, userId, channelId, channelName, geminiErr)
	}

	if geminiErr != nil {
		c.JSON(geminiErr.StatusCode, gin.H{
			"error": gin.H{
				"message": geminiErr.Error.Message,
				"type":    geminiErr.Error.Type,
				"code":    geminiErr.Error.Code,
			},
		})
	}
}
