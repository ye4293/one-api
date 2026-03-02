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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
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

	// 记录整个请求的开始时间，用于计算总耗时和首字时长
	totalStartTime := time.Now()
	// 将总请求开始时间设置到 context 中，供下游使用计算真正的首字时长
	c.Set("total_request_start_time", totalStartTime)

	// 获取或生成 X-Request-ID
	// 如果客户端没有传递，则自动生成：时间戳(YYYYMMDDHHmmss) + 8位UUID
	requestID := c.GetHeader("X-Request-ID")
	if requestID == "" {
		requestID = common.GenerateRequestID()
	}
	c.Set("X-Request-ID", requestID)
	// 同时设置到 Header 中，确保后续处理可以通过 GetHeader 获取
	c.Request.Header.Set("X-Request-ID", requestID)

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
	tokenName := c.GetString("token_name")

	retryTimes := config.RetryTimes
	if !shouldRetry(c, bizErr.StatusCode, bizErr.Error.Message) {
		logger.Errorf(ctx, "Relay error happen, status code is %d, won't retry in this case", bizErr.StatusCode)
		retryTimes = 0
	}

	// 记录使用的渠道历史，用于添加到日志中
	var channelHistory []int
	// 添加初始失败的渠道
	channelHistory = append(channelHistory, channelId)

	// 记录所有已失败的渠道ID，用于重试时排除
	// 初始不加入首次失败的渠道，第一次重试保持在原优先级
	initialFailedChannelId := channelId
	failedChannelIds := []int{}

	// 记录第一次调用的失败信息（累计耗时：从请求开始到当前失败的时间，同步记录保证顺序）
	cumulativeDuration := time.Since(totalStartTime).Seconds()

	// 检查是否是xAI内容违规错误，如果是则记录扣费日志而不是普通失败日志
	if isXAIContentViolation(bizErr.StatusCode, bizErr.Error.Message) {
		// xAI内容违规：直接记录扣费日志，不记录普通失败日志
		recordXAIContentViolationCharge(ctx, c, channelHistory)
	} else {
		// 普通失败：记录失败日志
		recordRetryFailureLog(ctx, userId, channelId, originalModel, tokenName, requestID, 0, cumulativeDuration, bizErr.Error.Message, channelName, channelHistory)
	}

	// 处理首次失败的渠道错误（包括自动禁用逻辑）
	go processChannelRelayError(ctx, userId, channelId, channelName, keyIndex, bizErr, originalModel)

	// 获取客户端传递的 X-Response-ID（用于 Claude 缓存）
	claudeResponseID := c.GetHeader("X-Response-ID")

	var lastChannel *dbmodel.Channel

	for i := retryTimes; i > 0; i-- {
		// 使用排除已失败渠道的方式选择新渠道，始终选择最高优先级的可用渠道
		channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, originalModel, 0, claudeResponseID, failedChannelIds)
		if err != nil {
			if lastChannel == nil {
				logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed and no fallback channel: %v (excludedChannels: %v)", err, failedChannelIds)
				break
			}
			logger.Infof(ctx, "No new channel found (excludedChannels: %v), retrying with last channel #%d (%d/%d)", failedChannelIds, lastChannel.Id, retryTimes-i+1, retryTimes)
			channel = lastChannel
		}
		lastChannel = channel

		// 第一次重试完成后，将初始失败渠道加入排除列表，后续重试降级到次优先级
		if i == retryTimes {
			failedChannelIds = append(failedChannelIds, initialFailedChannelId)
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
			// 重试成功，直接返回（无需记录错误日志）
			monitor.Emit(channel.Id, true)
			return
		}

		// 计算累计耗时（从请求开始到当前失败的时间）
		cumulativeDuration = time.Since(totalStartTime).Seconds()
		currentAttempt := retryTimes - i + 1

		channelId = c.GetInt("channel_id")
		channelName = c.GetString("channel_name")
		keyIndex = c.GetInt("key_index") // 在异步调用前获取keyIndex

		// 将本次失败的渠道ID添加到排除列表，避免重复选择
		failedChannelIds = append(failedChannelIds, channelId)

		// 检查是否是xAI内容违规错误
		if isXAIContentViolation(bizErr.StatusCode, bizErr.Error.Message) {
			// xAI内容违规：记录扣费日志并立即停止重试
			recordXAIContentViolationCharge(ctx, c, channelHistory)
			go processChannelRelayError(ctx, userId, channelId, channelName, keyIndex, bizErr, originalModel)
			// 跳出重试循环，直接返回错误
			break
		}

		// 普通失败：记录本次重试失败的日志（耗时为累计耗时，同步记录保证顺序）
		recordRetryFailureLog(ctx, userId, channel.Id, originalModel, tokenName, requestID, currentAttempt, cumulativeDuration, bizErr.Error.Message, channel.Name, channelHistory)

		go processChannelRelayError(ctx, userId, channelId, channelName, keyIndex, bizErr, originalModel)
	}

	// 如果所有尝试都失败
	if bizErr != nil {
		// 记录渠道历史到上下文中，供后续使用
		c.Set("admin_channel_history", channelHistory)

		// 注意：xAI内容违规的扣费已在上面处理，不再重复检查
		// 注意：不再调用 recordFailedRequestLog，因为每次失败已经单独记录了

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
		0,
	)

	logger.Infof(ctx, "Recorded failed request log: userId=%d, model=%s, error=%s, channels=%v",
		userId, originalModel, bizErr.Error.Message, channelHistory)
}

