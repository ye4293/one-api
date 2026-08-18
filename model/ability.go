package model

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Ability struct {
	Group     string `json:"group" gorm:"type:varchar(32);primaryKey;autoIncrement:false"`
	Model     string `json:"model" gorm:"primaryKey;autoIncrement:false;index:idx_ability_model"`
	ChannelId int    `json:"channel_id" gorm:"primaryKey;autoIncrement:false;index"`
	Enabled   bool   `json:"enabled"`
	Priority  *int64 `json:"priority" gorm:"bigint;default:0;index"`

	// DynamicPriority 由 Master 节点定时计算写入（common/dynamicprio 评分）。
	// 选渠道热路径在 DynamicPriorityEnabled 时按本字段 DESC 排序替代静态 Priority。
	// 0 表示未计算/无数据，选渠道时回退到 Priority。允许 NULL，AutoMigrate 后存量行 NULL。
	DynamicPriority *int64 `json:"dynamic_priority" gorm:"bigint;default:0;index"`
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

// normalizeAbilityKeys 把逗号分隔的 models / group 字段拆成规范化列表：
// 去首尾空白、丢弃空项、保序去重。
//
// 去重是必需的而非锦上添花：abilities 的主键是 (group, model, channel_id)，
// 重复项会构造出主键相同的记录导致整批插入失败。而管理员从 UI 手工编辑模型
// 列表时没有任何去重保护，一旦写重就会让该渠道的 abilities 无法重建
// （事务保证已有数据不被损坏，但这次更新会整体失败）。
func normalizeAbilityKeys(raw string) []string {
	parts := strings.Split(raw, ",")
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func (channel *Channel) AddAbilities() error {
	return channel.addAbilitiesTx(DB)
}

// addAbilitiesTx 在给定句柄（DB 或事务）上创建该渠道的 abilities
func (channel *Channel) addAbilitiesTx(tx *gorm.DB) error {
	models_ := normalizeAbilityKeys(channel.Models)
	groups_ := normalizeAbilityKeys(channel.Group)
	abilities := make([]Ability, 0, len(models_)*len(groups_))
	for _, model := range models_ {
		for _, group := range groups_ {
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
		if err := tx.Create(&batch).Error; err != nil {
			return err
		}
	}
	return nil
}

func (channel *Channel) DeleteAbilities() error {
	return channel.deleteAbilitiesTx(DB)
}

// deleteAbilitiesTx 在给定句柄（DB 或事务）上删除该渠道的全部 abilities
func (channel *Channel) deleteAbilitiesTx(tx *gorm.DB) error {
	return tx.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error
}

// UpdateAbilities updates abilities of this channel.
// Make sure the channel is completed before calling this function.
//
// 先 DELETE 后 INSERT 必须在同一个事务内：否则 INSERT 失败时 DELETE 已经提交，
// 该渠道的 abilities 会变成空 —— 等价于「所有模型不可路由」，且不会有任何报错
// 提示模型已经消失。真实的触发路径：channel.Models 含重复模型名时，
// addAbilitiesTx 会构造出主键 (group, model, channel_id) 相同的记录导致插入失败。
func (channel *Channel) UpdateAbilities() error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := channel.deleteAbilitiesTx(tx); err != nil {
			return err
		}
		return channel.addAbilitiesTx(tx)
	})
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

		// 修复不一致的数据 - 根据数据库类型使用不同语法
		var result *gorm.DB
		if common.UsingMySQL || common.UsingPostgreSQL {
			// MySQL/PostgreSQL: 支持UPDATE JOIN语法
			result = DB.Exec(`
				UPDATE abilities a
				JOIN channels c ON a.channel_id = c.id
				SET a.enabled = CASE 
					WHEN c.status = ? THEN 1
					ELSE 0
				END
				WHERE (c.status = ? AND a.enabled = 0) OR (c.status != ? AND a.enabled = 1)
			`, common.ChannelStatusEnabled, common.ChannelStatusEnabled, common.ChannelStatusEnabled)
		} else {
			// SQLite: 使用子查询语法
			result = DB.Exec(`
				UPDATE abilities 
				SET enabled = CASE 
					WHEN (SELECT status FROM channels WHERE channels.id = abilities.channel_id) = ? THEN 1
					ELSE 0
				END
				WHERE EXISTS (
					SELECT 1 FROM channels 
					WHERE channels.id = abilities.channel_id 
					AND ((channels.status = ? AND abilities.enabled = 0) OR (channels.status != ? AND abilities.enabled = 1))
				)
			`, common.ChannelStatusEnabled, common.ChannelStatusEnabled, common.ChannelStatusEnabled)
		}

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

// DynamicPriorityUpdate 是单个 Ability 的动态优先级更新项。
// 主键三元组 (ChannelId, Model, Group) 唯一定位一行。
type DynamicPriorityUpdate struct {
	ChannelId       int
	Model           string
	Group           string
	DynamicPriority int64
}

// BatchUpdateDynamicPriority 批量写入动态优先级分数。
//
// 单条批量 UPSERT（ON CONFLICT / ON DUPLICATE KEY UPDATE），一次性提交。
// abilities 主键是 (group, model, channel_id)，GORM 的 clause.OnConflict 会按 dialect
// 翻译成对应语法（MySQL: ON DUPLICATE KEY UPDATE；PG/SQLite: ON CONFLICT DO UPDATE），
// 与 model_metrics.UpsertModelMetrics 同一模式。
//
// 为什么不用逐行 Updates(map)：
//   - 逐行 UPDATE 每行一次 commit + 一次 dynamic_priority 索引维护，N 行 = N 次锁竞争，
//     落库期间会阻塞 abilities 的聚合读查询（模型视图页 GROUP BY），线上表现为页面卡顿。
//   - 批量 UPSERT 把 N 次 commit 压成 1 次，锁持有时间从「N × 单行」降到「单批」，
//     读查询被阻塞的概率与时长大幅下降。
//
// 用 OnConflict + DoUpdates 强制写零值：Create(struct) 本身会写入传入的 0 值字段，
// 冲突分支通过 AssignmentColumns 也覆盖 dynamic_priority，保证「该周期无数据应回退 0 分」
// 的渠道能把上一轮的高分覆盖掉，不会卡在历史值不下降。
func BatchUpdateDynamicPriority(updates []DynamicPriorityUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	abilities := make([]Ability, 0, len(updates))
	for _, u := range updates {
		dp := u.DynamicPriority
		abilities = append(abilities, Ability{
			Group:           u.Group,
			Model:           u.Model,
			ChannelId:       u.ChannelId,
			DynamicPriority: &dp,
		})
	}
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "group"},
			{Name: "model"},
			{Name: "channel_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"dynamic_priority"}),
	}).Create(&abilities).Error
}
