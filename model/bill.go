package model

type Bill struct {
	Id        int     `json:"id"`
	Username  string  `json:"username"`
	UserId    int     `json:"user_id"`
	Type      string  `json:"type"`
	CreatedAt int64   `json:"create_at"`
	UpdatedAt int64   `json:"updated_at"`
	Amount    float64 `json:"amount"`
	Status    int     `json:"status"`
	SourceId  string  `json:"source_id"` //uuid-crypto  apporderid-stripe
}

func UpdateBill(SourceId string, bill Bill) error {
	err := DB.Model(&Bill{}).Where("source_id=?", SourceId).Updates(bill).Error
	if err != nil {
		return err
	}
	return nil
}
