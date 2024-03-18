package controller

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/message"
	"github.com/songquanpeng/one-api/model"
)

func GetQrcode(c *gin.Context) {
	if config.AddressOut == "" || config.CryptCallbackUrl == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无法获取crypt支付，请先填入 服务器回调地址 和钱包收款地址！",
		})
		return
	}
	userId := c.GetInt("id")
	ticker := c.DefaultQuery("ticker", "polygon/usdt")
	qrcode, err := model.GetQrcode(ticker, userId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"message": err.Error(),
			"success": false,
		})
	} else {
		c.JSON(http.StatusOK, gin.H{
			"message": "success",
			"success": true,
			"data":    qrcode,
		})
	}
}
func GetPayChannel(c *gin.Context) {
	data := make(map[string][]string, 5)
	data["usdt"] = []string{"polygon"}
	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"success": true,
		"data":    data,
	})
}
func CryptCallback(c *gin.Context) {
	var response model.CryptCallbackResponse
	if err := c.ShouldBindQuery(&response); err != nil {
		c.String(http.StatusUnauthorized, err.Error())
	}
	err := model.HandleCryptCallback(response)
	if err != nil {
		c.String(http.StatusUnauthorized, err.Error())
	}
	userId := response.UserId
	addAmount := response.ValueCoin
	if response.Result == "3" {
		err := model.IncreaseUserQuota(userId, int64(addAmount*500000))
		if err != nil {
			return
		}
		//send email and back message
		email, err := model.GetUserEmail(userId)
		if err != nil {
			return
		}
		subject := fmt.Sprintf("%s's recharge notification email", config.SystemName)
		content := fmt.Sprintf("<p>hello,You have successfully recharged %f$</p>"+"<p>Congratulations on getting one step closer to the AI world!</p>", response.ValueCoin)
		err = message.SendEmail(subject, email, content)
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
		})
	}
}
