package controller

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/model"

	"github.com/gin-gonic/gin"
)

func validateOptionUpdate(option model.Option) string {
	switch option.Key {
	case "Theme":
		if !config.ValidThemes[option.Value] {
			return "无效的主题"
		}
	case "GitHubOAuthEnabled":
		if option.Value == "true" && config.GitHubClientId == "" {
			return "无法启用 GitHub OAuth，请先填入 GitHub Client Id 以及 GitHub Client Secret！"
		}
	case "GoogleOAuthEnabled":
		if option.Value == "true" && config.GoogleClientId == "" {
			return "无法启用 Google OAuth，请先填入 Google Client Id 以及 Google Client Secret！"
		}
	case "EmailDomainRestrictionEnabled":
		if option.Value == "true" && len(config.EmailDomainWhitelist) == 0 {
			return "无法启用邮箱域名限制，请先填入限制的邮箱域名！"
		}
	case "WeChatAuthEnabled":
		if option.Value == "true" && config.WeChatServerAddress == "" {
			return "无法启用微信登录，请先填入微信登录相关配置信息！"
		}
	case "TurnstileCheckEnabled":
		if option.Value == "true" && config.TurnstileSiteKey == "" {
			return "无法启用 Turnstile 校验，请先填入 Turnstile 校验相关配置信息！"
		}
	case "CryptPaymentEnabled":
		if option.Value == "true" && (config.AddressOut == "" || config.CryptCallbackUrl == "") {
			return "无法启用 cryptai支付，请先填入 服务器回调地址 和钱包收款地址！"
		}
	case "StripePaymentEnabled":
		if option.Value == "true" && (config.StripeApiSecret == "" || config.StripeWebhookSecret == "" || config.StripePriceId == "") {
			return "无法启用 Stripe 支付，请先填入 Stripe API Secret、Webhook Secret 和 Price ID！"
		}
	}
	return ""
}

func GetOptions(c *gin.Context) {
	var options []*model.Option
	config.OptionMapRWMutex.Lock()
	for k, v := range config.OptionMap {
		if strings.HasSuffix(k, "Token") || strings.HasSuffix(k, "Secret") {
			continue
		}
		options = append(options, &model.Option{
			Key:   k,
			Value: helper.Interface2String(v),
		})
	}
	config.OptionMapRWMutex.Unlock()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    options,
	})
	return
}

func UpdateOption(c *gin.Context) {
	var option model.Option
	err := json.NewDecoder(c.Request.Body).Decode(&option)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid parameter",
		})
		return
	}
	if errMsg := validateOptionUpdate(option); errMsg != "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": errMsg,
		})
		return
	}
	err = model.UpdateOption(option.Key, option.Value)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}
