package xai

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/relay/channel"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

type Adaptor struct {
	ChannelType int
}

func (a *Adaptor) Init(meta *util.RelayMeta) {
	a.ChannelType = meta.ChannelType
}

func (a *Adaptor) GetRequestURL(meta *util.RelayMeta) (string, error) {
	fullrequestUrl := fmt.Sprintf("%s%s", meta.BaseURL, "/v1/chat/completions")
	return fullrequestUrl, nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Request, meta *util.RelayMeta) error {
	return nil
}

func (a *Adaptor) ConvertRequest(c *gin.Context, relayMode int, request *model.GeneralOpenAIRequest) (any, error) {
	// 第一个转换：处理模型名称和 ReasoningEffort
	modelName := request.Model
	suffixes := []string{"low", "high", "medium"}

	for _, suffix := range suffixes {
		if strings.HasSuffix(modelName, "-"+suffix) {
			// 移除后缀，设置为基础模型名
			request.Model = strings.TrimSuffix(modelName, "-"+suffix)
			// 设置 ReasoningEffort
			request.ReasoningEffort = suffix
			break
		}
	}

	// 第二个转换：处理 MaxTokens 和 MaxCompletionTokens
	if request.MaxTokens > 0 {
		request.MaxCompletionTokens = request.MaxTokens
		request.MaxTokens = 0
	}

	return request, nil
}

func (a *Adaptor) ConvertImageRequest(request *model.ImageRequest) (any, error) {
	return request, nil
}

func (a *Adaptor) DoRequest(c *gin.Context, meta *util.RelayMeta, requestBody io.Reader) (*http.Response, error) {
	return channel.DoRequestHelper(a, c, meta, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, meta *util.RelayMeta) (usage *model.Usage, err *model.ErrorWithStatusCode) {
	if meta.IsStream {
		err, _, usage = openai.StreamHandler(c, resp, 1)
	} else {
		err, usage = openai.Handler(c, resp, meta.PromptTokens, meta.ActualModelName)
	}
	return
}

func (a *Adaptor) GetModelList() []string {
	return modelList
}

func (a *Adaptor) GetChannelName() string {
	return channelName
}

func (a *Adaptor) GetModelDetails() []model.APIModel {
	return ModelDetails
}
