package model

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"

	"gorm.io/gorm"
)

type Log struct {
	Id               int     `json:"id"`
	RequestId        string  `json:"request_id"`
	UserId           int     `json:"user_id" gorm:"index"`
	CreatedAt        int64   `json:"created_at" gorm:"bigint;index:idx_created_at_type"`
	Type             int     `json:"type" gorm:"index:idx_created_at_type"`
	Content          string  `json:"content"`
	Username         string  `json:"username" gorm:"index:index_username_model_name,priority:2;default:''"`
	TokenName        string  `json:"token_name" gorm:"index;default:''"`
	ModelName        string  `json:"model_name" gorm:"index;index:index_username_model_name,priority:1;default:''"`
	Quota            int     `json:"quota" gorm:"default:0"`
	PromptTokens     int     `json:"prompt_tokens" gorm:"default:0"`
	CompletionTokens int     `json:"completion_tokens" gorm:"default:0"`
	ChannelId        int     `json:"channel" gorm:"index"`
	Duration         float64 `json:"duration" gorm:"default:0"`
}

const (
	LogTypeUnknown = iota
	LogTypeTopup
	LogTypeConsume
	LogTypeManage
	LogTypeSystem
)

func RecordLog(userId int, logType int, content string) {
	if logType == LogTypeConsume && !config.LogConsumeEnabled {
		return
	}
	log := &Log{
		UserId:    userId,
		Username:  GetUsernameById(userId),
		CreatedAt: helper.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	err := LOG_DB.Create(log).Error
	if err != nil {
		logger.SysError("failed to record log: " + err.Error())
	}
}

func RecordConsumeLog(ctx context.Context, userId int, channelId int, promptTokens int, completionTokens int, modelName string, tokenName string, quota int64, content string, duration float64) {
	logger.Info(ctx, fmt.Sprintf("record consume log: userId=%d, channelId=%d, promptTokens=%d, completionTokens=%d, modelName=%s, tokenName=%s, quota=%d, content=%s", userId, channelId, promptTokens, completionTokens, modelName, tokenName, quota, content))
	if !config.LogConsumeEnabled {
		return
	}
	log := &Log{
		UserId:           userId,
		Username:         GetUsernameById(userId),
		CreatedAt:        helper.GetTimestamp(),
		Type:             LogTypeConsume,
		Content:          content,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TokenName:        tokenName,
		ModelName:        modelName,
		Quota:            int(quota),
		ChannelId:        channelId,
		Duration:         duration,
	}
	err := LOG_DB.Create(log).Error
	if err != nil {
		logger.Error(ctx, "failed to record log: "+err.Error())
	}
}

func GetCurrentAllLogsAndCount(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, page int, pageSize int, channel int) (logs []*Log, total int64, err error) {
	var tx *gorm.DB

	// 根据日志类型筛选
	if logType == LogTypeUnknown {
		tx = LOG_DB
	} else {
		tx = LOG_DB.Where("type = ?", logType)
	}

	// 进一步根据提供的参数筛选日志
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	if username != "" {
		tx = tx.Where("username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if channel != 0 {
		tx = tx.Where("channel_id = ?", channel)
	}

	// 首先计算满足条件的总数
	err = tx.Model(&Log{}).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	// 计算起始索引。第一页的起始索引为0。
	offset := (page - 1) * pageSize

	// 然后获取满足条件的日志数据
	err = tx.Order("id desc").Limit(pageSize).Offset(offset).Find(&logs).Error
	if err != nil {
		return nil, total, err
	}

	// 返回日志数据、总数以及错误信息
	return logs, total, nil
}

func GetCurrentUserLogsAndCount(userId int, logType int, startTimestamp int64, endTimestamp int64, modelName string, tokenName string, page int, pageSize int) (logs []*Log, total int64, err error) {
	var tx *gorm.DB

	// 筛选基于用户ID和日志类型
	if logType == LogTypeUnknown {
		tx = LOG_DB.Where("user_id = ?", userId)
	} else {
		tx = LOG_DB.Where("user_id = ? and type = ?", userId, logType)
	}

	// 进一步根据提供的参数筛选日志
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}

	// 首先计算满足条件的总数
	err = tx.Model(&Log{}).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	// 计算起始索引，基于page和pageSize。第一页的起始索引为0。
	offset := (page - 1) * pageSize

	// 然后获取满足条件的日志数据
	err = tx.Select("id, request_id, user_id, created_at, type, username, token_name, model_name, quota, prompt_tokens, completion_tokens, channel_id, duration").Order("id desc").Limit(pageSize).Offset(offset).Find(&logs).Error
	if err != nil {
		return nil, total, err
	}

	// 返回日志数据、总数以及错误信息
	return logs, total, nil
}

func SearchAllLogs(keyword string) (logs []*Log, err error) {
	err = LOG_DB.Where("type = ? or content LIKE ?", keyword, keyword+"%").Order("id desc").Limit(config.MaxRecentItems).Find(&logs).Error
	return logs, err
}

func SearchUserLogs(userId int, keyword string) (logs []*Log, err error) {
	err = LOG_DB.Where("user_id = ? and type = ?", userId, keyword).Order("id desc").Limit(config.MaxRecentItems).Omit("id").Find(&logs).Error
	return logs, err
}

func SumUsedQuota(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, channel int) (quota int64) {
	tx := LOG_DB.Table("logs").Select("ifnull(sum(quota),0)")
	if username != "" {
		tx = tx.Where("username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	if channel != 0 {
		tx = tx.Where("channel_id = ?", channel)
	}
	tx.Where("type = ?", LogTypeConsume).Scan(&quota)
	return quota
}

func SumUsedToken(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string) (token int) {
	tx := LOG_DB.Table("logs").Select("ifnull(sum(prompt_tokens),0) + ifnull(sum(completion_tokens),0)")
	if username != "" {
		tx = tx.Where("username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	tx.Where("type = ?", LogTypeConsume).Scan(&token)
	return token
}

func DeleteOldLog(targetTimestamp int64) (int64, error) {
	result := LOG_DB.Where("created_at < ?", targetTimestamp).Delete(&Log{})
	return result.RowsAffected, result.Error
}

type UsageData struct {
	TotalQuota  int64
	TotalTokens int64
	LogCount    int64
}

func GetAllUsageAndTokenAndCount(timestamp int64) (UsageData, error) {
	var usageData UsageData
	startOfDay := time.Unix(timestamp, 0).UTC().Truncate(24 * time.Hour)
	endOfDay := startOfDay.Add(24 * time.Hour)

	// 计算当天的日志总数
	err := LOG_DB.Model(&Log{}).
		Where("created_at >= ? AND created_at < ?", startOfDay.Unix(), endOfDay.Unix()).
		Count(&usageData.LogCount).Error
	if err != nil {
		return usageData, err
	}

	// 计算当天的总Quota
	err = LOG_DB.Model(&Log{}).
		Select("COALESCE(SUM(quota), 0) as total_quota").
		Where("created_at >= ? AND created_at < ?", startOfDay.Unix(), endOfDay.Unix()).
		Scan(&usageData.TotalQuota).Error
	if err != nil {
		return usageData, err
	}

	// 计算当天的PromptTokens和CompletionTokens的总和
	err = LOG_DB.Model(&Log{}).
		Select("COALESCE(SUM(prompt_tokens + completion_tokens), 0) as total_tokens").
		Where("created_at >= ? AND created_at < ?", startOfDay.Unix(), endOfDay.Unix()).
		Scan(&usageData.TotalTokens).Error
	if err != nil {
		return usageData, err
	}

	return usageData, nil
}

func GetUserUsageAndTokenAndCount(userId int, timestamp int64) (UsageData, error) {
	var usageData UsageData
	startOfDay := time.Unix(timestamp, 0).UTC().Truncate(24 * time.Hour)
	endOfDay := startOfDay.Add(24 * time.Hour)

	// 计算当天的日志总数
	err := LOG_DB.Model(&Log{}).
		Where("user_id = ? AND created_at >= ? AND created_at < ?", userId, startOfDay.Unix(), endOfDay.Unix()).
		Count(&usageData.LogCount).Error
	if err != nil {
		return usageData, err
	}

	// 计算当天的总Quota
	err = LOG_DB.Model(&Log{}).
		Select("COALESCE(SUM(quota), 0) as total_quota").
		Where("user_id = ? AND created_at >= ? AND created_at < ?", userId, startOfDay.Unix(), endOfDay.Unix()).
		Scan(&usageData.TotalQuota).Error
	if err != nil {
		return usageData, err
	}

	// 计算当天的PromptTokens和CompletionTokens的总和
	err = LOG_DB.Model(&Log{}).
		Select("COALESCE(SUM(prompt_tokens + completion_tokens), 0) as total_tokens").
		Where("user_id = ? AND created_at >= ? AND created_at < ?", userId, startOfDay.Unix(), endOfDay.Unix()).
		Scan(&usageData.TotalTokens).Error
	if err != nil {
		return usageData, err
	}

	return usageData, nil
}

type HourlyData struct {
	Hour   string `json:"hour"`
	Amount int64  `json:"amount"`
}

func GetAllGraph(timestamp int64, target string) ([]HourlyData, error) {
	var hourlyData []HourlyData
	startOfDay := time.Unix(timestamp, 0).UTC().Truncate(24 * time.Hour)
	endOfDay := startOfDay.Add(24 * time.Hour)

	// 初始化每个小时的数据为0，格式化小时为 "00", "01", ... "23"
	for i := 0; i < 24; i++ {
		hourlyData = append(hourlyData, HourlyData{Hour: fmt.Sprintf("%02d", i), Amount: 0})
	}

	// 根据target选择要查询的字段
	var field string
	switch target {
	case "quota":
		field = "COALESCE(SUM(quota), 0) as amount, LPAD(HOUR(FROM_UNIXTIME(created_at)), 2, '0') as hour"
	case "token":
		field = "COALESCE(SUM(prompt_tokens + completion_tokens), 0) as amount, LPAD(HOUR(FROM_UNIXTIME(created_at)), 2, '0') as hour"
	case "count":
		field = "COALESCE(COUNT(*), 0) as amount, LPAD(HOUR(FROM_UNIXTIME(created_at)), 2, '0') as hour"
	default:
		return nil, errors.New("invalid target")
	}

	// 从数据库获取数据
	var results []HourlyData
	err := LOG_DB.Model(&Log{}).
		Select(field).
		Where("created_at >= ? AND created_at < ?", startOfDay.Unix(), endOfDay.Unix()).
		Group("HOUR(FROM_UNIXTIME(created_at))").
		Scan(&results).Error
	if err != nil {
		return nil, err
	}

	// 更新hourlyData中的数据
	for _, result := range results {
		for i, h := range hourlyData {
			if h.Hour == result.Hour {
				hourlyData[i].Amount = result.Amount
				break
			}
		}
	}

	return hourlyData, nil
}

func GetUserGraph(userId int, timestamp int64, target string) ([]HourlyData, error) {
	var hourlyData []HourlyData
	startOfDay := time.Unix(timestamp, 0).UTC().Truncate(24 * time.Hour)
	endOfDay := startOfDay.Add(24 * time.Hour)

	// 初始化每个小时的数据为0，格式化小时为 "00", "01", ... "23"
	for i := 0; i < 24; i++ {
		hourlyData = append(hourlyData, HourlyData{Hour: fmt.Sprintf("%02d", i), Amount: 0})
	}

	// 根据target选择要查询的字段
	var field string
	switch target {
	case "quota":
		field = "COALESCE(SUM(quota), 0) as amount, LPAD(HOUR(FROM_UNIXTIME(created_at)), 2, '0') as hour"
	case "token":
		field = "COALESCE(SUM(prompt_tokens + completion_tokens), 0) as amount, LPAD(HOUR(FROM_UNIXTIME(created_at)), 2, '0') as hour"
	case "count":
		field = "COALESCE(COUNT(*), 0) as amount, LPAD(HOUR(FROM_UNIXTIME(created_at)), 2, '0') as hour"
	default:
		return nil, errors.New("invalid target")
	}

	// 从数据库获取数据
	var results []HourlyData
	err := LOG_DB.Model(&Log{}).
		Select(field).
		Where("user_id = ? AND created_at >= ? AND created_at < ?", userId, startOfDay.Unix(), endOfDay.Unix()).
		Group("HOUR(FROM_UNIXTIME(created_at))").
		Scan(&results).Error
	if err != nil {
		return nil, err
	}

	// 更新hourlyData中的数据
	for _, result := range results {
		for i, h := range hourlyData {
			if h.Hour == result.Hour {
				hourlyData[i].Amount = result.Amount
				break
			}
		}
	}

	return hourlyData, nil
}
