package controller

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/audit"
)

func TriggerAuditCompaction(c *gin.Context) {
	dateStr := c.Query("date") // 可选，格式 2006-01-02，默认昨天
	var target time.Time
	if dateStr != "" {
		t, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "date 格式错误，应为 YYYY-MM-DD"})
			return
		}
		target = t
	} else {
		target = time.Now().UTC().AddDate(0, 0, -1)
	}

	go audit.RunCompactionForDate(context.Background(), target)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "compaction 已触发，目标分区: " + target.Format("2006-01-02"),
	})
}
