package model

import (
	"context"
	"errors"
	"fmt"
	"math"
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
	Speed            float64 `json:"speed" gorm:"default:0"`
	Title            string  `json:"title"`
	HttpReferer      string  `json:"http_referer"`
	Provider         string  `json:"provider"`
	XRequestID       string  `json:"x_request_id"`
	FirstWordLatency float64 `json:"first_word_latency" gorm:"default:0"`
	VideoTaskId      string  `json:"video_task_id" gorm:"type:varchar(200);index:idx_video_task_id;default:''"`
	IsStream         bool    `json:"is_stream" gorm:"default:false"`
	Other            string  `json:"other"`
}

const (
	LogTypeUnknown = iota
	LogTypeTopup
	LogTypeConsume
	LogTypeManage
	LogTypeSystem
	LogTypeError
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

func RecordConsumeLog(ctx context.Context, userId int, channelId int, promptTokens int, completionTokens int, modelName string, tokenName string, quota int64, content string, duration float64, title string, httpReferer string, isStream bool, firstWordLatency float64) {
	RecordConsumeLogWithOther(ctx, userId, channelId, promptTokens, completionTokens, modelName, tokenName, quota, content, duration, title, httpReferer, isStream, firstWordLatency, "")
}

func RecordConsumeLogWithRequestID(ctx context.Context, userId int, channelId int, promptTokens int, completionTokens int, modelName string, tokenName string, quota int64, content string, duration float64, title string, httpReferer string, isStream bool, firstWordLatency float64, xRequestID string) {
	RecordConsumeLogWithOtherAndRequestID(ctx, userId, channelId, promptTokens, completionTokens, modelName, tokenName, quota, content, duration, title, httpReferer, isStream, firstWordLatency, "", xRequestID)
}

func RecordConsumeLogWithOther(ctx context.Context, userId int, channelId int, promptTokens int, completionTokens int, modelName string, tokenName string, quota int64, content string, duration float64, title string, httpReferer string, isStream bool, firstWordLatency float64, other string) {
	RecordConsumeLogWithOtherAndRequestID(ctx, userId, channelId, promptTokens, completionTokens, modelName, tokenName, quota, content, duration, title, httpReferer, isStream, firstWordLatency, other, "")
}

func RecordConsumeLogWithOtherAndRequestID(ctx context.Context, userId int, channelId int, promptTokens int, completionTokens int, modelName string, tokenName string, quota int64, content string, duration float64, title string, httpReferer string, isStream bool, firstWordLatency float64, other string, xRequestID string) {
	logger.Info(ctx, fmt.Sprintf("record consume log: userId=%d, channelId=%d, promptTokens=%d, completionTokens=%d, modelName=%s, tokenName=%s, quota=%d, content=%s, xRequestID=%s", userId, channelId, promptTokens, completionTokens, modelName, tokenName, quota, content, xRequestID))
	if !config.LogConsumeEnabled {
		return
	}

	var speed float64
	if duration > 0 {
		speed = math.Round(float64(completionTokens)/duration*100) / 100
	} else {
		speed = 0 // 或者设置为其他默认值
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
		Title:            title,
		HttpReferer:      httpReferer,
		Speed:            speed,
		IsStream:         isStream,
		FirstWordLatency: firstWordLatency,
		Other:            other,
		XRequestID:       xRequestID,
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
	err = tx.Select("id, request_id, user_id, created_at, type, username, token_name, model_name, quota, prompt_tokens, completion_tokens, channel_id, duration, is_stream, first_word_latency, content").Order("id desc").Limit(pageSize).Offset(offset).Find(&logs).Error
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

type HourlyData struct {
	Hour   string `json:"hour"`
	Amount int64  `json:"amount"`
}

func GetAllGraph(timestamp int64, target string) ([]HourlyData, error) {
	var hourlyData []HourlyData
	startOfDay := time.Unix(timestamp, 0).UTC().Truncate(24 * time.Hour)
	endOfDay := startOfDay.Add(24 * time.Hour)

	// 初始化每个小时的数据为0
	for i := 0; i < 24; i++ {
		hourlyData = append(hourlyData, HourlyData{Hour: fmt.Sprintf("%02d", i), Amount: 0})
	}

	// 构建查询
	var field string
	hourExpr := "LPAD(HOUR(FROM_UNIXTIME(created_at)), 2, '0')"
	switch target {
	case "quota":
		field = fmt.Sprintf("COALESCE(SUM(quota), 0) as amount, %s as hour", hourExpr)
	case "token":
		field = fmt.Sprintf("COALESCE(SUM(prompt_tokens + completion_tokens), 0) as amount, %s as hour", hourExpr)
	case "count":
		field = fmt.Sprintf("COALESCE(COUNT(*), 0) as amount, %s as hour", hourExpr)
	default:
		return nil, errors.New("invalid target")
	}

	// 执行查询
	var results []HourlyData
	err := LOG_DB.Model(&Log{}).
		Select(field).
		Where("created_at >= ? AND created_at < ?", startOfDay.Unix(), endOfDay.Unix()).
		Group(hourExpr).
		Scan(&results).Error

	if err != nil {
		return nil, err
	}

	// 更新数据
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

	// 初始化每个小时的数据为0
	for i := 0; i < 24; i++ {
		hourlyData = append(hourlyData, HourlyData{Hour: fmt.Sprintf("%02d", i), Amount: 0})
	}

	// 构建查询
	var field string
	hourExpr := "LPAD(HOUR(FROM_UNIXTIME(created_at)), 2, '0')"
	switch target {
	case "quota":
		field = fmt.Sprintf("COALESCE(SUM(quota), 0) as amount, %s as hour", hourExpr)
	case "token":
		field = fmt.Sprintf("COALESCE(SUM(prompt_tokens + completion_tokens), 0) as amount, %s as hour", hourExpr)
	case "count":
		field = fmt.Sprintf("COALESCE(COUNT(*), 0) as amount, %s as hour", hourExpr)
	default:
		return nil, errors.New("invalid target")
	}

	// 执行查询
	var results []HourlyData
	err := LOG_DB.Model(&Log{}).
		Select(field).
		Where("user_id = ? AND created_at >= ? AND created_at < ?", userId, startOfDay.Unix(), endOfDay.Unix()).
		Group(hourExpr).
		Scan(&results).Error

	if err != nil {
		return nil, err
	}

	// 更新数据
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

func GetAllMetrics() (rpm int64, tpm int64, quotaSum int64, err error) {
	// 获取当前时间戳
	currentTime := time.Now().Unix()
	// 一分钟前的时间戳
	oneMinuteAgo := currentTime - 60

	// 单次查询获取所有指标
	err = LOG_DB.Model(&Log{}).
		Select(`
            COUNT(*) as rpm, 
            COALESCE(SUM(prompt_tokens + completion_tokens), 0) as tpm,
            COALESCE(SUM(quota), 0) as quota_sum
        `).
		Where("created_at >= ? AND created_at <= ?",
			oneMinuteAgo, currentTime).
		Row().Scan(&rpm, &tpm, &quotaSum)

	if err != nil {
		return 0, 0, 0, err
	}

	return rpm, tpm, quotaSum, nil
}

func GetUserMetrics(userId int) (rpm int64, tpm int64, quotaSum int64, err error) {
	// 获取当前时间戳
	currentTime := time.Now().Unix()
	// 一分钟前的时间戳
	oneMinuteAgo := currentTime - 60

	// 单次查询获取所有指标
	err = LOG_DB.Model(&Log{}).
		Select(`
            COUNT(*) as rpm, 
            COALESCE(SUM(prompt_tokens + completion_tokens), 0) as tpm,
            COALESCE(SUM(quota), 0) as quota_sum
        `).
		Where("user_id = ? AND created_at >= ? AND created_at <= ?",
			userId, oneMinuteAgo, currentTime).
		Row().Scan(&rpm, &tpm, &quotaSum)

	if err != nil {
		return 0, 0, 0, err
	}

	return rpm, tpm, quotaSum, nil
}

// GetDailyMetrics 获取当天所有用户的请求总数和配额消耗总数
func GetDailyMetrics() (requestPD int64, usedPD int64, err error) {
	// 获取当天开始和结束的时间戳
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	endOfDay := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 999999999, now.Location()).Unix()

	// 单次查询获取当天的统计数据
	err = LOG_DB.Model(&Log{}).
		Select(`
            COUNT(*) as request_pd,
            COALESCE(SUM(quota), 0) as used_pd
        `).
		Where("created_at >= ? AND created_at <= ?",
			startOfDay, endOfDay).
		Row().Scan(&requestPD, &usedPD)

	if err != nil {
		return 0, 0, err
	}

	return requestPD, usedPD, nil
}

// GetUserDailyMetrics 获取指定用户当天的请求总数和配额消耗总数
func GetUserDailyMetrics(userId int) (requestPD int64, usedPD int64, err error) {
	// 获取当天开始和结束的时间戳
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	endOfDay := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 999999999, now.Location()).Unix()

	// 单次查询获取当天的统计数据
	err = LOG_DB.Model(&Log{}).
		Select(`
            COUNT(*) as request_pd,
            COALESCE(SUM(quota), 0) as used_pd
        `).
		Where("user_id = ? AND created_at >= ? AND created_at <= ?",
			userId, startOfDay, endOfDay).
		Row().Scan(&requestPD, &usedPD)

	if err != nil {
		return 0, 0, err
	}

	return requestPD, usedPD, nil
}

// ModelQuotaStats 模型消耗统计结构
type ModelQuotaStats struct {
	ModelName string `json:"model_name"`
	QuotaSum  int64  `json:"quota_sum"`
}

// GetTopModelQuotaStats 管理员查询当天消耗最高的前5个模型
func GetTopModelQuotaStats() ([]ModelQuotaStats, error) {
	// 获取当天的开始和结束时间戳
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	endOfDay := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 999999999, now.Location()).Unix()

	var stats []ModelQuotaStats

	err := LOG_DB.Model(&Log{}).
		Select("model_name, COALESCE(SUM(quota), 0) as quota_sum").
		Where("created_at >= ? AND created_at <= ?", startOfDay, endOfDay).
		Group("model_name").
		Order("quota_sum DESC").
		Limit(5).
		Scan(&stats).Error

	if err != nil {
		return nil, err
	}

	return stats, nil
}

// GetUserTopModelQuotaStats 普通用户查询自己当天消耗最高的前5个模型
func GetUserTopModelQuotaStats(userId int) ([]ModelQuotaStats, error) {
	// 获取当天的开始和结束时间戳
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	endOfDay := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 999999999, now.Location()).Unix()

	var stats []ModelQuotaStats

	err := LOG_DB.Model(&Log{}).
		Select("model_name, COALESCE(SUM(quota), 0) as quota_sum").
		Where("user_id = ? AND created_at >= ? AND created_at <= ?",
			userId, startOfDay, endOfDay).
		Group("model_name").
		Order("quota_sum DESC").
		Limit(5).
		Scan(&stats).Error

	if err != nil {
		return nil, err
	}

	return stats, nil
}