// recordRetryFailureLog 记录单次重试失败的日志
// 每次重试失败都会记录一条单独的日志，包含耗时、原因和RequestID
// 使用 LogTypeError 类型，方便后续筛选查看错误日志
// channelHistory 用于记录渠道重试历史
func recordRetryFailureLog(ctx context.Context, userId int, channelId int, modelName string, tokenName string, requestID string, attempt int, duration float64, reason string, channelName string, channelHistory []int) {
	// 构建失败日志内容
	var logContent string
	if attempt == 0 {
		logContent = fmt.Sprintf("首次调用失败 [RequestID: %s]: 渠道=%s(#%d), 耗时=%.3fs, 原因=%s",
			requestID, channelName, channelId, duration, reason)
	} else {
		logContent = fmt.Sprintf("第%d次重试失败 [RequestID: %s]: 渠道=%s(#%d), 耗时=%.3fs, 原因=%s",
			attempt, requestID, channelName, channelId, duration, reason)
	}

	// 构建 other 信息，包含渠道历史（用于前端显示重试栏）
	channelHistoryJSON, _ := json.Marshal(channelHistory)
	otherInfo := fmt.Sprintf("retryAttempt:%d;adminInfo:%s", attempt, string(channelHistoryJSON))

	// 记录失败日志，使用 LogTypeError 类型
	dbmodel.RecordErrorLogWithRequestID(
		ctx,
		userId,
		channelId,
		modelName,
		tokenName,
		logContent,
		duration,
		otherInfo,
		requestID,
	)

	logger.Infof(ctx, "Recorded retry failure log: requestID=%s, userId=%d, model=%s, attempt=%d, duration=%.3fs, error=%s",
		requestID, userId, modelName, attempt, duration, reason)
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
		0,
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
		0,
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
		0,
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

	// 记录所有已失败的渠道ID，用于重试时排除
	// 初始不加入首次失败的渠道，第一次重试保持在原优先级
	initialMjFailedId := channelId
	failedChannelIds := []int{}

	var lastMjChannel *dbmodel.Channel

	for i := retryTimes; i > 0; i-- {
		if originalModel != "" {
			// 使用排除已失败渠道的方式选择新渠道，始终选择最高优先级的可用渠道
			channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, originalModel, 0, "", failedChannelIds)
			if err != nil {
				if lastMjChannel == nil {
					logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed and no fallback channel: %+v (excludedChannels: %v)", err, failedChannelIds)
					break
				}
				logger.Infof(ctx, "No new channel found (excludedChannels: %v), retrying with last channel #%d (%d/%d)", failedChannelIds, lastMjChannel.Id, retryTimes-i+1, retryTimes)
				channel = lastMjChannel
			}
			lastMjChannel = channel
			logger.Infof(ctx, "Using channel #%d to retry (remain times %d)", channel.Id, i)

			// 第一次重试完成后，将初始失败渠道加入排除列表，后续重试降级到次优先级
			if i == retryTimes && initialMjFailedId > 0 {
				failedChannelIds = append(failedChannelIds, initialMjFailedId)
			}

			// 将新渠道添加到已失败列表（因为如果本次失败，下次不应该再选它）
			failedChannelIds = append(failedChannelIds, channel.Id)

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
			if err != nil {
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

	// 记录所有已失败的渠道ID，用于重试时排除
	// 初始不加入首次失败的渠道，第一次重试保持在原优先级
	failedChannelIds := []int{}

	var lastVideoChannel *dbmodel.Channel

	for i := retryTimes; i > 0; i-- {
		// 使用排除已失败渠道的方式选择新渠道，始终选择最高优先级的可用渠道
		channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, modelName, 0, "", failedChannelIds)
		if err != nil {
			if lastVideoChannel == nil {
				logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed and no fallback channel: %v (excludedChannels: %v)", err, failedChannelIds)
				break
			}
			logger.Infof(ctx, "No new channel found (excludedChannels: %v), retrying with last channel #%d (%d/%d)", failedChannelIds, lastVideoChannel.Id, retryTimes-i+1, retryTimes)
			channel = lastVideoChannel
		}
		lastVideoChannel = channel

		// 第一次重试完成后，将初始失败渠道加入排除列表，后续重试降级到次优先级
		if i == retryTimes {
			failedChannelIds = append(failedChannelIds, originalChannelId)
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

		// 将本次失败的渠道ID添加到排除列表，避免重复选择
		failedChannelIds = append(failedChannelIds, channelId)

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

	// 记录所有已失败的渠道ID，用于重试时排除
	// 初始不加入首次失败的渠道，第一次重试保持在原优先级
	initialRecraftFailedId := channelId
	failedChannelIds := []int{}

	var lastRecraftChannel *dbmodel.Channel

	for i := retryTimes; i > 0; i-- {
		// 使用排除已失败渠道的方式选择新渠道，始终选择最高优先级的可用渠道
		channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, modelName, 0, "", failedChannelIds)
		if err != nil {
			if lastRecraftChannel == nil {
				logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed and no fallback channel: %v (excludedChannels: %v)", err, failedChannelIds)
				break
			}
			logger.Infof(ctx, "No new channel found (excludedChannels: %v), retrying with last channel #%d (%d/%d)", failedChannelIds, lastRecraftChannel.Id, retryTimes-i+1, retryTimes)
			channel = lastRecraftChannel
		}
		lastRecraftChannel = channel

		// 第一次重试完成后，将初始失败渠道加入排除列表，后续重试降级到次优先级
		if i == retryTimes {
			failedChannelIds = append(failedChannelIds, initialRecraftFailedId)
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

		// 将本次失败的渠道ID添加到排除列表，避免重复选择
		failedChannelIds = append(failedChannelIds, channelId)

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
				modelName, tokenName, quota, logContent, duration, title, httpReferer, false, 0.0, otherInfo, xRequestID, 0)
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

	// 记录所有已失败的渠道ID，用于重试时排除
	// 初始不加入首次失败的渠道，第一次重试保持在原优先级
	initialImageFailedId := channelId
	failedChannelIds := []int{}

	var lastImageChannel *dbmodel.Channel

	for i := retryTimes; i > 0; i-- {
		// 使用排除已失败渠道的方式选择新渠道，始终选择最高优先级的可用渠道
		channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, modelName, 0, "", failedChannelIds)
		if err != nil {
			if lastImageChannel == nil {
				logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed and no fallback channel: %v (excludedChannels: %v)", err, failedChannelIds)
				break
			}
			logger.Infof(ctx, "No new channel found (excludedChannels: %v), retrying with last channel #%d (%d/%d)", failedChannelIds, lastImageChannel.Id, retryTimes-i+1, retryTimes)
			channel = lastImageChannel
		}
		lastImageChannel = channel

		// 第一次重试完成后，将初始失败渠道加入排除列表，后续重试降级到次优先级
		if i == retryTimes {
			failedChannelIds = append(failedChannelIds, initialImageFailedId)
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

		// 将本次失败的渠道ID添加到排除列表，避免重复选择
		failedChannelIds = append(failedChannelIds, channelId)

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

	// 记录所有已失败的渠道ID，用于重试时排除
	// 初始不加入首次失败的渠道，第一次重试保持在原优先级
	failedChannelIds := []int{}

	var lastRunwayChannel *dbmodel.Channel

	for i := retryTimes; i > 0; i-- {
		logger.Infof(ctx, "RelayRunway retry attempt %d/%d - looking for new channel", retryTimes-i+1, retryTimes)

		// 使用排除已失败渠道的方式选择新渠道，始终选择最高优先级的可用渠道
		channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, modelName, 0, "", failedChannelIds)
		if err != nil {
			if lastRunwayChannel == nil {
				logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed and no fallback channel on retry %d/%d: %v (excludedChannels: %v)", retryTimes-i+1, retryTimes, err, failedChannelIds)
				break
			}
			logger.Infof(ctx, "No new channel found (excludedChannels: %v), retrying with last channel #%d (%d/%d)", failedChannelIds, lastRunwayChannel.Id, retryTimes-i+1, retryTimes)
			channel = lastRunwayChannel
		}
		lastRunwayChannel = channel

		// 第一次重试完成后，将初始失败渠道加入排除列表，后续重试降级到次优先级
		if i == retryTimes {
			failedChannelIds = append(failedChannelIds, originalChannelId)
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

		// 将本次失败的渠道ID添加到排除列表，避免重复选择
		failedChannelIds = append(failedChannelIds, channelId)

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

	// 记录所有已失败的渠道ID，用于重试时排除
	failedChannelIds := []int{originalChannelId}

	for i := retryTimes; i > 0; i-- {
		logger.Infof(ctx, "RelaySoraVideo retry attempt %d/%d - looking for new channel", retryTimes-i+1, retryTimes)

		// 使用排除已失败渠道的方式选择新渠道，始终选择最高优先级的可用渠道
		channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, modelName, 0, "", failedChannelIds)
		if err != nil {
			logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed on retry %d/%d: %v (excludedChannels: %v)", retryTimes-i+1, retryTimes, err, failedChannelIds)
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

		// 将本次失败的渠道ID添加到排除列表，避免重复选择
		failedChannelIds = append(failedChannelIds, channelId)

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

	// 记录整个请求的开始时间，用于计算总耗时和首字时长
	totalStartTime := time.Now()
	// 将总请求开始时间设置到 context 中，供 RelayGeminiNative 使用计算真正的首字时长
	c.Set("total_request_start_time", totalStartTime)

	channelId := c.GetInt("channel_id")
	userId := c.GetInt("id")
	relayMode := c.GetInt("relay_mode")
	originalModel := c.GetString("original_model")
	originalChannelId := c.GetInt("channel_id")
	originalChannelName := c.GetString("channel_name")
	originalKeyIndex := c.GetInt("key_index")
	tokenName := c.GetString("token_name")

	// 获取或生成 X-Request-ID
	// 如果客户端没有传递，则自动生成：时间戳(YYYYMMDDHHmmss) + 8位UUID
	requestID := c.GetHeader("X-Request-ID")
	if requestID == "" {
		requestID = common.GenerateRequestID()
	}
	c.Set("X-Request-ID", requestID)
	// 同时设置到 Header 中，确保后续处理可以通过 GetHeader 获取
	c.Request.Header.Set("X-Request-ID", requestID)

	// 记录使用的渠道历史，用于添加到日志中
	// 在调用前就初始化并设置，确保成功时也能记录
	channelHistory := []int{originalChannelId}
	c.Set("admin_channel_history", channelHistory)

	geminiErr := relayGeminiHelper(c, relayMode)
	if geminiErr == nil {
		return
	}

	// 记录第一次调用的失败信息（累计耗时：从请求开始到当前失败的时间，同步记录保证顺序）
	cumulativeDuration := time.Since(totalStartTime).Seconds()
	recordRetryFailureLog(ctx, userId, originalChannelId, originalModel, tokenName, requestID, 0, cumulativeDuration, geminiErr.Error.Message, originalChannelName, channelHistory)

	// 处理首次失败的渠道错误（包括自动禁用逻辑）
	go processChannelRelayError(ctx, userId, originalChannelId, originalChannelName, originalKeyIndex, geminiErr, originalModel)

	// 记录所有已失败的渠道ID，用于重试时排除
	failedChannelIds := []int{channelId}
	group := c.GetString("group")
	retryTimes := config.RetryTimes
	if !shouldRetry(c, geminiErr.StatusCode, geminiErr.Error.Message) {
		logger.Errorf(ctx, "Gemini relay error happen, status code is %d, won't retry in this case", geminiErr.StatusCode)
		retryTimes = 0
	}

	for i := retryTimes; i > 0; i-- {
		// 使用排除已失败渠道的方式选择新渠道，始终选择最高优先级的可用渠道
		channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, originalModel, 0, "", failedChannelIds)
		if err != nil {
			logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed: %v (excludedChannels: %v)", err, failedChannelIds)
			break
		}

		// 获取重试原因 - 直接使用原始错误消息
		retryReason := geminiErr.Error.Message

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

		// 记录重试使用的渠道，更新上下文中的 channelHistory（在实际调用前追加）
		if historyInterface, exists := c.Get("admin_channel_history"); exists {
			if history, ok := historyInterface.([]int); ok {
				history = append(history, channel.Id)
				c.Set("admin_channel_history", history)
				channelHistory = history
			}
		}

		logger.Infof(ctx, "Using channel #%d to retry Gemini request (remain times %d)", channel.Id, i)

		middleware.SetupContextForSelectedChannel(c, channel, originalModel)
		requestBody, _ := common.GetRequestBody(c)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		geminiErr = relayGeminiHelper(c, relayMode)
		if geminiErr == nil {
			// 重试成功，直接返回（无需记录错误日志）
			return
		}

		// 计算累计耗时（从请求开始到当前失败的时间）
		cumulativeDuration = time.Since(totalStartTime).Seconds()
		currentAttempt := retryTimes - i + 1

		channelId = c.GetInt("channel_id")
		channelName := c.GetString("channel_name")
		keyIndex := c.GetInt("key_index")

		// 将本次失败的渠道ID添加到排除列表，避免重复选择
		failedChannelIds = append(failedChannelIds, channelId)

		// 记录本次重试失败的日志（耗时为累计耗时，同步记录保证顺序）
		recordRetryFailureLog(ctx, userId, channel.Id, originalModel, tokenName, requestID, currentAttempt, cumulativeDuration, geminiErr.Error.Message, channel.Name, channelHistory)

		go processChannelRelayError(ctx, userId, channelId, channelName, keyIndex, geminiErr, originalModel)
	}

	if geminiErr != nil {
		// 记录渠道历史到上下文中
		c.Set("admin_channel_history", channelHistory)
		// 注意：不再调用 recordFailedRequestLog，因为每次失败已经单独记录了
		c.JSON(geminiErr.StatusCode, gin.H{
			"error": gin.H{
				"message": geminiErr.Error.Message,
				"code":    geminiErr.Error.Code,
				"status":  geminiErr.Error.Status,
			},
		})
	}
}
func RelayClaude(c *gin.Context) {
	ctx := c.Request.Context()

	// 记录整个请求的开始时间，用于计算总耗时和首字时长
	totalStartTime := time.Now()
	// 将总请求开始时间设置到 context 中，供 RelayClaudeNative 使用计算真正的首字时长
	c.Set("total_request_start_time", totalStartTime)

	channelId := c.GetInt("channel_id")
	userId := c.GetInt("id")
	//relayMode := c.GetInt("relay_mode")
	originalModel := c.GetString("original_model")
	originalChannelId := c.GetInt("channel_id")
	originalChannelName := c.GetString("channel_name")
	originalKeyIndex := c.GetInt("key_index")
	tokenName := c.GetString("token_name")

	// 获取或生成 X-Request-ID
	// 如果客户端没有传递，则自动生成：时间戳(YYYYMMDDHHmmss) + 8位UUID
	requestID := c.GetHeader("X-Request-ID")
	if requestID == "" {
		requestID = common.GenerateRequestID()
	}
	c.Set("X-Request-ID", requestID)
	// 同时设置到 Header 中，确保后续处理可以通过 GetHeader 获取
	c.Request.Header.Set("X-Request-ID", requestID)

	// 记录使用的渠道历史，用于添加到日志中
	// 在调用前就初始化并设置，确保成功时也能记录
	channelHistory := []int{originalChannelId}
	c.Set("admin_channel_history", channelHistory)

	relayError := controller.RelayClaudeNative(c)
	if relayError == nil {
		return
	}

	// 记录第一次调用的失败信息（累计耗时：从请求开始到当前失败的时间，同步记录保证顺序）
	cumulativeDuration := time.Since(totalStartTime).Seconds()
	recordRetryFailureLog(ctx, userId, originalChannelId, originalModel, tokenName, requestID, 0, cumulativeDuration, relayError.Error.Message, originalChannelName, channelHistory)

	// 处理首次失败的渠道错误（包括自动禁用逻辑）
	go processChannelRelayError(ctx, userId, originalChannelId, originalChannelName, originalKeyIndex, relayError, originalModel)

	// 记录所有已失败的渠道ID，用于重试时排除
	failedChannelIds := []int{channelId}
	group := c.GetString("group")
	retryTimes := config.RetryTimes
	if !shouldRetry(c, relayError.StatusCode, relayError.Error.Message) {
		logger.Errorf(ctx, "claude relay error happen, status code is %d, won't retry in this case", relayError.StatusCode)
		retryTimes = 0
	}
	for i := retryTimes; i > 0; i-- {
		// 使用排除已失败渠道的方式选择新渠道，始终选择最高优先级的可用渠道
		channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, originalModel, 0, "", failedChannelIds)
		if err != nil {
			logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed: %v (excludedChannels: %v)", err, failedChannelIds)
			break
		}

		// 获取重试原因 - 直接使用原始错误消息
		retryReason := relayError.Error.Message

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

		// 记录重试使用的渠道，更新上下文中的 channelHistory（在实际调用前追加）
		if historyInterface, exists := c.Get("admin_channel_history"); exists {
			if history, ok := historyInterface.([]int); ok {
				history = append(history, channel.Id)
				c.Set("admin_channel_history", history)
				channelHistory = history
			}
		}

		logger.Infof(ctx, "Using channel #%d to retry Claude request (remain times %d)", channel.Id, i)

		middleware.SetupContextForSelectedChannel(c, channel, originalModel)
		requestBody, _ := common.GetRequestBody(c)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		relayError = controller.RelayClaudeNative(c)
		if relayError == nil {
			// 重试成功，直接返回（无需记录错误日志）
			return
		}

		// 计算累计耗时（从请求开始到当前失败的时间）
		cumulativeDuration = time.Since(totalStartTime).Seconds()
		currentAttempt := retryTimes - i + 1

		channelId = c.GetInt("channel_id")
		channelName := c.GetString("channel_name")
		keyIndex := c.GetInt("key_index")

		// 将本次失败的渠道ID添加到排除列表，避免重复选择
		failedChannelIds = append(failedChannelIds, channelId)

		// 记录本次重试失败的日志（耗时为累计耗时，同步记录保证顺序）
		recordRetryFailureLog(ctx, userId, channel.Id, originalModel, tokenName, requestID, currentAttempt, cumulativeDuration, relayError.Error.Message, channel.Name, channelHistory)

		go processChannelRelayError(ctx, userId, channelId, channelName, keyIndex, relayError, originalModel)
	}

	if relayError != nil {
		// 记录渠道历史到上下文中
		c.Set("admin_channel_history", channelHistory)
		// 注意：不再调用 recordFailedRequestLog，因为每次失败已经单独记录了
		//转换成claude 的错误格式
		c.JSON(relayError.StatusCode, gin.H{
			"type": "error",
			"error": model.ClaudeError{
				Type:    relayError.Error.Type,
				Message: relayError.Error.Message,
			},
		})
	}
}
func RelayResponse(c *gin.Context) {
	ctx := c.Request.Context()

	// 记录整个请求的开始时间，用于计算总耗时和首字时长
	totalStartTime := time.Now()
	// 将总请求开始时间设置到 context 中，供 RelayClaudeNative 使用计算真正的首字时长
	c.Set("total_request_start_time", totalStartTime)

	channelId := c.GetInt("channel_id")
	userId := c.GetInt("id")
	//relayMode := c.GetInt("relay_mode")
	originalModel := c.GetString("original_model")
	originalChannelId := c.GetInt("channel_id")
	originalChannelName := c.GetString("channel_name")
	originalKeyIndex := c.GetInt("key_index")
	tokenName := c.GetString("token_name")

	// 获取或生成 X-Request-ID
	// 如果客户端没有传递，则自动生成：时间戳(YYYYMMDDHHmmss) + 8位UUID
	requestID := c.GetHeader("X-Request-ID")
	if requestID == "" {
		requestID = common.GenerateRequestID()
	}
	c.Set("X-Request-ID", requestID)
	// 同时设置到 Header 中，确保后续处理可以通过 GetHeader 获取
	c.Request.Header.Set("X-Request-ID", requestID)

	// 记录使用的渠道历史，用于添加到日志中
	// 在调用前就初始化并设置，确保成功时也能记录
	channelHistory := []int{originalChannelId}
	c.Set("admin_channel_history", channelHistory)

	relayError := controller.RelayOpenaiResponseNative(c)
	if relayError == nil {
		return
	}

	// 记录第一次调用的失败信息（累计耗时：从请求开始到当前失败的时间，同步记录保证顺序）
	cumulativeDuration := time.Since(totalStartTime).Seconds()
	recordRetryFailureLog(ctx, userId, originalChannelId, originalModel, tokenName, requestID, 0, cumulativeDuration, relayError.Error.Message, originalChannelName, channelHistory)

	// 处理首次失败的渠道错误（包括自动禁用逻辑）
	go processChannelRelayError(ctx, userId, originalChannelId, originalChannelName, originalKeyIndex, relayError, originalModel)

	// 记录所有已失败的渠道ID，用于重试时排除
	failedChannelIds := []int{channelId}

	// 获取客户端传递的 X-Response-ID（用于 Claude 缓存定向）
	claudeResponseID := c.GetHeader("X-Response-ID")

	group := c.GetString("group")
	retryTimes := config.RetryTimes
	if !shouldRetry(c, relayError.StatusCode, relayError.Error.Message) {
		logger.Errorf(ctx, "claude relay error happen, status code is %d, won't retry in this case", relayError.StatusCode)
		retryTimes = 0
	}
	for i := retryTimes; i > 0; i-- {
		// 使用排除已失败渠道的方式选择新渠道，始终选择最高优先级的可用渠道
		channel, err := dbmodel.CacheGetRandomSatisfiedChannel(group, originalModel, 0, claudeResponseID, failedChannelIds)
		if err != nil {
			logger.Errorf(ctx, "CacheGetRandomSatisfiedChannel failed: %v (excludedChannels: %v)", err, failedChannelIds)
			break
		}

		// 获取重试原因 - 直接使用原始错误消息
		retryReason := relayError.Error.Message

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

		// 记录重试使用的渠道，更新上下文中的 channelHistory（在实际调用前追加）
		if historyInterface, exists := c.Get("admin_channel_history"); exists {
			if history, ok := historyInterface.([]int); ok {
				history = append(history, channel.Id)
				c.Set("admin_channel_history", history)
				channelHistory = history
			}
		}

		logger.Infof(ctx, "Using channel #%d to retry Claude request (remain times %d)", channel.Id, i)

		middleware.SetupContextForSelectedChannel(c, channel, originalModel)
		requestBody, _ := common.GetRequestBody(c)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		relayError = controller.RelayOpenaiResponseNative(c)
		if relayError == nil {
			// 重试成功，直接返回（无需记录错误日志）
			return
		}

		// 计算累计耗时（从请求开始到当前失败的时间）
		cumulativeDuration = time.Since(totalStartTime).Seconds()
		currentAttempt := retryTimes - i + 1

		channelId = c.GetInt("channel_id")
		channelName := c.GetString("channel_name")
		keyIndex := c.GetInt("key_index")

		// 记录失败的渠道ID，下次重试时排除
		failedChannelIds = append(failedChannelIds, channelId)

		// 记录本次重试失败的日志（耗时为累计耗时，同步记录保证顺序）
		recordRetryFailureLog(ctx, userId, channel.Id, originalModel, tokenName, requestID, currentAttempt, cumulativeDuration, relayError.Error.Message, channel.Name, channelHistory)

		go processChannelRelayError(ctx, userId, channelId, channelName, keyIndex, relayError, originalModel)
	}

	if relayError != nil {
		// 记录渠道历史到上下文中
		c.Set("admin_channel_history", channelHistory)
		// 注意：不再调用 recordFailedRequestLog，因为每次失败已经单独记录了
		//转换成claude 的错误格式
		c.JSON(relayError.StatusCode, gin.H{
			"error": gin.H{
				"message": relayError.Error.Message,
				"code":    relayError.Error.Code,
				"status":  relayError.Error.Status,
			},
		})
	}
}

// CountTokensRequest Claude count_tokens 请求结构
type CountTokensRequest struct {
	Model    string          `json:"model"`
	Messages json.RawMessage `json:"messages"`
	System   json.RawMessage `json:"system,omitempty"`
	Tools    json.RawMessage `json:"tools,omitempty"`
}

// CountTokensResponse Claude count_tokens 响应结构
type CountTokensResponse struct {
	InputTokens int `json:"input_tokens"`
}

// RelayClaudeCountTokens 处理 Claude count_tokens 请求
// 该接口用于在发送消息前计算 token 数量
func RelayClaudeCountTokens(c *gin.Context) {
	ctx := c.Request.Context()

	// 1. 读取并解析请求体
	bodyBytes, err := common.GetRequestBody(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "invalid_request_error",
				"message": "Failed to read request body: " + err.Error(),
			},
		})
		return
	}

	var req CountTokensRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "invalid_request_error",
				"message": "Invalid request format: " + err.Error(),
			},
		})
		return
	}

	if req.Model == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "invalid_request_error",
				"message": "Model is required",
			},
		})
		return
	}

	// 2. 获取用户信息和分组
	userId := c.GetInt("id")
	group, err := dbmodel.CacheGetUserGroup(userId)
	if err != nil {
		logger.Errorf(ctx, "Failed to get user group for user %d: %v", userId, err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "api_error",
				"message": "Failed to get user group",
			},
		})
		return
	}

	// 3. 使用带能力筛选的渠道选择，只选择支持 count_tokens 的渠道
	channel, err := dbmodel.CacheGetRandomSatisfiedChannelWithCapability(
		group,
		req.Model,
		dbmodel.FilterSupportCountTokens,
		0,  // skipPriorityLevels
		"", // responseID
	)
	if err != nil {
		logger.Errorf(ctx, "No channel available with count_tokens support for model %s: %v", req.Model, err)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "api_error",
				"message": "No channel available with count_tokens support: " + err.Error(),
			},
		})
		return
	}

	logger.Infof(ctx, "Selected channel #%d (%s) for count_tokens request, model: %s", channel.Id, channel.Name, req.Model)

	// 4. 根据渠道类型转发请求
	switch channel.Type {
	case common.ChannelTypeAnthropic:
		relayAnthropicCountTokens(c, channel, bodyBytes)
	case common.ChannelTypeAwsClaude:
		relayAwsCountTokens(c, channel, bodyBytes, req.Model)
	default:
		c.JSON(http.StatusBadRequest, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "invalid_request_error",
				"message": fmt.Sprintf("Channel type %d does not support count_tokens", channel.Type),
			},
		})
	}
}

