package middleware

import (
	"encoding/json"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
)

// AccessLogEntry HTTP 访问日志结构（JSON 格式，用于 Loki 维度筛选）
type AccessLogEntry struct {
	Ts        string `json:"ts"`
	Level     string `json:"level"`
	RequestId string `json:"request_id"`
	Status    int    `json:"status"`
	LatencyMs int64  `json:"latency_ms"`
	ClientIP  string `json:"client_ip"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Service   string `json:"service"`
	Instance  string `json:"instance"`
}

func SetUpLogger(server *gin.Engine) {
	server.Use(gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		// 只记录非 200 的请求（错误/异常请求）
		if param.StatusCode == 200 {
			return ""
		}

		// Get request ID from context
		var requestID string
		if param.Keys != nil {
			if v, ok := param.Keys[logger.RequestIdKey]; ok {
				requestID, _ = v.(string)
			}
		}

		// Determine log level based on status code
		level := "info"
		if param.StatusCode >= 500 {
			level = "error"
		} else if param.StatusCode >= 400 {
			level = "warn"
		}

		entry := AccessLogEntry{
			Ts:        param.TimeStamp.Format(time.RFC3339Nano),
			Level:     level,
			RequestId: requestID,
			Status:    param.StatusCode,
			LatencyMs: param.Latency.Milliseconds(),
			ClientIP:  param.ClientIP,
			Method:    param.Method,
			Path:      param.Path,
			Service:   config.ServiceName,
			Instance:  config.InstanceId,
		}

		jsonBytes, err := json.Marshal(entry)
		if err != nil {
			// Fallback if marshal fails
			return `{"level":"error","msg":"access log marshal error"}` + "\n"
		}
		return string(jsonBytes) + "\n"
	}))
}
