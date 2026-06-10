package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
)

func RelayPanicRecover() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				ctx := c.Request.Context()
				logger.Errorf(ctx, "panic detected: %v", err)
				logger.Errorf(ctx, "stacktrace from panic: %s", string(debug.Stack()))
				logger.Errorf(ctx, "request: %s %s", c.Request.Method, c.Request.URL.Path)
				body, _ := common.GetRequestBody(c)
				logger.Errorf(ctx, "request body: %s", string(body))
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": gin.H{
						"message": fmt.Sprintf("Panic detected, error: %v. Please submit send email help@ezlinkai.com", err),
						"type":    "api_panic",
					},
				})
				c.Abort()
			}
		}()
		c.Next()
	}
}
