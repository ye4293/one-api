package audit

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// FinalizeInput 占位定义，真实实现由 Task 12 提供。
type FinalizeInput struct {
	Start          time.Time
	OrigHeaders    http.Header
	OrigBody       []byte
	ClientResponse string
	ClientTrunc    bool
	StatusCode     int
}

// MaxRespBytes 占位实现，真实实现由 Task 12 提供。
func MaxRespBytes() int { return 4096 * 1024 }

// BuildAndSubmit 占位实现，真实实现由 Task 12 提供。
func BuildAndSubmit(c *gin.Context, in FinalizeInput) {}
