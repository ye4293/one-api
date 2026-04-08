package model

import (
	"fmt"

	"gorm.io/gorm"
)

type Image struct {
	Id            int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	TaskId        string `gorm:"type:varchar(255);index:idx_images_task_id,length:40" json:"task_id"`
	Username      string `gorm:"index:idx_images_username" json:"username"`
	ChannelId     int    `gorm:"index:idx_images_channel_id" json:"channel_id"`
	UserId        int    `gorm:"index:idx_images_user_id" json:"user_id"`
	Model         string `gorm:"index:idx_images_model" json:"model"`
	Status        string `gorm:"index:idx_images_status" json:"status"`
	FailReason    string `json:"fail_reason"`
	ImageId       string `json:"image_id"`
	StoreUrl      string `json:"store_url"`
	Provider      string `gorm:"index:idx_images_provider" json:"provider"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `gorm:"autoUpdateTime" json:"updated_at"`
	Mode          string `json:"mode"`
	N             int    `json:"n"`
	Quota         int64  `json:"quota"`
	Detail        string `json:"detail"`
	TotalDuration int    `json:"total_duration"`          // 总时长（秒）
	Result        string `gorm:"type:text" json:"result"` // API 响应结果（JSON 格式）
}

// applyImageIdRange 将时间范围转为 id 范围并应用到 images 查询
func applyImageIdRange(tx *gorm.DB, startTimestamp, endTimestamp int64) *gorm.DB {
	return applyTimestampIdRange(tx, DB, "images", startTimestamp, endTimestamp)
}

func (image *Image) Insert() error {
	var err error
	err = DB.Create(image).Error
	return err
}

func (image *Image) Update() error {
	return DB.Save(image).Error
}

func GetImageByTaskId(taskId string) (*Image, error) {
	var image Image
	err := DB.Where("task_id = ?", taskId).First(&image).Error
	return &image, err
}

func GetImageByTaskIdAndUserId(taskId string, userId int) (*Image, error) {
	var image Image
	err := DB.Where("task_id = ? AND user_id = ?", taskId, userId).First(&image).Error
	return &image, err
}

func GetImageById(id int64) (*Image, error) {
	var image Image
	err := DB.Where("id = ?", id).First(&image).Error
	return &image, err
}

func GetCurrentAllImagesAndCount(
	startTimestamp int64,
	endTimestamp int64,
	taskId string,
	provider string,
	username string,
	modelName string,
	page int,
	pageSize int,
	channel int,
) (images []Image, total int64, err error) {
	// 初始化查询，直接指定模型
	tx := DB.Model(&Image{})

	// 时间范围转 id 范围（二分查找主键）
	tx = applyImageIdRange(tx, startTimestamp, endTimestamp)

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
	if channel != 0 {
		tx = tx.Where("channel_id = ?", channel)
	}

	// 获取总数
	err = tx.Count(&total).Error
	if err != nil {
		return nil, 0, fmt.Errorf("count images error: %w", err)
	}

	// 如果没有数据，直接返回空结果
	if total == 0 {
		return make([]Image, 0), 0, nil
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
		Order("id DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&images).Error

	if err != nil {
		return nil, 0, fmt.Errorf("find images error: %w", err)
	}

	return images, total, nil
}

func GetCurrentUserImagesAndCount(
	startTimestamp int64,
	endTimestamp int64,
	taskId string,
	provider string,
	userId int,
	modelName string,
	page int,
	pageSize int,
) (images []Image, total int64, err error) {
	// 初始化查询，并指定模型
	tx := DB.Model(&Image{})

	// 构建查询条件
	tx = tx.Where("user_id = ?", userId)

	// 时间范围转 id 范围（二分查找主键）
	tx = applyImageIdRange(tx, startTimestamp, endTimestamp)

	// 添加其他可选条件
	if taskId != "" {
		tx = tx.Where("task_id = ?", taskId)
	}
	if provider != "" {
		tx = tx.Where("provider = ?", provider)
	}
	if modelName != "" {
		tx = tx.Where("model = ?", modelName)
	}

	// 获取总数
	err = tx.Count(&total).Error
	if err != nil {
		return nil, 0, fmt.Errorf("count images error: %w", err)
	}

	// 如果没有数据，直接返回空结果
	if total == 0 {
		return make([]Image, 0), 0, nil
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
		Order("id DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&images).Error

	if err != nil {
		return nil, 0, fmt.Errorf("find images error: %w", err)
	}

	return images, total, nil
}