// relayAnthropicCountTokens 处理 Anthropic 原生 API 的 count_tokens 请求
func relayAnthropicCountTokens(c *gin.Context, channel *dbmodel.Channel, requestBody []byte) {
	// 构建请求 URL
	baseURL := channel.GetBaseURL()
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	targetURL := baseURL + "/v1/messages/count_tokens"

	// 创建代理请求
	proxyReq, err := http.NewRequest("POST", targetURL, bytes.NewBuffer(requestBody))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "api_error",
				"message": "Failed to create request: " + err.Error(),
			},
		})
		return
	}

	// 获取实际使用的 key
	key := channel.Key
	if channel.MultiKeyInfo.IsMultiKey {
		var keyIndex int
		key, keyIndex, err = channel.GetNextAvailableKey()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"type": "error",
				"error": gin.H{
					"type":    "api_error",
					"message": "Failed to get available key: " + err.Error(),
				},
			})
			return
		}
		logger.Infof(c.Request.Context(), "Using key index %d for count_tokens request on channel #%d", keyIndex, channel.Id)
	}

	// 设置请求头
	proxyReq.Header.Set("Content-Type", "application/json")
	proxyReq.Header.Set("x-api-key", key)
	proxyReq.Header.Set("anthropic-version", "2023-06-01")

	// 发送请求
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(proxyReq)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "api_error",
				"message": "Failed to send request: " + err.Error(),
			},
		})
		return
	}
	defer resp.Body.Close()

	// 读取响应体
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "api_error",
				"message": "Failed to read response: " + err.Error(),
			},
		})
		return
	}

	// 转发响应
	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
}

