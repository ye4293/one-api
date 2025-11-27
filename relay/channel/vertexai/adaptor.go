package vertexai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

type Adaptor struct {
	RequestMode        int
	AccountCredentials Credentials
}

// Init implements channel.Adaptor.
func (a *Adaptor) Init(meta *util.RelayMeta) {
	// 支持多密钥模式：优先从当前密钥解析凭证
	keyIndex := 0
	if meta.KeyIndex != nil {
		keyIndex = *meta.KeyIndex
	}

	// 检查配置迁移状态
	CheckAndMigrateConfig(meta)

	// 验证配置是否正确（跳过系统级调用的验证）
	if meta.ChannelId != 0 {
		if err := ValidateVertexAIConfig(meta, keyIndex); err != nil {
			fmt.Printf("[Vertex AI] 配置验证失败: %v\n", err)
		}
	}

	// 尝试解析当前密钥的凭证
	if credentials, err := parseCredentialsFromKey(meta, keyIndex); err == nil {
		a.AccountCredentials = *credentials
		fmt.Printf("[Vertex AI] 成功加载凭证 - 项目: %s\n", credentials.ProjectID)
		return
	}

	// 回退：尝试从ADC配置解析
	if meta.Config.VertexAIADC != "" {
		var credentials Credentials
		if err := json.Unmarshal([]byte(meta.Config.VertexAIADC), &credentials); err == nil {
			a.AccountCredentials = credentials
			fmt.Printf("[Vertex AI] 使用ADC配置 - 项目: %s\n", credentials.ProjectID)
		}
	}
}

// GetRequestURL implements channel.Adaptor.
func (a *Adaptor) GetRequestURL(meta *util.RelayMeta) (string, error) {
	// 从Key字段提取项目ID，支持单密钥和多密钥模式
	keyIndex := 0
	if meta.KeyIndex != nil {
		keyIndex = *meta.KeyIndex
	}

	projectID := extractProjectIDFromKey(meta, keyIndex)
	if projectID == "" && a.AccountCredentials.ProjectID != "" {
		projectID = a.AccountCredentials.ProjectID
	}

	if projectID == "" {
		return "", fmt.Errorf("Vertex AI project ID not found in Key field or credentials")
	}

	region := meta.Config.Region
	if region == "" {
		region = "global"
	}

	modelName := meta.OriginModelName
	if modelName == "" {
		modelName = "gemini-pro"
	}

	// 构建Vertex AI API URL
	if region == "global" {
		return fmt.Sprintf("https://aiplatform.googleapis.com/v1/projects/%s/locations/global/publishers/google/models/%s:predict", projectID, modelName), nil
	} else {
		return fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s:predict", region, projectID, region, modelName), nil
	}
}

// SetupRequestHeader implements channel.Adaptor.
func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Request, meta *util.RelayMeta) error {
	// 获取访问令牌并设置到请求头
	accessToken, err := GetAccessToken(a, meta)
	if err != nil {
		return fmt.Errorf("failed to get access token: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	return nil
}

// ConvertRequest implements channel.Adaptor.
func (a *Adaptor) ConvertRequest(c *gin.Context, relayMode int, request *model.GeneralOpenAIRequest) (any, error) {
	// 将OpenAI格式的请求转换为VertexAI格式
	// 对于不支持的模型，返回错误而不是panic
	return nil, fmt.Errorf("model %s is not supported by VertexAI adapter", request.Model)
}

// ConvertImageRequest implements channel.Adaptor.
func (a *Adaptor) ConvertImageRequest(request *model.ImageRequest) (any, error) {
	// 将图像请求转换为VertexAI格式
	// 对于不支持的图像模型，返回错误而不是panic
	return nil, fmt.Errorf("image model %s is not supported by VertexAI adapter", request.Model)
}

// DoRequest implements channel.Adaptor.
func (a *Adaptor) DoRequest(c *gin.Context, meta *util.RelayMeta, requestBody io.Reader) (*http.Response, error) {
	// 获取请求URL
	url, err := a.GetRequestURL(meta)
	if err != nil {
		return nil, fmt.Errorf("failed to get request URL: %w", err)
	}

	// 创建HTTP请求
	req, err := http.NewRequest("POST", url, requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// 设置请求头（包括认证）
	if err := a.SetupRequestHeader(c, req, meta); err != nil {
		return nil, fmt.Errorf("failed to setup request headers: %w", err)
	}

	// 执行请求
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// DoResponse implements channel.Adaptor.
func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, meta *util.RelayMeta) (usage *model.Usage, err *model.ErrorWithStatusCode) {
	// 处理响应并返回使用情况
	return nil, nil
}

// GetModelList implements channel.Adaptor.
func (a *Adaptor) GetModelList() []string {
	// 返回支持的模型列表
	return ModelList
}

// GetModelDetails implements channel.Adaptor.
func (a *Adaptor) GetModelDetails() []model.APIModel {
	// 返回详细的模型信息
	return []model.APIModel{}
}

// GetChannelName implements channel.Adaptor.
func (a *Adaptor) GetChannelName() string {
	return "vertexai"
}

// HandleErrorResponse 处理Vertex AI特定的错误响应
func (a *Adaptor) HandleErrorResponse(resp *http.Response) *model.ErrorWithStatusCode {
	// 根据不同的HTTP状态码提供针对性的错误信息
	switch resp.StatusCode {
	case 401:
		return &model.ErrorWithStatusCode{
			StatusCode: resp.StatusCode,
			Error: model.Error{
				Type:    "authentication_error",
				Code:    "vertex_ai_unauthorized",
				Message: "🔐 Vertex AI认证失败 (401) - 请检查Key字段中的service account JSON凭证是否有效，包括private_key和client_email字段",
			},
		}
	case 403:
		return &model.ErrorWithStatusCode{
			StatusCode: resp.StatusCode,
			Error: model.Error{
				Type:    "permission_error",
				Code:    "vertex_ai_forbidden",
				Message: "🚫 Vertex AI权限不足 (403) - 请确保service account具有Vertex AI API访问权限，并检查项目ID是否正确",
			},
		}
	case 400:
		return &model.ErrorWithStatusCode{
			StatusCode: resp.StatusCode,
			Error: model.Error{
				Type:    "invalid_request",
				Code:    "vertex_ai_bad_request",
				Message: "📝 Vertex AI请求参数错误 (400) - 请检查模型名称、区域设置和请求格式是否正确",
			},
		}
	case 429:
		return &model.ErrorWithStatusCode{
			StatusCode: resp.StatusCode,
			Error: model.Error{
				Type:    "rate_limit_exceeded",
				Code:    "vertex_ai_rate_limit",
				Message: "⏰ Vertex AI请求频率限制 (429) - 请稍后重试，或考虑启用多密钥模式分散负载",
			},
		}
	}

	// 对于其他错误，返回nil让通用处理器处理
	return nil
}
