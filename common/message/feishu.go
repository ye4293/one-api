package message

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/songquanpeng/one-api/common/config"
)

// 复用 HTTP 客户端以提高性能
var feishuClient = &http.Client{Timeout: 10 * time.Second}

// SendFeishuNotification 发送飞书通知
// 支持多个 Webhook URL，用换行符分隔
func SendFeishuNotification(title string, content string) error {
	if config.FeishuWebhookUrls == "" {
		return nil // 未配置飞书 Webhook，静默返回
	}

	// 在标题前加入系统名称标识，方便区分不同站点
	titleWithSystem := title
	if config.SystemName != "" {
		titleWithSystem = fmt.Sprintf("[%s] %s", config.SystemName, title)
	}

	// 构建飞书卡片消息
	feishuMsg := buildFeishuCardMessage(titleWithSystem, content, "red")

	return sendToFeishuWebhooks(feishuMsg)
}

// SendFeishuChannelDisableNotification 发送渠道禁用通知到飞书
func SendFeishuChannelDisableNotification(channelId int, channelName string, statusCode int, reason string, modelName string) error {
	if config.FeishuWebhookUrls == "" {
		return nil // 未配置飞书 Webhook，静默返回
	}

	title := fmt.Sprintf("[%s] 🚨 渠道「%s」(#%d) 已被禁用", config.SystemName, channelName, channelId)

	// 构建详细内容
	content := fmt.Sprintf(
		"**渠道ID：** %d\n"+
			"**渠道名称：** %s\n"+
			"**触发模型：** %s\n"+
			"**状态码：** %d\n"+
			"**错误详情：** %s\n"+
			"**禁用时间：** %s",
		channelId,
		channelName,
		modelName,
		statusCode,
		reason,
		time.Now().Format("2006-01-02 15:04:05"),
	)

	feishuMsg := buildFeishuCardMessage(title, content, "red")
	return sendToFeishuWebhooks(feishuMsg)
}

// SendFeishuKeyDisableNotification 发送 Key 禁用通知到飞书
func SendFeishuKeyDisableNotification(channelId int, channelName string, keyIndex int, maskedKey string, statusCode int, reason string) error {
	if config.FeishuWebhookUrls == "" {
		return nil // 未配置飞书 Webhook，静默返回
	}

	title := fmt.Sprintf("[%s] ⚠️ 渠道「%s」(#%d) 中的 Key 已被禁用", config.SystemName, channelName, channelId)

	// 构建详细内容
	content := fmt.Sprintf(
		"**渠道ID：** %d\n"+
			"**渠道名称：** %s\n"+
			"**被禁用Key：** Key #%d (%s)\n"+
			"**状态码：** %d\n"+
			"**错误详情：** %s\n"+
			"**禁用时间：** %s",
		channelId,
		channelName,
		keyIndex,
		maskedKey,
		statusCode,
		reason,
		time.Now().Format("2006-01-02 15:04:05"),
	)

	feishuMsg := buildFeishuCardMessage(title, content, "orange")
	return sendToFeishuWebhooks(feishuMsg)
}

// SendFeishuChannelFullDisableNotification 发送多Key渠道完全禁用通知到飞书
func SendFeishuChannelFullDisableNotification(channelId int, channelName string, reason string) error {
	if config.FeishuWebhookUrls == "" {
		return nil // 未配置飞书 Webhook，静默返回
	}

	title := fmt.Sprintf("[%s] 🔴 多Key渠道「%s」(#%d) 已被完全禁用", config.SystemName, channelName, channelId)

	// 构建详细内容
	content := fmt.Sprintf(
		"**渠道ID：** %d\n"+
			"**渠道名称：** %s\n"+
			"**禁用原因：** %s\n"+
			"**禁用时间：** %s\n\n"+
			"该渠道的所有Key都已被禁用，整个渠道已被系统自动禁用。",
		channelId,
		channelName,
		reason,
		time.Now().Format("2006-01-02 15:04:05"),
	)

	feishuMsg := buildFeishuCardMessage(title, content, "red")
	return sendToFeishuWebhooks(feishuMsg)
}

// sendToFeishuWebhooks 发送消息到所有配置的飞书 Webhook
func sendToFeishuWebhooks(feishuMsg map[string]interface{}) error {
	if config.FeishuWebhookUrls == "" {
		return nil
	}

	// 支持多个 Webhook URL，用换行符分隔
	webhookUrls := strings.Split(config.FeishuWebhookUrls, "\n")

	jsonData, err := json.Marshal(feishuMsg)
	if err != nil {
		return fmt.Errorf("构建飞书消息失败: %s", err.Error())
	}

	successCount := 0
	var lastError string

	for _, webhookUrl := range webhookUrls {
		webhookUrl = strings.TrimSpace(webhookUrl)
		if webhookUrl == "" {
			continue
		}

		err := sendSingleFeishuRequest(webhookUrl, jsonData)
		if err != nil {
			lastError = err.Error()
		} else {
			successCount++
		}
	}

	if successCount == 0 && lastError != "" {
		return fmt.Errorf("所有飞书 Webhook 发送失败: %s", lastError)
	}

	return nil
}

// sendSingleFeishuRequest 发送单个飞书请求
func sendSingleFeishuRequest(webhookUrl string, jsonData []byte) error {
	resp, err := feishuClient.Post(webhookUrl, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("发送失败: %s", err.Error())
	}
	defer resp.Body.Close()

	// 解析飞书响应
	var feishuResp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&feishuResp); err != nil {
		// 如果无法解析响应，但 HTTP 状态码正常，也认为成功
		if resp.StatusCode == http.StatusOK {
			return nil
		}
		return fmt.Errorf("解析响应失败，HTTP状态码: %d", resp.StatusCode)
	}

	if feishuResp.Code != 0 {
		return fmt.Errorf("飞书返回错误: %s", feishuResp.Msg)
	}

	return nil
}

// buildFeishuCardMessage 构建飞书卡片消息
func buildFeishuCardMessage(title string, content string, color string) map[string]interface{} {
	return map[string]interface{}{
		"msg_type": "interactive",
		"card": map[string]interface{}{
			"header": map[string]interface{}{
				"title": map[string]interface{}{
					"tag":     "plain_text",
					"content": title,
				},
				"template": color,
			},
			"elements": []map[string]interface{}{
				{
					"tag": "div",
					"text": map[string]interface{}{
						"tag":     "lark_md",
						"content": content,
					},
				},
				{
					"tag": "hr",
				},
				{
					"tag": "note",
					"elements": []map[string]interface{}{
						{
							"tag":     "plain_text",
							"content": fmt.Sprintf("来自 %s 系统 | %s", config.SystemName, time.Now().Format("2006-01-02 15:04:05")),
						},
					},
				},
			},
		},
	}
}
