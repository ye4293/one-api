package controller

import (
	"bytes"
	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/relay/channel"
	"github.com/songquanpeng/one-api/relay/util"
	"net/http"
)

// doOpenaiResponseRequest 每次尝试只发送一次完整请求，重试由外层统一控制。
func doOpenaiResponseRequest(c *gin.Context, meta *util.RelayMeta, adaptor channel.Adaptor, body []byte) (*http.Response, error) {
	return adaptor.DoRequest(c, meta, bytes.NewReader(body))
}
