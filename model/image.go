package model

import "fmt"

type Image struct {
	TaskId     string `json:"task_id"`
	Username   string `json:"username"`
	ChannelId  int    `json:"channel_id"`
	UserId     int    `json:"user_id"`
	Model      string `json:"model"`
	Status     string `json:"status"`
	FailReason string `json:"fail_reason"`
	ImageId    string `json:"image_id"`
	StoreUrl   string `json:"store_url"`
	Provider   string `json:"provider"`
	CreatedAt  int64  `json:"created_at"`
	Mode       string `json:"mode"`
	N          int    `json:"n"`
	Quota      int64  `json:"quota"`
}

func (image *Image) Insert() error {
	var err error
	err = DB.Create(image).Error
	return err
}

func GetImageByTaskId(taskId string) (*Image, error) {
	var image Image
	err := DB.Where("task_id = ?", taskId).First(&image).Error
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
		Order("created_at DESC").
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
	tx := DB.Model(&Image{}) // 明确指定使用 Image 模型

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
		Order("created_at DESC"). // 按创建时间降序
		Offset(offset).
		Limit(pageSize).
		Find(&images).Error

	if err != nil {
		return nil, 0, fmt.Errorf("find images error: %w", err)
	}

	return images, total, nil
}
