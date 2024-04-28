package model

import (
	"encoding/json"
	"fmt"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/paymentlink"
	"github.com/stripe/stripe-go/v76/webhook"
	"io/ioutil"
	"math/rand"
	"net/http"
	"os"
	"time"
)

type ChargeOrder struct {
	Id         int     `json:"id"`
	UserId     int     `json:"user_id"`
	AppOrderId string  `json:"app_order_id"`
	OrderNo    string  `json:"order_no"`
	ChargeId   int     `json:"charge_id"`
	Status     int     `json:"status"`
	Currency   string  `json:"currency"`
	Extension  string  `json:"extension"`
	Amount     float64 `json:"amount"`
	Price      float64 `json:"price"`
	RealAmount float64 `json:"real_amount"`
	OrderCost  float64 `json:"order_cost"`
	Ip         string  `json:"ip"`
	SourceName string  `json:"source_name"`
	IsFraud    int     `json:"is_fraud"`
	IsRefund   int     `json:"is_refund"`
	IsDispute  int     `json:"is_dispute"`
	UpdatedAt  string  `json:"updated_at"`
	CreatedAt  string  `json:"created_at"`
}
type OrderInfo struct {
	ChargeUrl string
}

var StatusMap = map[string]int{
	"create":  1,
	"success": 3,
	"fail":    4,
	"refund":  5,
}

func GetUserChargeOrdersAndCount(conditions map[string]interface{}, page int, pageSize int) (chargeOrders []*ChargeOrder, total int64, err error) {
	var chargeOrder ChargeOrder
	for k, v := range conditions {
		if k == "userId" {
			DB.Where("user_id = ?", v)
		}
		if k == "appOrderId" {
			DB.Where("app_order_id = ?", v)
		}
		if k == "status" {
			DB.Where("status = ?", v)
		}
	}
	err = DB.Model(&chargeOrder).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}
	// 计算起始索引。第一页的起始索引为0。
	offset := (page - 1) * pageSize

	// 然后获取满足条件的订单数据
	err = DB.Model(&chargeOrder).Limit(pageSize).Offset(offset).Find(&chargeOrders).Error
	if err != nil {
		return nil, total, err
	}

	// 返回日志数据、总数以及错误信息
	return chargeOrders, total, nil
}

func CreateStripOrder(userId, chargeId int) (string, error) {
	//查询配置项
	chargeConfig, err := GetChargeConfigById(chargeId)
	if err != nil {
		return "", err
	}
	appOrderId := getAppOrderId(userId)
	chargeOrder := ChargeOrder{
		UserId:     userId,
		ChargeId:   chargeConfig.Id,
		Currency:   chargeConfig.Currency,
		AppOrderId: appOrderId,
		Status:     StatusMap["create"],
		Amount:     chargeConfig.Amount,
		Ip:         helper.GetIp(),
	}

	//创建订单
	err = DB.Model(&ChargeOrder{}).Create(&chargeOrder).Error
	if err != nil {
		return "", err
	}
	//创建价格
	stripe.Key = config.StripePrivateKey
	params := &stripe.PaymentLinkParams{
		LineItems: []*stripe.PaymentLinkLineItemParams{
			&stripe.PaymentLinkLineItemParams{
				Price:    stripe.String(chargeConfig.Price),
				Quantity: stripe.Int64(1),
			},
		},
		Metadata: map[string]string{
			"userId":     fmt.Sprintf("%s", userId),
			"appOrderId": appOrderId,
		},
	}
	result, err := paymentlink.New(params)
	if err != nil {
		return "", err
	}
	return result.URL, nil
}
func getAppOrderId(userId int) string {
	rand.Seed(time.Now().UnixNano())
	// 生成一个随机整数
	randomInt := rand.Int()
	return helper.GetTimeString() + "-" + fmt.Sprintf("%s", randomInt) + "-" + fmt.Sprintf("%s", userId)
}
func HandleStripeCallback(w http.ResponseWriter, req *http.Request) error {
	const MaxBodyBytes = int64(65536)
	req.Body = http.MaxBytesReader(w, req.Body, MaxBodyBytes)
	payload, err := ioutil.ReadAll(req.Body)
	if err != nil {
		//fmt.Fprintf(os.Stderr, "Error reading request body: %v\n", err)
		//w.WriteHeader(http.StatusServiceUnavailable)
		return err
	}

	event := stripe.Event{}

	if err := json.Unmarshal(payload, &event); err != nil {
		//fmt.Fprintf(os.Stderr, "⚠️  Webhook error while parsing basic request. %v\n", err.Error())
		//w.WriteHeader(http.StatusBadRequest)
		return err
	}
	endpointSecret := config.StripeEndpointSecret
	signatureHeader := req.Header.Get("Stripe-Signature")
	event, err = webhook.ConstructEvent(payload, signatureHeader, endpointSecret)
	if err != nil {
		//fmt.Fprintf(os.Stderr, "⚠️  Webhook signature verification failed. %v\n", err)
		//w.WriteHeader(http.StatusBadRequest) // Return a 400 error on a bad signature
		return err
	}
	// Unmarshal the event data into an appropriate struct depending on its Type
	switch event.Type {
	case "payment_intent.succeeded":
		var paymentIntent stripe.PaymentIntent
		err := json.Unmarshal(event.Data.Raw, &paymentIntent)
		if err != nil {
			logger.SysLog(fmt.Sprintf("Error parsing webhook JSON: %v\n", err))
			//	w.WriteHeader(http.StatusBadRequest)
			return err
		}

		// Then define and call a func to handle the successful payment intent.
		// handlePaymentIntentSucceeded(paymentIntent)
	case "payment_method.attached":
		var paymentMethod stripe.PaymentMethod
		err := json.Unmarshal(event.Data.Raw, &paymentMethod)
		if err != nil {
			logger.SysLog(fmt.Sprintf("Error parsing webhook JSON: %v\n", err))
			//w.WriteHeader(http.StatusBadRequest)
			return err
		}
		// Then define and call a func to handle the successful attachment of a PaymentMethod.
		// handlePaymentMethodAttached(paymentMethod)
	default:
		logger.SysLog(fmt.Sprintf("Unhandled event type: %s\n", event.Type))
	}
	return nil
}
