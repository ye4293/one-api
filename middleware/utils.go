package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
)

func abortWithMessage(c *gin.Context, statusCode int, message string) {
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"message": helper.MessageWithRequestId(message, c.GetString(logger.RequestIdKey)),
			"type":    "api_error",
		},
	})
	c.Abort()
	logger.Error(c.Request.Context(), message)
}

func abortWithMidjourneyMessage(c *gin.Context, statusCode int, code int, description string) {
	c.JSON(statusCode, gin.H{
		"description": description,
		"type":        "api_error",
		"code":        code,
	})
	c.Abort()
	logger.Error(c.Request.Context(), description)
}
