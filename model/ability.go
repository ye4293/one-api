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

	// AutoDisabled 标记该 (group, model, channel_id) 行是否因「该模型自身故障」被模型级自动禁用，
	// 用于与「因整渠道禁用而 enabled=false」区分。核心不变式：
	//   enabled = (channel.status == enabled) AND (auto_disabled == false)
	// 存量行 AutoMigrate 后为 false，语义等价旧行为。
	AutoDisabled     bool  `json:"auto_disabled" gorm:"default:false;index"`
	AutoDisabledTime int64 `json:"auto_disabled_time" gorm:"default:0"`
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
//
// 不变式：enabled = (channel.status == enabled) AND (auto_disabled == false)
// 因此「渠道启用但某模型被模型级自动禁用（auto_disabled=1）」时，该行 enabled 应保持 0，
// 不能被强制恢复——否则会静默撤销模型级禁用。
func CheckDataConsistency() error {
	// 先检查不一致的数量
	var inconsistentCount int64
	err := DB.Table("abilities a").
		Joins("JOIN channels c ON a.channel_id = c.id").
		Where("(c.status = ? AND a.auto_disabled = 0 AND a.enabled = 0) OR (c.status = ? AND a.auto_disabled = 1 AND a.enabled = 1) OR (c.status != ? AND a.enabled = 1)",
			common.ChannelStatusEnabled, common.ChannelStatusEnabled, common.ChannelStatusEnabled).
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
					WHEN c.status = ? AND a.auto_disabled = 0 THEN 1
					ELSE 0
				END
				WHERE (c.status = ? AND a.auto_disabled = 0 AND a.enabled = 0)
				   OR (c.status = ? AND a.auto_disabled = 1 AND a.enabled = 1)
				   OR (c.status != ? AND a.enabled = 1)
			`, common.ChannelStatusEnabled, common.ChannelStatusEnabled, common.ChannelStatusEnabled, common.ChannelStatusEnabled)
		} else {
			// SQLite: 使用子查询语法
			result = DB.Exec(`
				UPDATE abilities
				SET enabled = CASE
					WHEN (SELECT status FROM channels WHERE channels.id = abilities.channel_id) = ? AND abilities.auto_disabled = 0 THEN 1
					ELSE 0
				END
				WHERE EXISTS (
					SELECT 1 FROM channels
					WHERE channels.id = abilities.channel_id
					AND (
						(channels.status = ? AND abilities.auto_disabled = 0 AND abilities.enabled = 0)
						OR (channels.status = ? AND abilities.auto_disabled = 1 AND abilities.enabled = 1)
						OR (channels.status != ? AND abilities.enabled = 1)
					)
				)
			`, common.ChannelStatusEnabled, common.ChannelStatusEnabled, common.ChannelStatusEnabled, common.ChannelStatusEnabled)
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
//
// 按不变式 enabled = (channel.status==enabled) AND (auto_disabled==false) 设置：
// 渠道启用时，模型级已禁用（auto_disabled=true）的行仍保持 enabled=false，不被误恢复。
func SyncChannelAbilities(channelId int) error {
	var channel Channel
	err := DB.First(&channel, channelId).Error
	if err != nil {
		return fmt.Errorf("channel not found: %w", err)
	}

	var result *gorm.DB
	if channel.Status == common.ChannelStatusEnabled {
		// 渠道启用：仅未被模型级禁用的行 enabled=true，其余保持 false
		result = DB.Model(&Ability{}).
			Where("channel_id = ? AND auto_disabled = ?", channelId, false).
			Update("enabled", true)
		if result.Error == nil {
			if e := DB.Model(&Ability{}).
				Where("channel_id = ? AND auto_disabled = ?", channelId, true).
				Update("enabled", false).Error; e != nil {
				logger.SysError(fmt.Sprintf("Failed to sync disabled abilities for channel %d: %s", channelId, e.Error()))
				return e
			}
		}
	} else {
		// 渠道未启用：全部 enabled=false（auto_disabled 不动）
		result = DB.Model(&Ability{}).Where("channel_id = ?", channelId).Update("enabled", false)
	}

	if result.Error != nil {
		logger.SysError(fmt.Sprintf("Failed to sync abilities for channel %d: %s", channelId, result.Error.Error()))
		return result.Error
	}

	logger.SysLog(fmt.Sprintf("Synced abilities for channel %d (channel enabled=%v)", channelId, channel.Status == common.ChannelStatusEnabled))
	return nil
}

// AutoDisableModelOnChannel 模型级自动禁用：把某渠道上某模型的所有 (group) 行标记禁用。
//
// 复用 getChannelStatusLock 保证禁用的原子性，避免并发下同一 (channel, model) 被
// 并发禁用产生竞态。不在此函数内直接禁渠道，避免与 AutoDisableChannelById
// 的同一把锁重入死锁。
//
// 「是否要禁整个渠道」不再由本函数返回：改由统一恢复链路 recoverAutoDisabledModels
// 每轮探测收尾时，用 ShouldDisableChannelByRecentUsage 按「最近使用的模型全部被
// 自动禁用且超过抖动窗口」判定并触发。参见 docs/plans/2026-08-21-channel-disable-by-recent-usage.md。
func AutoDisableModelOnChannel(channelId int, modelName, reason string) error {
	lock := getChannelStatusLock(channelId)
	lock.Lock()
	defer lock.Unlock()

	currentTime := time.Now().Unix()
	err := DB.Model(&Ability{}).
		Where("channel_id = ? AND model = ? AND enabled = ?", channelId, modelName, true).
		Updates(map[string]interface{}{
			"enabled":            false,
			"auto_disabled":      true,
			"auto_disabled_time": currentTime,
		}).Error
	if err != nil {
		return err
	}

	logger.SysLog(fmt.Sprintf("model-scope auto-disable: channel #%d model %s disabled, reason: %s",
		channelId, modelName, reason))
	return nil
}

// AutoDisabledAbility 是待恢复扫描的最小定位信息。
type AutoDisabledAbility struct {
	ChannelId        int    `gorm:"column:channel_id"`
	Model            string `gorm:"column:model"`
	AutoDisabledTime int64  `gorm:"column:auto_disabled_time"`
}

// GetAutoDisabledAbilities 返回所有被模型级禁用、且渠道非「手动禁用」的 (channel_id, model)。
//
// 覆盖两类：
//   - 渠道 status=enabled 且该模型 auto_disabled=true：常规模型级恢复目标。
//   - 渠道 status=auto_disabled（全模型都被禁）：模型级恢复接管，测通任一模型后由
//     EnableModelOnChannel 顺带把 status 提升回 enabled。
//
// 排除 manually_disabled：手动禁用是运维明确决策，不做自动测试恢复。
// 按 auto_disabled_time 降序：优先探测「最近被禁」的——号池换 key 间隙的瞬时 401 等
// 属于误禁、最可能已自愈，恢复投入产出比最高。反之最久没恢复的多是真失效僵尸，
// 若按升序优先它们会反复占满每轮预算、把新被误禁的活跃渠道饿死在队尾（见僵尸退避）。
func GetAutoDisabledAbilities() ([]AutoDisabledAbility, error) {
	var items []AutoDisabledAbility
	err := DB.Table("abilities a").
		Select("DISTINCT a.channel_id, a.model, MIN(a.auto_disabled_time) as auto_disabled_time").
		Joins("JOIN channels c ON c.id = a.channel_id").
		Where("a.auto_disabled = ? AND c.status != ?", true, common.ChannelStatusManuallyDisabled).
		Group("a.channel_id, a.model").
		Order("auto_disabled_time DESC").
		Scan(&items).Error
	if err != nil {
		return nil, err
	}
	return items, nil
}

// GetChannelsWithAutoDisabledAbilities 返回所有当前 status=enabled、且存在 auto_disabled abilities 的渠道 id（去重）。
//
// 用途：独立收尾判定（evaluateUsageBasedChannelDisable）的输入源，替代原来"从恢复候选队列反查渠道"的路径。
//
// 与 GetAutoDisabledAbilities 的区别：
//   - 本函数只关心「渠道-级」判定，返回 int 列表；不返回 model 明细
//   - status 过滤更严格：只取 enabled，避免把已 auto_disabled / manually_disabled 的渠道再评估一次
//     * auto_disabled：已被禁，评估无意义
//     * manually_disabled：运维决策，AutoDisableChannelById 内部并未防御性排除该状态，
//       若不在此处过滤，评估触发的 DisableChannelByRecentUsage 会把 status 从 2 覆盖成 3，
//       污染运维手动决策
//
// 参见 docs/plans/2026-08-27-auto-disable-refactor.md
func GetChannelsWithAutoDisabledAbilities() ([]int, error) {
	var ids []int
	err := DB.Table("abilities a").
		Joins("JOIN channels c ON c.id = a.channel_id").
		Where("a.auto_disabled = ? AND c.status = ?", true, common.ChannelStatusEnabled).
		Distinct("a.channel_id").
		Pluck("a.channel_id", &ids).Error
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// EnableModelOnChannel 模型级恢复：清 (channel_id, model) 所有行的 auto_disabled 标记并 enable。
//
// 若渠道当前 status=auto_disabled（因该 model 被禁而连带禁用），一并提升 status=enabled；
// manually_disabled 的渠道不主动提升——尊重人工决策。
//
// 复用 channelStatusLock 与 AutoDisableChannelById/AutoDisableModelOnChannel 串行，避免
// 「刚恢复模型就被并发禁用整渠道再清零」这类竞态。
func EnableModelOnChannel(channelId int, modelName string) error {
	lock := getChannelStatusLock(channelId)
	lock.Lock()
	defer lock.Unlock()

	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Ability{}).
			Where("channel_id = ? AND model = ?", channelId, modelName).
			Updates(map[string]interface{}{
				"enabled":            true,
				"auto_disabled":      false,
				"auto_disabled_time": 0,
			}).Error; err != nil {
			return err
		}
		// 条件 UPDATE：仅从 auto_disabled 提升，manually_disabled 保持不动
		return tx.Model(&Channel{}).
			Where("id = ? AND status = ?", channelId, common.ChannelStatusAutoDisabled).
			Update("status", common.ChannelStatusEnabled).Error
	})
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
