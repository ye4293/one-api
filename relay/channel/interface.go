package channel

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

type Adaptor interface {
	Init(meta *util.RelayMeta)
	GetRequestURL(meta *util.RelayMeta) (string, error)
	SetupRequestHeader(c *gin.Context, req *http.Request, meta *util.RelayMeta) error
	ConvertRequest(c *gin.Context, relayMode int, request *model.GeneralOpenAIRequest) (any, error)
	ConvertImageRequest(request *model.ImageRequest) (any, error)
	DoRequest(c *gin.Context, meta *util.RelayMeta, requestBody io.Reader) (*http.Response, error)
	DoResponse(c *gin.Context, resp *http.Response, meta *util.RelayMeta) (usage *model.Usage, err *model.ErrorWithStatusCode)
	GetModelList() []string
	GetModelDetails() []model.APIModel
	GetChannelName() string
}
