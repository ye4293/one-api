package vertexai

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bytedance/gopkg/cache/asynccache"
	"github.com/golang-jwt/jwt"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/util"
)

type Credentials struct {
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
	ClientID     string `json:"client_id"`
}

var Cache = asynccache.NewAsyncCache(asynccache.Options{
	RefreshDuration: time.Minute * 35,
	EnableExpire:    true,
	ExpireDuration:  time.Minute * 30,
	Fetcher: func(key string) (interface{}, error) {
		return nil, errors.New("not found")
	},
})

func GetAccessToken(a *Adaptor, meta *util.RelayMeta) (string, error) {
	// 支持多密钥模式：使用密钥索引作为缓存区分
	keyIndex := 0
	if meta.KeyIndex != nil {
		keyIndex = *meta.KeyIndex
	}

	// 添加调试日志
	fmt.Printf("[Vertex AI] 获取访问令牌 - 渠道:%d, 密钥索引:%d, 多密钥模式:%v\n",
		meta.ChannelId, keyIndex, meta.IsMultiKey)

	cacheKey := fmt.Sprintf("access-token-%d-%d", meta.ChannelId, keyIndex)
	val, err := Cache.Get(cacheKey)
	if err == nil {
		fmt.Printf("[Vertex AI] 使用缓存令牌 - 渠道:%d, 密钥:%d\n", meta.ChannelId, keyIndex)
		return val.(string), nil
	}

	// 解析当前密钥的凭证
	credentials, err := parseCredentialsFromKey(meta, keyIndex)
	if err != nil {
		return "", fmt.Errorf("🔐 Vertex AI凭证解析失败 (渠道:%d, 密钥:%d): %w", meta.ChannelId, keyIndex, err)
	}

	fmt.Printf("[Vertex AI] 开始JWT认证 - 服务账号: %s, 项目: %s\n",
		credentials.ClientEmail, credentials.ProjectID)

	signedJWT, err := createSignedJWT(credentials.ClientEmail, credentials.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("🔑 JWT签名失败 - 服务账号: %s, 错误: %w", credentials.ClientEmail, err)
	}

	newToken, err := exchangeJwtForAccessToken(signedJWT)
	if err != nil {
		return "", fmt.Errorf("🌐 Google OAuth2令牌交换失败 - 项目: %s, 错误: %w", credentials.ProjectID, err)
	}

	fmt.Printf("[Vertex AI] ✅ 令牌获取成功 - 渠道:%d, 密钥:%d\n", meta.ChannelId, keyIndex)
	if err := Cache.SetDefault(cacheKey, newToken); err {
		return newToken, nil
	}
	return newToken, nil
}

func createSignedJWT(email, privateKeyPEM string) (string, error) {

	privateKeyPEM = strings.ReplaceAll(privateKeyPEM, "-----BEGIN PRIVATE KEY-----", "")
	privateKeyPEM = strings.ReplaceAll(privateKeyPEM, "-----END PRIVATE KEY-----", "")
	privateKeyPEM = strings.ReplaceAll(privateKeyPEM, "\r", "")
	privateKeyPEM = strings.ReplaceAll(privateKeyPEM, "\n", "")
	privateKeyPEM = strings.ReplaceAll(privateKeyPEM, "\\n", "")

	block, _ := pem.Decode([]byte("-----BEGIN PRIVATE KEY-----\n" + privateKeyPEM + "\n-----END PRIVATE KEY-----"))
	if block == nil {
		return "", fmt.Errorf("failed to parse PEM block containing the private key")
	}

	privateKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", err
	}

	rsaPrivateKey, ok := privateKey.(*rsa.PrivateKey)
	if !ok {
		return "", fmt.Errorf("not an RSA private key")
	}

	now := time.Now()
	claims := jwt.MapClaims{
		"iss":   email,
		"scope": "https://www.googleapis.com/auth/cloud-platform",
		"aud":   "https://www.googleapis.com/oauth2/v4/token",
		"exp":   now.Add(time.Minute * 35).Unix(),
		"iat":   now.Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signedToken, err := token.SignedString(rsaPrivateKey)
	if err != nil {
		return "", err
	}

	return signedToken, nil
}

