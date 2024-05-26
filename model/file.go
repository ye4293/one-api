package model

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

type File struct {
	Id         int64  `json:"id"`
	UserId     int    `json:"user_id"`
	CreatTime  int64  `json:"creat_time"`
	FinishTime int64  `json:"finish_time"`
	Bytes      int64  `json:"bytes"`
	StoreUrl   string `json:"store_url"`
	Purpose    string `json:"purpose"`
	FileName   string `json:"file_name"`
}

func SumBytesByUserId(userId int) (int64, error) {
	if userId == 0 {
		return 0, errors.New("userId is empty!")
	}

	var totalBytes int64
	err := DB.Model(&File{}).Where("user_id = ?", userId).Select("SUM(bytes)").Scan(&totalBytes).Error
	if err != nil {
		return 0, err
	}

	return totalBytes, nil
}

func DeleteFileByFilename(filename string) error {
	var file File
	err := DB.Where("file_name = ?", filename).First(&file).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("file with filename '%s' not found", filename)
		}
		return err
	}

	err = file.Delete()
	if err != nil {
		return err
	}

	return nil
}

func (file *File) Insert() error {
	var err error
	err = DB.Create(file).Error
	return err
}

func (file *File) Delete() error {
	var err error
	err = DB.Delete(file).Error
	return err
}
