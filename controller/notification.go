package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/message"
)

// TestSMTP 测试 SMTP 邮件发送
// POST /api/test/smtp
func TestSMTP(c *gin.Context) {
	var request struct {
		Email string `json:"email" binding:"required"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请提供有效的邮箱地址",
		})
		return
	}

	// 检查 SMTP 是否已配置
	if config.SMTPServer == "" || config.SMTPAccount == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "SMTP 服务器未配置，请先保存 SMTP 设置",
		})
		return
	}

	// 发送测试邮件
	subject := fmt.Sprintf("[%s] SMTP 配置测试", config.SystemName)
	content := fmt.Sprintf(`
		<div style="font-family: Arial, sans-serif; max-width: 600px; margin: 0 auto;">
			<h2 style="color: #333;">🎉 SMTP 配置测试成功！</h2>
			<p>恭喜！您的 SMTP 邮件服务已配置成功。</p>
			<hr style="border: none; border-top: 1px solid #eee; margin: 20px 0;">
			<p style="color: #666; font-size: 14px;">
				<strong>服务器:</strong> %s<br>
				<strong>端口:</strong> %d<br>
				<strong>发送时间:</strong> %s
			</p>
			<p style="color: #999; font-size: 12px;">此邮件由 %s 系统自动发送，用于测试 SMTP 配置。</p>
		</div>
	`, config.SMTPServer, config.SMTPPort, time.Now().Format("2006-01-02 15:04:05"), config.SystemName)

	err := message.SendEmail(subject, request.Email, content)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": fmt.Sprintf("发送测试邮件失败: %s", err.Error()),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "测试邮件发送成功",
	})
}

// TestFeishuWebhook 测试飞书 Webhook（支持多个 Webhook URL）
// POST /api/test/feishu
func TestFeishuWebhook(c *gin.Context) {
	var request struct {
		WebhookUrls []string `json:"webhookUrls"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请提供有效的 Webhook URL 列表",
		})
		return
	}

	if len(request.WebhookUrls) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请提供至少一个 Webhook URL",
		})
		return
	}

	// 构建飞书消息
	feishuMsg := map[string]interface{}{
		"msg_type": "interactive",
		"card": map[string]interface{}{
			"header": map[string]interface{}{
				"title": map[string]interface{}{
					"tag":     "plain_text",
					"content": fmt.Sprintf("🎉 %s 飞书通知测试", config.SystemName),
				},
				"template": "green",
			},
			"elements": []map[string]interface{}{
				{
					"tag": "div",
					"text": map[string]interface{}{
						"tag":     "lark_md",
						"content": "恭喜！飞书 Webhook 配置测试成功！\n\n系统将通过此 Webhook 发送重要通知。",
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
							"content": fmt.Sprintf("发送时间: %s", time.Now().Format("2006-01-02 15:04:05")),
						},
					},
				},
			},
		},
	}

	jsonData, err := json.Marshal(feishuMsg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "构建消息失败",
		})
		return
	}

	// 向所有 Webhook URL 发送测试消息
	client := &http.Client{Timeout: 10 * time.Second}
	successCount := 0
	var lastError string

	for _, webhookUrl := range request.WebhookUrls {
		resp, err := client.Post(webhookUrl, "application/json", bytes.NewBuffer(jsonData))
		if err != nil {
			lastError = fmt.Sprintf("发送失败: %s", err.Error())
			continue
		}

		// 解析飞书响应
		var feishuResp struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&feishuResp); err != nil {
			// 如果无法解析响应，但 HTTP 状态码正常，也认为成功
			if resp.StatusCode == http.StatusOK {
				successCount++
			} else {
				lastError = "解析响应失败"
			}
		} else if feishuResp.Code == 0 {
			successCount++
		} else {
			lastError = fmt.Sprintf("飞书返回错误: %s", feishuResp.Msg)
		}
		resp.Body.Close()
	}

	if successCount == len(request.WebhookUrls) {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": fmt.Sprintf("全部 %d 个 Webhook 测试消息发送成功", successCount),
		})
	} else if successCount > 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": fmt.Sprintf("部分成功：%d/%d 个 Webhook 发送成功", successCount, len(request.WebhookUrls)),
		})
	} else {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": fmt.Sprintf("所有 Webhook 发送失败，最后错误: %s", lastError),
		})
	}
}

