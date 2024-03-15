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
	Id                 int     `json:"id"`
	UserId             int     `json:"user_id" gorm:"type:int;index"`
	OrderId            string  `json:"order_id" gorm:"type:varchar(32);index"`
	Status             int     `json:"status" gorm:"default:1"`
	Coin               string  `json:"coin" gorm:"type:varchar(20)"`
	Address            string  `json:"adress" gorm:"type:varchar(100);default:''"`
	AddressIn          string  `json:"adress_in" gorm:"type:varchar(100);default:''"`
	CallbackUrl        string  `json:"callback_url" gorm:"type:varchar(100)"`
	CreatedTime        int64   `json:"created_time" gorm:"bigint"`
	UpdatedTime        int64   `json:"updated_time" gorm:"bigint"`
	FreeCoin           *string `json:"free_coin" gorm:"type:decimal(20,6);default:0"`
	ValueCoin          *string `json:"value_coin" gorm:"type:decimal(20,6);default:0"`
	ValueForwardedCoin *string `json:"value_forwarded_coin" gorm:"type:decimal(20,6);default:0"`
	Extra              string  `json:"extra" gorm:"type:text"`
	Params             string  `json:"params" gorm:"type:text"`
}

func CreateOrder(userId int, coin string) (Order, error) {
	var err error
	order := &Order{
		UserId:      userId,
		OrderId:     CreateOrderId(),
		Coin:        coin,
		CallbackUrl: config.OptionMap["CryptCallbackUrl"],
	}
	result := DB.Create(order)
	if err = result.Error; err != nil {
		return nil, err
	}
	return result, nil
}
func GetAdress() {

}
func CreateOrderId() string {
	return helper.GetUUID()
}
func getCoin() string {
	return "polygon_usdt"
}
func (order *Order) Insert() error {
	var err error
	err = DB.Create(order).Error
	if err != nil {
		return err
	}
	err = channel.AddAbilities()
	return err
}

func (order *Order) Update() error {
	var err error
	err = DB.Model(order).Updates(order).Error
	if err != nil {
		return err
	}
	DB.Model(order).First(order, "id = ?", order.Id)
	err = channel.UpdateAbilities()
	return err
}

// func GetChannelsAndCount(page int, pageSize int) (channels []*Channel, total int64, err error) {
// 	// 首先计算频道总数
// 	err = DB.Model(&Channel{}).Count(&total).Error
// 	if err != nil {
// 		return nil, 0, err
// 	}

// 	// 计算起始索引，基于page和pageSize。第一页的起始索引为0。

// 	offset := (page - 1) * pageSize

// 	// 获取当前页面的频道列表，忽略key字段
// 	err = DB.Order("id desc").Limit(pageSize).Offset(offset).Omit("key").Find(&channels).Error
// 	if err != nil {
// 		return nil, total, err
// 	}

// 	// 返回频道列表、总数以及可能的错误信息
// 	return channels, total, nil
// }

// func GetAllChannels(startIdx int, num int, scope string) ([]*Channel, error) {
// 	var channels []*Channel
// 	var err error
// 	switch scope {
// 	case "all":
// 		err = DB.Order("id desc").Find(&channels).Error
// 	case "disabled":
// 		err = DB.Order("id desc").Where("status = ? or status = ?", common.ChannelStatusAutoDisabled, common.ChannelStatusManuallyDisabled).Find(&channels).Error
// 	default:
// 		err = DB.Order("id desc").Limit(num).Offset(startIdx).Omit("key").Find(&channels).Error
// 	}
// 	return channels, err
// }

// func SearchChannelsAndCount(keyword string, status *int, page int, pageSize int) (channels []*Channel, total int64, err error) {
// 	keyCol := "`key`"

// 	// 用于LIKE查询的关键词格式
// 	likeKeyword := "%" + keyword + "%"

// 	// 构建基础查询
// 	baseQuery := DB.Model(&Channel{}).Where("(id = ? OR name LIKE ? OR "+keyCol+" = ?)", helper.String2Int(keyword), likeKeyword, keyword)

// 	// 如果status不为nil，加入status作为查询条件
// 	if status != nil {
// 		baseQuery = baseQuery.Where("status = ?", *status)
// 	}

// 	// 计算满足条件的频道总数
// 	err = baseQuery.Count(&total).Error
// 	if err != nil {
// 		return nil, 0, err
// 	}

// 	// 计算分页的偏移量
// 	offset := (page - 1) * pageSize

// 	// 获取满足条件的频道列表的子集，忽略key字段，并应用分页参数
// 	// 添加Order方法以按照id字段进行降序排列
// 	err = baseQuery.Omit("key").Order("id DESC").Offset(offset).Limit(pageSize).Find(&channels).Error
// 	if err != nil {
// 		return nil, total, err
// 	}

// 	// 返回频道列表的子集、总数以及可能的错误信息
// 	return channels, total, nil
// }

// func SearchChannels(keyword string) (channels []*Channel, err error) {
// 	err = DB.Omit("key").Where("id = ? or name LIKE ?", helper.String2Int(keyword), keyword+"%").Find(&channels).Error
// 	return channels, err
// }

// func GetChannelById(id int, selectAll bool) (*Channel, error) {
// 	channel := Channel{Id: id}
// 	var err error = nil
// 	if selectAll {
// 		err = DB.First(&channel, "id = ?", id).Error
// 	} else {
// 		err = DB.Omit("key").First(&channel, "id = ?", id).Error
// 	}
// 	return &channel, err
// }

