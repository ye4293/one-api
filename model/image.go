package model

type Image struct {
	TaskId     int    `json:"id"`
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
}

func (image *Image) Insert() error {
	var err error
	err = DB.Create(image).Error
	return err
}
