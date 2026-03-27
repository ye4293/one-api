package controller

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/model"
	"github.com/stripe/stripe-go/v78"
	"github.com/stripe/stripe-go/v78/checkout/session"
	"github.com/stripe/stripe-go/v78/webhook"
)

const PaymentMethodStripe = "stripe"

type StripePayRequest struct {
	Amount        int64  `json:"amount"`
	PaymentMethod string `json:"payment_method"`
	SuccessURL    string `json:"success_url,omitempty"`
	CancelURL     string `json:"cancel_url,omitempty"`
}

func getStripeAvailability() (bool, string) {
	if !config.StripePaymentEnabled {
		return false, "管理员未开启 Stripe 支付"
	}
	if config.StripeApiSecret == "" {
		return false, "当前管理员未配置 Stripe API Secret"
	}
	if config.StripeWebhookSecret == "" {
		return false, "当前管理员未配置 Stripe Webhook Secret"
	}
	if config.StripePriceId == "" {
		return false, "当前管理员未配置 Stripe Price ID"
	}
	return true, ""
}

func getStripePayMoney(amount int64) float64 {
	return float64(amount) * config.StripeUnitPrice
}

func genStripeTradeNo(userId int) string {
	raw := fmt.Sprintf("one-api-ref-%d-%d-%s", userId, time.Now().UnixMilli(), helper.GetRandomString(6))
	hash := sha256.Sum256([]byte(raw))
	return "ref_" + fmt.Sprintf("%x", hash[:16])
}

func RequestStripeAmount(c *gin.Context) {
	var req StripePayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误"})
		return
	}

	enabled, reason := getStripeAvailability()
	if !enabled {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": reason})
		return
	}
	if req.Amount < int64(config.StripeMinTopUp) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("充值数量不能小于 %d", config.StripeMinTopUp)})
		return
	}

	payMoney := getStripePayMoney(req.Amount)
	if payMoney < 0.01 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "充值金额过低"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    strconv.FormatFloat(payMoney, 'f', 2, 64),
	})
}

func RequestStripePay(c *gin.Context) {
	var req StripePayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误"})
		return
	}

	enabled, reason := getStripeAvailability()
	if !enabled {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": reason})
		return
	}

	if req.PaymentMethod == "" {
		req.PaymentMethod = PaymentMethodStripe
	}
	if req.PaymentMethod != PaymentMethodStripe {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "不支持的支付渠道"})
		return
	}
	if req.Amount < int64(config.StripeMinTopUp) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("充值数量不能小于 %d", config.StripeMinTopUp)})
		return
	}
	if req.Amount > 10000 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "充值数量不能大于 10000"})
		return
	}

	payMoney := getStripePayMoney(req.Amount)
	if payMoney < 0.01 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "充值金额过低"})
		return
	}

	userId := c.GetInt("id")
	tradeNo := genStripeTradeNo(userId)

	payLink, err := genStripeCheckoutLink(tradeNo, req.Amount, req.SuccessURL, req.CancelURL)
	if err != nil {
		log.Printf("创建 Stripe Checkout 失败: %v\n", err)
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "拉起支付失败"})
		return
	}

	if err := model.CreateStripeTopUp(userId, req.Amount, payMoney, tradeNo); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "创建订单失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"pay_link": payLink,
		},
	})
}

func genStripeCheckoutLink(referenceId string, amount int64, successURL string, cancelURL string) (string, error) {
	if !strings.HasPrefix(config.StripeApiSecret, "sk_") && !strings.HasPrefix(config.StripeApiSecret, "rk_") {
		return "", fmt.Errorf("无效的 Stripe API 密钥")
	}

	stripe.Key = config.StripeApiSecret

	
	if config.FrontendServerAddress != "" {
		successURL = config.FrontendServerAddress + "/dashboard/topup"
		cancelURL = config.FrontendServerAddress + "/dashboard/topup"
	}else if config.ServerAddress != "" {
		successURL = config.ServerAddress + "/dashboard/topup"
		cancelURL = config.ServerAddress + "/dashboard/topup"
	}

	params := &stripe.CheckoutSessionParams{
		ClientReferenceID: stripe.String(referenceId),
		SuccessURL:        stripe.String(successURL),
		CancelURL:         stripe.String(cancelURL),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				Price:    stripe.String(config.StripePriceId),
				Quantity: stripe.Int64(amount),
			},
		},
		Mode:                stripe.String(string(stripe.CheckoutSessionModePayment)),
		AllowPromotionCodes: stripe.Bool(config.StripePromotionCodesEnabled),
	}

	params.CustomerCreation = stripe.String(string(stripe.CheckoutSessionCustomerCreationAlways))

	result, err := session.New(params)
	if err != nil {
		return "", err
	}

	return result.URL, nil
}

func StripeWebhook(c *gin.Context) {
	payload, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Printf("读取 Stripe Webhook 请求体失败: %v\n", err)
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}

	signature := c.GetHeader("Stripe-Signature")
	event, err := webhook.ConstructEventWithOptions(payload, signature, config.StripeWebhookSecret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})
	if err != nil {
		log.Printf("Stripe Webhook 验签失败: %v\n", err)
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	switch event.Type {
	case stripe.EventTypeCheckoutSessionCompleted:
		stripeSessionCompleted(event)
	case stripe.EventTypeCheckoutSessionExpired:
		stripeSessionExpired(event)
	default:
		log.Printf("不支持的 Stripe Webhook 事件类型: %s\n", event.Type)
	}

	c.Status(http.StatusOK)
}

func stripeSessionCompleted(event stripe.Event) {
	referenceId := event.GetObjectValue("client_reference_id")
	status := event.GetObjectValue("status")
	if status != "complete" {
		log.Printf("Stripe Checkout 状态异常: %s, 订单: %s\n", status, referenceId)
		return
	}

	if referenceId == "" {
		log.Println("Stripe Webhook 未提供 client_reference_id")
		return
	}

	LockOrder(referenceId)
	defer UnlockOrder(referenceId)

	if err := model.CompleteStripeTopUp(referenceId); err != nil {
		log.Printf("Stripe 充值完成失败: %s, 错误: %v\n", referenceId, err)
		return
	}

	total, _ := strconv.ParseFloat(event.GetObjectValue("amount_total"), 64)
	currency := strings.ToUpper(event.GetObjectValue("currency"))
	log.Printf("Stripe 收到款项: %s, %.2f(%s)\n", referenceId, total/100, currency)
}

func stripeSessionExpired(event stripe.Event) {
	referenceId := event.GetObjectValue("client_reference_id")
	if referenceId == "" {
		log.Println("Stripe Webhook 过期事件未提供订单号")
		return
	}

	LockOrder(referenceId)
	defer UnlockOrder(referenceId)

	if err := model.ExpireStripeTopUp(referenceId); err != nil {
		log.Printf("Stripe 订单过期处理失败: %s, 错误: %v\n", referenceId, err)
		return
	}

	log.Printf("Stripe 订单已过期: %s\n", referenceId)
}
