package utils

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	relaymodel "github.com/songquanpeng/one-api/relay/model"
)

// httpStatusCodeError 定义接口用于从 AWS SDK 错误中提取 HTTP 状态码
// AWS SDK v2 的 smithy HTTP 响应错误实现了此接口
type httpStatusCodeError interface {
	HTTPStatusCode() int
}

// statusCodeRegex 用于从错误信息中提取 "StatusCode: NNN" 格式的状态码
var statusCodeRegex = regexp.MustCompile(`StatusCode:\s*(\d{3})`)

func WrapErr(err error) *relaymodel.ErrorWithStatusCode {
	if err == nil {
		return nil
	}

	errMsg := err.Error()
	statusCode := 0

	// 1. 优先尝试从 AWS SDK 错误链中提取 HTTP 状态码（最可靠）
	var httpErr httpStatusCodeError
	if errors.As(err, &httpErr) {
		statusCode = httpErr.HTTPStatusCode()
	}

	// 2. 如果接口提取失败，尝试从错误信息中解析 "StatusCode: NNN"
	if statusCode == 0 {
		if matches := statusCodeRegex.FindStringSubmatch(errMsg); len(matches) == 2 {
			if code, parseErr := strconv.Atoi(matches[1]); parseErr == nil && code >= 100 && code < 600 {
				statusCode = code
			}
		}
	}

	// 3. 最后 fallback：根据错误信息关键词判断
	if statusCode == 0 {
		statusCode = http.StatusInternalServerError
		switch {
		case strings.Contains(errMsg, "credentials not provided"):
			statusCode = http.StatusUnauthorized
		case strings.Contains(errMsg, "not found"):
			statusCode = http.StatusNotFound
		case strings.Contains(errMsg, "access denied") || strings.Contains(errMsg, "permission"):
			statusCode = http.StatusForbidden
		case strings.Contains(errMsg, "rate limit") || strings.Contains(errMsg, "throttle") || strings.Contains(errMsg, "ThrottlingException"):
			statusCode = http.StatusTooManyRequests
		case strings.Contains(errMsg, "bad request") || strings.Contains(errMsg, "invalid parameter") || strings.Contains(errMsg, "ValidationException"):
			statusCode = http.StatusBadRequest
		case strings.Contains(errMsg, "timeout"):
			statusCode = http.StatusRequestTimeout
		}
	}

	return &relaymodel.ErrorWithStatusCode{
		StatusCode: statusCode,
		Error: relaymodel.Error{
			Message: errMsg,
			Type:    "aws_error",
		},
	}
}
