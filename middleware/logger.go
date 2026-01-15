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
	Msg       string `json:"msg"` // 添加 msg 字段以兼容 Promtail 配置
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
		// 只记录非 200 的请求，或者在 debug 模式下记录所有请求
		if param.StatusCode == 200 && !config.DebugEnabled {
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
			Msg:       "HTTP request", // 添加固定的 msg 值
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

		logLine := string(jsonBytes) + "\n"

		// 直接写入 gin.DefaultWriter（包含 stdout + 文件）
		// 这确保访问日志被写入日志文件
		if gin.DefaultWriter != nil {
			gin.DefaultWriter.Write([]byte(logLine))
		}

		// 返回空字符串，避免 Gin 再次写入
		return ""
	}))
}
