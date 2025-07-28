package xai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/relay/channel"
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
	channel.SetupCommonRequestHeader(c, req, meta)
	req.Header.Set("Authorization", "Bearer "+meta.APIKey)
	return nil
}

func (a *Adaptor) ConvertRequest(c *gin.Context, relayMode int, request *model.GeneralOpenAIRequest) (any, error) {
	// 处理模型名称以 "-search" 结尾的情况
	if strings.HasSuffix(request.Model, "-search") {
		request.Model = strings.TrimSuffix(request.Model, "-search")

		// 直接设置 SearchParameters
		mode := "on"
		request.SearchParameters = &model.SearchParameters{
			Mode: &mode,
		}
	}

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

	if request.Stream {
		// 直接在原请求结构中设置 StreamOptions
		request.StreamOptions = &model.StreamOptions{
			IncludeUsage: true,
		}
	}

	return request, nil
}

func (a *Adaptor) ConvertImageRequest(request *model.ImageRequest) (any, error) {
	xaiRequest := model.ImageRequest{
		Model:          request.Model,
		Prompt:         request.Prompt,
		N:              request.N,
		ResponseFormat: request.ResponseFormat,
	}
	return xaiRequest, nil
}

func (a *Adaptor) DoRequest(c *gin.Context, meta *util.RelayMeta, requestBody io.Reader) (*http.Response, error) {
	return channel.DoRequestHelper(a, c, meta, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, meta *util.RelayMeta) (usage *model.Usage, err *model.ErrorWithStatusCode) {
	if meta.IsStream {
		err, _, usage = StreamHandler(c, resp, 1)
	} else {
		err, usage = Handler(c, resp, meta.PromptTokens, meta.ActualModelName)
	}
	return
}

// HandleErrorResponse 处理XAI特有的错误响应格式
func (a *Adaptor) HandleErrorResponse(resp *http.Response) *model.ErrorWithStatusCode {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return &model.ErrorWithStatusCode{
			Error: model.Error{
				Message: "failed to read error response body",
				Type:    "api_error",
				Code:    "read_error_failed",
			},
			StatusCode: resp.StatusCode,
		}
	}
	defer resp.Body.Close()

	// 尝试解析XAI错误格式
	var xaiError map[string]interface{}
	if unmarshalErr := json.Unmarshal(responseBody, &xaiError); unmarshalErr == nil {
		// 检查是否是XAI错误格式（包含code和error字段）
		if code, hasCode := xaiError["code"]; hasCode {
			if errorMsg, hasError := xaiError["error"]; hasError {
				// 转换为OpenAI错误格式
				return &model.ErrorWithStatusCode{
					Error: model.Error{
						Message: fmt.Sprintf("%v", errorMsg),
						Type:    "api_error",
						Param:   "",
						Code:    fmt.Sprintf("%v", code),
					},
					StatusCode: resp.StatusCode,
				}
			}
		}
	}

	// 如果不是XAI格式，返回nil让通用处理器处理
	return nil
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