// relayAwsCountTokens 处理 AWS Bedrock 的 count_tokens 请求
func relayAwsCountTokens(c *gin.Context, channel *dbmodel.Channel, requestBody []byte, modelName string) {
	ctx := c.Request.Context()

	// 1. 解析 AWS 凭证
	key := channel.Key
	if channel.MultiKeyInfo.IsMultiKey {
		var keyIndex int
		var err error
		key, keyIndex, err = channel.GetNextAvailableKey()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"type": "error",
				"error": gin.H{
					"type":    "api_error",
					"message": "Failed to get available key: " + err.Error(),
				},
			})
			return
		}
		logger.Infof(ctx, "Using key index %d for AWS count_tokens request on channel #%d", keyIndex, channel.Id)
	}

	parts := strings.Split(key, "|")
	var accessKey, secretKey, region string

	if len(parts) == 3 {
		accessKey = parts[0]
		secretKey = parts[1]
		region = parts[2]
	} else {
		cfg, _ := channel.LoadConfig()
		accessKey = cfg.AK
		secretKey = cfg.SK
		region = cfg.Region
	}

	if accessKey == "" || secretKey == "" || region == "" {
		c.JSON(http.StatusInternalServerError, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "api_error",
				"message": "AWS credentials not properly configured",
			},
		})
		return
	}

	// 2. 创建 AWS Bedrock Runtime Client
	awsClient := bedrockruntime.New(bedrockruntime.Options{
		Region:      region,
		Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	})

	// 3. 获取 AWS 模型 ID
	awsModelId := getAwsModelIdForCountTokens(modelName)

	// 4. 构建 AWS Bedrock 格式的请求体
	// 需要将 Anthropic 格式转换为 AWS Bedrock 格式
	awsRequestBody, err := convertToAwsBedrockFormat(requestBody)
	if err != nil {
		logger.Errorf(ctx, "Failed to convert request to AWS format: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "invalid_request_error",
				"message": "Failed to convert request format: " + err.Error(),
			},
		})
		return
	}

	// 5. 构建 CountTokens 请求
	// AWS CountTokens API 使用 InvokeModel 格式的输入
	countTokensInput := &bedrockruntime.CountTokensInput{
		ModelId: aws.String(awsModelId),
		Input: &types.CountTokensInputMemberInvokeModel{
			Value: types.InvokeModelTokensRequest{
				Body: awsRequestBody,
			},
		},
	}

	// 6. 调用 CountTokens API
	result, err := awsClient.CountTokens(ctx, countTokensInput)
	if err != nil {
		logger.Errorf(ctx, "AWS CountTokens API error: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "api_error",
				"message": "AWS CountTokens API error: " + err.Error(),
			},
		})
		return
	}

	// 7. 返回结果（与 Anthropic 格式兼容）
	c.JSON(http.StatusOK, gin.H{
		"input_tokens": aws.ToInt32(result.InputTokens),
	})
}

