package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/monitor"
)

// CloudWatchMetrics CloudWatch 指标中间件
func CloudWatchMetrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. 记录开始时间
		startTime := time.Now()

		// 2. 增加并发计数
		monitor.IncrementConcurrent()
		defer monitor.DecrementConcurrent()

		// 3. 处理请求
		c.Next()

		// 4. 计算延迟
		latency := time.Since(startTime)

		// 5. 获取状态码
		statusCode := c.Writer.Status()

		// 6. 判断是否成功（2xx, 3xx 为成功）
		success := statusCode >= 200 && statusCode < 400

		// 7. 记录到 CloudWatch
		monitor.RecordRequest(latency, statusCode, success)
	}
}
