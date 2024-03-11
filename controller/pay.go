package controller

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
)

type PayRequestFront struct {
	Id     int     `json:"id"`
	Chain  string  `json:"chain"`
	Token  string  `json:"token"`
	Amount float64 `json:"amount"`
}

type PayRequestSend struct {
	Ticker        string `json:"ticker"`
	Callback      string `json:"callback"`
	Address       string `json:"address"`
	Pending       int    `json:"pending"`
	Confirmations int    `json:"confirmations"`
	Post          int    `json:"post"`
	Priority      string `json:"priority"`
	MultiToken    int    `json:"multi_token"`
	Convert       int    `json:"convert"`
}

type PaymentCallback struct {
	TransactionID string  `json:"transaction_id"`
	Amount        float64 `json:"amount"`
	Status        string  `json:"status"`
	// 其他可能的字段...
}

func GetPayRequest(c *gin.Context) {
	var payrequestfront PayRequestFront
	err := json.NewDecoder(c.Request.Body).Decode(&payrequestfront)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"message": "failed to get json",
			"success": false,
		})
	}
}
