package model

import (
	"encoding/json"
	"fmt"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	"gorm.io/gorm"
)

var CallbackUrl = "ddddddddddd"
var ReceviveAdress = "dddddddddd"
var BaseUrl = "https://api.cryptapi.io"

type Order struct {
	Id         int    `json:"id"`
	UserId     int    `json:"user_id" gorm:"type:int;index"`
	OrderId    string `json:"order_id" gorm:"type:varchar(32);index"`
	Status     int    `json:"status" gorm:"default:1"`
	Ticker     string `json:"ticker" gorm:"type:varchar(20)"`
	AddressOut string `json:"adress_out" gorm:"type:varchar(100);default:''"`
	AddressIn  string `json:"adress_in" gorm:"type:varchar(100);default:''"`
	//CallbackUrl        string  `json:"callback_url" gorm:"type:varchar(100)"`
	CreatedTime        int64   `json:"created_time" gorm:"bigint"`
	UpdatedTime        int64   `json:"updated_time" gorm:"bigint"`
	FeeCoin            float64 `json:"free_coin" gorm:"type:decimal(20,6);default:0"`
	ValueCoin          float64 `json:"value_coin" gorm:"type:decimal(20,6);default:0"`
	ValueForwardedCoin float64 `json:"value_forwarded_coin" gorm:"type:decimal(20,6);default:0"`
	Extra              string  `json:"extra" gorm:"type:text"`
	//Params             string  `json:"params" gorm:"type:text"`
}

func CreateOrUpdateOrder(response CryptCallbackResponse) int {

	status := CryptResponseResult[response.Result]
	//先查询订单
	order := Order{
		UserId:     response.UserId,
		OrderId:    response.OrderId,
		Status:     status,
		Ticker:     response.Coin,
		AddressOut: response.AddressOut,
		AddressIn:  response.AddressIn,
		//CallbackUrl:        respons.Call,
		FeeCoin:            response.FeeCoin,
		ValueCoin:          response.ValueCoin,
		ValueForwardedCoin: response.ValueForwardedCoin,
		CreatedTime:        helper.GetTimestamp(),
		UpdatedTime:        helper.GetTimestamp(),
	}
	err := DB.FirstOrCreate(&order, Order{OrderId: response.OrderId, UserId: response.UserId})
	if err != nil {
		panic(err)
	}
	if order.Status < status {
		UpdateOrder(order.OrderId, Order{Status: status})
	}
	return order.Id
}

func UpdateOrder(orderId string, order Order) {
	var err error
	err = DB.Model(&Order{OrderId: orderId}).Updates(order).Error
	if err != nil {
		panic(err)
	}
}

func SearchOrdersAndCount(orderId string, userId, status *int, page int, pageSize int) (orders []*Order, total int64, err error) {
	// 构建基础查询
	baseQuery := DB.Model(&Order{})
	// 如果status不为nil，加入status作为查询条件
	if status != nil {
		baseQuery = baseQuery.Where("status = ?", *status)
	}
	if userId != nil {
		baseQuery = baseQuery.Where("user_id = ?", *userId)
	}
	if orderId != "" {
		baseQuery = baseQuery.Where("order_id = ?", orderId)
	}

	// 计算满足条件的频道总数
	err = baseQuery.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	// 计算分页的偏移量
	offset := (page - 1) * pageSize

	// 获取满足条件的频道列表的子集，忽略key字段，并应用分页参数
	// 添加Order方法以按照id字段进行降序排列
	err = baseQuery.Order("id DESC").Offset(offset).Limit(pageSize).Find(&orders).Error
	if err != nil {
		return nil, total, err
	}

	// 返回频道列表的子集、总数以及可能的错误信息
	return orders, total, nil
}
