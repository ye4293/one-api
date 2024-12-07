package common

import (
	"bytes"
	"io"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

const KeyRequestBody = "key_request_body"

func GetRequestBody(c *gin.Context) ([]byte, error) {
	requestBody, _ := c.Get(KeyRequestBody)
	if requestBody != nil {
		return requestBody.([]byte), nil
	}
	requestBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, err
	}
	_ = c.Request.Body.Close()
	c.Set(KeyRequestBody, requestBody)
	return requestBody.([]byte), nil
}

// func UnmarshalBodyReusable(c *gin.Context, v any) error {
// 	requestBody, err := GetRequestBody(c)
// 	if err != nil {
// 		return err
// 	}
// 	contentType := c.Request.Header.Get("Content-Type")
// 	if strings.HasPrefix(contentType, "application/json") {
// 		err = json.Unmarshal(requestBody, &v)
// 	} else {
// 		// skip for now
// 		// TODO: someday non json request have variant model, we will need to implementation this
// 	}
// 	if err != nil {
// 		return err
// 	}
// 	// Reset request body
// 	c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
// 	return nil
// }

func UnmarshalBodyReusable(c *gin.Context, v any) error {
	// 保存原始请求体
	requestBody, err := GetRequestBody(c)
	if err != nil {
		return err
	}

	contentType := c.Request.Header.Get("Content-Type")

	// 根据 Content-Type 选择绑定方式
	if strings.HasPrefix(contentType, "application/json") {
		// 重置请求体
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		return c.ShouldBindJSON(v)
	} else if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") ||
		strings.HasPrefix(contentType, "multipart/form-data") {
		// 重置请求体
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		return c.ShouldBindWith(v, binding.Form)
	}

	// 没有 Content-Type 时先尝试 JSON
	c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
	if err := c.ShouldBindJSON(v); err == nil {
		return nil
	}

	// JSON 失败则尝试 Form
	c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
	return c.ShouldBindWith(v, binding.Form)
}

func SetEventStreamHeaders(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
}
