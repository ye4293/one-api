package monitor

import (
	"fmt"
	"time"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/common/message"
	"github.com/songquanpeng/one-api/model"
)

func notifyRootUser(subject string, content string) {
	if config.MessagePusherAddress != "" {
		err := message.SendMessage(subject, content, content)
		if err != nil {
			logger.SysError(fmt.Sprintf("failed to send message: %s", err.Error()))
		} else {
			return
		}
	}
	if config.RootUserEmail == "" {
		config.RootUserEmail = model.GetRootUserEmail()
	}
	err := message.SendEmail(subject, config.RootUserEmail, content)
	if err != nil {
		logger.SysError(fmt.Sprintf("failed to send email: %s", err.Error()))
	}
}

// DisableChannelSafely disable & notify with multi-key channel protection
func DisableChannelSafely(channelId int, channelName string, reason string, modelName string) {
	DisableChannelSafelyWithStatusCode(channelId, channelName, reason, modelName, 0)
}

// DisableChannelSafelyWithStatusCode disable & notify with multi-key channel protection, including status code
func DisableChannelSafelyWithStatusCode(channelId int, channelName string, reason string, modelName string, statusCode int) {
	// 检查渠道信息
	channel, err := model.GetChannelById(channelId, true)
	if err != nil {
		logger.SysError(fmt.Sprintf("Failed to get channel %d: %s", channelId, err.Error()))
		return
	}

	// 构建包含状态码的完整禁用原因
	fullReason := reason
	if statusCode > 0 {
		fullReason = fmt.Sprintf("%s (状态码: %d)", reason, statusCode)
	}

	if channel.MultiKeyInfo.IsMultiKey {
		// 对于多key渠道，不应该直接禁用整个渠道
		// 记录警告信息，需要管理员手动处理
		logger.SysLog(fmt.Sprintf("Multi-key channel #%d (%s) has external issues: %s. Not auto-disabling the entire channel as it may have working keys. Manual intervention may be required.",
			channelId, channelName, fullReason))
		return
	}

	// 单key渠道使用内联逻辑，避免重复获取渠道信息
	disableChannelInternal(channel, channelId, channelName, fullReason, modelName)
}

// disableChannelInternal 内部禁用函数，接受已获取的channel对象
func disableChannelInternal(channel *model.Channel, channelId int, channelName string, reason string, modelName string) {
	if !channel.AutoDisabled {
		logger.SysLog(fmt.Sprintf("channel #%d (%s) should be disabled but auto-disable is turned off, reason: %s", channelId, channelName, reason))
		return
	}

	// 记录禁用原因和时间
	currentTime := time.Now().Unix()
	channel.AutoDisabledReason = &reason
	channel.AutoDisabledTime = &currentTime
	channel.AutoDisabledModel = &modelName
	channel.Status = common.ChannelStatusAutoDisabled

	// 保存到数据库
	err := channel.Update()
	if err != nil {
		logger.SysError(fmt.Sprintf("Failed to update channel %d with disable reason: %s", channelId, err.Error()))
		// 如果更新失败，至少要更新状态
		err = model.UpdateChannelStatusById(channelId, common.ChannelStatusAutoDisabled)
		if err != nil {
			logger.SysError(fmt.Sprintf("Failed to disable channel %d: %s", channelId, err.Error()))
		}
	}

	logger.SysLog(fmt.Sprintf("channel #%d has been disabled: %s", channelId, reason))
	subject := fmt.Sprintf("渠道「%s」（#%d）已被禁用", channelName, channelId)
	content := fmt.Sprintf(`
<h3>渠道自动禁用通知</h3>
<p><strong>渠道名称：</strong>%s</p>
<p><strong>渠道ID：</strong>#%d</p>
<p><strong>禁用原因：</strong>%s</p>
<p><strong>禁用时间：</strong>%s</p>
<hr>
<p>该渠道因出现错误已被系统自动禁用，请检查渠道配置和密钥的有效性。</p>
`, channelName, channelId, reason, time.Now().Format("2006-01-02 15:04:05"))
	notifyRootUser(subject, content)
}

// DisableChannel disable & notify
func DisableChannel(channelId int, channelName string, reason string, modelName string) {
	DisableChannelWithStatusCode(channelId, channelName, reason, modelName, 0)
}

// DisableChannelWithStatusCode disable & notify, including status code
func DisableChannelWithStatusCode(channelId int, channelName string, reason string, modelName string, statusCode int) {
	// 检查渠道是否允许自动禁用
	channel, err := model.GetChannelById(channelId, true)
	if err != nil {
		logger.SysError(fmt.Sprintf("Failed to get channel %d: %s", channelId, err.Error()))
		return
	}

	// 构建包含状态码的完整禁用原因
	fullReason := reason
	if statusCode > 0 {
		fullReason = fmt.Sprintf("%s (状态码: %d)", reason, statusCode)
	}

	disableChannelInternal(channel, channelId, channelName, fullReason, modelName)
}