func exchangeJwtForAccessToken(signedJWT string) (string, error) {

	authURL := "https://www.googleapis.com/oauth2/v4/token"
	data := url.Values{}
	data.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	data.Set("assertion", signedJWT)

	resp, err := http.PostForm(authURL, data)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if accessToken, ok := result["access_token"].(string); ok {
		return accessToken, nil
	}

	return "", fmt.Errorf("failed to get access token: %v", result)
}

// parseCredentialsFromKey 从Key字段解析指定索引的Vertex AI JSON凭证
func parseCredentialsFromKey(meta *util.RelayMeta, keyIndex int) (*Credentials, error) {
	// 对于Vertex AI，所有凭证都应该存储在Key字段中
	// 这样可以统一处理单密钥和多密钥模式

	// 方案1：如果是多密钥模式，从Keys列表中获取
	if meta.IsMultiKey && meta.Keys != nil && keyIndex < len(meta.Keys) {
		keyData := meta.Keys[keyIndex]
		if keyData != "" {
			var credentials Credentials
			if err := json.Unmarshal([]byte(keyData), &credentials); err != nil {
				// 如果JSON解析失败，记录详细错误
				return nil, fmt.Errorf("failed to parse JSON credentials at key index %d: %v", keyIndex, err)
			}
			return &credentials, nil
		}
	}

	// 方案2：如果是单密钥模式，直接使用ActualAPIKey
	if !meta.IsMultiKey && meta.ActualAPIKey != "" {
		var credentials Credentials
		if err := json.Unmarshal([]byte(meta.ActualAPIKey), &credentials); err != nil {
			// 如果JSON解析失败，记录详细错误
			return nil, fmt.Errorf("failed to parse JSON credentials from ActualAPIKey: %v", err)
		}
		return &credentials, nil
	}

	// 兼容性方案：如果Key字段没有JSON凭证，尝试从Config.VertexAIADC获取
	// 这主要是为了向后兼容已存在的配置
	if meta.Config.VertexAIADC != "" {
		var credentials Credentials
		if err := json.Unmarshal([]byte(meta.Config.VertexAIADC), &credentials); err == nil {
			return &credentials, nil
		}
	}

	return nil, fmt.Errorf("no valid Vertex AI JSON credentials found (keyIndex: %d, isMultiKey: %v)", keyIndex, meta.IsMultiKey)
}

// extractProjectIDFromKey 从Key字段提取项目ID，支持单密钥和多密钥模式
func extractProjectIDFromKey(meta *util.RelayMeta, keyIndex int) string {
	// 尝试从当前密钥解析项目ID
	credentials, err := parseCredentialsFromKey(meta, keyIndex)
	if err == nil && credentials.ProjectID != "" {
		return credentials.ProjectID
	}

	// 回退到Config中的项目ID（向后兼容）
	if meta.Config.VertexAIProjectID != "" {
		return meta.Config.VertexAIProjectID
	}

	return ""
}

// ValidateVertexAIConfig 验证Vertex AI配置是否正确
func ValidateVertexAIConfig(meta *util.RelayMeta, keyIndex int) error {
	// 检查是否能解析凭证
	credentials, err := parseCredentialsFromKey(meta, keyIndex)
	if err != nil {
		return fmt.Errorf("凭证解析失败: %w", err)
	}

	// 检查必要字段
	if credentials.ProjectID == "" {
		return fmt.Errorf("缺少project_id字段")
	}
	if credentials.ClientEmail == "" {
		return fmt.Errorf("缺少client_email字段")
	}
	if credentials.PrivateKey == "" {
		return fmt.Errorf("缺少private_key字段")
	}

	// 检查项目ID是否能提取
	projectID := extractProjectIDFromKey(meta, keyIndex)
	if projectID == "" {
		return fmt.Errorf("无法提取project_id")
	}

	fmt.Printf("[Vertex AI] 配置验证成功 - 项目ID: %s, 服务账号: %s\n",
		projectID, credentials.ClientEmail)

	return nil
}

