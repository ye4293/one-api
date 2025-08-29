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

	// 查询所有有可用渠道的优先级（确保abilities和channels状态一致）
	var priorities []int
	err := DB.Table("abilities").
		Joins("JOIN channels ON abilities.channel_id = channels.id").
		Where("abilities."+groupCol+" = ? AND abilities.model = ? AND abilities.enabled = ? AND channels.status = ?", group, model, trueVal, common.ChannelStatusEnabled).
		Pluck("DISTINCT abilities.priority", &priorities).Error
	if err != nil {
		return nil, fmt.Errorf("failed to fetch priorities: %w", err)
	}

	logger.SysLog(fmt.Sprintf("Found priorities for group=%s, model=%s: %v", group, model, priorities))

	if len(priorities) == 0 {
		logger.SysError(fmt.Sprintf("No priorities found for group=%s, model=%s", group, model))
		return nil, errors.New("no priorities available")
	}

	// 确定使用哪个优先级
	var priorityToUse int
	// 首先，按照从大到小的顺序对priorities进行排序
	sort.Slice(priorities, func(i, j int) bool {
		return priorities[i] > priorities[j]
	})

	// 智能选择有可用渠道的优先级
	if ignoreFirstPriority && len(priorities) > 1 {
		// 重试时，尝试选择次高优先级
		priorityToUse = priorities[1]
	} else {
		// 初始请求或只有一个优先级时，选择最高优先级
		priorityToUse = priorities[0]
	}

	// 验证选择的优先级是否有可用渠道
	logger.SysLog(fmt.Sprintf("Selected priority %d for group=%s, model=%s, ignoreFirstPriority=%v", priorityToUse, group, model, ignoreFirstPriority))

	// 获取符合条件的所有渠道及其权重
	var channels []Channel
	err = DB.Table("channels").
		Joins("JOIN abilities ON channels.id = abilities.channel_id").
		Where("abilities."+groupCol+" = ? AND abilities.model = ? AND abilities.enabled = ? AND abilities.priority = ? AND channels.status = ?", group, model, trueVal, priorityToUse, common.ChannelStatusEnabled).
		Find(&channels).Error
	if err != nil {
		return nil, fmt.Errorf("failed to fetch channels: %w", err)
	}

	if len(channels) == 0 {
		logger.SysError(fmt.Sprintf("No channels found for group=%s, model=%s, priority=%d, ignoreFirstPriority=%v", group, model, priorityToUse, ignoreFirstPriority))

		// 回退机制：如果当前优先级没有可用渠道，尝试使用其他优先级
		if ignoreFirstPriority && len(priorities) > 1 {
			logger.SysLog(fmt.Sprintf("Fallback: trying highest priority %d instead", priorities[0]))
			priorityToUse = priorities[0]

			// 重新查询
			err = DB.Table("channels").
				Joins("JOIN abilities ON channels.id = abilities.channel_id").
				Where("abilities."+groupCol+" = ? AND abilities.model = ? AND abilities.enabled = ? AND abilities.priority = ? AND channels.status = ?", group, model, trueVal, priorityToUse, common.ChannelStatusEnabled).
				Find(&channels).Error
			if err != nil {
				return nil, fmt.Errorf("failed to fetch channels in fallback: %w", err)
			}

			if len(channels) > 0 {
				logger.SysLog(fmt.Sprintf("Fallback successful: found %d channels with priority %d", len(channels), priorityToUse))
			}
		}

		if len(channels) == 0 {
			return nil, errors.New("no channels available with the required priority and weight")
		}
	}

	// 计算总权重并准备加权随机选择
	totalWeight := 0
	channelWeights := make([]int, len(channels))
	for i, channel := range channels {
		weight := int(*channel.Weight)
		if weight <= 0 {
			weight = 1
		}
		channelWeights[i] = weight
		totalWeight += weight
	}

	if totalWeight == 0 {
		return nil, errors.New("total weight of channels is zero")
	}

	logger.SysLog(fmt.Sprintf("Found %d channels for group=%s, model=%s, priority=%d, totalWeight=%d", len(channels), group, model, priorityToUse, totalWeight))

	// 调试：输出找到的渠道详情
	for i, ch := range channels {
		logger.SysLog(fmt.Sprintf("  Channel[%d]: ID=%d, Name=%s, Status=%d, Weight=%d", i, ch.Id, ch.Name, ch.Status, *ch.Weight))
	}

	// 使用更好的随机数生成算法
	// 添加额外的随机性来避免高并发时的种子冲突
	randSource := rand.NewSource(time.Now().UnixNano() + int64(rand.Intn(10000)))
	randGen := rand.New(randSource)
	weightThreshold := randGen.Intn(totalWeight) + 1

	currentWeight := 0
	for i, channel := range channels {
		currentWeight += channelWeights[i]
		if currentWeight >= weightThreshold {
			logger.SysLog(fmt.Sprintf("Selected channel %d (name=%s) with weight %d, threshold=%d", channel.Id, channel.Name, channelWeights[i], weightThreshold))
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
