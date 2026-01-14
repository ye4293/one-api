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

// LogEntry JSON 日志结构
type LogEntry struct {
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

		// Open general log file (for INFO/WARN/DEBUG)
		generalLogPath := filepath.Join(LogDir, fmt.Sprintf("oneapi-%s.log", dateStr))
		fd, err := os.OpenFile(generalLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatal("failed to open general log file")
		}

		// Open error log file (for ERROR only)
		errorLogPath := filepath.Join(LogDir, fmt.Sprintf("oneapi-error-%s.log", dateStr))
		errFd, err := os.OpenFile(errorLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatal("failed to open error log file")
		}

		// Close previous file handles if they exist
		if generalLogFile != nil {
			generalLogFile.Close()
		}
		if errorLogFile != nil {
			errorLogFile.Close()
		}

		generalLogFile = fd
		errorLogFile = errFd

		// INFO/WARN/DEBUG go to stdout + general log file
		gin.DefaultWriter = io.MultiWriter(os.Stdout, generalLogFile)
		// ERROR go to stderr + error log file
		gin.DefaultErrorWriter = io.MultiWriter(os.Stderr, errorLogFile)
	}
}

// writeJSONLog 写入 JSON 格式日志
func writeJSONLog(writer io.Writer, level, requestId, msg string) {
	entry := LogEntry{
		Ts:        time.Now().Format(time.RFC3339Nano),
		Level:     level,
		RequestId: requestId,
		Msg:       msg,
		Service:   config.ServiceName,
		Instance:  config.InstanceId,
	}
	jsonBytes, err := json.Marshal(entry)
	if err != nil {
		// Fallback to plain text if JSON marshal fails
		_, _ = fmt.Fprintf(writer, `{"ts":"%s","level":"%s","msg":"json marshal error","service":"%s","instance":"%s"}`+"\n",
			entry.Ts, level, config.ServiceName, config.InstanceId)
		return
	}
	_, _ = writer.Write(append(jsonBytes, '\n'))
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
	// ERROR goes to DefaultErrorWriter (stderr + error log file)
	// INFO/WARN/DEBUG go to DefaultWriter (stdout + general log file)
	writer := gin.DefaultWriter
	if level == loggerError {
		writer = gin.DefaultErrorWriter
	}

	// Get request ID from context
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
	msg := fmt.Sprintf("%v", v)
	writeJSONLog(gin.DefaultErrorWriter, "fatal", "", msg)
	os.Exit(1)
}
