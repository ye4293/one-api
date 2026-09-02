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
	// 发送飞书通知
	if config.FeishuWebhookUrls != "" {
		err := message.SendFeishuNotification(subject, content)
		if err != nil {
			logger.SysError(fmt.Sprintf("failed to send feishu notification: %s", err.Error()))
		}
	}

	notifyRootUserWithoutFeishu(subject, content)
}

// notifyRootUserWithoutFeishu 发送通知（不包括飞书，用于避免重复发送）
func notifyRootUserWithoutFeishu(subject string, content string) {
	// 发送 MessagePusher 通知
	if config.MessagePusherAddress != "" {
		err := message.SendMessage(subject, content, content)
		if err != nil {
			logger.SysError(fmt.Sprintf("failed to send message: %s", err.Error()))
		} else {
			return
		}
	}

	// 发送邮件通知
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

	if channel.MultiKeyInfo.IsMultiKey {
		// 对于多key渠道，不应该直接禁用整个渠道
		// 记录警告信息，需要管理员手动处理
		logger.SysLog(fmt.Sprintf("Multi-key channel #%d (%s) has external issues: %s (状态码: %d). Not auto-disabling the entire channel as it may have working keys. Manual intervention may be required.",
			channelId, channelName, reason, statusCode))
		return
	}

	// 单key渠道使用内联逻辑，避免重复获取渠道信息
	disableChannelInternalWithStatusCode(channel, channelId, channelName, reason, modelName, statusCode)
}

// disableChannelInternal 内部禁用函数，接受已获取的channel对象
func disableChannelInternal(channel *model.Channel, channelId int, channelName string, reason string, modelName string) {
	disableChannelInternalWithStatusCode(channel, channelId, channelName, reason, modelName, 0)
}

// disableChannelInternalWithStatusCode 内部禁用函数，包含状态码
func disableChannelInternalWithStatusCode(channel *model.Channel, channelId int, channelName string, reason string, modelName string, statusCode int) {
	if !channel.AutoDisabled {
		logger.SysLog(fmt.Sprintf("channel #%d (%s) should be disabled but auto-disable is turned off, reason: %s", channelId, channelName, reason))
		return
	}

	disabled, err := model.AutoDisableChannelById(channelId, reason, modelName)
	if err != nil {
		logger.SysError(fmt.Sprintf("Failed to auto disable channel %d: %s", channelId, err.Error()))
		return
	}

	if !disabled {
		logger.SysLog(fmt.Sprintf("channel #%d (%s) auto disable skipped because it was already disabled or not eligible, reason: %s", channelId, channelName, reason))
		return
	}

	logger.SysLog(fmt.Sprintf("channel #%d has been disabled: %s", channelId, reason))

	// 发送飞书通知（带详细信息）
	if config.FeishuWebhookUrls != "" {
		err := message.SendFeishuChannelDisableNotification(channelId, channelName, statusCode, reason, modelName)
		if err != nil {
			logger.SysError(fmt.Sprintf("failed to send feishu channel disable notification: %s", err.Error()))
		}
	}

	// 发送邮件和其他通知
	subject := fmt.Sprintf("渠道「%s」（#%d）已被禁用", channelName, channelId)
	content := fmt.Sprintf(`
<h3>渠道自动禁用通知</h3>
<p><strong>渠道名称：</strong>%s</p>
<p><strong>渠道ID：</strong>#%d</p>
<p><strong>触发模型：</strong>%s</p>
<p><strong>状态码：</strong>%d</p>
<p><strong>禁用原因：</strong>%s</p>
<p><strong>禁用时间：</strong>%s</p>
<hr>
<p>该渠道因出现错误已被系统自动禁用，请检查渠道配置和密钥的有效性。</p>
`, channelName, channelId, modelName, statusCode, reason, time.Now().Format("2006-01-02 15:04:05"))
	notifyRootUserWithoutFeishu(subject, content)
}