// convertToAwsBedrockFormat 将 Anthropic 请求格式转换为 AWS Bedrock 格式
// 注意：AWS Bedrock CountTokens API 使用 InvokeModel 格式，但只需要计算 token 相关的字段
func convertToAwsBedrockFormat(requestBody []byte) ([]byte, error) {
	// 解析原始请求
	var anthropicReq struct {
		Model    string          `json:"model"`
		Messages json.RawMessage `json:"messages"`
		System   json.RawMessage `json:"system,omitempty"`
		Tools    json.RawMessage `json:"tools,omitempty"`
	}
	if err := json.Unmarshal(requestBody, &anthropicReq); err != nil {
		return nil, err
	}

	// 构建 AWS Bedrock 格式的请求
	// AWS Bedrock CountTokens API 需要 InvokeModel 格式的 body
	// 参考: https://docs.aws.amazon.com/bedrock/latest/userguide/count-tokens.html
	awsReq := map[string]interface{}{
		"anthropic_version": "bedrock-2023-05-31",
		// max_tokens 是 InvokeModel 的必需字段，设置最小值
		// CountTokens 只计算输入 token，不会实际生成输出
		"max_tokens": 1,
	}

	if anthropicReq.Messages != nil {
		awsReq["messages"] = json.RawMessage(anthropicReq.Messages)
	}

	if anthropicReq.System != nil && len(anthropicReq.System) > 0 {
		awsReq["system"] = json.RawMessage(anthropicReq.System)
	}

	if anthropicReq.Tools != nil && len(anthropicReq.Tools) > 0 {
		awsReq["tools"] = json.RawMessage(anthropicReq.Tools)
	}

	return json.Marshal(awsReq)
}

