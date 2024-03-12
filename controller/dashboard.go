package controller

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/model"
)

func GetAdminDashboard(c *gin.Context) {
	days, _ := strconv.Atoi(c.Query("days"))
	dataQuotes, totalQuota, err := model.GetAllUsersLogsQuoteAndSum(days)
	if err != nil {
		return
	}
	totalCount, err := model.GetAllUsersLogsCount(days)
	if err != nil {
		return
	}

	totalQuotaFloat := float64(totalQuota) / 500000.0
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"dailyquotes": dataQuotes,
			"totalquote":  totalQuotaFloat,
			"totalcount":  totalCount,
		},
	})
	return
}

func GetUserDashboard2(c *gin.Context) {
	userId := c.GetInt("id")
	days, _ := strconv.Atoi(c.Query("days"))
	modelQuotes, totalQuota, err := model.GetUsersLogsQuoteAndSum(userId, days)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "1",
		})
		return
	}
	totalCount, err := model.GetUserLogsCount(userId, days)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "2",
		})
		return
	}
	totalQuotaFloat := float64(totalQuota) / 500000.0
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"dailyquotes": modelQuotes,
			"totalquote":  totalQuotaFloat,
			"totalcount":  totalCount,
		},
	})
	return
}
