package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
)

func GetQrcode(c *gin.Context) {
	if config.AddressOut == "" || config.CryptCallbackUrl == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Unable to obtain crypt payment, please fill in the server callback address and wallet payment address first!",
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
		logger.SysLog("failed to binf query")
		c.String(http.StatusUnauthorized, err.Error())
		return
	}
	userId := response.UserId
	username := model.GetUsernameById(userId)
	err := model.HandleCryptCallback(response, username)
	if err != nil {
		logger.SysLog("failed to handle callback")
		c.String(http.StatusUnauthorized, err.Error())
		return
	}
	err = UserLevelUpgrade(userId)
	if err != nil {
		return
	}

	c.String(http.StatusOK, "ok")
}
