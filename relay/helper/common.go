package helper

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/relay/channel/anthropic"
	"github.com/songquanpeng/one-api/relay/channel/openai"
)

func FlushWriter(c *gin.Context) error {
	if c.Writer == nil {
		return nil
	}
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
		return nil
	}
	return errors.New("streaming error: flusher not found")
}

func ClaudeChunkData(c *gin.Context, resp anthropic.StreamResponse, data string) {
	c.Writer.Write([]byte(fmt.Sprintf("event: %s\ndata: %s\n\n", resp.Type, data)))
	_ = FlushWriter(c)
}

func OpenaiResponseChunkData(c *gin.Context, resp openai.OpenaiResponseStreamResponse, data string) {
	c.Writer.Write([]byte(fmt.Sprintf("event: %s\ndata: %s\n\n", resp.Type, data)))
	_ = FlushWriter(c)
}

func StringData(c *gin.Context, str string) error {
	c.Render(-1, common.CustomEvent{Data: "data: " + str})
	_ = FlushWriter(c)
	return nil
}

func PingData(c *gin.Context) error {
	c.Writer.Write([]byte(": PING\n\n"))
	_ = FlushWriter(c)
	return nil
}