// getAwsModelIdForCountTokens 获取用于 CountTokens 的 AWS 模型 ID
func getAwsModelIdForCountTokens(requestModel string) string {
	// AWS 模型 ID 映射表
	awsModelIDMap := map[string]string{
		"claude-instant-1.2":                  "anthropic.claude-instant-v1",
		"claude-2.0":                          "anthropic.claude-v2",
		"claude-2.1":                          "anthropic.claude-v2:1",
		"claude-3-sonnet-20240229":            "anthropic.claude-3-sonnet-20240229-v1:0",
		"claude-3-opus-20240229":              "anthropic.claude-3-opus-20240229-v1:0",
		"claude-3-haiku-20240307":             "anthropic.claude-3-haiku-20240307-v1:0",
		"claude-3-5-sonnet-20240620":          "anthropic.claude-3-5-sonnet-20240620-v1:0",
		"claude-3-5-sonnet-20241022":          "anthropic.claude-3-5-sonnet-20241022-v2:0",
		"claude-3-5-haiku-20241022":           "anthropic.claude-3-5-haiku-20241022-v1:0",
		"claude-3-7-sonnet-20250219":          "anthropic.claude-3-7-sonnet-20250219-v1:0",
		"claude-sonnet-4-20250514":            "anthropic.claude-sonnet-4-20250514-v1:0",
		"claude-opus-4-20250514":              "anthropic.claude-opus-4-20250514-v1:0",
		"claude-opus-4-1-20250805":            "anthropic.claude-opus-4-1-20250805-v1:0",
		"claude-sonnet-4-5-20250929":          "anthropic.claude-sonnet-4-5-20250929-v1:0",
		"claude-haiku-4-5-20251001":           "anthropic.claude-haiku-4-5-20251001-v1:0",
		"claude-opus-4-5-20251101":            "anthropic.claude-opus-4-5-20251101-v1:0",
		"claude-opus-4-6":                     "anthropic.claude-opus-4-6-v1",
		"claude-3-7-sonnet-20250219-thinking": "anthropic.claude-3-7-sonnet-20250219-v1:0",
		"claude-sonnet-4-20250514-thinking":   "anthropic.claude-sonnet-4-20250514-v1:0",
		"claude-opus-4-20250514-thinking":     "anthropic.claude-opus-4-20250514-v1:0",
		"claude-opus-4-1-20250805-thinking":   "anthropic.claude-opus-4-1-20250805-v1:0",
		"claude-sonnet-4-5-20250929-thinking": "anthropic.claude-sonnet-4-5-20250929-v1:0",
		"claude-haiku-4-5-20251001-thinking":  "anthropic.claude-haiku-4-5-20251001-v1:0",
		"claude-opus-4-5-20251101-thinking":   "anthropic.claude-opus-4-5-20251101-v1:0",
		"claude-opus-4-6-thinking":            "anthropic.claude-opus-4-6-v1",
	}

	if awsModelID, ok := awsModelIDMap[requestModel]; ok {
		return awsModelID
	}
	// 如果已经是 AWS 模型 ID 格式，直接返回
	if strings.Contains(requestModel, "anthropic.") {
		return requestModel
	}
	return requestModel
}