// DisableModelOnChannelWithStatusCode 模型级自动禁用：只禁用该渠道上的该模型；
// 当该渠道所有模型都被禁用时，回落到整渠道禁用（走完整通知）。仅用于单 Key 渠道。
func DisableModelOnChannelWithStatusCode(channelId int, channelName string, reason string, modelName string, statusCode int) {
	channel, err := model.GetChannelById(channelId, true)
	if err != nil {
		logger.SysError(fmt.Sprintf("Failed to get channel %d: %s", channelId, err.Error()))
		return
	}

	if !channel.AutoDisabled {
		logger.SysLog(fmt.Sprintf("channel #%d (%s) model %s should be disabled but auto-disable is turned off, reason: %s", channelId, channelName, modelName, reason))
		return
	}

	// 多 Key 渠道不做模型级禁用，交由既有 key 级逻辑处理（理论上调用方已过滤）
	if channel.MultiKeyInfo.IsMultiKey {
		logger.SysLog(fmt.Sprintf("channel #%d (%s) is multi-key, skip model-scope disable for model %s", channelId, channelName, modelName))
		return
	}

	if err := model.AutoDisableModelOnChannel(channelId, modelName, reason); err != nil {
		logger.SysError(fmt.Sprintf("Failed to model-scope disable channel %d model %s: %s", channelId, modelName, err.Error()))
		return
	}
	// 「是否禁整个渠道」由统一恢复链路 recoverAutoDisabledModels 尾部按
	// 「最近使用的模型全部被自动禁用且超过抖动窗口」判定并触发 DisableChannelByRecentUsage，
	// 不再由此处的模型级禁用同步触发。这样避免瞬时抖动引发误禁，也让「渠道配了 60 个模型
	// 但只有 5 个真实使用」的场景能正确禁掉。
	// 参见 docs/plans/2026-08-21-channel-disable-by-recent-usage.md
}

// composeUsageDisableReason 组装「最近使用的模型全部被自动禁用」的整渠道禁用原因。
//
// lastModel/lastReason 为该渠道最后一条被模型级禁用的模型与其落库的真实原因
// （abilities.auto_disabled_reason），任一为空时（查询失败、存量数据列默认空）
// 回退到通用文案，保证新功能不引入新的失败路径。
func composeUsageDisableReason(usedModels int, lastModel, lastReason string) string {
	reason := fmt.Sprintf("最近使用中的 %d 个模型全部被自动禁用", usedModels)
	if lastModel == "" || lastReason == "" {
		return reason
	}
	return fmt.Sprintf("%s，最后模型禁用原因：%s（模型：%s）", reason, lastReason, lastModel)
}

// DisableChannelByRecentUsage 由统一恢复链路在判定「最近使用的模型全部被自动禁用」后调用。
// 与 DisableChannelSafelyWithStatusCode 的区别：不由单次上游请求错误驱动，
// statusCode 传 0；reason 拼接最后被禁模型的真实原因，modelName 传该模型名。
// 参见 docs/plans/2026-08-31-ability-disable-reason.md
func DisableChannelByRecentUsage(channelId int, usedModels int) {
	channel, err := model.GetChannelById(channelId, true)
	if err != nil {
		logger.SysError(fmt.Sprintf("Failed to get channel %d for usage-based disable: %s", channelId, err.Error()))
		return
	}
	if channel.MultiKeyInfo.IsMultiKey {
		// 多 Key 渠道不由本判定禁用，与 DisableChannelSafelyWithStatusCode 语义一致。
		logger.SysLog(fmt.Sprintf("multi-key channel #%d (%s) not auto-disabled by usage-based rule", channelId, channel.Name))
		return
	}
	lastModel, lastReason, reasonErr := model.GetLatestAutoDisabledModelReason(channelId)
	if reasonErr != nil {
		// 查询失败不阻塞禁用本身，回退通用文案
		logger.SysError(fmt.Sprintf("Failed to get latest auto-disabled model reason for channel %d: %s", channelId, reasonErr.Error()))
		lastModel, lastReason = "", ""
	}
	reason := composeUsageDisableReason(usedModels, lastModel, lastReason)
	disableChannelInternalWithStatusCode(channel, channelId, channel.Name, reason, lastModel, 0)
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

	disableChannelInternalWithStatusCode(channel, channelId, channelName, reason, modelName, statusCode)
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
			// 发送飞书通知（带详细信息）
			if config.FeishuWebhookUrls != "" {
				err := message.SendFeishuKeyDisableNotification(
					notification.ChannelId,
					notification.ChannelName,
					notification.KeyIndex,
					notification.MaskedKey,
					notification.StatusCode,
					notification.ErrorMessage,
				)
				if err != nil {
					logger.SysError(fmt.Sprintf("failed to send feishu key disable notification: %s", err.Error()))
				}
			}

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

			// 发送邮件和其他通知（不包括飞书，避免重复发送）
			notifyRootUserWithoutFeishu(subject, content)
		}
	}()

	// 启动渠道级别的禁用通知监听器
	go func() {
		for notification := range model.ChannelDisableNotificationChan {
			// 发送飞书通知（带详细信息）
			if config.FeishuWebhookUrls != "" {
				err := message.SendFeishuChannelFullDisableNotification(
					notification.ChannelId,
					notification.ChannelName,
					notification.Reason,
				)
				if err != nil {
					logger.SysError(fmt.Sprintf("failed to send feishu channel full disable notification: %s", err.Error()))
				}
			}

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

			// 发送邮件和其他通知（不包括飞书，避免重复发送）
			notifyRootUserWithoutFeishu(subject, content)
		}
	}()
}
