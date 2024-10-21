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
