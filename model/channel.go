package model

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	"gorm.io/gorm"
)

// KeyDisableNotification Key禁用通知结构
type KeyDisableNotification struct {
	ChannelId    int       `json:"channel_id"`
	ChannelName  string    `json:"channel_name"`
	KeyIndex     int       `json:"key_index"`
	MaskedKey    string    `json:"masked_key"`
	ErrorMessage string    `json:"error_message"`
	StatusCode   int       `json:"status_code"`
	DisabledTime time.Time `json:"disabled_time"`
}

// KeyDisableNotificationChan Key禁用通知通道
var KeyDisableNotificationChan = make(chan KeyDisableNotification, 100)

// ChannelDisableNotification 渠道禁用通知结构
type ChannelDisableNotification struct {
	ChannelId    int       `json:"channel_id"`
	ChannelName  string    `json:"channel_name"`
	Reason       string    `json:"reason"`
	DisabledTime time.Time `json:"disabled_time"`
}

// ChannelDisableNotificationChan 渠道禁用通知通道
var ChannelDisableNotificationChan = make(chan ChannelDisableNotification, 100)

type Channel struct {
	Id                 int     `json:"id"`
	Type               int     `json:"type" gorm:"default:0"`
	Key                string  `json:"key" gorm:"type:mediumtext"`
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
	AutoDisabled       bool    `json:"auto_disabled" gorm:"default:true"`
	// 新增多Key聚合相关字段
	MultiKeyInfo MultiKeyInfo `json:"multi_key_info" gorm:"type:json"`
	// 新增自动禁用原因字段
	AutoDisabledReason *string `json:"auto_disabled_reason" gorm:"type:text"`
	AutoDisabledTime   *int64  `json:"auto_disabled_time" gorm:"bigint"`
	AutoDisabledModel  *string `json:"auto_disabled_model" gorm:"type:varchar(255)"`
}

// 多Key聚合信息结构
type MultiKeyInfo struct {
	IsMultiKey          bool                `json:"is_multi_key"`           // 是否启用多Key聚合模式
	KeyCount            int                 `json:"key_count"`              // Key总数量
	EnabledKeyCount     int                 `json:"enabled_key_count"`      // 可用Key数量
	KeySelectionMode    KeySelectionMode    `json:"key_selection_mode"`     // Key选择模式：轮询或随机
	PollingIndex        int                 `json:"polling_index"`          // 轮询模式的当前索引
	KeyStatusList       map[int]int         `json:"key_status_list"`        // Key状态列表：索引 -> 状态
	KeyMetadata         map[int]KeyMetadata `json:"key_metadata"`           // Key元数据：索引 -> 元数据
	LastBatchImportTime int64               `json:"last_batch_import_time"` // 最后批量导入时间
	BatchImportMode     BatchImportMode     `json:"batch_import_mode"`      // 批量导入模式
}

// Key选择模式
type KeySelectionMode int

const (
	KeySelectionPolling KeySelectionMode = 0 // 轮询模式
	KeySelectionRandom  KeySelectionMode = 1 // 随机模式
)

// 批量导入模式
type BatchImportMode int

const (
	BatchImportOverride BatchImportMode = 0 // 覆盖模式
	BatchImportAppend   BatchImportMode = 1 // 追加模式
)

// Key元数据
type KeyMetadata struct {
	Balance     float64 `json:"balance"`      // 该Key的余额
	Usage       int64   `json:"usage"`        // 该Key的使用量
	LastUsed    int64   `json:"last_used"`    // 最后使用时间
	ImportBatch string  `json:"import_batch"` // 导入批次标识
	Note        string  `json:"note"`         // 备注信息
	// 新增自动禁用相关字段
	DisabledReason *string `json:"disabled_reason"` // 禁用原因
	DisabledTime   *int64  `json:"disabled_time"`   // 禁用时间戳
	StatusCode     *int    `json:"status_code"`     // HTTP状态码
	DisabledModel  *string `json:"disabled_model"`  // 导致禁用的模型
}

// 实现 database/sql/driver.Valuer 接口，用于存储到数据库
func (m MultiKeyInfo) Value() (driver.Value, error) {
	return json.Marshal(m)
}

