package controller

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/model"
)

type DashboardData struct {
	CurrentQuota int64                   `json:"current_quota"`
	UsedQuota    int64                   `json:"used_quota"`
	TPM          int64                   `json:"tpm"`
	RPM          int64                   `json:"rpm"`
	QuotaPM      int64                   `json:"quota_pm"`
	RequestPD    int64                   `json:"request_pd"`
	UsedPD       int64                   `json:"used_pd"`
	ModelStats   []model.ModelQuotaStats `json:"model_stats"` // 使用 model 包中的类型
}

func GetAdminDashboard(c *gin.Context) {
	// 获取管理员用户信息
	userId := c.GetInt("id")
	user, err := model.GetUserById(userId, false)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// 获取所有用户的总配额和已使用配额

	// 获取最近一分钟的统计数据
	rpm, tpm, quotaPM, err := model.GetAllMetrics()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Failed to get metrics: " + err.Error(),
		})
		return
	}

	requestPd, usedPd, err := model.GetDailyMetrics()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Failed to get metrics: " + err.Error(),
		})
		return
	}

	var modelStats []model.ModelQuotaStats
	modelStats, err = model.GetTopModelQuotaStats()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Failed to get model statistics: " + err.Error(),
		})
		return
	}
	// 构造返回数据
	dashboard := DashboardData{
		CurrentQuota: user.Quota,
		UsedQuota:    user.UsedQuota,
		TPM:          tpm,
		RPM:          rpm,
		QuotaPM:      quotaPM,
		RequestPD:    requestPd,
		UsedPD:       usedPd,
		ModelStats:   modelStats, // 添加模型统计数据
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    dashboard,
		"message": "",
	})
}

func GetUserDashboard(c *gin.Context) {
	// 获取管理员用户信息
	userId := c.GetInt("id")
	user, err := model.GetUserById(userId, false)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// 获取最近一分钟的统计数据
	rpm, tpm, quotaPM, err := model.GetUserMetrics(userId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Failed to get metrics: " + err.Error(),
		})
		return
	}

	requestPd, usedPd, err := model.GetUserDailyMetrics(userId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Failed to get metrics: " + err.Error(),
		})
		return
	}
	var modelStats []model.ModelQuotaStats
	modelStats, err = model.GetUserTopModelQuotaStats(userId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Failed to get model statistics: " + err.Error(),
		})
		return
	}
	// 构造返回数据
	dashboard := DashboardData{
		CurrentQuota: user.Quota,
		UsedQuota:    user.UsedQuota,
		TPM:          tpm,
		RPM:          rpm,
		QuotaPM:      quotaPM,
		RequestPD:    requestPd,
		UsedPD:       usedPd,
		ModelStats:   modelStats, // 添加模型统计数据
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    dashboard,
		"message": "",
	})
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