func MetricDisableChannel(channelId int, successRate float64) {
	// 检查渠道是否允许自动禁用
	channel, err := model.GetChannelById(channelId, true)
	if err != nil {
		logger.SysError(fmt.Sprintf("Failed to get channel %d: %s", channelId, err.Error()))
		return
	}

	if !channel.AutoDisabled {
		logger.SysLog(fmt.Sprintf("channel #%d should be disabled due to low success rate %.2f%% but auto-disable is turned off", channelId, successRate*100))
		return
	}

	// 对于多key渠道，不应该基于整体成功率直接禁用整个渠道
	// 因为可能只是部分key有问题，应该让单个key的错误处理来决定
	if channel.MultiKeyInfo.IsMultiKey {
		logger.SysLog(fmt.Sprintf("Multi-key channel #%d has low success rate %.2f%%, but not auto-disabling the entire channel. Individual key errors will be handled separately. Manual review recommended.",
			channelId, successRate*100))

		// 发送通知但不禁用
		subject := fmt.Sprintf("多Key渠道 #%d 成功率过低", channelId)
		content := fmt.Sprintf("多Key渠道（#%d）在最近 %d 次调用中成功率为 %.2f%%，低于阈值 %.2f%%。由于这是多Key渠道，系统未自动禁用，请手动检查各个Key的状态。",
			channelId, config.MetricQueueSize, successRate*100, config.MetricSuccessRateThreshold*100)
		notifyRootUser(subject, content)
		return
	}

	// 单key渠道使用禁用逻辑
	reason := fmt.Sprintf("success rate %.2f%% below threshold %.2f%%", successRate*100, config.MetricSuccessRateThreshold*100)
	modelName := "N/A (Metric)" // 成功率禁用没有特定的模型名称
	disableChannelInternal(channel, channelId, channel.Name, reason, modelName)
}

// EnableChannel enable & notify
func EnableChannel(channelId int, channelName string) {
	err := model.UpdateChannelStatusById(channelId, common.ChannelStatusEnabled)
	if err != nil {
		logger.SysError(fmt.Sprintf("Failed to enable channel %d: %s", channelId, err.Error()))
	}
	logger.SysLog(fmt.Sprintf("channel #%d has been enabled", channelId))
	subject := fmt.Sprintf("渠道「%s」（#%d）已被启用", channelName, channelId)
	content := fmt.Sprintf("渠道「%s」（#%d）已被启用", channelName, channelId)
	notifyRootUser(subject, content)
}

// StartKeyNotificationListener 启动Key禁用通知监听器
func StartKeyNotificationListener() {
	// 启动Key级别的禁用通知监听器
	go func() {
		for notification := range model.KeyDisableNotificationChan {
			// 构建邮件主题和内容
			subject := fmt.Sprintf("多Key渠道「%s」（#%d）中的Key已被禁用", notification.ChannelName, notification.ChannelId)
			content := fmt.Sprintf(`
<h3>多Key渠道Key自动禁用通知</h3>
<p><strong>渠道名称：</strong>%s</p>
<p><strong>渠道ID：</strong>#%d</p>
<p><strong>被禁用的Key：</strong>Key #%d (%s)</p>
<p><strong>禁用原因：</strong>%s</p>
<p><strong>状态码：</strong>%d</p>
<p><strong>禁用时间：</strong>%s</p>
<hr>
<p>该Key因出现错误已被系统自动禁用，请检查Key的有效性。如果所有Key都被禁用，整个渠道也将被禁用。</p>
`, notification.ChannelName, notification.ChannelId, notification.KeyIndex, notification.MaskedKey,
				notification.ErrorMessage, notification.StatusCode, notification.DisabledTime.Format("2006-01-02 15:04:05"))

			// 发送邮件通知
			notifyRootUser(subject, content)
		}
	}()

	// 启动渠道级别的禁用通知监听器
	go func() {
		for notification := range model.ChannelDisableNotificationChan {
			// 构建邮件主题和内容
			subject := fmt.Sprintf("多Key渠道「%s」（#%d）已被完全禁用", notification.ChannelName, notification.ChannelId)
			content := fmt.Sprintf(`
<h3>多Key渠道完全禁用通知</h3>
<p><strong>渠道名称：</strong>%s</p>
<p><strong>渠道ID：</strong>#%d</p>
<p><strong>禁用原因：</strong>%s</p>
<p><strong>禁用时间：</strong>%s</p>
<hr>
<p>该渠道的所有Key都已被禁用，因此整个渠道已被系统自动禁用。请检查并修复所有Key的问题后重新启用。</p>
`, notification.ChannelName, notification.ChannelId, notification.Reason, notification.DisabledTime.Format("2006-01-02 15:04:05"))

			// 发送邮件通知
			notifyRootUser(subject, content)
		}
	}()
}