// 实现 sql.Scanner 接口，用于从数据库读取
func (m *MultiKeyInfo) Scan(value interface{}) error {
	if value == nil {
		return nil
	}

	bytes, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("cannot scan %T into MultiKeyInfo", value)
	}

	return json.Unmarshal(bytes, m)
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

	// 定义需要选择的字段列表，确保所有必要信息都被包含
	selectFields := []string{
		"id", "type", "status", "name", "weight", "created_time", "test_time",
		"response_time", "base_url", "other", "balance", "balance_updated_time",
		"models", "`group`", "used_quota", "model_mapping", "priority", "config",
		"channel_ratio", "auto_disabled", "multi_key_info", "auto_disabled_reason", "auto_disabled_time", "auto_disabled_model",
	}

	// 使用明确的 Select 来获取当前页面的频道列表，替代 Omit("key")
	err = DB.Select(selectFields).Order("id desc").Limit(pageSize).Offset(offset).Find(&channels).Error
	if err != nil {
		return nil, total, err
	}

	// 为多Key渠道计算可用Key统计信息（优化：并行处理）
	type channelStats struct {
		index        int
		keyCount     int
		enabledCount int
	}

	statsChan := make(chan channelStats, len(channels))

	// 并行计算每个多Key渠道的统计信息
	for i, channel := range channels {
		if channel.MultiKeyInfo.IsMultiKey {
			go func(idx int, ch *Channel) {
				// 重新获取完整的Key信息以计算统计
				fullChannel, err := GetChannelById(ch.Id, true)
				if err == nil {
					// 自动修复Key状态（如果需要的话）
					keys := fullChannel.ParseKeys()
					needFix := len(fullChannel.MultiKeyInfo.KeyStatusList) == 0

					// 检查是否有Key缺少状态
					if !needFix {
						for i := range keys {
							if _, exists := fullChannel.MultiKeyInfo.KeyStatusList[i]; !exists {
								needFix = true
								break
							}
						}
					}

					if needFix {
						logger.SysLog(fmt.Sprintf("Auto-fixing multi-key status for channel %d", ch.Id))
						err := fullChannel.FixMultiKeyStatus()
						if err == nil {
							// 重新获取更新后的数据
							fullChannel, _ = GetChannelById(ch.Id, true)
							keys = fullChannel.ParseKeys() // 重新解析Key
						}
					}

					enabledCount := 0
					for j := range keys {
						if fullChannel.GetKeyStatus(j) == common.ChannelStatusEnabled {
							enabledCount++
						}
					}
					statsChan <- channelStats{
						index:        idx,
						keyCount:     len(keys),
						enabledCount: enabledCount,
					}
				} else {
					statsChan <- channelStats{index: idx, keyCount: 0, enabledCount: 0}
				}
			}(i, channel)
		}
	}

	// 收集统计结果
	multiKeyCount := 0
	for _, channel := range channels {
		if channel.MultiKeyInfo.IsMultiKey {
			multiKeyCount++
		}
	}

	for i := 0; i < multiKeyCount; i++ {
		stats := <-statsChan
		channels[stats.index].MultiKeyInfo.KeyCount = stats.keyCount
		channels[stats.index].MultiKeyInfo.EnabledKeyCount = stats.enabledCount
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

func GetAllChannelsForTest(startIdx int, num int, scope string) ([]*Channel, error) {
	var channels []*Channel
	var err error
	switch scope {
	case "all":
		err = DB.Order("id desc").Find(&channels).Error
	case "disabled":
		err = DB.Order("id desc").Where("status = ? or status = ?", common.ChannelStatusAutoDisabled, common.ChannelStatusManuallyDisabled).Find(&channels).Error
	default:
		// 对于测试，我们总是需要包含key字段
		err = DB.Order("id desc").Limit(num).Offset(startIdx).Find(&channels).Error
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

	// 定义需要选择的字段列表
	selectFields := []string{
		"id", "type", "status", "name", "weight", "created_time", "test_time",
		"response_time", "base_url", "other", "balance", "balance_updated_time",
		"models", "`group`", "used_quota", "model_mapping", "priority", "config",
		"channel_ratio", "auto_disabled", "multi_key_info", "auto_disabled_reason", "auto_disabled_time", "auto_disabled_model",
	}

	// 获取满足条件的频道列表的子集，并应用分页参数
	// 明确选择所有需要的字段，除了`key`
	err = baseQuery.Select(selectFields).Order("id DESC").Offset(offset).Limit(pageSize).Find(&channels).Error
	if err != nil {
		return nil, total, err
	}

	// 为多Key渠道计算可用Key统计信息
	type channelStats struct {
		index        int
		keyCount     int
		enabledCount int
	}

	statsChan := make(chan channelStats, len(channels))

	// 并行计算每个多Key渠道的统计信息
	for i, channel := range channels {
		if channel.MultiKeyInfo.IsMultiKey {
			go func(idx int, ch *Channel) {
				// 重新获取完整的Key信息以计算统计
				fullChannel, err := GetChannelById(ch.Id, true)
				if err == nil {
					// 自动修复Key状态（如果需要的话）
					keys := fullChannel.ParseKeys()
					needFix := len(fullChannel.MultiKeyInfo.KeyStatusList) == 0

					// 检查是否有Key缺少状态
					if !needFix {
						for i := range keys {
							if _, exists := fullChannel.MultiKeyInfo.KeyStatusList[i]; !exists {
								needFix = true
								break
							}
						}
					}

					if needFix {
						logger.SysLog(fmt.Sprintf("Auto-fixing multi-key status for channel %d", ch.Id))
						err := fullChannel.FixMultiKeyStatus()
						if err == nil {
							// 重新获取更新后的数据
							fullChannel, _ = GetChannelById(ch.Id, true)
							keys = fullChannel.ParseKeys() // 重新解析Key
						}
					}

					enabledCount := 0
					for j := range keys {
						if fullChannel.GetKeyStatus(j) == common.ChannelStatusEnabled {
							enabledCount++
						}
					}
					statsChan <- channelStats{
						index:        idx,
						keyCount:     len(keys),
						enabledCount: enabledCount,
					}
				} else {
					statsChan <- channelStats{index: idx, keyCount: 0, enabledCount: 0}
				}
			}(i, channel)
		}
	}

	// 收集统计结果
	multiKeyCount := 0
	for _, channel := range channels {
		if channel.MultiKeyInfo.IsMultiKey {
			multiKeyCount++
		}
	}

	for i := 0; i < multiKeyCount; i++ {
		stats := <-statsChan
		channels[stats.index].MultiKeyInfo.KeyCount = stats.keyCount
		channels[stats.index].MultiKeyInfo.EnabledKeyCount = stats.enabledCount
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

	// 分批插入channels以避免 "too many SQL variables" 错误
	// Channel结构体字段较多，保守设置每批20个channels
	batchSize := 20
	for i := 0; i < len(channels); i += batchSize {
		end := i + batchSize
		if end > len(channels) {
			end = len(channels)
		}
		batch := channels[i:end]
		err = DB.Create(&batch).Error
		if err != nil {
			return err
		}
	}

	// 为每个channel添加abilities
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

	// 保存更新前的重要信息
	savedMultiKeyInfo := channel.MultiKeyInfo

	// 使用常规的 Updates 方法更新非零值字段（GORM 默认行为）
	// 这样可以避免零值覆盖数据库中的现有数据
	err = DB.Model(channel).Updates(channel).Error
	if err != nil {
		return err
	}

	// 单独处理 auto_disabled 字段，因为 false 是零值会被 Updates 忽略
	// 使用 map 可以强制更新，无论值是 true 还是false
	err = DB.Model(channel).Select("auto_disabled").Updates(map[string]interface{}{
		"auto_disabled": channel.AutoDisabled,
	}).Error
	if err != nil {
		return err
	}

	// 重新查询渠道信息，但要保留MultiKeyInfo更新
	DB.Model(channel).First(channel, "id = ?", channel.Id)

	// 如果MultiKeyInfo有更新，重新设置并保存
	if savedMultiKeyInfo.IsMultiKey &&
		(savedMultiKeyInfo.KeyCount != channel.MultiKeyInfo.KeyCount ||
			savedMultiKeyInfo.KeySelectionMode != channel.MultiKeyInfo.KeySelectionMode ||
			savedMultiKeyInfo.BatchImportMode != channel.MultiKeyInfo.BatchImportMode) {
		channel.MultiKeyInfo = savedMultiKeyInfo
		// 再次更新MultiKeyInfo字段
		err = DB.Model(channel).Select("multi_key_info").Updates(map[string]interface{}{
			"multi_key_info": savedMultiKeyInfo,
		}).Error
		if err != nil {
			return err
		}
	}

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

func UpdateChannelStatusById(id int, status int) error {
	tx := DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			logger.SysError(fmt.Sprintf("panic during channel status update for channel %d: %v", id, r))
		}
	}()

	// 先检查渠道是否存在
	var channelExists bool
	err := tx.Model(&Channel{}).Select("1").Where("id = ?", id).Find(&channelExists).Error
	if err != nil {
		tx.Rollback()
		logger.SysError(fmt.Sprintf("failed to check channel existence for id %d: %s", id, err.Error()))
		return fmt.Errorf("failed to check channel existence: %w", err)
	}

	// 更新Ability状态
	enabled := status == common.ChannelStatusEnabled
	abilityResult := tx.Model(&Ability{}).Where("channel_id = ?", id).Update("enabled", enabled)
	if abilityResult.Error != nil {
		tx.Rollback()
		logger.SysError(fmt.Sprintf("failed to update ability status for channel %d: %s", id, abilityResult.Error.Error()))
		return fmt.Errorf("failed to update ability status: %w", abilityResult.Error)
	}

	// 更新Channel状态
	channelResult := tx.Model(&Channel{}).Where("id = ?", id).Update("status", status)
	if channelResult.Error != nil {
		tx.Rollback()
		logger.SysError(fmt.Sprintf("failed to update channel status for channel %d: %s", id, channelResult.Error.Error()))
		return fmt.Errorf("failed to update channel status: %w", channelResult.Error)
	}

	// 提交事务
	err = tx.Commit().Error
	if err != nil {
		logger.SysError(fmt.Sprintf("failed to commit channel status update for channel %d: %s", id, err.Error()))
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	// 记录状态变更类型
	var statusText string
	switch status {
	case common.ChannelStatusEnabled:
		statusText = "启用"
	case common.ChannelStatusManuallyDisabled:
		statusText = "手动禁用"
	case common.ChannelStatusAutoDisabled:
		statusText = "自动禁用"
	default:
		statusText = fmt.Sprintf("状态%d", status)
	}

	logger.SysLog(fmt.Sprintf("Successfully updated channel %d status to %s, affected %d abilities", id, statusText, abilityResult.RowsAffected))
	return nil
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

// GetChannelModelsbyId 根据渠道ID获取该渠道配置的模型列表
func GetChannelModelsbyId(channelId int) ([]string, error) {
	var channel Channel
	err := DB.Select("models").Where("id = ?", channelId).First(&channel).Error
	if err != nil {
		return nil, err
	}

	var models []string
	if channel.Models != "" {
		channelModels := strings.Split(channel.Models, ",")
		for _, model := range channelModels {
			modelName := strings.TrimSpace(model)
			if modelName != "" {
				models = append(models, modelName)
			}
		}
	}

	return models, nil
}

// ==================== 多Key聚合管理方法 ====================

// 线程安全的轮询索引锁
var channelPollingLocks sync.Map

// 获取渠道轮询锁
func getChannelPollingLock(channelId int) *sync.Mutex {
	if lock, exists := channelPollingLocks.Load(channelId); exists {
		return lock.(*sync.Mutex)
	}
	newLock := &sync.Mutex{}
	actual, _ := channelPollingLocks.LoadOrStore(channelId, newLock)
	return actual.(*sync.Mutex)
}

// ParseKeys 解析Key字符串为Key列表
func (channel *Channel) ParseKeys() []string {
	if channel.Key == "" {
		return []string{}
	}

	trimmed := strings.TrimSpace(channel.Key)

	// 首先检查是否是VertexAI渠道，如果是，则使用专用的JSON解析逻辑
	if channel.Type == common.ChannelTypeVertexAI {
		return common.ExtractJSONObjects(trimmed)
	}

	// 支持JSON数组格式: ["key1", "key2", "key3"]
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		var keys []string
		if err := json.Unmarshal([]byte(trimmed), &keys); err == nil {
			return keys
		}
	}

	// 回退到换行符分隔: "key1\nkey2\nkey3"
	keys := strings.Split(strings.Trim(trimmed, "\n"), "\n")
	// 过滤空字符串
	var validKeys []string
	for _, key := range keys {
		if trimmedKey := strings.TrimSpace(key); trimmedKey != "" {
			validKeys = append(validKeys, trimmedKey)
		}
	}

	return validKeys
}

// GetKeyStatus 获取Key状态，默认为启用状态
func (channel *Channel) GetKeyStatus(index int) int {
	if channel.MultiKeyInfo.KeyStatusList == nil {
		return common.ChannelStatusEnabled
	}
	if status, exists := channel.MultiKeyInfo.KeyStatusList[index]; exists {
		return status
	}
	return common.ChannelStatusEnabled
}

// 获取下一个可用的Key
func (channel *Channel) GetNextAvailableKey() (string, int, error) {
	// 如果不是多Key模式，直接返回原始Key
	if !channel.MultiKeyInfo.IsMultiKey {
		return channel.Key, 0, nil
	}

	keys := channel.ParseKeys()
	if len(keys) == 0 {
		return "", 0, errors.New("no keys available")
	}

	// 收集所有启用的Key索引
	enabledIndices := make([]int, 0, len(keys))
	for i := range keys {
		if channel.GetKeyStatus(i) == common.ChannelStatusEnabled {
			enabledIndices = append(enabledIndices, i)
		}
	}

	if len(enabledIndices) == 0 {
		return "", 0, errors.New("no enabled keys available")
	}

	switch channel.MultiKeyInfo.KeySelectionMode {
	case KeySelectionRandom:
		// 随机选择
		rand.Seed(time.Now().UnixNano())
		selectedIdx := enabledIndices[rand.Intn(len(enabledIndices))]
		return keys[selectedIdx], selectedIdx, nil

	case KeySelectionPolling:
		// 轮询选择（线程安全）
		lock := getChannelPollingLock(channel.Id)
		lock.Lock()
		defer lock.Unlock()

		start := channel.MultiKeyInfo.PollingIndex
		if start < 0 || start >= len(keys) {
			start = 0
		}

		// 从当前索引开始查找下一个启用的Key
		for i := 0; i < len(keys); i++ {
			idx := (start + i) % len(keys)
			if channel.GetKeyStatus(idx) == common.ChannelStatusEnabled {
				// 更新轮询索引到下一个位置
				channel.MultiKeyInfo.PollingIndex = (idx + 1) % len(keys)
				// 异步保存轮询索引
				go channel.saveMultiKeyInfo()
				return keys[idx], idx, nil
			}
		}

		// 理论上不应该到达这里，因为前面已经检查了启用的Key数量
		return keys[enabledIndices[0]], enabledIndices[0], nil

	default:
		// 未知模式，回退到第一个启用的Key
		return keys[enabledIndices[0]], enabledIndices[0], nil
	}
}

// 批量导入Keys
func (channel *Channel) BatchImportKeys(newKeys []string, mode BatchImportMode) error {
	if len(newKeys) == 0 {
		return errors.New("no keys provided")
	}

	var finalKeys []string

	switch mode {
	case BatchImportOverride:
		// 覆盖模式：清空现有Key和状态
		finalKeys = newKeys
		channel.MultiKeyInfo.KeyStatusList = make(map[int]int)
		channel.MultiKeyInfo.KeyMetadata = make(map[int]KeyMetadata)

	case BatchImportAppend:
		// 追加模式：保持现有Key和状态
		existingKeys := channel.ParseKeys()
		finalKeys = append(existingKeys, newKeys...)

	default:
		return errors.New("invalid batch import mode")
	}

	// 更新Key字符串（使用换行符分隔）
	channel.Key = strings.Join(finalKeys, "\n")

	// 更新多Key信息
	// 保护原有多key渠道：只在追加模式下保护，覆盖模式允许改变
	wasMultiKey := channel.MultiKeyInfo.IsMultiKey
	originalKeyCount := channel.MultiKeyInfo.KeyCount

	if mode == BatchImportOverride {
		// 覆盖模式：严格按照当前key数量设置
		channel.MultiKeyInfo.IsMultiKey = len(finalKeys) > 1
	} else {
		// 追加模式：保护原有多key渠道状态
		channel.MultiKeyInfo.IsMultiKey = len(finalKeys) > 1 || (wasMultiKey && originalKeyCount > 1)
	}
	channel.MultiKeyInfo.KeyCount = len(finalKeys)
	channel.MultiKeyInfo.LastBatchImportTime = helper.GetTimestamp()
	channel.MultiKeyInfo.BatchImportMode = mode

	// 初始化新Key的元数据
	if channel.MultiKeyInfo.KeyMetadata == nil {
		channel.MultiKeyInfo.KeyMetadata = make(map[int]KeyMetadata)
	}

	batchId := fmt.Sprintf("batch_%d", time.Now().Unix())
	startIndex := len(finalKeys) - len(newKeys) // 新Key的起始索引

	for i, _ := range newKeys {
		keyIndex := startIndex + i
		if _, exists := channel.MultiKeyInfo.KeyMetadata[keyIndex]; !exists {
			channel.MultiKeyInfo.KeyMetadata[keyIndex] = KeyMetadata{
				Balance:     0,
				Usage:       0,
				LastUsed:    0,
				ImportBatch: batchId,
				Note:        "",
			}
		}
	}

	return channel.Update()
}

// 切换单个Key的状态
func (channel *Channel) ToggleKeyStatus(keyIndex int, enabled bool) error {
	keys := channel.ParseKeys()
	if keyIndex < 0 || keyIndex >= len(keys) {
		return errors.New("invalid key index")
	}

	if channel.MultiKeyInfo.KeyStatusList == nil {
		channel.MultiKeyInfo.KeyStatusList = make(map[int]int)
	}

	if enabled {
		// 启用Key：删除状态记录（默认为启用）
		delete(channel.MultiKeyInfo.KeyStatusList, keyIndex)
	} else {
		// 禁用Key：记录禁用状态
		channel.MultiKeyInfo.KeyStatusList[keyIndex] = common.ChannelStatusManuallyDisabled
	}

	// 检查是否所有Key都被禁用
	channel.checkAndUpdateChannelStatus()

	return channel.saveMultiKeyInfo()
}

// 批量切换Key状态
func (channel *Channel) BatchToggleKeyStatus(keyIndices []int, enabled bool) error {
	keys := channel.ParseKeys()
	if channel.MultiKeyInfo.KeyStatusList == nil {
		channel.MultiKeyInfo.KeyStatusList = make(map[int]int)
	}

	for _, keyIndex := range keyIndices {
		if keyIndex < 0 || keyIndex >= len(keys) {
			continue // 跳过无效索引
		}

		if enabled {
			delete(channel.MultiKeyInfo.KeyStatusList, keyIndex)
		} else {
			channel.MultiKeyInfo.KeyStatusList[keyIndex] = common.ChannelStatusManuallyDisabled
		}
	}

	// 检查是否所有Key都被禁用
	channel.checkAndUpdateChannelStatus()

	return channel.saveMultiKeyInfo()
}

// 根据导入批次切换Key状态
func (channel *Channel) ToggleKeysByBatch(batchId string, enabled bool) error {
	var targetIndices []int

	for index, metadata := range channel.MultiKeyInfo.KeyMetadata {
		if metadata.ImportBatch == batchId {
			targetIndices = append(targetIndices, index)
		}
	}

	if len(targetIndices) == 0 {
		return errors.New("no keys found for the specified batch")
	}

	return channel.BatchToggleKeyStatus(targetIndices, enabled)
}

// 获取Key统计信息
func (channel *Channel) GetKeyStats() map[string]interface{} {
	keys := channel.ParseKeys()

	stats := map[string]interface{}{
		"total":             len(keys),
		"enabled":           0,
		"manually_disabled": 0,
		"auto_disabled":     0,
		"is_multi_key":      channel.MultiKeyInfo.IsMultiKey,
		"selection_mode":    channel.MultiKeyInfo.KeySelectionMode,
	}

	for i := range keys {
		status := channel.GetKeyStatus(i)
		if status == common.ChannelStatusEnabled {
			stats["enabled"] = stats["enabled"].(int) + 1
		} else if status == common.ChannelStatusManuallyDisabled {
			stats["manually_disabled"] = stats["manually_disabled"].(int) + 1
		} else if status == common.ChannelStatusAutoDisabled {
			stats["auto_disabled"] = stats["auto_disabled"].(int) + 1
		}
	}

	return stats
}

// 修复聚合渠道的Key状态初始化问题
func (channel *Channel) FixMultiKeyStatus() error {
	if !channel.MultiKeyInfo.IsMultiKey {
		return errors.New("not a multi-key channel")
	}

	keys := channel.ParseKeys()
	if channel.MultiKeyInfo.KeyStatusList == nil {
		channel.MultiKeyInfo.KeyStatusList = make(map[int]int)
	}

	// 为没有状态的Key设置默认状态为启用
	for i := range keys {
		if _, exists := channel.MultiKeyInfo.KeyStatusList[i]; !exists {
			channel.MultiKeyInfo.KeyStatusList[i] = common.ChannelStatusEnabled
		}
	}

	// 更新数据库
	return channel.Update()
}

// 删除所有禁用的Key
func (channel *Channel) DeleteDisabledKeys() error {
	if !channel.MultiKeyInfo.IsMultiKey {
		return errors.New("not a multi-key channel")
	}

	keys := channel.ParseKeys()
	if len(keys) == 0 {
		return nil // 没有key，无需操作
	}

	var keptKeys []string
	keptKeyMetadata := make(map[int]KeyMetadata)
	keptKeyStatusList := make(map[int]int)

	newIndex := 0
	for i, key := range keys {
		status := channel.GetKeyStatus(i)
		if status == common.ChannelStatusEnabled {
			keptKeys = append(keptKeys, key)
			if metadata, ok := channel.MultiKeyInfo.KeyMetadata[i]; ok {
				keptKeyMetadata[newIndex] = metadata
			}
			// 启用的状态我们不需要显式存储，因为默认就是启用
			// 但如果未来有其他启用状态，可以在这里设置
			// keptKeyStatusList[newIndex] = common.ChannelStatusEnabled
			newIndex++
		}
	}

	// 更新渠道信息
	channel.Key = strings.Join(keptKeys, "\n")
	channel.MultiKeyInfo.KeyCount = len(keptKeys)
	channel.MultiKeyInfo.KeyMetadata = keptKeyMetadata
	channel.MultiKeyInfo.KeyStatusList = keptKeyStatusList // 几乎总是空的
	channel.MultiKeyInfo.PollingIndex = 0

	// 检查并更新渠道聚合状态和整体状态
	channel.checkAndUpdateChannelStatus()

	return channel.Update()
}

// 检查并更新渠道状态
func (channel *Channel) checkAndUpdateChannelStatus() {
	if !channel.MultiKeyInfo.IsMultiKey {
		return
	}

	keys := channel.ParseKeys()
	if len(keys) == 0 {
		channel.Status = common.ChannelStatusAutoDisabled
		logger.SysLog(fmt.Sprintf("Channel %d auto-disabled: no keys available", channel.Id))
		return
	}

	// 检查是否所有Key都被禁用（包括手动禁用和自动禁用）
	enabledCount := 0
	autoDisabledCount := 0
	manualDisabledCount := 0

	for i := range keys {
		status := channel.GetKeyStatus(i)
		if status == common.ChannelStatusEnabled {
			enabledCount++
		} else if status == common.ChannelStatusAutoDisabled {
			autoDisabledCount++
		} else if status == common.ChannelStatusManuallyDisabled {
			manualDisabledCount++
		}
	}

	totalKeys := len(keys)
	allDisabled := enabledCount == 0

	if allDisabled {
		// 所有Key都被禁用，禁用整个渠道
		oldStatus := channel.Status
		channel.Status = common.ChannelStatusAutoDisabled

		// 设置渠道级别的自动禁用原因
		currentTime := time.Now().Unix()
		reasonText := "all keys disabled"
		channel.AutoDisabledReason = &reasonText
		channel.AutoDisabledTime = &currentTime

		if oldStatus != common.ChannelStatusAutoDisabled {
			logger.SysLog(fmt.Sprintf("Channel %d auto-disabled: all %d keys are disabled (auto: %d, manual: %d)",
				channel.Id, totalKeys, autoDisabledCount, manualDisabledCount))

			// 发送渠道级别的禁用通知
			channel.notifyChannelDisabled(reasonText)
		}
	} else if channel.Status == common.ChannelStatusAutoDisabled && enabledCount > 0 {
		// 如果有Key重新启用，且渠道是自动禁用状态，可以考虑重新启用
		// 这里暂时不自动重新启用渠道，需要管理员手动启用
		logger.SysLog(fmt.Sprintf("Channel %d has %d enabled keys but remains auto-disabled, manual intervention required",
			channel.Id, enabledCount))
	}
}

// 保存多Key信息到数据库
func (channel *Channel) saveMultiKeyInfo() error {
	return DB.Model(channel).Update("multi_key_info", channel.MultiKeyInfo).Error
}

// 更新渠道状态到数据库
func (channel *Channel) updateChannelStatus() error {
	return DB.Model(channel).Update("status", channel.Status).Error
}

// 处理Key使用后的状态更新
func (channel *Channel) HandleKeyUsed(keyIndex int, success bool) error {
	if !channel.MultiKeyInfo.IsMultiKey {
		return nil
	}

	// 更新使用统计
	if channel.MultiKeyInfo.KeyMetadata == nil {
		channel.MultiKeyInfo.KeyMetadata = make(map[int]KeyMetadata)
	}

	metadata := channel.MultiKeyInfo.KeyMetadata[keyIndex]
	metadata.Usage++
	metadata.LastUsed = helper.GetTimestamp()
	channel.MultiKeyInfo.KeyMetadata[keyIndex] = metadata

	return channel.saveMultiKeyInfo()
}

// HandleKeyError 处理特定Key的错误，决定是否需要自动禁用
func (channel *Channel) HandleKeyError(keyIndex int, errorMessage string, statusCode int, modelName string) error {
	if !channel.MultiKeyInfo.IsMultiKey {
		return nil
	}

	// 直接检查渠道级别的自动禁用设置（已经在上层做过shouldDisable检查）
	if channel.AutoDisabled {
		// 禁用特定的Key（设置为自动禁用状态）
		if channel.MultiKeyInfo.KeyStatusList == nil {
			channel.MultiKeyInfo.KeyStatusList = make(map[int]int)
		}
		channel.MultiKeyInfo.KeyStatusList[keyIndex] = common.ChannelStatusAutoDisabled

		// 记录禁用原因和时间
		if channel.MultiKeyInfo.KeyMetadata == nil {
			channel.MultiKeyInfo.KeyMetadata = make(map[int]KeyMetadata)
		}
		metadata := channel.MultiKeyInfo.KeyMetadata[keyIndex]
		currentTime := time.Now().Unix()
		metadata.DisabledReason = &errorMessage
		metadata.DisabledTime = &currentTime
		metadata.StatusCode = &statusCode
		metadata.DisabledModel = &modelName
		channel.MultiKeyInfo.KeyMetadata[keyIndex] = metadata

		keys := channel.ParseKeys()
		maskedKey := "unknown"
		if keyIndex < len(keys) {
			key := keys[keyIndex]
			if len(key) > 8 {
				maskedKey = key[:4] + "***" + key[len(key)-4:]
			} else {
				maskedKey = key
			}
		}

		logger.SysLog(fmt.Sprintf("Auto-disabled key %d (%s) in multi-key channel %d due to error: %s (status: %d)",
			keyIndex, maskedKey, channel.Id, errorMessage, statusCode))

		// 发送邮件通知
		channel.notifyKeyDisabled(keyIndex, maskedKey, errorMessage, statusCode)

		// 检查是否所有Key都被禁用，如果是则禁用整个渠道
		channel.checkAndUpdateChannelStatus()

		// 保存Key状态和渠道状态到数据库
		err := channel.saveMultiKeyInfo()
		if err != nil {
			logger.SysError(fmt.Sprintf("Failed to save multi-key info for channel %d: %s",
				channel.Id, err.Error()))
			return err
		}

		// 如果渠道被自动禁用，需要单独更新渠道状态
		if channel.Status == common.ChannelStatusAutoDisabled {
			err = channel.updateChannelStatus()
			if err != nil {
				logger.SysError(fmt.Sprintf("Failed to update channel status for channel %d: %s",
					channel.Id, err.Error()))
				return err
			}
		}
	}

	return nil
}

// notifyKeyDisabled 发送Key禁用邮件通知
func (channel *Channel) notifyKeyDisabled(keyIndex int, maskedKey string, errorMessage string, statusCode int) {
	// 使用monitor包的通知函数来避免循环导入
	// 我们将在monitor包中添加一个专门的函数来处理这个通知
	go func() {
		// 先记录日志
		logger.SysLog(fmt.Sprintf("Key #%d (%s) in multi-key channel #%d has been auto-disabled due to error: %s (status: %d)",
			keyIndex, maskedKey, channel.Id, errorMessage, statusCode))

		// 通过一个回调机制来发送邮件，避免循环导入
		// 这里先记录，后续可以通过事件机制来处理
		KeyDisableNotificationChan <- KeyDisableNotification{
			ChannelId:    channel.Id,
			ChannelName:  channel.Name,
			KeyIndex:     keyIndex,
			MaskedKey:    maskedKey,
			ErrorMessage: errorMessage,
			StatusCode:   statusCode,
			DisabledTime: time.Now(),
		}
	}()
}

// notifyChannelDisabled 发送多Key渠道完全禁用的邮件通知
func (channel *Channel) notifyChannelDisabled(reason string) {
	go func() {
		// 记录日志
		logger.SysLog(fmt.Sprintf("Multi-key channel #%d (%s) has been auto-disabled: %s",
			channel.Id, channel.Name, reason))

		// 发送渠道级别的禁用通知
		ChannelDisableNotificationChan <- ChannelDisableNotification{
			ChannelId:    channel.Id,
			ChannelName:  channel.Name,
			Reason:       reason,
			DisabledTime: time.Now(),
		}
	}()
}

// GetNextAvailableKeyWithRetry 获取下一个可用Key，支持重试和自动跳过禁用Key
func (channel *Channel) GetNextAvailableKeyWithRetry(excludeIndices []int) (string, int, error) {
	if !channel.MultiKeyInfo.IsMultiKey {
		return channel.Key, 0, nil
	}

	keys := channel.ParseKeys()
	if len(keys) == 0 {
		return "", 0, errors.New("no keys available")
	}

	// 收集所有启用且不在排除列表中的Key索引
	availableIndices := make([]int, 0, len(keys))
	for i := range keys {
		if channel.GetKeyStatus(i) == common.ChannelStatusEnabled {
			excluded := false
			for _, excludeIdx := range excludeIndices {
				if i == excludeIdx {
					excluded = true
					break
				}
			}
			if !excluded {
				availableIndices = append(availableIndices, i)
			}
		}
	}

	if len(availableIndices) == 0 {
		return "", 0, errors.New("no available keys after excluding failed ones")
	}

	// 根据选择模式选择Key
	switch channel.MultiKeyInfo.KeySelectionMode {
	case KeySelectionRandom:
		rand.Seed(time.Now().UnixNano())
		selectedIdx := availableIndices[rand.Intn(len(availableIndices))]
		return keys[selectedIdx], selectedIdx, nil

	case KeySelectionPolling:
		// 从当前轮询索引开始，找到下一个可用的Key
		start := channel.MultiKeyInfo.PollingIndex
		for i := 0; i < len(keys); i++ {
			idx := (start + i) % len(keys)
			for _, availableIdx := range availableIndices {
				if idx == availableIdx {
					channel.MultiKeyInfo.PollingIndex = (idx + 1) % len(keys)
					go channel.saveMultiKeyInfo()
					return keys[idx], idx, nil
				}
			}
		}

		// 如果没有找到，使用第一个可用的
		selectedIdx := availableIndices[0]
		return keys[selectedIdx], selectedIdx, nil

	default:
		selectedIdx := availableIndices[0]
		return keys[selectedIdx], selectedIdx, nil
	}
}

// ClearUsedQuota 清空渠道的使用配额
func (channel *Channel) ClearUsedQuota() error {
	// 使用事务确保数据一致性
	return DB.Transaction(func(tx *gorm.DB) error {
		// 更新渠道的 used_quota 字段为 0
		err := tx.Model(channel).Update("used_quota", 0).Error
		if err != nil {
			return fmt.Errorf("failed to clear used quota: %w", err)
		}

		// 更新当前实例的字段
		channel.UsedQuota = 0

		return nil
	})
}
