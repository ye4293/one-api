package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/logger"
)

func RelayPanicRecover() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				logger.SysError(fmt.Sprintf("panic detected: %v", err))
				logger.SysError(fmt.Sprintf("stacktrace from panic: %s", string(debug.Stack())))
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": gin.H{
						"message": fmt.Sprintf("Panic detected, error: %v. Please submit send email help@ezlinkai.com", err),
						"type":    "one_api_panic",
					},
				})
				c.Abort()
			}
		}()
		c.Next()
	}
}
