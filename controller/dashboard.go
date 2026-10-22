package controller

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/model"
)

// 时间范围和用户范围由服务端决定，不接受客户端覆盖用户 ID 或角色。
func GetDashboardModelUsage(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	now := time.Now().Unix()
	q := model.RequestStatsQuery{
		Kind: "dashboard_models", ActorID: c.GetInt("id"),
		Admin: c.GetInt("role") >= common.RoleAdminUser,
		Start: now/3600*3600 - 23*3600, End: now / 60 * 60, Interval: 3600,
	}
	if q.ActorID <= 0 {
		writeRequestStatistics(c, ctx, q, requestStatsError(401, "请先登录"))
		return
	}
	if !q.Admin {
		id := int64(q.ActorID)
		q.Filters.UserID = &id
	}
	writeRequestStatistics(c, ctx, q, nil)
}

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

	// 一次性获取所有 Dashboard 指标
	metrics, err := model.GetAllDashboardMetrics()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Failed to get metrics: " + err.Error(),
		})
		return
	}

	// 构造返回数据
	dashboard := DashboardData{
		CurrentQuota: user.Quota,
		UsedQuota:    user.UsedQuota,
		TPM:          metrics.TPM,
		RPM:          metrics.RPM,
		QuotaPM:      metrics.QuotaPM,
		RequestPD:    metrics.RequestPD,
		UsedPD:       metrics.UsedPD,
		ModelStats:   metrics.ModelStats,
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    dashboard,
		"message": "",
	})
}

func GetUserDashboard(c *gin.Context) {
	// 获取用户信息
	userId := c.GetInt("id")
	user, err := model.GetUserById(userId, false)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// 一次性获取所有 Dashboard 指标
	metrics, err := model.GetUserDashboardMetrics(userId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Failed to get metrics: " + err.Error(),
		})
		return
	}

	// 构造返回数据
	dashboard := DashboardData{
		CurrentQuota: user.Quota,
		UsedQuota:    user.UsedQuota,
		TPM:          metrics.TPM,
		RPM:          metrics.RPM,
		QuotaPM:      metrics.QuotaPM,
		RequestPD:    metrics.RequestPD,
		UsedPD:       metrics.UsedPD,
		ModelStats:   metrics.ModelStats,
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
