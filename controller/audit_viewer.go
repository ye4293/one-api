package controller

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/audit"
)

func GetAuditLogs(c *gin.Context) {
	startTS, err := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	if err != nil || startTS <= 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "start_timestamp and end_timestamp are required",
		})
		return
	}
	endTS, err := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	if err != nil || endTS <= 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "start_timestamp and end_timestamp are required",
		})
		return
	}

	if endTS-startTS > 31*24*3600 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "date range cannot exceed 31 days",
		})
		return
	}

	page, _ := strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.Query("pagesize"))
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	params := audit.QueryParams{
		StartTimestamp: startTS,
		EndTimestamp:   endTS,
		Page:           page,
		PageSize:       pageSize,
		XRequestID:     c.Query("x_request_id"),
		ActualModel:    c.Query("actual_model"),
	}
	if uid, err := strconv.Atoi(c.Query("user_id")); err == nil {
		params.UserID = uid
	}
	if cid, err := strconv.Atoi(c.Query("channel_id")); err == nil {
		params.ChannelID = cid
	}
	if sc, err := strconv.Atoi(c.Query("status_code")); err == nil {
		params.StatusCode = sc
	}

	logs, total, err := audit.QueryLogs(c.Request.Context(), params)
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
			"list":        logs,
			"currentPage": page,
			"pageSize":    pageSize,
			"total":       total,
		},
	})
}

func GetAuditDetail(c *gin.Context) {
	xRequestID := c.Query("x_request_id")
	if xRequestID == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "x_request_id is required",
		})
		return
	}

	startTS, err := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	if err != nil || startTS <= 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "start_timestamp and end_timestamp are required",
		})
		return
	}
	endTS, err := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	if err != nil || endTS <= 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "start_timestamp and end_timestamp are required",
		})
		return
	}

	detail, err := audit.QueryDetail(c.Request.Context(), xRequestID, startTS, endTS)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	if detail == nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "record not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    detail,
	})
}