// func BatchInsertChannels(channels []Channel) error {
// 	var err error
// 	err = DB.Create(&channels).Error
// 	if err != nil {
// 		return err
// 	}
// 	for _, channel_ := range channels {
// 		err = channel_.AddAbilities()
// 		if err != nil {
// 			return err
// 		}
// 	}
// 	return nil
// }

// func (channel *Channel) GetPriority() int64 {
// 	if channel.Priority == nil {
// 		return 0
// 	}
// 	return *channel.Priority
// }

// func (channel *Channel) GetWeight() *uint {
// 	if channel.Weight == nil {
// 		defaultWeight := uint(1) // 定义默认权重值为1
// 		return &defaultWeight    // 返回指向默认权重值的指针
// 	}
// 	return channel.Weight // 直接返回Weight字段的值
// }

// func (channel *Channel) GetBaseURL() string {
// 	if channel.BaseURL == nil {
// 		return ""
// 	}
// 	return *channel.BaseURL
// }

// func (channel *Channel) GetModelMapping() map[string]string {
// 	if channel.ModelMapping == nil || *channel.ModelMapping == "" || *channel.ModelMapping == "{}" {
// 		return nil
// 	}
// 	modelMapping := make(map[string]string)
// 	err := json.Unmarshal([]byte(*channel.ModelMapping), &modelMapping)
// 	if err != nil {
// 		logger.SysError(fmt.Sprintf("failed to unmarshal model mapping for channel %d, error: %s", channel.Id, err.Error()))
// 		return nil
// 	}
// 	return modelMapping
// }

// func (channel *Channel) Insert() error {
// 	var err error
// 	err = DB.Create(channel).Error
// 	if err != nil {
// 		return err
// 	}
// 	err = channel.AddAbilities()
// 	return err
// }

// func (channel *Channel) Update() error {
// 	var err error
// 	err = DB.Model(channel).Updates(channel).Error
// 	if err != nil {
// 		return err
// 	}
// 	DB.Model(channel).First(channel, "id = ?", channel.Id)
// 	err = channel.UpdateAbilities()
// 	return err
// }

// func (channel *Channel) UpdateResponseTime(responseTime int64) {
// 	err := DB.Model(channel).Select("response_time", "test_time").Updates(Channel{
// 		TestTime:     helper.GetTimestamp(),
// 		ResponseTime: int(responseTime),
// 	}).Error
// 	if err != nil {
// 		logger.SysError("failed to update response time: " + err.Error())
// 	}
// }

// func (channel *Channel) UpdateBalance(balance float64) {
// 	err := DB.Model(channel).Select("balance_updated_time", "balance").Updates(Channel{
// 		BalanceUpdatedTime: helper.GetTimestamp(),
// 		Balance:            balance,
// 	}).Error
// 	if err != nil {
// 		logger.SysError("failed to update balance: " + err.Error())
// 	}
// }

// func (channel *Channel) Delete() error {
// 	var err error
// 	err = DB.Delete(channel).Error
// 	if err != nil {
// 		return err
// 	}
// 	err = channel.DeleteAbilities()
// 	return err
// }

// func BatchDeleteChannel(ids []int) error {
// 	// 开始一个事务
// 	tx := DB.Begin()

// 	// 检查事务是否开始成功
// 	if tx.Error != nil {
// 		return tx.Error
// 	}

// 	// 批量删除所有渠道的Abilities
// 	if err := tx.Where("channel_id IN ?", ids).Delete(&Ability{}).Error; err != nil {
// 		tx.Rollback() // 如果出错，回滚事务
// 		return err
// 	}

// 	// 批量删除渠道本身
// 	if err := tx.Where("id IN ?", ids).Delete(&Channel{}).Error; err != nil {
// 		tx.Rollback() // 如果出错，回滚事务
// 		return err
// 	}

// 	// 提交事务
// 	return tx.Commit().Error
// }

// func (channel *Channel) LoadConfig() (map[string]string, error) {
// 	if channel.Config == "" {
// 		return nil, nil
// 	}
// 	cfg := make(map[string]string)
// 	err := json.Unmarshal([]byte(channel.Config), &cfg)
// 	if err != nil {
// 		return nil, err
// 	}
// 	return cfg, nil
// }

// func UpdateChannelStatusById(id int, status int) {
// 	err := UpdateAbilityStatus(id, status == common.ChannelStatusEnabled)
// 	if err != nil {
// 		logger.SysError("failed to update ability status: " + err.Error())
// 	}
// 	err = DB.Model(&Channel{}).Where("id = ?", id).Update("status", status).Error
// 	if err != nil {
// 		logger.SysError("failed to update channel status: " + err.Error())
// 	}
// }

// func UpdateChannelUsedQuota(id int, quota int64) {
// 	if config.BatchUpdateEnabled {
// 		addNewRecord(BatchUpdateTypeChannelUsedQuota, id, quota)
// 		return
// 	}
// 	updateChannelUsedQuota(id, quota)
// }

// func updateChannelUsedQuota(id int, quota int64) {
// 	err := DB.Model(&Channel{}).Where("id = ?", id).Update("used_quota", gorm.Expr("used_quota + ?", quota)).Error
// 	if err != nil {
// 		logger.SysError("failed to update channel used quota: " + err.Error())
// 	}
// }

// func DeleteChannelByStatus(status int64) (int64, error) {
// 	result := DB.Where("status = ?", status).Delete(&Channel{})
// 	return result.RowsAffected, result.Error
// }

// func DeleteDisabledChannel() (int64, error) {
// 	result := DB.Where("status = ? or status = ?", common.ChannelStatusAutoDisabled, common.ChannelStatusManuallyDisabled).Delete(&Channel{})
// 	return result.RowsAffected, result.Error
// }
