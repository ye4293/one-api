package anthropic

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/relay/channel"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

type Adaptor struct {
}

// ConvertImageRequest implements channel.Adaptor.
func (a *Adaptor) ConvertImageRequest(request *model.ImageRequest) (any, error) {
	panic("unimplemented")
}

func (a *Adaptor) Init(meta *util.RelayMeta) {

}

func (a *Adaptor) GetRequestURL(meta *util.RelayMeta) (string, error) {
	return fmt.Sprintf("%s/v1/messages", meta.BaseURL), nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Request, meta *util.RelayMeta) error {
	channel.SetupCommonRequestHeader(c, req, meta)
	req.Header.Set("x-api-key", meta.APIKey)
	anthropicVersion := c.Request.Header.Get("anthropic-version")
	if anthropicVersion == "" {
		anthropicVersion = "2023-06-01"
	}
	req.Header.Set("anthropic-version", anthropicVersion)
	anthropicBeta := c.Request.Header.Get("anthropic-beta")
	if anthropicBeta != "" {
		req.Header.Set("anthropic-beta", anthropicBeta)
	}

	// 应用自定义请求头覆盖（从配置读取）
	customHeaders := common.GetClaudeRequestHeaders(meta.ActualModelName)
	if customHeaders != nil {
		for key, value := range customHeaders {
			if key == "anthropic-beta" {
				// 对于 anthropic-beta，追加而不是覆盖
				existingBeta := req.Header.Get("anthropic-beta")
				if existingBeta != "" {
					req.Header.Set("anthropic-beta", existingBeta+","+value)
				} else {
					req.Header.Set("anthropic-beta", value)
				}
			} else {
				req.Header.Set(key, value)
			}
		}
	}

	// 为 thinking 模型添加 anthropic-beta header
	if IsThinkingModel(meta.ActualModelName) {
		// 如果已经有 anthropic-beta header，追加；否则设置新的
		existingBeta := req.Header.Get("anthropic-beta")
		if existingBeta != "" {
			// 检查是否已包含 interleaved-thinking
			if !strings.Contains(existingBeta, "interleaved-thinking") {
				req.Header.Set("anthropic-beta", existingBeta+",interleaved-thinking-2025-05-14")
			}
		} else {
			req.Header.Set("anthropic-beta", "interleaved-thinking-2025-05-14")
		}
	}
	return nil
}

func (a *Adaptor) ConvertRequest(c *gin.Context, relayMode int, request *model.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	return ConvertRequest(*request), nil
}

func (a *Adaptor) DoRequest(c *gin.Context, meta *util.RelayMeta, requestBody io.Reader) (*http.Response, error) {
	return channel.DoRequestHelper(a, c, meta, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, meta *util.RelayMeta) (usage *model.Usage, err *model.ErrorWithStatusCode) {
	if meta.IsStream {
		err, usage = StreamHandler(c, resp, meta)
	} else {
		err, usage = Handler(c, resp, meta.PromptTokens, meta.ActualModelName)
	}
	return
}

// HandleErrorResponse 处理Anthropic特有的错误响应格式
func (a *Adaptor) HandleErrorResponse(resp *http.Response) *model.ErrorWithStatusCode {
	// ✅ 关键修复：defer必须在读取之前，确保一定会关闭
	defer func() {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()

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

	// 尝试解析Anthropic错误格式
	var claudeResponse Response
	if unmarshalErr := json.Unmarshal(responseBody, &claudeResponse); unmarshalErr == nil {
		if claudeResponse.Error != nil && claudeResponse.Error.Type != "" {
			return &model.ErrorWithStatusCode{
				Error: model.Error{
					Message: claudeResponse.Error.Message,
					Type:    claudeResponse.Error.Type,
					Param:   "",
					Code:    claudeResponse.Error.Type,
				},
				StatusCode: resp.StatusCode,
			}
		}
	}

	// 如果不是Anthropic格式，返回nil让通用处理器处理
	return nil
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return "anthropic"
}

func (a *Adaptor) GetModelDetails() []model.APIModel {
	return ModelDetails
}
