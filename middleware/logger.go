package middleware

import (
	"encoding/json"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
)

type accessLogEntry struct {
	Ts        string `json:"ts"`
	Level     string `json:"level"`
	RequestId string `json:"request_id"`
	Msg       string `json:"msg"`
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
		// 200 请求：只记录超过慢请求阈值的，其余跳过
		if param.StatusCode < 400 {
			threshold := int64(config.SlowRequestThresholdMs)
			if threshold <= 0 || param.Latency.Milliseconds() < threshold {
				return ""
			}
		}

		var requestID string
		if param.Keys != nil {
			if v, ok := param.Keys[logger.RequestIdKey]; ok {
				requestID, _ = v.(string)
			}
		}

		level := "info"
		if param.StatusCode >= 500 {
			level = "error"
		} else if param.StatusCode >= 400 {
			level = "warn"
		}

		entry := accessLogEntry{
			Ts:        param.TimeStamp.Format(time.RFC3339Nano),
			Level:     level,
			RequestId: requestID,
			Msg:       "http request",
			Status:    param.StatusCode,
			LatencyMs: param.Latency.Milliseconds(),
			ClientIP:  param.ClientIP,
			Method:    param.Method,
			Path:      param.Path,
			Service:   config.ServiceName,
			Instance:  config.InstanceId,
		}

		b, err := json.Marshal(entry)
		if err != nil {
			return `{"level":"error","msg":"access log marshal error"}` + "\n"
		}

		logLine := string(b) + "\n"

		// 写入 gin.DefaultWriter，确保 access log 进入日志文件
		if gin.DefaultWriter != nil {
			gin.DefaultWriter.Write([]byte(logLine))
		}

		// 返回空字符串避免 gin 重复写入
		return ""
	}))
}
