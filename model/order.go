package model

import (
	"sync"

	"github.com/songquanpeng/one-api/common/helper"
)

type Order struct {
	Id                 int     `json:"id"`
	Username            string `json:"username" gorm:"unique;index" validate:"max=12"`
	UserId             int     `json:"user_id" gorm:"type:varchar(20);index"`
	Uuid               string  `json:"uuid" gorm:"type:varchar(100);index"`
	Status             int     `json:"status" gorm:"default:1"`
	Ticker             string  `json:"ticker" gorm:"type:varchar(20)"`
	AddressOut         string  `json:"address_out" gorm:"type:varchar(100);default:''"`
	AddressIn          string  `json:"address_in" gorm:"type:varchar(100);default:''"`
	CreatedTime        int64   `json:"created_time" gorm:"bigint"`
	UpdatedTime        int64   `json:"updated_time" gorm:"bigint"`
	FeeCoin            float64 `json:"fee_coin" gorm:"type:decimal(20,6);default:0"`
	ValueCoin          float64 `json:"value_coin" gorm:"type:decimal(20,6);default:0"`
	ValueForwardedCoin float64 `json:"value_forwarded_coin" gorm:"type:decimal(20,6);default:0"`
	Extra              string  `json:"extra" gorm:"type:text"`
}

var lock sync.Mutex

func CreateOrUpdateOrder(response CryptCallbackResponse, username string) error {
	lock.Lock()
	defer lock.Unlock()
	status := CryptResponseResult[response.Result]
	//userId,err:=Decrypt(response.UserId)
	//先查询订单
	order := Order{
		UserId:             response.UserId,
		Username:           username,
		Uuid:               response.Uuid,
		Status:             status,
		Ticker:             response.Coin,
		AddressOut:         response.AddressOut,
		AddressIn:          response.AddressIn,
		FeeCoin:            response.FeeCoin,
		ValueCoin:          response.ValueCoin,
		ValueForwardedCoin: response.ValueForwardedCoin,
		CreatedTime:        helper.GetTimestamp(),
		UpdatedTime:        helper.GetTimestamp(),
	}
	err := DB.FirstOrCreate(&order, Order{Uuid: response.Uuid}).Error
	if err != nil {
		return err
	}

	if order.Status < status {
		err = UpdateOrder(order.Uuid, Order{
			Status:      status,
			UpdatedTime: helper.GetTimestamp(),
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func UpdateOrder(uuid string, order Order) error {
	err := DB.Model(&Order{}).Where("uuid=?", uuid).Updates(order).Error
	if err != nil {
		return err
	}
	return nil
}

func GetAllBillsAndCount(page int, pageSize int, username string, startTimestamp int64, endTimestamp int64) (orders []*Order, total int64, err error) {
	// 进一步根据提供的参数筛选日志
	if username != "" {
		DB = DB.Where("username = ?", username)
	}
	if startTimestamp != 0 {
		DB = DB.Where("created_time >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		DB = DB.Where("created_time <= ?", endTimestamp)
	}

	// 首先计算满足条件的总数
	err = DB.Model(&Order{}).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	// 计算起始索引。第一页的起始索引为0。
	offset := (page - 1) * pageSize

	// 然后获取满足条件的日志数据
	err = DB.Order("id desc").Limit(pageSize).Offset(offset).Find(&orders).Error
	if err != nil {
		return nil, total, err
	}

	// 返回日志数据、总数以及错误信息
	return orders, total, nil

}

func GetUserBillsAndCount(page int, pageSize int, userId int, startTimestamp int64, endTimestamp int64) (orders []*Order, total int64, err error) {
	// 进一步根据提供的参数筛选日志
	DB = DB.Where("user_id = ?", userId)

	if startTimestamp != 0 {
		DB = DB.Where("created_time >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		DB = DB.Where("created_time <= ?", endTimestamp)
	}
	// 首先计算满足条件的总数
	err = DB.Model(&Order{}).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	// 计算起始索引。第一页的起始索引为0。
	offset := (page - 1) * pageSize

	// 然后获取满足条件的日志数据
	err = DB.Order("id desc").Limit(pageSize).Offset(offset).Find(&orders).Error
	if err != nil {
		return nil, total, err
	}

	// 返回日志数据、总数以及错误信息
	return orders, total, nil
}
