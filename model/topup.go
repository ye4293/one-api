package model

import (
	"errors"
	"fmt"

	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	"gorm.io/gorm"
)

type TopUp struct {
	Id            int     `json:"id"`
	UserId        int     `json:"user_id" gorm:"index"`
	Amount        int64   `json:"amount"`
	Money         float64 `json:"money"`
	TradeNo       string  `json:"trade_no" gorm:"uniqueIndex;type:varchar(255)"`
	PaymentMethod string  `json:"payment_method" gorm:"type:varchar(50)"`
	CreateTime    int64   `json:"create_time"`
	CompleteTime  int64   `json:"complete_time"`
	Status        string  `json:"status" gorm:"type:varchar(20);default:'pending'"`
}

func (topUp *TopUp) Insert() error {
	return DB.Create(topUp).Error
}

func (topUp *TopUp) Update() error {
	return DB.Save(topUp).Error
}

func GetTopUpByTradeNo(tradeNo string) *TopUp {
	var topUp TopUp
	err := DB.Where("trade_no = ?", tradeNo).First(&topUp).Error
	if err != nil {
		return nil
	}
	return &topUp
}

const maxPageSize = 100

func GetUserTopUps(userId int, page int, pageSize int) (topups []*TopUp, total int64, err error) {
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	tx := DB.Model(&TopUp{}).Where("user_id = ?", userId)
	err = tx.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	err = tx.Order("id desc").Limit(pageSize).Offset(offset).Find(&topups).Error
	if err != nil {
		return nil, total, err
	}
	return topups, total, nil
}

func GetAllTopUps(page int, pageSize int) (topups []*TopUp, total int64, err error) {
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	tx := DB.Model(&TopUp{})
	err = tx.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	err = tx.Order("id desc").Limit(pageSize).Offset(offset).Find(&topups).Error
	if err != nil {
		return nil, total, err
	}
	return topups, total, nil
}

func CompleteTopUpOrder(tradeNo string) error {
	if tradeNo == "" {
		return errors.New("未提供订单号")
	}

	var userId int
	var quotaToAdd int64
	var money float64

	err := DB.Transaction(func(tx *gorm.DB) error {
		var topUp TopUp
		if err := tx.Set("gorm:query_option", "FOR UPDATE").
			Where("trade_no = ?", tradeNo).First(&topUp).Error; err != nil {
			return errors.New("充值订单不存在")
		}

		if topUp.Status != "pending" {
			return nil
		}

		quotaToAdd = int64(float64(topUp.Amount) * config.QuotaPerUnit)
		if quotaToAdd <= 0 {
			return errors.New("无效的充值额度")
		}

		topUp.Status = "success"
		topUp.CompleteTime = helper.GetTimestamp()
		if err := tx.Save(&topUp).Error; err != nil {
			return err
		}

		if err := tx.Model(&User{}).Where("id = ?", topUp.UserId).
			Update("quota", gorm.Expr("quota + ?", quotaToAdd)).Error; err != nil {
			return err
		}

		userId = topUp.UserId
		money = topUp.Money
		return nil
	})

	if err != nil {
		return err
	}

	if userId > 0 && quotaToAdd > 0 {
		RecordLog(userId, LogTypeTopup, fmt.Sprintf("在线充值成功，充值金额: %d，支付金额: %.2f", quotaToAdd, money))
		logger.SysLog(fmt.Sprintf("易支付充值成功: userId=%d, tradeNo=%s, quota=%d, money=%.2f", userId, tradeNo, quotaToAdd, money))
	}
	return nil
}
