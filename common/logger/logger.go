package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
)

const (
	loggerDEBUG = "debug"
	loggerINFO  = "info"
	loggerWarn  = "warn"
	loggerError = "error"
)

type logEntry struct {
	Ts        string `json:"ts"`
	Level     string `json:"level"`
	RequestId string `json:"request_id,omitempty"`
	Msg       string `json:"msg"`
	Service   string `json:"service"`
	Instance  string `json:"instance"`
}

var setupLogLock sync.Mutex
var setupLogWorking bool
var generalLogFile *os.File
var errorLogFile *os.File
var accessLogFile *os.File
var accessWriter io.Writer

func SetupLogger() {
	if LogDir != "" {
		ok := setupLogLock.TryLock()
		if !ok {
			log.Println("setup log is already working")
			return
		}
		defer func() {
			setupLogLock.Unlock()
			setupLogWorking = false
		}()

		dateStr := time.Now().Format("20060102")

		generalLogPath := filepath.Join(LogDir, fmt.Sprintf("oneapi-%s.log", dateStr))
		fd, err := os.OpenFile(generalLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatal("failed to open general log file")
		}

		errorLogPath := filepath.Join(LogDir, fmt.Sprintf("oneapi-error-%s.log", dateStr))
		errFd, err := os.OpenFile(errorLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatal("failed to open error log file")
		}

		accessLogPath := filepath.Join(LogDir, fmt.Sprintf("oneapi-access-%s.log", dateStr))
		accFd, err := os.OpenFile(accessLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatal("failed to open access log file")
		}

		if generalLogFile != nil {
			generalLogFile.Close()
		}
		if errorLogFile != nil {
			errorLogFile.Close()
		}
		if accessLogFile != nil {
			accessLogFile.Close()
		}

		generalLogFile = fd
		errorLogFile = errFd
		accessLogFile = accFd

		// info/warn/debug → stdout + general log
		gin.DefaultWriter = io.MultiWriter(os.Stdout, generalLogFile)
		// error → stderr + error log
		gin.DefaultErrorWriter = io.MultiWriter(os.Stderr, errorLogFile)
		// http access log → stdout + access log（独立通道，便于 Promtail 用 stream label 切分）
		accessWriter = io.MultiWriter(os.Stdout, accessLogFile)
	}
}

// WriteAccessLog 由 middleware 调用，把 HTTP 请求日志写到 oneapi-access-*.log。
// 与应用日志物理分离，Promtail 可用 stream=access 标签独立采集，不污染 general/error 流。
func WriteAccessLog(line []byte) {
	w := accessWriter
	if w == nil {
		// SetupLogger 未跑或 LogDir 未设置时回落到 stdout，保证不丢日志。
		w = os.Stdout
	}
	_, _ = w.Write(line)

	if !setupLogWorking {
		setupLogWorking = true
		go func() {
			SetupLogger()
		}()
	}
}

func writeJSONLog(writer io.Writer, level, requestId, msg string) {
	entry := logEntry{
		Ts:        time.Now().Format(time.RFC3339Nano),
		Level:     level,
		RequestId: requestId,
		Msg:       msg,
		Service:   config.ServiceName,
		Instance:  config.InstanceId,
	}
	b, err := json.Marshal(entry)
	if err != nil {
		_, _ = fmt.Fprintf(writer, `{"ts":"%s","level":"%s","msg":"json marshal error"}`+"\n",
			entry.Ts, level)
		return
	}
	_, _ = writer.Write(append(b, '\n'))
}

func SysLog(s string) {
	writeJSONLog(gin.DefaultWriter, loggerINFO, "", s)
}

func SysError(s string) {
	writeJSONLog(gin.DefaultErrorWriter, loggerError, "", s)
}

func Debug(ctx context.Context, msg string) {
	if config.DebugEnabled {
		logHelper(ctx, loggerDEBUG, msg)
	}
}

func Info(ctx context.Context, msg string) {
	logHelper(ctx, loggerINFO, msg)
}

func Warn(ctx context.Context, msg string) {
	logHelper(ctx, loggerWarn, msg)
}

func Error(ctx context.Context, msg string) {
	logHelper(ctx, loggerError, msg)
}

func Debugf(ctx context.Context, format string, a ...any) {
	Debug(ctx, fmt.Sprintf(format, a...))
}

func Infof(ctx context.Context, format string, a ...any) {
	Info(ctx, fmt.Sprintf(format, a...))
}

func Warnf(ctx context.Context, format string, a ...any) {
	Warn(ctx, fmt.Sprintf(format, a...))
}

func Errorf(ctx context.Context, format string, a ...any) {
	Error(ctx, fmt.Sprintf(format, a...))
}

func logHelper(ctx context.Context, level string, msg string) {
	writer := gin.DefaultWriter
	if level == loggerError {
		writer = gin.DefaultErrorWriter
	}

	id := ""
	if ctx != nil {
		if v := ctx.Value(RequestIdKey); v != nil {
			id = fmt.Sprintf("%v", v)
		}
	}
	if id == "" {
		id = helper.GenRequestID()
	}

	writeJSONLog(writer, level, id, msg)

	if !setupLogWorking {
		setupLogWorking = true
		go func() {
			SetupLogger()
		}()
	}
}

func FatalLog(v ...any) {
	writeJSONLog(gin.DefaultErrorWriter, "fatal", "", fmt.Sprintf("%v", v))
	os.Exit(1)
}
