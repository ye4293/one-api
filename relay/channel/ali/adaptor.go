package ali

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/relay/channel"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/constant"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

// 阿里云百炼 Adaptor
// - chat / embedding 走 OpenAI 兼容端点 /compatible-mode/v1/...
// - Anthropic Messages 走 /apps/anthropic/v1/messages（由 RelayClaudeNative 透传）
// - OpenAI Responses 走 /api/v2/apps/protocols/compatible-mode/v1/responses（由 RelayOpenaiResponseNative 透传）
// 官方文档：https://help.aliyun.com/zh/model-studio/anthropic-api-messages

// EnableSearchModelSuffix 保留历史语义：模型名以 -internet 结尾时，启用百炼搜索增强
const EnableSearchModelSuffix = "-internet"

// compatibleChatRequest 是 compatible-mode chat 的请求包装，透出百炼专有的 enable_search 字段
type compatibleChatRequest struct {
	*model.GeneralOpenAIRequest
	EnableSearch bool `json:"enable_search,omitempty"`
}

type Adaptor struct{}

// ConvertImageRequest 当前未实现（图像生成接口未接入）
func (a *Adaptor) ConvertImageRequest(request *model.ImageRequest) (any, error) {
	panic("unimplemented")
}

func (a *Adaptor) Init(meta *util.RelayMeta) {}

func (a *Adaptor) GetRequestURL(meta *util.RelayMeta) (string, error) {
	base := strings.TrimRight(meta.BaseURL, "/")
	switch meta.Mode {
	case constant.RelayModeClaude:
		return base + "/apps/anthropic/v1/messages", nil
	case constant.RelayModeOpenaiResponse:
		return base + "/api/v2/apps/protocols/compatible-mode/v1/responses", nil
	case constant.RelayModeEmbeddings:
		return base + "/compatible-mode/v1/embeddings", nil
	default:
		return base + "/compatible-mode/v1/chat/completions", nil
	}
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Request, meta *util.RelayMeta) error {
	channel.SetupCommonRequestHeader(c, req, meta)
	req.Header.Set("Authorization", "Bearer "+meta.APIKey)
	if meta.IsStream {
		req.Header.Set("Accept", "text/event-stream")
	}
	if meta.Mode == constant.RelayModeClaude {
		anthropicVersion := c.Request.Header.Get("anthropic-version")
		if anthropicVersion == "" {
			anthropicVersion = "2023-06-01"
		}
		req.Header.Set("anthropic-version", anthropicVersion)
		if beta := c.Request.Header.Get("anthropic-beta"); beta != "" {
			req.Header.Set("anthropic-beta", beta)
		}
	}
	return nil
}

func (a *Adaptor) ConvertRequest(c *gin.Context, relayMode int, request *model.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	// Messages / Responses 由原生控制器走 passthrough，不经过 ConvertRequest
	if relayMode == constant.RelayModeClaude || relayMode == constant.RelayModeOpenaiResponse {
		return request, nil
	}
	// 模型名 -internet 后缀解析：去后缀后启用 enable_search（百炼 compatible-mode 支持该字段）
	enableSearch := false
	if strings.HasSuffix(request.Model, EnableSearchModelSuffix) {
		request.Model = strings.TrimSuffix(request.Model, EnableSearchModelSuffix)
		enableSearch = true
	}
	// 其余委托 openai adaptor 做通用处理（包括 audio stream 的 StreamOptions 注入）
	converted, err := (&openai.Adaptor{}).ConvertRequest(c, relayMode, request)
	if err != nil {
		return nil, err
	}
	if enableSearch {
		if req, ok := converted.(*model.GeneralOpenAIRequest); ok {
			return compatibleChatRequest{GeneralOpenAIRequest: req, EnableSearch: true}, nil
		}
	}
	return converted, nil
}

func (a *Adaptor) DoRequest(c *gin.Context, meta *util.RelayMeta, requestBody io.Reader) (*http.Response, error) {
	return channel.DoRequestHelper(a, c, meta, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, meta *util.RelayMeta) (usage *model.Usage, err *model.ErrorWithStatusCode) {
	// Messages / Responses 路径不经过此函数（原生控制器直接处理 resp）
	if meta.IsStream {
		var responseText string
		err, responseText, usage = openai.StreamHandler(c, resp, meta.Mode)
		if usage == nil || usage.TotalTokens == 0 {
			usage = openai.ResponseText2Usage(responseText, meta.ActualModelName, meta.PromptTokens)
		}
		return
	}
	err, usage = openai.Handler(c, resp, meta.PromptTokens, meta.ActualModelName)
	return
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return "ali"
}

func (a *Adaptor) GetModelDetails() []model.APIModel {
	return ModelDetails
}
