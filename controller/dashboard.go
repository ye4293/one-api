package controller

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/model"
)

func GetAdminDashboard(c *gin.Context) {
	timestamp, _ := strconv.ParseInt(c.Query("time"), 10, 64)
	usageData, err := model.GetAllUsageAndTokenAndCount(timestamp)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data":    usageData,
		"success": true,
		"message": "",
	})
	return

}

func GetUserDashboard(c *gin.Context) {
	userId := c.GetInt("id")
	timestamp, _ := strconv.ParseInt(c.Query("time"), 10, 64)
	usageData, err := model.GetUserUsageAndTokenAndCount(userId, timestamp)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data":    usageData,
		"success": true,
		"message": "",
	})
	return
}

func GetAllGraph(c *gin.Context) {
	target := c.Query("target")
	timestamp, _ := strconv.ParseInt(c.Query("time"), 10, 64)
	hourlyDate, err := model.GetAllGraph(timestamp, target)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data":    hourlyDate,
		"success": true,
		"message": "",
	})
	return
}

func GetUserGraph(c *gin.Context) {
	userId := c.GetInt("id")
	target := c.Query("target")
	timestamp, _ := strconv.ParseInt(c.Query("time"), 10, 64)
	hourlyDate, err := model.GetUserGraph(userId, timestamp, target)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data":    hourlyDate,
		"success": true,
		"message": "",
	})
	return
}
