package utils

import (
	"net/http"
	"strings"

	relaymodel "github.com/songquanpeng/one-api/relay/model"
)

func WrapErr(err error) *relaymodel.ErrorWithStatusCode {
	if err == nil {
		return nil
	}

	errMsg := err.Error()
	statusCode := http.StatusInternalServerError

	// 根据错误信息判断合适的 HTTP 状态码
	switch {
	case strings.Contains(errMsg, "credentials not provided") || strings.Contains(errMsg, "invalid"):
		statusCode = http.StatusUnauthorized
	case strings.Contains(errMsg, "not found"):
		statusCode = http.StatusNotFound
	case strings.Contains(errMsg, "access denied") || strings.Contains(errMsg, "permission"):
		statusCode = http.StatusForbidden
	case strings.Contains(errMsg, "rate limit") || strings.Contains(errMsg, "throttle"):
		statusCode = http.StatusTooManyRequests
	case strings.Contains(errMsg, "bad request") || strings.Contains(errMsg, "invalid parameter"):
		statusCode = http.StatusBadRequest
	case strings.Contains(errMsg, "timeout"):
		statusCode = http.StatusRequestTimeout
	}

	return &relaymodel.ErrorWithStatusCode{
		StatusCode: statusCode,
		Error: relaymodel.Error{
			Message: errMsg,
			Type:    "aws_error",
		},
	}
}
