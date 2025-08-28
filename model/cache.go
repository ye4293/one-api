package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
)

var (
	TokenCacheSeconds         = config.SyncFrequency
	UserId2GroupCacheSeconds  = config.SyncFrequency
	UserId2QuotaCacheSeconds  = config.SyncFrequency
	UserId2StatusCacheSeconds = config.SyncFrequency
)

func CacheGetTokenByKey(key string) (*Token, error) {
	keyCol := "`key`"
	if common.UsingPostgreSQL {
		keyCol = `"key"`
	}
	var token Token
	if !common.RedisEnabled {
		err := DB.Where(keyCol+" = ?", key).First(&token).Error
		return &token, err
	}
	tokenObjectString, err := common.RedisGet(fmt.Sprintf("token:%s", key))
	if err != nil {
		err := DB.Where(keyCol+" = ?", key).First(&token).Error
		if err != nil {
			return nil, err
		}
		jsonBytes, err := json.Marshal(token)
		if err != nil {
			return nil, err
		}
		err = common.RedisSet(fmt.Sprintf("token:%s", key), string(jsonBytes), time.Duration(TokenCacheSeconds)*time.Second)
		if err != nil {
			logger.SysError("Redis set token error: " + err.Error())
		}
		return &token, nil
	}
	err = json.Unmarshal([]byte(tokenObjectString), &token)
	return &token, err
}

func CacheGetUserGroup(id int) (group string, err error) {
	if !common.RedisEnabled {
		return GetUserGroup(id)
	}
	group, err = common.RedisGet(fmt.Sprintf("user_group:%d", id))
	if err != nil {
		group, err = GetUserGroup(id)
		if err != nil {
			return "", err
		}
		err = common.RedisSet(fmt.Sprintf("user_group:%d", id), group, time.Duration(UserId2GroupCacheSeconds)*time.Second)
		if err != nil {
			logger.SysError("Redis set user group error: " + err.Error())
		}
	}
	return group, err
}

func fetchAndUpdateUserQuota(ctx context.Context, id int) (quota int64, err error) {
	quota, err = GetUserQuota(id)
	if err != nil {
		return 0, err
	}
	err = common.RedisSet(fmt.Sprintf("user_quota:%d", id), fmt.Sprintf("%d", quota), time.Duration(UserId2QuotaCacheSeconds)*time.Second)
	if err != nil {
		logger.Error(ctx, "Redis set user quota error: "+err.Error())
	}
	return
}

func CacheGetUserQuota(ctx context.Context, id int) (quota int64, err error) {
	if !common.RedisEnabled {
		return GetUserQuota(id)
	}
	quotaString, err := common.RedisGet(fmt.Sprintf("user_quota:%d", id))
	if err != nil {
		return fetchAndUpdateUserQuota(ctx, id)
	}
	quota, err = strconv.ParseInt(quotaString, 10, 64)
	if err != nil {
		return 0, nil
	}
	if quota <= config.PreConsumedQuota { // when user's quota is less than pre-consumed quota, we need to fetch from db
		logger.Infof(ctx, "user %d's cached quota is too low: %d, refreshing from db", quota, id)
		return fetchAndUpdateUserQuota(ctx, id)
	}
	return quota, nil
}

func CacheUpdateUserQuota(ctx context.Context, id int) error {
	if !common.RedisEnabled {
		return nil
	}
	quota, err := CacheGetUserQuota(ctx, id)
	if err != nil {
		return err
	}
	err = common.RedisSet(fmt.Sprintf("user_quota:%d", id), fmt.Sprintf("%d", quota), time.Duration(UserId2QuotaCacheSeconds)*time.Second)
	return err
}

func CacheUpdateUserQuota2(id int) error {
	if !common.RedisEnabled {
		return nil
	}
	quota, err := GetUserQuota(id)
	if err != nil {
		return err
	}
	err = common.RedisSet(fmt.Sprintf("user_quota:%d", id), fmt.Sprintf("%d", quota), time.Duration(UserId2QuotaCacheSeconds)*time.Second)
	return err
}

func CacheDecreaseUserQuota(id int, quota int64) error {
	if !common.RedisEnabled {
		return nil
	}
	err := common.RedisDecrease(fmt.Sprintf("user_quota:%d", id), int64(quota))
	return err
}

func CacheIsUserEnabled(userId int) (bool, error) {
	if !common.RedisEnabled {
		return IsUserEnabled(userId)
	}
	enabled, err := common.RedisGet(fmt.Sprintf("user_enabled:%d", userId))
	if err == nil {
		return enabled == "1", nil
	}

	userEnabled, err := IsUserEnabled(userId)
	if err != nil {
		return false, err
	}
	enabled = "0"
	if userEnabled {
		enabled = "1"
	}
	err = common.RedisSet(fmt.Sprintf("user_enabled:%d", userId), enabled, time.Duration(UserId2StatusCacheSeconds)*time.Second)
	if err != nil {
		logger.SysError("Redis set user enabled error: " + err.Error())
	}
	return userEnabled, err
}

// 首先在文件顶部声明全局变量时就进行初始化
var (
	channelSyncLock      sync.RWMutex
	group2model2channels = make(map[string]map[string][]*Channel)
	channelsIDM          = make(map[int]*Channel)
)

