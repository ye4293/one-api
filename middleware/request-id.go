package middleware

import (
	"context"
	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
)

func RequestId() func(c *gin.Context) {
	return func(c *gin.Context) {
		// 优先使用 X-Request-ID，其次 Request-ID（均 case-insensitive），没传用系统生成的
		id := c.GetHeader(logger.RequestIdKey)
		if id == "" {
			id = c.GetHeader("Request-ID")
		}
		if id == "" {
			id = helper.GenRequestID()
		}
		c.Set(logger.RequestIdKey, id)
		ctx := context.WithValue(c.Request.Context(), logger.RequestIdKey, id)
		c.Request = c.Request.WithContext(ctx)
		// 设置到请求头，确保下游通过 GetHeader 也能获取
		c.Request.Header.Set(logger.RequestIdKey, id)
		c.Header(logger.RequestIdKey, id)
		c.Next()
	}
}
