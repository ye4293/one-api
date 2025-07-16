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

type Channel struct {
	Id                 int     `json:"id"`
	Type               int     `json:"type" gorm:"default:0"`
	Key                string  `json:"key" gorm:"type:text"`
	Status             int     `json:"status" gorm:"default:1"`
	Name               string  `json:"name" gorm:"index"`
	Weight             *uint   `json:"weight" gorm:"default:0"`
	CreatedTime        int64   `json:"created_time" gorm:"bigint"`
	TestTime           int64   `json:"test_time" gorm:"bigint"`
	ResponseTime       int     `json:"response_time"` // in milliseconds
	BaseURL            *string `json:"base_url" gorm:"column:base_url;default:''"`
	Other              string  `json:"other"`   // DEPRECATED: please save config to field Config
	Balance            float64 `json:"balance"` // in USD
	BalanceUpdatedTime int64   `json:"balance_updated_time" gorm:"bigint"`
	Models             string  `json:"models"`
	Group              string  `json:"group" gorm:"type:varchar(32);default:'default'"`
	UsedQuota          int64   `json:"used_quota" gorm:"bigint;default:0"`
	ModelMapping       *string `json:"model_mapping" gorm:"type:varchar(1024);default:''"`
	Priority           *int64  `json:"priority" gorm:"bigint;default:0"`
	Config             string  `json:"config"`
	ChannelRatio       float64 `json:"channel_ratio" gorm:"default:1"`
}

type ChannelConfig struct {
	Region            string `json:"region,omitempty"`
	SK                string `json:"sk,omitempty"`
	AK                string `json:"ak,omitempty"`
	UserID            string `json:"user_id,omitempty"`
	APIVersion        string `json:"api_version,omitempty"`
	LibraryID         string `json:"library_id,omitempty"`
	Plugin            string `json:"plugin,omitempty"`
	VertexAIProjectID string `json:"vertex_ai_project_id,omitempty"`
	VertexAIADC       string `json:"vertex_ai_adc,omitempty"`
}

func (channel *Channel) LoadConfig() (ChannelConfig, error) {
	var cfg ChannelConfig
	if channel.Config == "" {
		return cfg, nil
	}
	err := json.Unmarshal([]byte(channel.Config), &cfg)
	if err != nil {
		return cfg, err
	}
	return cfg, nil
}

func GetChannelsAndCount(page int, pageSize int) (channels []*Channel, total int64, err error) {
	// 首先计算频道总数
	err = DB.Model(&Channel{}).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	// 计算起始索引，基于page和pageSize。第一页的起始索引为0。

	offset := (page - 1) * pageSize

	// 获取当前页面的频道列表，忽略key字段
	err = DB.Order("id desc").Limit(pageSize).Offset(offset).Omit("key").Find(&channels).Error
	if err != nil {
		return nil, total, err
	}

	// 返回频道列表、总数以及可能的错误信息
	return channels, total, nil
}

func GetAllChannels(startIdx int, num int, scope string) ([]*Channel, error) {
	var channels []*Channel
	var err error
	switch scope {
	case "all":
		err = DB.Order("id desc").Find(&channels).Error
	case "disabled":
		err = DB.Order("id desc").Where("status = ? or status = ?", common.ChannelStatusAutoDisabled, common.ChannelStatusManuallyDisabled).Find(&channels).Error
	default:
		err = DB.Order("id desc").Limit(num).Offset(startIdx).Omit("key").Find(&channels).Error
	}
	return channels, err
}

