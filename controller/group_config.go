package controller

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/model"
)

// GetAllGroupConfigs 获取所有分组等级配置
func GetAllGroupConfigs(c *gin.Context) {
	configs, err := model.GetAllGroupConfigs()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "获取分组配置失败: " + err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    configs,
	})
}

// CreateGroupConfigHandler 创建分组等级配置
func CreateGroupConfigHandler(c *gin.Context) {
	var config model.GroupConfig
	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无效的参数: " + err.Error(),
		})
		return
	}

	if config.GroupKey == "" || config.DisplayName == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "group_key 和 display_name 不能为空",
		})
		return
	}

	// discount 是计费乘数：1.0 = 无折扣，0.5 = 五折，0 = 免费。
	// 任何 > 1 的值都会让当前分组的所有请求被放大 N 倍，必须挡住。
	if config.Discount < 0 || config.Discount > 1 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "discount 必须在 0-1 之间（乘数，1=无折扣；前端按百分比展示，UI 保存时会自动除以 100）",
		})
		return
	}

	if err := model.CreateGroupConfig(&config); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "创建分组配置失败: " + err.Error(),
		})
		return
	}

	// 同步更新 common.GroupRatio
	common.GroupRatio[config.GroupKey] = config.Discount

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "创建成功",
	})
}

// UpdateGroupConfigHandler 更新分组等级配置
func UpdateGroupConfigHandler(c *gin.Context) {
	var config model.GroupConfig
	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无效的参数: " + err.Error(),
		})
		return
	}

	if config.ID == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "缺少 id",
		})
		return
	}

	// 同 Create：discount 必须在 [0, 1] 区间内，防止 UI 以外的客户端误把百分比传进来
	if config.Discount < 0 || config.Discount > 1 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "discount 必须在 0-1 之间（乘数，1=无折扣；前端按百分比展示，UI 保存时会自动除以 100）",
		})
		return
	}

	if err := model.UpdateGroupConfig(&config); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "更新分组配置失败: " + err.Error(),
		})
		return
	}

	// 同步更新 common.GroupRatio
	common.GroupRatio[config.GroupKey] = config.Discount

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "更新成功",
	})
}

// DeleteGroupConfigHandler 删除分组等级配置
func DeleteGroupConfigHandler(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无效的 id",
		})
		return
	}

	// 先查询要删除的配置，以便同步清理内存
	config, err := model.GetGroupConfigByID(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "未找到该分组配置",
		})
		return
	}

	if err := model.DeleteGroupConfigByID(id); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "删除分组配置失败: " + err.Error(),
		})
		return
	}

	// 同步删除 common.GroupRatio 中的条目
	delete(common.GroupRatio, config.GroupKey)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "删除成功",
	})
}
