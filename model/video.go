package model

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

type Video struct {
	Prompt    string `json:"prompt"`
	CreatedAt int64  `json:"created_at"`
	TaskId    string `json:"task_id"`
	Type      string `json:"type"`
	Provider  string `json:"provider"`
	Mode      string `json:"mode"`
	Duration  string `json:"duration"`
	Username  string `json:"username"`
	ChannelId int    `json:"channel_id"`
	UseId     int    `json:"user_id"`
	Model     string `json:"model"`
}

func (video *Video) Insert() error {
	var err error
	err = DB.Create(video).Error
	return err
}

func GetVideoTaskById(taskId string) (*Video, error) {
	var video Video
	result := DB.Where("task_id = ?", taskId).First(&video)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("no record found for task_id: %s", taskId)
		}
		return nil, result.Error
	}
	return &video, nil
}

func GetCurrentAllVideosAndCount(startTimestamp int64, endTimestamp int64, taskId string, provider string, username string, modelName string, page int, pageSize int, channel int) (videos []*Video, total int64, err error) {
	var tx *gorm.DB

	// 初始化查询
	tx = DB // 假设你的数据库连接是 DB

	// 根据提供的参数筛选视频
	if taskId != "" {
		tx = tx.Where("task_id = ?", taskId)
	}
	if provider != "" {
		tx = tx.Where("provider = ?", provider)
	}
	if username != "" {
		tx = tx.Where("username = ?", username)
	}
	if modelName != "" {
		tx = tx.Where("model = ?", modelName) // 假设 modelName 对应 Video 结构体中的 Type 字段
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

	// 计算满足条件的总数
	err = tx.Model(&Video{}).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	// 计算分页的起始索引
	offset := (page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}

	// 获取分页数据
	err = tx.Order("created_at desc").Limit(pageSize).Offset(offset).Find(&videos).Error
	if err != nil {
		return nil, total, err
	}

	return videos, total, nil
}

func GetCurrentUserVideosAndCount(
	startTimestamp int64,
	endTimestamp int64,
	taskId string,
	provider string,
	userId int,
	modelName string,
	page int,
	pageSize int,
) (videos []*Video, total int64, err error) {
	var tx *gorm.DB

	// 初始化查询
	tx = DB // 假设你的数据库连接是 DB

	// 构建查询条件
	tx = tx.Where("user_id = ?", userId)

	// 添加时间范围条件
	if startTimestamp > 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp > 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}

	// 添加其他可选条件
	if taskId != "" {
		tx = tx.Where("task_id = ?", taskId)
	}
	if provider != "" {
		tx = tx.Where("provider = ?", provider)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}

	// 获取总数
	err = tx.Count(&total).Error
	if err != nil {
		return nil, 0, fmt.Errorf("count videos error: %w", err)
	}

	// 如果没有数据，直接返回空结果
	if total == 0 {
		return make([]*Video, 0), 0, nil
	}

	// 处理分页参数
	if pageSize <= 0 {
		pageSize = 10 // 默认每页10条
	}
	if page <= 0 {
		page = 1 // 默认第1页
	}
	offset := (page - 1) * pageSize

	// 执行分页查询
	err = tx.
		Order("created_at DESC"). // 按创建时间降序
		Offset(offset).
		Limit(pageSize).
		Find(&videos).Error

	if err != nil {
		return nil, 0, fmt.Errorf("tx videos error: %w", err)
	}

	return videos, total, nil
}
