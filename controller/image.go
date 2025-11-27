package controller

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/model"
)

func GetALLImages(c *gin.Context) {
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 0 {
		page = 0
	}
	pagesize, _ := strconv.Atoi(c.Query("pagesize"))
	currentPage := page
	taskId := c.Query("taskid")
	provider := c.Query("provider")
	username := c.Query("username")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel_id"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	images, total, err := model.GetCurrentAllImagesAndCount(
		startTimestamp,
		endTimestamp,
		taskId,
		provider,
		username,
		modelName,
		currentPage,
		pagesize,
		channel,
	)
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
			"list":         images,
			"currentPage":  currentPage,
			"UserpageSize": pagesize,
			"total":        total,
		},
	})
	return
}

func GetUserImages(c *gin.Context) {
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 0 {
		page = 0
	}
	pagesize, _ := strconv.Atoi(c.Query("pagesize"))
	currentPage := page
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	taskId := c.Query("taskid")
	provider := c.Query("provider")
	userId := c.GetInt("id")
	username := c.Query("username")
	images, total, err := model.GetCurrentUserImagesAndCount(
		startTimestamp,
		endTimestamp,
		taskId,
		provider,
		userId,
		username,
		currentPage,
		pagesize,
	)
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
			"list":        images,
			"currentPage": currentPage,
			"pageSize":    pagesize,
			"total":       total,
		},
	})
	return
}
