package model

type Flux struct {
	Id        string `json:"id"`
	Username  string `json:"username"`
	UserId    int    `json:"user_id"`
	Prompt    string `json:"prompt"`
	ChannelId int    `json:"channel_id"`
	Model     string `json:"model"`
}

func (flux *Flux) Insert() error {
	var err error
	err = DB.Create(flux).Error
	return err
}

func GetChannelIdByFluxId(id string) int {
	var flux Flux
	var err error
	err = DB.Select("channel_id").Where("id = ?", id).First(&flux).Error
	if err != nil {
		return 0
	}
	return flux.ChannelId
}
