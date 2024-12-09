package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

const KeyRequestBody = "key_request_body"

func GetRequestBody(c *gin.Context) ([]byte, error) {
	// 先从上下文中获取
	requestBody, exists := c.Get(KeyRequestBody)
	if exists && requestBody != nil {
		return requestBody.([]byte), nil
	}

	// 读取请求体
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, err
	}

	// 关闭原始 body
	_ = c.Request.Body.Close()

	// 重要：重新设置请求体，这样后续还能读取
	c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

	// 保存到上下文
	c.Set(KeyRequestBody, body)

	return body, nil
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

	// 添加日志
	// fmt.Printf("Received request body: %s\n", string(requestBody))

	contentType := c.Request.Header.Get("Content-Type")
	fmt.Printf("Content-Type: %s\n", contentType)

	// 根据 Content-Type 选择绑定方式
	if strings.HasPrefix(contentType, "application/json") {
		// 使用 json.Unmarshal 而不是 ShouldBindJSON
		err = json.Unmarshal(requestBody, &v)
		if err != nil {
			return err
		}
	} else if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") ||
		strings.HasPrefix(contentType, "multipart/form-data") {
		// 重置请求体
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		return c.ShouldBindWith(v, binding.Form)
	} else {
		// 没有 Content-Type 时先尝试 JSON
		err = json.Unmarshal(requestBody, &v)
		if err == nil {
			return nil
		}

		// JSON 失败则尝试 Form
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		return c.ShouldBindWith(v, binding.Form)
	}

	// 重置请求体，以便后续可能的使用
	c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
	return nil
}

func SetEventStreamHeaders(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
}
