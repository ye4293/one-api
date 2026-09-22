package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/service"
)

func shouldRetryResponses(c *gin.Context, err *model.ErrorWithStatusCode) bool {
	if attempted, exists := c.Get("responses_upstream_attempted"); exists && attempted == false {
		return false
	}
	if err == nil || c.Writer.Written() || c.Request.Context().Err() != nil || err.Error.Type == "responses_state_error" {
		return false
	}
	if constraint, active := dbmodel.GetResponsesConstraint(c.Request.Context()); active && constraint.Resource != nil {
		return false
	}
	if service.ShouldSkipRetryAfterChannelAffinityFailure(c) {
		service.ClearChannelAffinityContext(c)
		return false
	}
	return shouldRetry(c, err.StatusCode, err.Error.Message)
}

func responsesChannelHealthError(c *gin.Context, err *model.ErrorWithStatusCode) bool {
	if attempted, exists := c.Get("responses_upstream_attempted"); exists && attempted == false {
		return false
	}
	return err != nil && err.Error.Type != "responses_state_error" && c.Request.Context().Err() == nil && !c.GetBool("responses_client_write_failed")
}

func responseRoutingError(err error) *model.ErrorWithStatusCode {
	status, code := 503, "responses_routing_unavailable"
	var stateErr *dbmodel.ResponsesStateError
	if errors.As(err, &stateErr) {
		status, code = stateErr.Status, stateErr.Code
	}
	return &model.ErrorWithStatusCode{Error: model.Error{Message: err.Error(), Type: "responses_state_error", Code: code}, StatusCode: status}
}

func writeResponsesRelayError(c *gin.Context, err *model.ErrorWithStatusCode) {
	if c.Request.Context().Err() != nil || c.GetBool("responses_client_write_failed") {
		return
	}
	if c.Writer.Written() {
		if strings.HasPrefix(c.Writer.Header().Get("Content-Type"), "text/event-stream") {
			payload, _ := json.Marshal(gin.H{"type": "error", "error": err.Error})
			_, _ = fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", payload)
			c.Writer.Flush()
		}
		return
	}
	for _, name := range []string{"Content-Type", "Cache-Control", "Connection", "X-Accel-Buffering"} {
		c.Writer.Header().Del(name)
	}
	c.JSON(err.StatusCode, gin.H{"error": err.Error})
}