// CheckAndMigrateConfig 检查并提示配置迁移
func CheckAndMigrateConfig(meta *util.RelayMeta) {
	// 如果发现使用了旧的Config.VertexAIADC方式，提供迁移提示
	if meta.Config.VertexAIADC != "" && (meta.ActualAPIKey == "" || meta.ActualAPIKey == meta.Config.VertexAIADC) {
		fmt.Printf("⚠️  [Vertex AI] 检测到使用旧的配置方式\n")
		fmt.Printf("💡 建议：将JSON凭证迁移到Key字段以获得更好的多密钥支持\n")
		fmt.Printf("📋 当前配置：Config.VertexAIADC\n")
		fmt.Printf("🎯 推荐配置：Key字段（支持单密钥和多密钥）\n")
	}

	if meta.IsMultiKey && meta.Keys != nil {
		fmt.Printf("✅ [Vertex AI] 多密钥模式已启用，共 %d 个密钥\n", len(meta.Keys))
	}
}

// GetCredentialsFromConfig 从ChannelConfig获取Vertex AI凭证（向后兼容）
// 用于视频处理等需要从Config直接获取凭证的场景
func GetCredentialsFromConfig(cfg model.ChannelConfig, channel *model.Channel) (*Credentials, error) {
	// 方案1：优先尝试从渠道的Key字段解析（新方案）
	if channel != nil {
		if channel.MultiKeyInfo.IsMultiKey {
			// 多密钥模式：使用第一个可用的密钥
			keys := channel.ParseKeys()
			if len(keys) > 0 {
				var credentials Credentials
				if err := json.Unmarshal([]byte(keys[0]), &credentials); err == nil {
					fmt.Printf("[Vertex AI] 从多密钥Key字段获取凭证 - 项目: %s\n", credentials.ProjectID)
					return &credentials, nil
				}
			}
		} else {
			// 单密钥模式：从Key字段解析
			if channel.Key != "" {
				var credentials Credentials
				if err := json.Unmarshal([]byte(channel.Key), &credentials); err == nil {
					fmt.Printf("[Vertex AI] 从单密钥Key字段获取凭证 - 项目: %s\n", credentials.ProjectID)
					return &credentials, nil
				}
			}
		}
	}

	// 方案2：回退到Config.VertexAIADC（向后兼容）
	if cfg.VertexAIADC != "" {
		var credentials Credentials
		if err := json.Unmarshal([]byte(cfg.VertexAIADC), &credentials); err == nil {
			fmt.Printf("[Vertex AI] 从Config.VertexAIADC获取凭证 - 项目: %s（建议迁移到Key字段）\n", credentials.ProjectID)
			return &credentials, nil
		}
	}

	return nil, fmt.Errorf("无法从Config或Key字段获取有效的Vertex AI凭证")
}

// MigrateConfigToKey 迁移Config中的凭证到Key字段（管理员工具）
func MigrateConfigToKey(channelId int) error {
	channel, err := model.GetChannelById(channelId, true)
	if err != nil {
		return fmt.Errorf("获取渠道失败: %w", err)
	}

	cfg, err := channel.LoadConfig()
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}

	// 检查是否需要迁移
	if cfg.VertexAIADC == "" {
		return fmt.Errorf("渠道 %d 没有Config.VertexAIADC配置，无需迁移", channelId)
	}

	if channel.Key != "" && channel.Key != cfg.VertexAIADC {
		return fmt.Errorf("渠道 %d 的Key字段已有其他内容，请手动检查", channelId)
	}

	// 执行迁移
	fmt.Printf("🔄 开始迁移渠道 %d 的Vertex AI配置...\n", channelId)

	// 验证JSON格式
	var testCredentials Credentials
	if err := json.Unmarshal([]byte(cfg.VertexAIADC), &testCredentials); err != nil {
		return fmt.Errorf("Config.VertexAIADC中的JSON格式无效: %w", err)
	}

	// 迁移到Key字段
	channel.Key = cfg.VertexAIADC

	// 清空Config中的ADC字段（可选，保留用于兼容性）
	// cfg.VertexAIADC = ""
	// 更新配置...

	if err := channel.Update(); err != nil {
		return fmt.Errorf("更新渠道失败: %w", err)
	}

	fmt.Printf("✅ 渠道 %d 迁移完成 - 项目: %s\n", channelId, testCredentials.ProjectID)
	return nil
}
