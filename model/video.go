package model

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

type Video struct {
	Prompt      string `json:"prompt"`
	CreatedAt   int64  `json:"created_at"`
	TaskId      string `json:"task_id" gorm:"type:varchar(200);index:idx_tid,length:40"`
	Type        string `json:"type"`
	Provider    string `json:"provider"`
	Mode        string `json:"mode"`
	Duration    string `json:"duration"`
	Username    string `json:"username"`
	ChannelId   int    `json:"channel_id"`
	UserId      int    `json:"user_id"`
	Model       string `json:"model"`
	Status      string `json:"status"`
	FailReason  string `json:"fail_reason"`
	VideoId     string `json:"video_id"`
	StoreUrl    string `json:"store_url"` // 直接存储JSON化的URL数组字符串
	Quota       int64  `json:"quota"`
	N           int    `json:"n"`
	Credentials string `json:"credentials"` // 保存任务创建时使用的完整JSON凭证
}

func (video *Video) Insert() error {
	var err error
	err = DB.Create(video).Error
	return err
}

func (video *Video) Update() error {
	var err error
	// TaskId现在是主键，需要提供WHERE条件进行更新
	if video.TaskId != "" {
		err = DB.Model(&Video{}).Where("task_id = ?", video.TaskId).Updates(video).Error
	} else {
		return fmt.Errorf("TaskId must be provided for update")
	}
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

func GetVideoTaskByVideoId(videoId string) (*Video, error) {
	var video Video
	result := DB.Where("video_id = ?", videoId).First(&video)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("no record found for video_id: %s", videoId)
		}
		return nil, result.Error
	}
	return &video, nil
}

// UpdateVideoTaskStatusWithCondition 原子性更新视频任务状态，防止并发冲突
// 只有当当前状态等于expectedStatus时才更新为newStatus
func UpdateVideoTaskStatusWithCondition(taskId string, expectedStatus string, newStatus string, quota int64) bool {
	// 使用WHERE条件确保原子性更新
	result := DB.Model(&Video{}).
		Where("task_id = ? AND status = ?", taskId, expectedStatus).
		Updates(map[string]interface{}{
			"status": newStatus,
			"quota":  quota,
		})

	// 如果RowsAffected为1，说明更新成功
	return result.RowsAffected == 1
}

func GetCurrentAllVideosAndCount(
	startTimestamp int64,
	endTimestamp int64,
	taskId string,
	provider string,
	username string,
	modelName string,
	page int,
	pageSize int,
	channel int,
) (videos []*Video, total int64, err error) {
	// 初始化查询，直接指定模型
	tx := DB.Model(&Video{})

	// 添加查询条件
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
		tx = tx.Where("model = ?", modelName)
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
		pageSize = 10
	}
	if page <= 0 {
		page = 1
	}
	offset := (page - 1) * pageSize

	// 执行分页查询
	err = tx.
		Order("created_at DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&videos).Error

	if err != nil {
		return nil, 0, fmt.Errorf("find videos error: %w", err)
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

	// 初始化查询，并指定模型
	tx = DB.Model(&Video{}) // 明确指定使用 Video 模型

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

// UpdateStoreUrl 更新视频任务的存储URL
func UpdateVideoStoreUrl(taskId string, storeUrl string) error {
	result := DB.Model(&Video{}).
		Where("task_id = ?", taskId).
		Update("store_url", storeUrl)

	if result.Error != nil {
		return fmt.Errorf("failed to update store_url for task_id %s: %w", taskId, result.Error)
	}

	if result.RowsAffected == 0 {
		return fmt.Errorf("no record found for task_id: %s", taskId)
	}

	return nil
}