// GetLogsByVideoTaskId 通过视频任务ID查找日志记录
func GetLogsByVideoTaskId(videoTaskId string) (*Log, error) {
	var log Log
	err := LOG_DB.Where("video_task_id = ?", videoTaskId).First(&log).Error
	if err != nil {
		return nil, err
	}
	return &log, nil
}

// RecordVideoConsumeLog 记录视频任务的消费日志，包含VideoTaskId
func RecordVideoConsumeLog(ctx context.Context, userId int, channelId int, promptTokens int, completionTokens int, modelName string, tokenName string, quota int64, content string, duration float64, title string, httpReferer string, videoTaskId string) {
	logger.Info(ctx, fmt.Sprintf("record video consume log: userId=%d, channelId=%d, promptTokens=%d, completionTokens=%d, modelName=%s, tokenName=%s, quota=%d, content=%s, videoTaskId=%s", userId, channelId, promptTokens, completionTokens, modelName, tokenName, quota, content, videoTaskId))
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
		Title:            title,
		HttpReferer:      httpReferer,
		Speed:            0,
		VideoTaskId:      videoTaskId,
	}
	err := LOG_DB.Create(log).Error
	if err != nil {
		logger.Error(ctx, "failed to record video log: "+err.Error())
	}
}

// UpdateLogQuotaAndTokens 更新日志记录的Quota和CompletionTokens字段
func UpdateLogQuotaAndTokens(videoTaskId string, quota int64, completionTokens int) error {
	result := LOG_DB.Model(&Log{}).
		Where("video_task_id = ?", videoTaskId).
		Updates(map[string]interface{}{
			"quota":             int(quota),
			"completion_tokens": completionTokens,
		})

	if result.Error != nil {
		return result.Error
	}

	if result.RowsAffected == 0 {
		return errors.New("no log found with the given video_task_id")
	}

	return nil
}
