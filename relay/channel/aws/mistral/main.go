package aws

// import (
// 	"errors"

// 	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
// 	"github.com/gin-gonic/gin"
// 	"github.com/songquanpeng/one-api/common/ctxkey"
// 	"github.com/songquanpeng/one-api/relay/channel/aws/utils"
// 	"github.com/songquanpeng/one-api/relay/channel/cohere"
// 	"github.com/songquanpeng/one-api/relay/model"
// 	"github.com/songquanpeng/one-api/relay/util"
// )

// var _ utils.AwsAdapter = new(Adaptor)

// type Adaptor struct {
// }

// func (a *Adaptor) ConvertRequest(c *gin.Context, relayMode int, request *model.GeneralOpenAIRequest) (any, error) {
// 	if request == nil {
// 		return nil, errors.New("request is nil")
// 	}

// 	claudeReq := cohere.ConvertRequest(*request)
// 	c.Set(ctxkey.RequestModel, request.Model)
// 	c.Set(ctxkey.ConvertedRequest, claudeReq)
// 	return claudeReq, nil
// }

// func (a *Adaptor) DoResponse(c *gin.Context, awsCli *bedrockruntime.Client, meta *util.RelayMeta) (usage *model.Usage, err *model.ErrorWithStatusCode) {
// 	if meta.IsStream {
// 		err, usage = StreamHandler(c, awsCli)
// 	} else {
// 		err, usage = Handler(c, awsCli, meta.ActualModelName)
// 	}
// 	return
// }
