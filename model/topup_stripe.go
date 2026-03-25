package model

import (
	"errors"

	"github.com/songquanpeng/one-api/common/helper"
	"gorm.io/gorm"
)

const StripeTopUpPaymentMethod = "stripe"

func CreateStripeTopUp(userID int, amount int64, money float64, tradeNo string) error {
	topUp := &TopUp{
		UserId:        userID,
		Amount:        amount,
		Money:         money,
		TradeNo:       tradeNo,
		PaymentMethod: StripeTopUpPaymentMethod,
		CreateTime:    helper.GetTimestamp(),
		Status:        "pending",
	}
	return topUp.Insert()
}

func CompleteStripeTopUp(tradeNo string) error {
	return CompleteTopUpOrder(tradeNo)
}

func ExpireStripeTopUp(tradeNo string) error {
	return ExpireTopUpOrder(tradeNo)
}

func ExpireTopUpOrder(tradeNo string) error {
	if tradeNo == "" {
		return errors.New("未提供订单号")
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		var topUp TopUp
		if err := tx.Set("gorm:query_option", "FOR UPDATE").
			Where("trade_no = ?", tradeNo).First(&topUp).Error; err != nil {
			return errors.New("充值订单不存在")
		}

		if topUp.Status != "pending" {
			return nil
		}

		topUp.Status = "expired"
		topUp.CompleteTime = helper.GetTimestamp()
		return tx.Save(&topUp).Error
	})
}
