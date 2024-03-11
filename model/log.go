package model

import (
	"context"
	"fmt"
	"time"

	"github.com/songquanpeng/one-api/common"
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
	err := DB.Create(log).Error
	if err != nil {
		logger.SysError("failed to record log: " + err.Error())
	}
}

func RecordConsumeLog(ctx context.Context, userId int, channelId int, promptTokens int, completionTokens int, modelName string, tokenName string, quota int, content string, duration float64) {
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
		Quota:            quota,
		ChannelId:        channelId,
		Duration:         duration,
	}
	err := DB.Create(log).Error
	if err != nil {
		logger.Error(ctx, "failed to record log: "+err.Error())
	}
}

func GetCurrentAllLogsAndCount(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, page int, pageSize int, channel int) (logs []*Log, total int64, err error) {
	var tx *gorm.DB

	// 根据日志类型筛选
	if logType == LogTypeUnknown {
		tx = DB
	} else {
		tx = DB.Where("type = ?", logType)
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
		tx = DB.Where("user_id = ?", userId)
	} else {
		tx = DB.Where("user_id = ? AND type = ?", userId, logType)
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
	err = DB.Where("type = ? or content LIKE ?", keyword, keyword+"%").Order("id desc").Limit(config.MaxRecentItems).Find(&logs).Error
	return logs, err
}

func SearchUserLogs(userId int, keyword string) (logs []*Log, err error) {
	err = DB.Where("user_id = ? and type = ?", userId, keyword).Order("id desc").Limit(config.MaxRecentItems).Omit("id").Find(&logs).Error
	return logs, err
}

func SumUsedQuota(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, channel int) (quota int) {
	tx := DB.Table("logs").Select("ifnull(sum(quota),0)")
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
	tx := DB.Table("logs").Select("ifnull(sum(prompt_tokens),0) + ifnull(sum(completion_tokens),0)")
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
	result := DB.Where("created_at < ?", targetTimestamp).Delete(&Log{})
	return result.RowsAffected, result.Error
}

type LogStatistic struct {
	Day              string `gorm:"column:day"`
	ModelName        string `gorm:"column:model_name"`
	RequestCount     int    `gorm:"column:request_count"`
	Quota            int    `gorm:"column:quota"`
	PromptTokens     int    `gorm:"column:prompt_tokens"`
	CompletionTokens int    `gorm:"column:completion_tokens"`
}

func SearchLogsByDayAndModel(userId, start, end int) (LogStatistics []*LogStatistic, err error) {
	groupSelect := "DATE_FORMAT(FROM_UNIXTIME(created_at), '%Y-%m-%d') as day"

	if common.UsingPostgreSQL {
		groupSelect = "TO_CHAR(date_trunc('day', to_timestamp(created_at)), 'YYYY-MM-DD') as day"
	}

	if common.UsingSQLite {
		groupSelect = "strftime('%Y-%m-%d', datetime(created_at, 'unixepoch')) as day"
	}

	err = DB.Raw(`
		SELECT `+groupSelect+`,
		model_name, count(1) as request_count,
		sum(quota) as quota,
		sum(prompt_tokens) as prompt_tokens,
		sum(completion_tokens) as completion_tokens
		FROM logs
		WHERE type=2
		AND user_id= ?
		AND created_at BETWEEN ? AND ?
		GROUP BY day, model_name
		ORDER BY day, model_name
	`, userId, start, end).Scan(&LogStatistics).Error

	return LogStatistics, err
}

type ModelQuota struct {
	ModelName string
	Quota     float64
}

type DateQuotaSummary struct {
	Date        string
	ModelQuotas []ModelQuota
}

func GetAllUsersLogsQuoteAndSum(days int) ([]DateQuotaSummary, float64, error) {
	// 计算起始时间
	startTime := time.Now().AddDate(0, 0, -days)

	// 生成所有日期的初始列表
	allDates := make([]string, 0, days)
	for d := 0; d < days; d++ {
		date := startTime.AddDate(0, 0, d).Format("01-02")
		allDates = append(allDates, date)
	}

	// 用于存储查询结果
	var results []struct {
		Date      string
		ModelName string
		Quota     float64
	}

	// 查询每一天的不同ModelName的Quota之和，只返回月份和日子
	if err := DB.Table("logs").
		Select("DATE_FORMAT(FROM_UNIXTIME(created_at), '%m-%d') as date, model_name, SUM(quota) as quota").
		Where("created_at >= ? AND type = ?", startTime.Unix(), 2).
		Group("DATE_FORMAT(FROM_UNIXTIME(created_at), '%m-%d'), model_name").
		Order("DATE_FORMAT(FROM_UNIXTIME(created_at), '%m-%d')").
		Find(&results).Error; err != nil {
		return nil, 0, err
	}

	// 创建一个map来按日期聚合数据
	dateQuotaMap := make(map[string][]ModelQuota)
	for _, date := range allDates {
		dateQuotaMap[date] = []ModelQuota{} // 初始化空切片
	}
	for _, result := range results {
		dateQuotaMap[result.Date] = append(dateQuotaMap[result.Date], ModelQuota{
			ModelName: result.ModelName,
			Quota:     result.Quota,
		})
	}

	// 将map转换为切片，并确保即使没有结果，也能为每个日期返回一个条目
	var dateQuotas []DateQuotaSummary
	for _, date := range allDates {
		dateQuotas = append(dateQuotas, DateQuotaSummary{
			Date:        date,
			ModelQuotas: dateQuotaMap[date], // 这将添加一个空切片或者含有数据的切片
		})
	}

	// 计算总和
	var totalQuotaSum float64
	if err := DB.Table("logs").
		Where("created_at >= ? AND type = ?", startTime.Unix(), 2).
		Select("SUM(quota) as quota").
		Row().Scan(&totalQuotaSum); err != nil {
		return nil, 0, err
	}

	// 如果数据库返回的是NULL，确保将总和设置为0
	if totalQuotaSum != totalQuotaSum { // 利用 NaN 不等于自身的特性来检查
		totalQuotaSum = 0
	}

	return dateQuotas, totalQuotaSum, nil
}

func GetUsersLogsQuoteAndSum(userId int, days int) ([]DateQuotaSummary, float64, error) {
	// 计算起始时间
	startTime := time.Now().AddDate(0, 0, -days)

	// 生成所有日期的初始列表
	allDates := make([]string, 0, days)
	for d := 0; d < days; d++ {
		date := startTime.AddDate(0, 0, d).Format("01-02")
		allDates = append(allDates, date)
	}

	// 用于存储查询结果
	var results []struct {
		Date      string
		ModelName string
		Quota     float64
	}

	// 查询每一天的不同ModelName的Quota之和，只返回月份和日子
	err := DB.Table("logs").
		Select("DATE_FORMAT(FROM_UNIXTIME(created_at), '%m-%d') as date, model_name, SUM(quota) as quota").
		Where("created_at >= ? AND type = ? AND user_id = ?", startTime.Unix(), 2, userId).
		Group("DATE_FORMAT(FROM_UNIXTIME(created_at), '%m-%d'), model_name").
		Order("DATE_FORMAT(FROM_UNIXTIME(created_at), '%m-%d')").
		Find(&results).Error
	if err != nil {
		return nil, 0, err
	}

	// 创建一个map来按日期聚合数据
	dateQuotaMap := make(map[string][]ModelQuota)
	for _, date := range allDates {
		dateQuotaMap[date] = []ModelQuota{} // 初始化空切片
	}
	for _, result := range results {
		dateQuotaMap[result.Date] = append(dateQuotaMap[result.Date], ModelQuota{
			ModelName: result.ModelName,
			Quota:     result.Quota,
		})
	}

	// 将map转换为切片
	var dateQuotas []DateQuotaSummary
	for _, date := range allDates {
		dateQuotas = append(dateQuotas, DateQuotaSummary{
			Date:        date,
			ModelQuotas: dateQuotaMap[date], // 这将添加一个空切片或者含有数据的切片
		})
	}

	// 计算总和
	var totalQuotaSum float64
	err = DB.Table("logs").
		Where("created_at >= ? AND type = ? AND user_id = ?", startTime.Unix(), 2, userId).
		Select("SUM(quota) as quota").
		Row().Scan(&totalQuotaSum)
	if err != nil {
		return nil, 0, err
	}

	return dateQuotas, totalQuotaSum, nil
}

func GetAllUsersLogsCount(days int) (int, error) {
	// 计算起始时间
	startTime := time.Now().AddDate(0, 0, -days)

	// 定义变量来存储总数量
	totalCount := 0

	// 查询指定时间范围内的日志条目总数量
	if err := DB.Table("logs").
		Where("created_at >= ? AND type = ? ", startTime.Unix(), 2).
		Select("COUNT(*) as count").
		Row().Scan(&totalCount); err != nil {
		return 0, err
	}

	return totalCount, nil
}

func GetUserLogsCount(userId int, days int) (int, error) {
	// 计算起始时间
	startTime := time.Now().AddDate(0, 0, -days)

	// 定义变量来存储总数量
	totalCount := 0

	// 查询指定用户和时间范围内的日志条目总数量
	if err := DB.Table("logs").
		Where("user_id = ? AND created_at >= ? AND type=?", userId, startTime.Unix(), 2).
		Select("COUNT(*) as count").
		Row().Scan(&totalCount); err != nil {
		return 0, err
	}

	return totalCount, nil
}
