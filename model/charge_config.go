package model

import (
	"errors"
)

type ChargeConfig struct {
	Id        int     `json:"id"`
	Type      string  `json:"type"`
	Order     int     `json:"order"`
	Amount    float64 `json:"amount"`
	Price     string  `json:"price"`
	Currency  string  `json:"currency"`
	Status    int     `json:"status"`
	UpdatedAt string  `json:"updated_at"`
	CreatedAt string  `json:"created_at"`
}

func GetChargeConfigs() (chargeConfigs []*ChargeConfig, err error) {
	// 获取所有充值项,可以根据条件过滤
	err = DB.Model(&ChargeConfig{}).Where("status = ?", 1).Order("`order` asc").Find(&chargeConfigs).Error
	// 然后获取满足条件的充值数据
	if err != nil {
		return nil, err
	}
	return chargeConfigs, nil
}
func GetChargeConfigById(id int) (chargeConfig *ChargeConfig, err error) {
	if id <= 0 {
		return nil, errors.New("id is invalid")
	}

	err = DB.Model(&ChargeConfig{}).First(&chargeConfig, id).Error
	if err != nil {
		return nil, err
	}
	return chargeConfig, nil
}
