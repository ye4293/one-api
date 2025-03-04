package minimax

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

type VideoAdaptor struct {
}

func (a *VideoAdaptor) Init(meta *util.RelayMeta) {

}

func GetRequestURL(meta *util.RelayMeta) (string, error) {
	var fullRequestUrl string
	return fullRequestUrl, nil
}

func (a *VideoAdaptor) SetupRequestHeader(c *gin.Context, req *http.Request, meta *util.RelayMeta) error {
	req.Header.Set("Authorization", "Bearer "+meta.APIKey)
	return nil
}

func (a *VideoAdaptor) ConvertVideoRequest(c *gin.Context, request *model.VideoRequest) (any, error) {
	return nil, nil
}

func (a *VideoAdaptor) DoRequest(c *gin.Context, meta *util.RelayMeta, requestBody io.Reader) (*http.Response, error) {
	return nil, nil
}
