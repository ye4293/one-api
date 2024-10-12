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
	Username  string `json:"username"`
	ChannelId int    `json:"channel_id"`
	UseId     int    `json:"user_id"`
}

func (video *Video) Insert() error {
	var err error
	err = DB.Create(video).Error
	return err
}

func GetChannelIdByTaskIdAndType(taskId string, typeParam string) (int, error) {
	var video Video
	result := DB.Where("task_id = ? AND type = ?", taskId, typeParam).First(&video)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return 0, fmt.Errorf("no record found for task_id: %s and type: %s", taskId, typeParam)
		}
		return 0, result.Error
	}
	return video.ChannelId, nil
}

func GetVideoTaskByIdAndProvider(taskId string, provider string) (*Video, error) {
	var video Video
	result := DB.Where("task_id = ? AND provider = ?", taskId, provider).First(&video)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("no record found for task_id: %s and provider: %s", taskId, provider)
		}
		return nil, result.Error
	}
	return &video, nil
}
