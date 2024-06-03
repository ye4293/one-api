package model

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

type Sd struct {
	Id           int    `json:"id"`
	Username     string `json:"username"`
	ChannelId    int    `json:"channel_id"`
	UserId       int    `json:"user_id" gorm:"index"`
	Prompt       string `json:"prompt"`
	Description  string `json:"description"`
	Quota        int64  `json:"quota"`
	StoreUrl     string `json:"store_url"`
	CreatedAt    int64  `json:"created_at"`
	FinishAt     int64  `json:"finish_at"`
	Model        string `json:"model"`
	GenerationId string `json:"generation_id"`
}

func (sd *Sd) Insert() error {
	var err error
	err = DB.Create(sd).Error
	return err
}

func GetChannelIdByGenerationId(generationId string) (int, error) {
	var sd Sd
	result := DB.Where("generation_id = ?", generationId).First(&sd)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return 0, fmt.Errorf("no record found for generation_id: %s", generationId)
		}
		return 0, result.Error
	}
	return sd.ChannelId, nil
}