func InitChannelCache() {
	// 创建新的 map 实例
	newChannelId2channel := make(map[int]*Channel)
	var channels []*Channel
	DB.Where("status = ?", common.ChannelStatusEnabled).Find(&channels)
	for _, channel := range channels {
		newChannelId2channel[channel.Id] = channel
	}

	var abilities []*Ability
	DB.Find(&abilities)
	groups := make(map[string]bool)
	for _, ability := range abilities {
		groups[ability.Group] = true
	}

	// 创建新的 map 实例
	newGroup2model2channels := make(map[string]map[string][]*Channel)
	newChannelsIDM := make(map[int]*Channel)

	// 初始化每个组的 model map
	for group := range groups {
		newGroup2model2channels[group] = make(map[string][]*Channel)
	}

	// 处理 channels
	for _, channel := range channels {
		newChannelsIDM[channel.Id] = channel
		groups := strings.Split(channel.Group, ",")
		for _, group := range groups {
			group = strings.TrimSpace(group) // 添加空格处理
			if group == "" {
				continue // 跳过空组
			}

			models := strings.Split(channel.Models, ",")
			for _, model := range models {
				model = strings.TrimSpace(model) // 添加空格处理
				if model == "" {
					continue // 跳过空模型
				}

				// 确保 map 已初始化
				if _, ok := newGroup2model2channels[group]; !ok {
					newGroup2model2channels[group] = make(map[string][]*Channel)
				}
				if _, ok := newGroup2model2channels[group][model]; !ok {
					newGroup2model2channels[group][model] = make([]*Channel, 0)
				}

				newGroup2model2channels[group][model] = append(newGroup2model2channels[group][model], channel)
			}
		}
	}

	// 排序
	for group, model2channels := range newGroup2model2channels {
		for model, channels := range model2channels {
			sort.Slice(channels, func(i, j int) bool {
				return channels[i].GetPriority() > channels[j].GetPriority()
			})
			newGroup2model2channels[group][model] = channels
		}
	}

	// 使用锁保护全局变量更新
	channelSyncLock.Lock()
	group2model2channels = newGroup2model2channels
	channelsIDM = newChannelsIDM
	channelSyncLock.Unlock()

	logger.SysLog("channels synced from database")
}

func SyncChannelCache(frequency int) {
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		logger.SysLog("syncing channels from database")
		InitChannelCache()
	}
}

func CacheGetRandomSatisfiedChannel(group string, model string, ignoreFirstPriority bool) (*Channel, error) {
	groupCol := "`group`"
	trueVal := "1"
	if common.UsingPostgreSQL {
		groupCol = `"group"`
		trueVal = "true"
	}

	// 查询所有可用的优先级
	var priorities []int
	err := DB.Model(&Ability{}).Where(groupCol+" = ? and model = ? and enabled = "+trueVal, group, model).
		Pluck("DISTINCT priority", &priorities).Error
	if err != nil {
		return nil, fmt.Errorf("failed to fetch priorities: %w", err)
	}

	if len(priorities) == 0 {
		// 提供更详细的错误信息用于调试
		var totalChannels int64
		var enabledChannels int64
		var disabledChannels int64

		DB.Model(&Channel{}).Count(&totalChannels)
		DB.Model(&Channel{}).Where("status = ?", common.ChannelStatusEnabled).Count(&enabledChannels)
		DB.Model(&Channel{}).Where("status = ?", common.ChannelStatusAutoDisabled).Count(&disabledChannels)

		var totalAbilities int64
		var enabledAbilities int64
		DB.Model(&Ability{}).Where(groupCol+" = ? and model = ?", group, model).Count(&totalAbilities)
		DB.Model(&Ability{}).Where(groupCol+" = ? and model = ? and enabled = "+trueVal, group, model).Count(&enabledAbilities)

		logger.SysError(fmt.Sprintf("No available channels for group=%s, model=%s. Stats: total_channels=%d, enabled_channels=%d, disabled_channels=%d, total_abilities=%d, enabled_abilities=%d",
			group, model, totalChannels, enabledChannels, disabledChannels, totalAbilities, enabledAbilities))

		return nil, fmt.Errorf("no available channels for group '%s' and model '%s'. Total channels: %d, Enabled: %d, Auto-disabled: %d",
			group, model, totalChannels, enabledChannels, disabledChannels)
	}

	// 确定使用哪个优先级
	var priorityToUse int
	// 首先，按照从大到小的顺序对priorities进行排序
	sort.Slice(priorities, func(i, j int) bool {
		return priorities[i] > priorities[j]
	})

	// 如果有多于一个优先级且需要忽略最高优先级
	if len(priorities) > 1 && ignoreFirstPriority {
		// 选择次高优先级，即在降序列表中的第二个元素
		priorityToUse = priorities[1]
	} else {
		// 否则，选择最高优先级，即在降序列表中的第一个元素
		priorityToUse = priorities[0]
	}

	// 获取符合条件的所有渠道及其权重
	var channels []Channel
	err = DB.Table("channels").
		Joins("JOIN abilities ON channels.id = abilities.channel_id").
		Where("`abilities`.`group` = ? AND abilities.model = ? AND abilities.enabled = ? AND abilities.priority = ?", group, model, trueVal, priorityToUse).
		Find(&channels).Error
	if err != nil {
		return nil, fmt.Errorf("failed to fetch channels: %w", err)
	}

	if len(channels) == 0 {
		return nil, errors.New("no channels available with the required priority and weight")
	}

	totalWeight := 0
	for _, channel := range channels {
		weight := int(*channel.Weight)
		if weight <= 0 {
			weight = 1
		}
		totalWeight += weight
	}

	if totalWeight == 0 {
		return nil, errors.New("total weight of channels is zero")
	}

	// 生成一个随机权重阈值
	randSource := rand.NewSource(time.Now().UnixNano())
	randGen := rand.New(randSource)
	weightThreshold := randGen.Intn(totalWeight) + 1

	currentWeight := 0
	for _, channel := range channels {
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

func CacheGetChannel(id int) (*Channel, error) {
	if !config.MemoryCacheEnabled {
		return GetChannelById(id, true)
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, errors.New(fmt.Sprintf("当前渠道# %d，已不存在", id))
	}
	return c, nil
}
