package controller

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/model"
)

func GetAllRedemptions(c *gin.Context) {
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 0 {
		page = 0
	}
	pagesize, _ := strconv.Atoi(c.Query("pagesize"))
	currentPage := page
	redemptions, total, err := model.GetAllRedemptionsAndCount(page, pagesize)
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
			"list":        redemptions,
			"currentPage": currentPage,
			"pageSize":    pagesize,
			"total":       total,
		},
	})
	return
}

func SearchRedemptions(c *gin.Context) {
	keyword := c.Query("keyword")
	pageStr := c.Query("page")
	pageSizeStr := c.Query("pagesize")
	statusStr := c.Query("status") // 获取status参数

	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}

	pagesize, err := strconv.Atoi(pageSizeStr)
	if err != nil || pagesize <= 0 {
		pagesize = 10
	}

	var status *int
	if statusStr != "" {
		statusInt, err := strconv.Atoi(statusStr)
		if err == nil && (statusInt == 1 || statusInt == 2) {
			status = &statusInt
		}
	}

	currentPage := page
	channels, total, err := model.SearchRedemptionsAndCount(keyword, status, page, pagesize)
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
			"list":        channels,
			"currentPage": currentPage,
			"pageSize":    pagesize,
			"total":       total,
		},
	})
	return
}

func GetRedemption(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	redemption, err := model.GetRedemptionById(id)
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
		"data":    redemption,
	})
	return
}

func AddRedemption(c *gin.Context) {
	redemption := model.Redemption{}
	err := c.ShouldBindJSON(&redemption)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	if len(redemption.Name) == 0 || len(redemption.Name) > 20 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "The length of the redemption code name must be between 1-20",
		})
		return
	}
	if redemption.Count <= 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "The number of redemption codes must be greater than 0",
		})
		return
	}
	if redemption.Count > 100 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "The number of redemption codes generated in batches at one time cannot be greater than 100",
		})
		return
	}
	var keys []string
	for i := 0; i < redemption.Count; i++ {
		key := helper.GetUUID()
		cleanRedemption := model.Redemption{
			UserId:      c.GetInt("id"),
			Name:        redemption.Name,
			Key:         key,
			CreatedTime: helper.GetTimestamp(),
			Quota:       redemption.Quota,
		}
		err = cleanRedemption.Insert()
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
				"data":    keys,
			})
			return
		}
		keys = append(keys, key)
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    keys,
	})
	return
}

func DeleteRedemption(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	err := model.DeleteRedemptionById(id)
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
	})
	return
}

func BatchDeleteRedemption(c *gin.Context) {
	var request struct {
		Ids []int `json:"ids"`
	}

	if err := c.BindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request body",
		})
		return
	}
	if len(request.Ids) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "No IDs provided for deletion",
		})
		return
	}

	err := model.DeleteRedemptionsByIds(request.Ids)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Redemptions deleted successfully",
	})
}

func UpdateRedemption(c *gin.Context) {

	type RedemptionUpdate struct {
		Id         int    `json:"id"`
		Name       string `json:"name"`
		Quota      int    `json:"quota"`
		StatusOnly *bool  `json:"status_only"`
		Status     int    `json:"status"`
	}

	var redemptionupdate RedemptionUpdate
	err := c.ShouldBindJSON(&redemptionupdate)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	cleanRedemption, err := model.GetRedemptionById(redemptionupdate.Id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	if redemptionupdate.StatusOnly != nil && *redemptionupdate.StatusOnly {
		cleanRedemption.Status = redemptionupdate.Status
	} else {
		// If you add more fields, please also update token.Update()
		cleanRedemption.Name = redemptionupdate.Name
		cleanRedemption.Quota = redemptionupdate.Quota
	}
	err = cleanRedemption.Update()
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
		"data":    cleanRedemption,
	})
	return
}
