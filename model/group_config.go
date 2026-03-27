package model

import (
	"github.com/songquanpeng/one-api/common"
	"gorm.io/gorm"
)

// GroupConfig 分组等级配置表
type GroupConfig struct {
	ID          int     `json:"id" gorm:"primaryKey;autoIncrement"`
	GroupKey    string  `json:"group_key" gorm:"type:varchar(32);uniqueIndex;not null"` // 对应 GroupRatio 的 key
	DisplayName string  `json:"display_name" gorm:"type:varchar(64);not null"`          // 显示名称，如 "Lv1 基础版"
	Discount    float64 `json:"discount" gorm:"type:decimal(4,2);default:1.0"`          // 等级折扣倍率
	SortOrder   int     `json:"sort_order" gorm:"default:0"`                            // 显示排序
	Description string  `json:"description" gorm:"type:varchar(255)"`                   // 等级描述
}

func GetAllGroupConfigs() (configs []GroupConfig, err error) {
	err = DB.Order("sort_order asc, id asc").Find(&configs).Error
	return configs, err
}

func GetGroupConfigByKey(key string) (*GroupConfig, error) {
	var config GroupConfig
	err := DB.Where("group_key = ?", key).First(&config).Error
	return &config, err
}

func CreateGroupConfig(config *GroupConfig) error {
	return DB.Create(config).Error
}

func UpdateGroupConfig(config *GroupConfig) error {
	return DB.Save(config).Error
}

func DeleteGroupConfigByID(id int) error {
	return DB.Delete(&GroupConfig{}, id).Error
}

func GetGroupConfigByID(id int) (*GroupConfig, error) {
	var config GroupConfig
	err := DB.First(&config, id).Error
	return &config, err
}

// InitGroupConfigs 在表为空时，从现有 GroupRatio 初始化默认配置
func InitGroupConfigs(db *gorm.DB) error {
	var count int64
	db.Model(&GroupConfig{}).Count(&count)
	if count > 0 {
		return nil
	}
	order := 0
	for key, ratio := range common.GroupRatio {
		config := GroupConfig{
			GroupKey:    key,
			DisplayName: key,
			Discount:    ratio,
			SortOrder:   order,
		}
		if err := db.Create(&config).Error; err != nil {
			return err
		}
		order++
	}
	return nil
}
