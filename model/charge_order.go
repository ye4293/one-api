package model

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/stripe/stripe-go/v78"
	"github.com/stripe/stripe-go/v78/paymentlink"
	"github.com/stripe/stripe-go/v78/webhook"
	"gorm.io/gorm"
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
	RealAmount float64 `json:"real_amount"`
	OrderCost  float64 `json:"order_cost"`
	Ip         string  `json:"ip"`
	SourceName string  `json:"source_name"`
	UpdatedAt  string  `json:"updated_at"`
	CreatedAt  string  `json:"created_at"`
}
type OrderInfo struct {
	ChargeUrl string
}

var StatusMap = map[string]int{
	"create":  1, //待支付
	"success": 3, //成功
	"fail":    4, //失败
	"refund":  5, //退款
	"dispute": 6, //争议
	"fraud":   7, //欺诈
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

func CreateStripOrder(userId, chargeId int) (string, string, error) {
	//查询配置项
	chargeConfig, err := GetChargeConfigById(chargeId)
	if err != nil {
		return "", "", err
	}
	appOrderId := helper.GetRandomString(16)
	chargeOrder := ChargeOrder{
		UserId:     userId,
		ChargeId:   chargeConfig.Id,
		Currency:   chargeConfig.Currency,
		AppOrderId: appOrderId,
		Status:     StatusMap["create"],
		Amount:     chargeConfig.Amount,
		Ip:         helper.GetIp(),
		UpdatedAt:  helper.GetFormatTimeString(),
		CreatedAt:  helper.GetFormatTimeString(),
	}

	//创建订单
	err = DB.Model(&ChargeOrder{}).Create(&chargeOrder).Error
	if err != nil {
		return "", "", err
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
		PaymentIntentData: &stripe.PaymentLinkPaymentIntentDataParams{
			Metadata: map[string]string{
				"userId":     fmt.Sprintf("%d", userId),
				"appOrderId": appOrderId,
			},
		},
	}
	result, err := paymentlink.New(params)
	if err != nil {
		return "", "", err
	}
	return result.URL, appOrderId, nil
}
func stripeChargeFail(charge *stripe.Charge) error {
	return nil
}
func stripeChargeDispute() {

}
func stripeChargeFraud() {

}
func stripeChargeRefund(charge *stripe.Charge) error {
	var stripLock sync.Mutex
	stripLock.Lock()
	defer stripLock.Unlock()
	if charge.Status == "succeeded" {
		//获取meta数据里的订单id
		orderId := charge.Metadata["appOrderId"]
		userId := charge.Metadata["userId"]
		var chargeOrder ChargeOrder
		if err := DB.Model(&ChargeOrder{}).Where("app_order_id = ? ", orderId).Where("user_id = ?", userId).First(&chargeOrder).Error; err != nil {
			return err
		}
		//如果已经支付成功直接返回
		if chargeOrder.Status == StatusMap["refund"] {
			return nil
		}
		if err := DB.Model(chargeOrder).Updates(ChargeOrder{Status: StatusMap["refund"]}).Error; err != nil {
			return err
		}
	}
	return nil
}
func stripeChargeSuccess(charge *stripe.Charge) error {
	// fmt.Printf("%+v\n",charge)
	// return nil
	var stripLock sync.Mutex
	stripLock.Lock()
	defer stripLock.Unlock()
	//获取meta数据里的订单id
	if charge.Status == "succeeded" {
		orderId := charge.Metadata["appOrderId"]
		userId := charge.Metadata["userId"]
		var chargeOrder ChargeOrder
		if err := DB.Model(&ChargeOrder{}).Where("app_order_id = ? ", orderId).Where("user_id = ?", userId).First(&chargeOrder).Error; err != nil {
			return err
		}
		//如果已经支付成功直接返回
		if chargeOrder.Status == StatusMap["success"] {
			return nil
		}
		if err := DB.Transaction(func(tx *gorm.DB) error {
			//更新订单
			amount := float64(charge.Amount / 100)
			orderCost := float64(charge.ApplicationFeeAmount / 100)
			realAmount := amount - orderCost
			if err := DB.Model(chargeOrder).Updates(ChargeOrder{Status: StatusMap["success"], RealAmount: realAmount, OrderCost: orderCost, OrderNo: charge.ID, Amount: amount}).Error; err != nil {
				return err
			}
			//更新余额 待定手续费和用户组别的变更
			err := IncreaseUserQuota(chargeOrder.UserId, int64(amount*500000))
			if err != nil {
				return err
			}
			// 返回 nil 提交事务
			return nil
		}); err != nil {
			return err
		}
		//支付成功处理一下其它
		AfterChargeSuccess(chargeOrder.UserId, float64(charge.Amount/100-charge.ApplicationFeeAmount/100))
	}
	return nil
}
func HandleStripeCallback(req *http.Request) error {
	payload, err := io.ReadAll(req.Body)
	logger.SysLog(fmt.Sprintf("stripePayload:%+v\n", payload))
	logger.SysLog(fmt.Sprintf("stripePayloaderr:%+v\n", err))
	logger.SysLog(fmt.Sprintf("stripePayloadheader:%+v\n", req.Header.Get("Stripe-Signature")))
	if err != nil {
		//fmt.Fprintf(os.Stderr, "Error reading request body: %v\n", err)
		//w.WriteHeader(http.StatusServiceUnavailable)
		return err
	}

	event := stripe.Event{}

	if err := json.Unmarshal(payload, &event); err != nil {
		return err
	}
	endpointSecret := config.StripeEndpointSecret
	signatureHeader := req.Header.Get("Stripe-Signature")
	event, err = webhook.ConstructEvent(payload, signatureHeader, endpointSecret)
	if err != nil {
		logger.SysLog(fmt.Sprintf("eventerr:%+v\n", err))
		return err
	}
	switch event.Type {
	case "payment_intent.succeeded":
		//var paymentIntent stripe.PaymentIntent
		var charge stripe.Charge
		err := json.Unmarshal(event.Data.Raw, &charge)
		if err != nil {
			logger.SysLog(fmt.Sprintf("Error parsing webhook JSON: %v\n", err))
			return err
		}
		err = stripeChargeSuccess(&charge)
		if err != nil {
			return err
		}
	case "charge.refunded":
		var charge stripe.Charge
		err := json.Unmarshal(event.Data.Raw, &charge)
		if err != nil {
			logger.SysLog(fmt.Sprintf("Error parsing webhook JSON: %v\n", err))
			return err
		}
		err = stripeChargeRefund(&charge)
		if err != nil {
			return err
		}
	case "charge.failed":
		var charge stripe.Charge
		err := json.Unmarshal(event.Data.Raw, &charge)
		if err != nil {
			logger.SysLog(fmt.Sprintf("Error parsing webhook JSON: %v\n", err))
			return err
		}
		err = stripeChargeFail(&charge)
		if err != nil {
			return err
		}
	default:
		logger.SysLog(fmt.Sprintf("Unhandled event type: %s\n", event.Type))
	}
	return nil
}
