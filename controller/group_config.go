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
