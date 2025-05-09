package controller

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
)

func UserLevelUpgrade(userId int) error {
	user, err := model.GetUserById(userId, true)
	if err != nil {
		return err
	}

	levels := []string{"Lv1", "Lv2", "Lv3", "Lv4", "Lv5"}
	levelMap := map[string]int64{
		"Lv1": 0,
		"Lv2": 5 * 500000,
		"Lv3": 50 * 500000,
		"Lv4": 100 * 500000,
		"Lv5": 250 * 500000,
	}

	totalQuota := user.Quota + user.UsedQuota

	for i := 0; i < len(levels)-1; i++ {
		currentLevel := levels[i]
		nextLevel := levels[i+1]

		if user.Group == currentLevel &&
			totalQuota > levelMap[currentLevel] &&
			totalQuota <= levelMap[nextLevel] {
			user.Group = nextLevel
			user.Update(false)
			break
		}
	}
	return nil
}

func GetChargeConfigs(c *gin.Context) {
	chargeConfigs, err := model.GetChargeConfigs()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"list": chargeConfigs,
		},
	})
}

func CreateChargeOrder(c *gin.Context) {
	var CreateChargeOrderRequest struct {
		ChrargeId int `json:"charge_id"`
	}
	//绑定json和结构体
	if err := c.BindJSON(&CreateChargeOrderRequest); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	chargeId := CreateChargeOrderRequest.ChrargeId
	//获取充值配置
	//创建支付链接
	userId := c.GetInt("id")
	chargeUrl, appOrderId, err := model.CreateStripOrder(userId, chargeId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"charge_url":   chargeUrl,
			"app_order_id": appOrderId,
		},
	})
}

func StripeCallback(c *gin.Context) {
	err := model.HandleStripeCallback(c.Request)
	if err != nil {
		logger.SysLog(fmt.Sprintf("err1:%+v\n", err))
		c.String(http.StatusBadRequest, "fail")
		return
	}
	userId := c.GetInt("id")
	err = UserLevelUpgrade(userId)
	if err != nil {
		return
	}

	c.String(http.StatusOK, "ok")
}

func GetUserChargeOrders(c *gin.Context) {
	page, _ := strconv.Atoi(c.Query("page"))
	appOrderId := c.DefaultQuery("app_order_id", "")
	if page < 0 {
		page = 1
	}
	pagesize, _ := strconv.Atoi(c.Query("pagesize"))
	status, _ := strconv.Atoi(c.Query("status"))
	currentPage := page
	userId := c.GetInt("id")
	myRole := c.GetInt("role")
	var conditions = make(map[string]interface{}, 10)
	if appOrderId != "" {
		conditions["app_order_id"] = appOrderId
	}
	if status != 0 {
		conditions["status"] = status
	}
	if myRole != common.RoleRootUser || myRole != common.RoleAdminUser {
		conditions["user_id"] = userId
	}

	orders, total, err := model.GetUserChargeOrdersAndCount(conditions, page, pagesize)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"list":        orders,
			"currentPage": currentPage,
			"pageSize":    pagesize,
			"total":       total,
		},
	})
	return
}
