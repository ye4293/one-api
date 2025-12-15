package gemini

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/logger"
	channelhelper "github.com/songquanpeng/one-api/relay/channel"
	"github.com/songquanpeng/one-api/relay/channel/openai"
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
	// 从请求路径中提取 API 版本（v1beta、v1alpha、v1）
	// 默认使用 v1beta
	version := "v1beta"
	if meta.RequestURLPath != "" {
		if strings.HasPrefix(meta.RequestURLPath, "/v1alpha/") {
			version = "v1alpha"
		} else if strings.HasPrefix(meta.RequestURLPath, "/v1/") {
			version = "v1"
		}
		// v1beta 保持默认
	}

	// 从请求路径中提取 action（支持 generateContent、streamGenerateContent 等）
	action := extractActionFromPath(meta.RequestURLPath)
	if action == "" {
		action = "generateContent"
		if meta.IsStream {
			action = "streamGenerateContent?alt=sse"
		}
	}

	// 优先使用 ActualModelName（已应用模型映射），如果为空则使用 OriginModelName
	modelName := meta.ActualModelName
	if modelName == "" {
		modelName = meta.OriginModelName
	}
	if strings.HasSuffix(modelName, "-thinking") {
		modelName = strings.TrimSuffix(modelName, "-thinking")
	} else if strings.HasSuffix(modelName, "-nothinking") {
		modelName = strings.TrimSuffix(modelName, "-nothinking")
	}

	fullURL := fmt.Sprintf("%s/%s/models/%s:%s", meta.BaseURL, version, modelName, action)
	logger.SysLog(fmt.Sprintf("[Gemini] RequestURLPath: %s, Version: %s, ModelName: %s, Action: %s, FullURL: %s", 
		meta.RequestURLPath, version, modelName, action, fullURL))
	return fullURL, nil
}

// extractActionFromPath 从请求路径中提取动作名称
// 例如: /v1beta/models/gemini-2.0-flash:generateContent -> generateContent
// 例如: /v1alpha/models/gemini-3-pro-preview:streamGenerateContent?alt=sse -> streamGenerateContent?alt=sse
func extractActionFromPath(path string) string {
	if path == "" {
		return ""
	}

	// 先分离查询参数
	pathOnly := path
	queryString := ""
	if qIdx := strings.Index(path, "?"); qIdx != -1 {
		pathOnly = path[:qIdx]
		queryString = path[qIdx:] // 包含 ?
	}

	// 查找冒号后的动作部分
	colonIdx := strings.LastIndex(pathOnly, ":")
	if colonIdx == -1 {
		return ""
	}

	action := pathOnly[colonIdx+1:]
	// 去除前后空白
	action = strings.TrimSpace(action)

	// 如果原始请求有查询参数，保留它（但排除 key 参数）
	if queryString != "" {
		// 解析并过滤掉 key 参数（避免重复添加）
		filteredQuery := filterQueryParams(queryString, "key")
		if filteredQuery != "" {
			action = action + filteredQuery
		}
	}

	return action
}

// filterQueryParams 过滤掉指定的查询参数
func filterQueryParams(queryString string, excludeParams ...string) string {
	if queryString == "" {
		return ""
	}

	// 移除开头的 ?
	query := strings.TrimPrefix(queryString, "?")
	if query == "" {
		return ""
	}

	excludeSet := make(map[string]bool)
	for _, p := range excludeParams {
		excludeSet[p] = true
	}

	parts := strings.Split(query, "&")
	var filtered []string
	for _, part := range parts {
		if part == "" {
			continue
		}
		key := part
		if eqIdx := strings.Index(part, "="); eqIdx != -1 {
			key = part[:eqIdx]
		}
		if !excludeSet[key] {
			filtered = append(filtered, part)
		}
	}

	if len(filtered) == 0 {
		return ""
	}
	return "?" + strings.Join(filtered, "&")
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Request, meta *util.RelayMeta) error {
	channelhelper.SetupCommonRequestHeader(c, req, meta)
	req.Header.Set("x-goog-api-key", meta.APIKey)
	return nil
}

func (a *Adaptor) ConvertRequest(c *gin.Context, relayMode int, request *model.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	return ConvertRequest(*request)
}

func (a *Adaptor) DoRequest(c *gin.Context, meta *util.RelayMeta, requestBody io.Reader) (*http.Response, error) {
	return channelhelper.DoRequestHelper(a, c, meta, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, meta *util.RelayMeta) (usage *model.Usage, err *model.ErrorWithStatusCode) {
	if meta.IsStream {
		var responseText string
		err, responseText = StreamHandler(c, resp, meta.ActualModelName)
		usage = openai.ResponseText2Usage(responseText, meta.ActualModelName, meta.PromptTokens)
	} else {
		err, usage = Handler(c, resp, meta.PromptTokens, meta.ActualModelName)
	}
	return
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return "google gemini"
}

func (a *Adaptor) GetModelDetails() []model.APIModel {
	return ModelDetails
}