func SearchChannelsAndCount(keyword string, status *int, page int, pageSize int) (channels []*Channel, total int64, err error) {
	keyCol := "`key`"

	// 用于LIKE查询的关键词格式
	likeKeyword := "%" + keyword + "%"

	// 构建基础查询
	baseQuery := DB.Model(&Channel{}).Where("(id = ? OR name LIKE ? OR "+keyCol+" = ?)", helper.String2Int(keyword), likeKeyword, keyword)

	// 如果status不为nil，加入status作为查询条件
	if status != nil {
		baseQuery = baseQuery.Where("status = ?", *status)
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
	err = baseQuery.Omit("key").Order("id DESC").Offset(offset).Limit(pageSize).Find(&channels).Error
	if err != nil {
		return nil, total, err
	}

	// 返回频道列表的子集、总数以及可能的错误信息
	return channels, total, nil
}

func SearchChannels(keyword string) (channels []*Channel, err error) {
	err = DB.Omit("key").Where("id = ? or name LIKE ?", helper.String2Int(keyword), keyword+"%").Find(&channels).Error
	return channels, err
}

func GetChannelById(id int, selectAll bool) (*Channel, error) {
	channel := Channel{Id: id}
	var err error = nil
	if selectAll {
		err = DB.First(&channel, "id = ?", id).Error
	} else {
		err = DB.Omit("key").First(&channel, "id = ?", id).Error
	}
	return &channel, err
}

func BatchInsertChannels(channels []Channel) error {
	var err error
	err = DB.Create(&channels).Error
	if err != nil {
		return err
	}
	for _, channel_ := range channels {
		err = channel_.AddAbilities()
		if err != nil {
			return err
		}
	}
	return nil
}

func (channel *Channel) GetPriority() int64 {
	if channel.Priority == nil {
		return 0
	}
	return *channel.Priority
}

func (channel *Channel) GetWeight() *uint {
	if channel.Weight == nil {
		defaultWeight := uint(1) // 定义默认权重值为1
		return &defaultWeight    // 返回指向默认权重值的指针
	}
	return channel.Weight // 直接返回Weight字段的值
}

func (channel *Channel) GetBaseURL() string {
	if channel.BaseURL == nil {
		return ""
	}
	return *channel.BaseURL
}

func (channel *Channel) GetModelMapping() map[string]string {
	if channel.ModelMapping == nil || *channel.ModelMapping == "" || *channel.ModelMapping == "{}" {
		return nil
	}
	modelMapping := make(map[string]string)
	err := json.Unmarshal([]byte(*channel.ModelMapping), &modelMapping)
	if err != nil {
		logger.SysError(fmt.Sprintf("failed to unmarshal model mapping for channel %d, error: %s", channel.Id, err.Error()))
		return nil
	}
	return modelMapping
}

func (channel *Channel) Insert() error {
	var err error
	err = DB.Create(channel).Error
	if err != nil {
		return err
	}
	err = channel.AddAbilities()
	return err
}

func (channel *Channel) Update() error {
	var err error
	err = DB.Model(channel).Updates(channel).Error
	if err != nil {
		return err
	}
	DB.Model(channel).First(channel, "id = ?", channel.Id)
	err = channel.UpdateAbilities()
	return err
}

func (channel *Channel) UpdateResponseTime(responseTime int64) {
	err := DB.Model(channel).Select("response_time", "test_time").Updates(Channel{
		TestTime:     helper.GetTimestamp(),
		ResponseTime: int(responseTime),
	}).Error
	if err != nil {
		logger.SysError("failed to update response time: " + err.Error())
	}
}

func (channel *Channel) UpdateBalance(balance float64) {
	err := DB.Model(channel).Select("balance_updated_time", "balance").Updates(Channel{
		BalanceUpdatedTime: helper.GetTimestamp(),
		Balance:            balance,
	}).Error
	if err != nil {
		logger.SysError("failed to update balance: " + err.Error())
	}
}

func (channel *Channel) Delete() error {
	var err error
	err = DB.Delete(channel).Error
	if err != nil {
		return err
	}
	err = channel.DeleteAbilities()
	return err
}

func BatchDeleteChannel(ids []int) error {
	// 开始一个事务
	tx := DB.Begin()

	// 检查事务是否开始成功
	if tx.Error != nil {
		return tx.Error
	}

	// 批量删除所有渠道的Abilities
	if err := tx.Where("channel_id IN ?", ids).Delete(&Ability{}).Error; err != nil {
		tx.Rollback() // 如果出错，回滚事务
		return err
	}

	// 批量删除渠道本身
	if err := tx.Where("id IN ?", ids).Delete(&Channel{}).Error; err != nil {
		tx.Rollback() // 如果出错，回滚事务
		return err
	}

	// 提交事务
	return tx.Commit().Error
}

func UpdateChannelStatusById(id int, status int) {
	err := UpdateAbilityStatus(id, status == common.ChannelStatusEnabled)
	if err != nil {
		logger.SysError("failed to update ability status: " + err.Error())
	}
	err = DB.Model(&Channel{}).Where("id = ?", id).Update("status", status).Error
	if err != nil {
		logger.SysError("failed to update channel status: " + err.Error())
	}
}

func UpdateChannelUsedQuota(id int, quota int64) {
	if config.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeChannelUsedQuota, id, quota)
		return
	}
	updateChannelUsedQuota(id, quota)
}

func updateChannelUsedQuota(id int, quota int64) {
	err := DB.Model(&Channel{}).Where("id = ?", id).Update("used_quota", gorm.Expr("used_quota + ?", quota)).Error
	if err != nil {
		logger.SysError("failed to update channel used quota: " + err.Error())
	}
}

func DeleteChannelByStatus(status int64) (int64, error) {
	result := DB.Where("status = ?", status).Delete(&Channel{})
	return result.RowsAffected, result.Error
}

func DeleteDisabledChannel() (int64, error) {
	result := DB.Where("status = ? or status = ?", common.ChannelStatusAutoDisabled, common.ChannelStatusManuallyDisabled).Delete(&Channel{})
	return result.RowsAffected, result.Error
}

// CompensateChannelQuota 补偿渠道配额，用于任务失败时减少渠道的已使用配额
func CompensateChannelQuota(channelId int, quota int64) error {
	err := DB.Model(&Channel{}).Where("id = ?", channelId).Update("used_quota", gorm.Expr("used_quota - ?", quota)).Error
	if err != nil {
		logger.SysError("failed to compensate channel used quota: " + err.Error())
		return err
	}
	return nil
}
