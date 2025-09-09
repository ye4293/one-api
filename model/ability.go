package model

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
)

type Ability struct {
	Group     string `json:"group" gorm:"type:varchar(32);primaryKey;autoIncrement:false"`
	Model     string `json:"model" gorm:"primaryKey;autoIncrement:false"`
	ChannelId int    `json:"channel_id" gorm:"primaryKey;autoIncrement:false;index"`
	Enabled   bool   `json:"enabled"`
	Priority  *int64 `json:"priority" gorm:"bigint;default:0;index"`
}

func GetRandomSatisfiedChannel(group string, model string) (*Channel, error) {
	groupCol := "`group`"
	trueVal := "1"
	if common.UsingPostgreSQL {
		groupCol = `"group"`
		trueVal = "true"
	}

	// 获取同优先级下所有可用的渠道及其权重
	var channels []Channel
	maxPrioritySubQuery := DB.Model(&Ability{}).Select("MAX(priority)").Where(groupCol+" = ? and model = ? and enabled = "+trueVal, group, model)

	err := DB.Table("channels").
		Joins("JOIN abilities ON channels.id = abilities.channel_id").
		Where("`abilities`.`group` = ? AND abilities.model = ? AND abilities.enabled = ? AND abilities.priority = (?)", group, model, trueVal, maxPrioritySubQuery).
		Find(&channels).Error

	if err != nil {
		return nil, err
	}

	totalWeight := 0
	for _, channel := range channels {
		// 检查 weight 值，如果小于等于 0，则将其设置为 1
		weight := int(*channel.Weight)
		if weight <= 0 {
			weight = 1
		}
		totalWeight += weight
	}

	if totalWeight == 0 || len(channels) == 0 {
		return nil, errors.New("no channels available with the required priority and weight")
	}

	// 生成一个随机权重阈值
	randSource := rand.NewSource(time.Now().UnixNano())
	randGen := rand.New(randSource)
	weightThreshold := randGen.Intn(totalWeight) + 1

	currentWeight := 0
	for _, channel := range channels {
		// 同样地，检查并调整 weight 值
		weight := int(*channel.Weight)
		if weight <= 0 {
			weight = 1
		}
		currentWeight += weight
		if currentWeight >= weightThreshold {
			return &channel, nil
		}
	}

	return nil, errors.New("unable to select a channel based on weight")
}

func (channel *Channel) AddAbilities() error {
	models_ := strings.Split(channel.Models, ",")
	groups_ := strings.Split(channel.Group, ",")
	abilities := make([]Ability, 0, len(models_)*len(groups_))
	for _, model := range models_ {
		model = strings.TrimSpace(model) // 去除空格
		if model == "" {
			continue // 跳过空模型
		}
		for _, group := range groups_ {
			group = strings.TrimSpace(group) // 去除空格
			if group == "" {
				continue // 跳过空组
			}
			ability := Ability{
				Group:     group,
				Model:     model,
				ChannelId: channel.Id,
				Enabled:   channel.Status == common.ChannelStatusEnabled,
				Priority:  channel.Priority,
			}
			abilities = append(abilities, ability)
		}
	}

	// 分批插入以避免 "too many SQL variables" 错误
	// SQLite 默认限制为999个变量，每条记录5个字段，所以每批最多150条记录 (150 * 5 = 750 < 999)
	// MySQL 限制更高，但使用相同的批量大小保持兼容性
	batchSize := 150
	for i := 0; i < len(abilities); i += batchSize {
		end := i + batchSize
		if end > len(abilities) {
			end = len(abilities)
		}
		batch := abilities[i:end]
		if err := DB.Create(&batch).Error; err != nil {
			return err
		}
	}
	return nil
}

func (channel *Channel) DeleteAbilities() error {
	return DB.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error
}

// UpdateAbilities updates abilities of this channel.
// Make sure the channel is completed before calling this function.
func (channel *Channel) UpdateAbilities() error {
	// A quick and dirty way to update abilities
	// First delete all abilities of this channel
	err := channel.DeleteAbilities()
	if err != nil {
		return err
	}
	// Then add new abilities
	err = channel.AddAbilities()
	if err != nil {
		return err
	}
	return nil
}

// UpdateAbilityStatus 已废弃：请使用 UpdateChannelStatusById 确保数据一致性
// Deprecated: Use UpdateChannelStatusById instead to ensure data consistency
func UpdateAbilityStatus(channelId int, status bool) error {
	logger.SysError("WARNING: UpdateAbilityStatus is deprecated and may cause data inconsistency. Use UpdateChannelStatusById instead.")
	return DB.Model(&Ability{}).Where("channel_id = ?", channelId).Select("enabled").Update("enabled", status).Error
}

// CheckDataConsistency 检查并修复 channels 和 abilities 表的数据一致性
func CheckDataConsistency() error {
	// 先检查不一致的数量
	var inconsistentCount int64
	err := DB.Table("abilities a").
		Joins("JOIN channels c ON a.channel_id = c.id").
		Where("(c.status = ? AND a.enabled = 0) OR (c.status != ? AND a.enabled = 1)", common.ChannelStatusEnabled, common.ChannelStatusEnabled).
		Count(&inconsistentCount).Error

	if err != nil {
		logger.SysError("Failed to check data consistency: " + err.Error())
		return err
	}

	if inconsistentCount > 0 {
		logger.SysLog(fmt.Sprintf("Found %d inconsistent ability records, fixing...", inconsistentCount))

		// 修复不一致的数据
		result := DB.Exec(`
			UPDATE abilities a
			JOIN channels c ON a.channel_id = c.id
			SET a.enabled = CASE 
				WHEN c.status = ? THEN 1
				ELSE 0
			END
			WHERE (c.status = ? AND a.enabled = 0) OR (c.status != ? AND a.enabled = 1)
		`, common.ChannelStatusEnabled, common.ChannelStatusEnabled, common.ChannelStatusEnabled)

		if result.Error != nil {
			logger.SysError("Failed to fix data consistency: " + result.Error.Error())
			return result.Error
		}

		logger.SysLog(fmt.Sprintf("Fixed %d ability records for data consistency", result.RowsAffected))
	} else {
		logger.SysLog("Data consistency check passed - no issues found")
	}

	return nil
}

// SyncChannelAbilities 同步指定渠道的 abilities 状态
func SyncChannelAbilities(channelId int) error {
	var channel Channel
	err := DB.First(&channel, channelId).Error
	if err != nil {
		return fmt.Errorf("channel not found: %w", err)
	}

	enabled := channel.Status == common.ChannelStatusEnabled
	result := DB.Model(&Ability{}).Where("channel_id = ?", channelId).Update("enabled", enabled)

	if result.Error != nil {
		logger.SysError(fmt.Sprintf("Failed to sync abilities for channel %d: %s", channelId, result.Error.Error()))
		return result.Error
	}

	logger.SysLog(fmt.Sprintf("Synced %d abilities for channel %d (enabled=%v)", result.RowsAffected, channelId, enabled))
	return nil
}

func FindEnabledModelsByGroup(group string) ([]string, error) {
	var models []string

	// 构建查询，选择不同的model，确保enabled为true，属于给定的group
	// 并且按照priority降序排列
	err := DB.Model(&Ability{}).
		Select("DISTINCT model").
		Where("`group` = ? AND enabled = ?", group, true).
		Order("priority DESC").
		Pluck("model", &models).Error // 使用Pluck来选择model列，填充到models切片中

	if err != nil {
		return nil, err
	}

	return models, nil
}
