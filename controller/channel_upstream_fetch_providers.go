package controller

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	"github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/aws/smithy-go/auth/bearer"
	"github.com/golang-jwt/jwt"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/model"
)

// ──────────────────────────────────────────
// AWS Bedrock: 通过 ListFoundationModels API 获取可用模型
// ──────────────────────────────────────────

func fetchBedrockModelList(channel *model.Channel) ([]string, error) {
	key := selectChannelKey(channel)
	if key == "" {
		return nil, fmt.Errorf("渠道密钥为空")
	}

	// Mantle 端点使用 Anthropic 原生 API 格式，走 /v1/models
	baseURL := channel.GetBaseURL()
	if strings.Contains(baseURL, "bedrock-mantle") {
		return fetchMantleModelList(baseURL, key)
	}

	cfg, _ := channel.LoadConfig()
	keyType := cfg.AwsKeyType
	parts := strings.Split(key, "|")

	if keyType == "" {
		if len(parts) == 2 {
			keyType = model.AwsKeyTypeAPIKey
		} else {
			keyType = model.AwsKeyTypeAKSK
		}
	}

	var client *bedrock.Client

	switch keyType {
	case model.AwsKeyTypeAPIKey:
		if len(parts) != 2 {
			return nil, fmt.Errorf("API Key 格式错误，期望 <token>|<region>")
		}
		token, region := parts[0], parts[1]
		client = bedrock.New(bedrock.Options{
			Region: region,
			BearerAuthTokenProvider: bearer.TokenProviderFunc(func(ctx context.Context) (bearer.Token, error) {
				return bearer.Token{Value: token}, nil
			}),
			AuthSchemePreference: []string{"httpBearerAuth"},
		})
	default:
		var accessKey, secretKey, region string
		if len(parts) == 3 {
			accessKey, secretKey, region = parts[0], parts[1], parts[2]
		} else {
			accessKey, secretKey, region = cfg.AK, cfg.SK, cfg.Region
		}
		if accessKey == "" || secretKey == "" || region == "" {
			return nil, fmt.Errorf("AWS 凭证不完整（需要 accessKey|secretKey|region）")
		}
		client = bedrock.New(bedrock.Options{
			Region:      region,
			Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	output, err := client.ListFoundationModels(ctx, &bedrock.ListFoundationModelsInput{
		ByProvider: aws.String("Anthropic"),
	})
	if err != nil {
		return nil, fmt.Errorf("ListFoundationModels 失败: %w", err)
	}

	seen := make(map[string]bool)
	var models []string
	for _, summary := range output.ModelSummaries {
		if summary.ModelId == nil {
			continue
		}
		if summary.ModelLifecycle != nil && summary.ModelLifecycle.Status == types.FoundationModelLifecycleStatusLegacy {
			continue
		}
		id := *summary.ModelId
		if !seen[id] {
			seen[id] = true
			models = append(models, id)
		}
	}
	return models, nil
}

// ──────────────────────────────────────────
// Vertex AI: 通过 publishers/google/models 或 OpenAI 兼容端点获取模型
// ──────────────────────────────────────────

func fetchVertexAIModelList(channel *model.Channel) ([]string, error) {
	key := selectChannelKey(channel)
	if key == "" {
		return nil, fmt.Errorf("渠道密钥为空")
	}

	cfg, _ := channel.LoadConfig()
	region := cfg.Region
	if region == "" {
		region = "us-central1"
	}

	isAPIKey := isVertexAIAPIKeyMode(cfg, key)

	if isAPIKey {
		return fetchVertexAIModelsWithAPIKey(key)
	}
	return fetchVertexAIModelsWithServiceAccount(key, cfg, region)
}

func isVertexAIAPIKeyMode(cfg model.ChannelConfig, key string) bool {
	if cfg.VertexKeyType == model.VertexKeyTypeAPIKey {
		return true
	}
	if cfg.VertexKeyType == "" && !strings.HasPrefix(strings.TrimSpace(key), "{") {
		return true
	}
	return false
}

func fetchVertexAIModelsWithAPIKey(apiKey string) ([]string, error) {
	// API Key 模式使用 Gemini API 全局端点（所有区域模型相同）
	apiURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models?key=%s&pageSize=1000", apiKey)
	return fetchVertexAIModelsFromURL(apiURL, "")
}

func fetchVertexAIModelsWithServiceAccount(key string, cfg model.ChannelConfig, region string) ([]string, error) {
	var creds vertexCredentials
	if err := json.Unmarshal([]byte(key), &creds); err != nil {
		if cfg.VertexAIADC != "" {
			if err2 := json.Unmarshal([]byte(cfg.VertexAIADC), &creds); err2 != nil {
				return nil, fmt.Errorf("无法解析 Vertex AI 凭证: %w", err)
			}
		} else {
			return nil, fmt.Errorf("无法解析 Vertex AI JSON 凭证: %w", err)
		}
	}

	if creds.ClientEmail == "" || creds.PrivateKey == "" {
		return nil, fmt.Errorf("Vertex AI 凭证缺少 client_email 或 private_key")
	}

	token, err := getVertexAIAccessToken(creds.ClientEmail, creds.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("获取 Vertex AI 访问令牌失败: %w", err)
	}

	projectID := creds.ProjectID
	if projectID == "" {
		projectID = cfg.VertexAIProjectID
	}
	if projectID == "" {
		return nil, fmt.Errorf("缺少 Vertex AI project_id")
	}

	var apiURL string
	if region == "global" {
		apiURL = fmt.Sprintf("https://aiplatform.googleapis.com/v1/projects/%s/locations/us-central1/publishers/google/models", projectID)
	} else {
		apiURL = fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models", region, projectID, region)
	}

	return fetchVertexAIModelsFromURL(apiURL, token)
}

type vertexCredentials struct {
	ProjectID   string `json:"project_id"`
	PrivateKey  string `json:"private_key"`
	ClientEmail string `json:"client_email"`
}

type vertexModelsResponse struct {
	// Gemini API (generativelanguage.googleapis.com) 使用 "models" 字段
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
	// Vertex AI publishers 端点使用 "publisherModels" 字段
	PublisherModels []struct {
		Name string `json:"name"`
	} `json:"publisherModels"`
	NextPageToken string `json:"nextPageToken"`
}

const vertexAIMaxResponseBytes = 10 << 20 // 10 MB

func fetchVertexAIModelsFromURL(apiURL, token string) ([]string, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	parsed, err := url.Parse(apiURL)
	if err != nil {
		return nil, fmt.Errorf("解析 API URL 失败: %w", err)
	}

	var allModels []string

	for {
		req, err := http.NewRequest("GET", parsed.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("创建请求失败: %w", err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("请求失败: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, vertexAIMaxResponseBytes))
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("读取响应体失败: %w", readErr)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("API 返回 %d: %s", resp.StatusCode, string(body[:min(200, len(body))]))
		}

		var result vertexModelsResponse
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("解析响应失败: %w", err)
		}

		for _, m := range result.Models {
			name := m.Name
			if idx := strings.LastIndex(name, "/"); idx >= 0 {
				name = name[idx+1:]
			}
			if name != "" {
				allModels = append(allModels, name)
			}
		}
		for _, m := range result.PublisherModels {
			name := m.Name
			if idx := strings.LastIndex(name, "/"); idx >= 0 {
				name = name[idx+1:]
			}
			if name != "" {
				allModels = append(allModels, name)
			}
		}

		if result.NextPageToken == "" {
			break
		}
		q := parsed.Query()
		q.Set("pageToken", result.NextPageToken)
		parsed.RawQuery = q.Encode()
	}

	return allModels, nil
}

// ── Vertex AI token 缓存 ──
// Google OAuth2 access token 有效期通常为 1 小时，JWT claims 中设置了 35 min exp。
// 在有效期剩余 5 分钟前复用缓存，避免并发巡检时对 Google token endpoint 产生过多请求。

type vertexTokenEntry struct {
	token  string
	expiry time.Time
}

var (
	vertexTokenCache sync.Map // key: clientEmail → *vertexTokenEntry
)

const vertexTokenRefreshMargin = 5 * time.Minute

func getVertexAIAccessToken(email, privateKeyPEM string) (string, error) {
	if entry, ok := vertexTokenCache.Load(email); ok {
		te := entry.(*vertexTokenEntry)
		if time.Now().Before(te.expiry.Add(-vertexTokenRefreshMargin)) {
			return te.token, nil
		}
	}

	token, expiry, err := exchangeVertexAIAccessToken(email, privateKeyPEM)
	if err != nil {
		return "", err
	}

	vertexTokenCache.Store(email, &vertexTokenEntry{token: token, expiry: expiry})
	return token, nil
}

func exchangeVertexAIAccessToken(email, privateKeyPEM string) (string, time.Time, error) {
	privateKeyPEM = strings.ReplaceAll(privateKeyPEM, "\\n", "\n")
	cleanKey := strings.ReplaceAll(privateKeyPEM, "-----BEGIN PRIVATE KEY-----", "")
	cleanKey = strings.ReplaceAll(cleanKey, "-----END PRIVATE KEY-----", "")
	cleanKey = strings.ReplaceAll(cleanKey, "\r", "")
	cleanKey = strings.ReplaceAll(cleanKey, "\n", "")

	block, _ := pem.Decode([]byte("-----BEGIN PRIVATE KEY-----\n" + cleanKey + "\n-----END PRIVATE KEY-----"))
	if block == nil {
		return "", time.Time{}, fmt.Errorf("failed to parse PEM block")
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("解析私钥失败: %w", err)
	}

	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return "", time.Time{}, fmt.Errorf("非 RSA 私钥")
	}

	now := time.Now()
	tokenLifetime := 35 * time.Minute
	claims := jwt.MapClaims{
		"iss":   email,
		"scope": "https://www.googleapis.com/auth/cloud-platform",
		"aud":   "https://www.googleapis.com/oauth2/v4/token",
		"exp":   now.Add(tokenLifetime).Unix(),
		"iat":   now.Unix(),
	}

	signedJWT, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(rsaKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("JWT 签名失败: %w", err)
	}

	data := url.Values{}
	data.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	data.Set("assertion", signedJWT)

	resp, err := http.PostForm("https://www.googleapis.com/oauth2/v4/token", data)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("令牌交换失败: %w", err)
	}
	defer resp.Body.Close()

	var tokenResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", time.Time{}, fmt.Errorf("解析令牌响应失败: %w", err)
	}

	accessToken, ok := tokenResp["access_token"].(string)
	if !ok || accessToken == "" {
		return "", time.Time{}, fmt.Errorf("未获取到 access_token: %v", tokenResp)
	}
	return accessToken, now.Add(tokenLifetime), nil
}

// ──────────────────────────────────────────
// AWS Bedrock Mantle: Anthropic 原生 API 格式，支持 /v1/models
// ──────────────────────────────────────────

func fetchMantleModelList(baseURL, apiKey string) ([]string, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	url := baseURL + "/v1/models"

	headers := make(http.Header)
	headers.Set("x-api-key", apiKey)
	headers.Set("anthropic-version", "2023-06-01")

	return fetchModelsFromURL(url, headers)
}

// ──────────────────────────────────────────
// 公共辅助
// ──────────────────────────────────────────

// selectChannelKey 从渠道中选取第一个可用的 Key
func selectChannelKey(channel *model.Channel) string {
	key := channel.Key
	if keys := channel.ParseKeys(); len(keys) > 0 {
		selected := ""
		for i, k := range keys {
			if channel.GetKeyStatus(i) == common.ChannelStatusEnabled {
				selected = k
				break
			}
		}
		if selected == "" {
			selected = keys[0]
		}
		key = selected
	}
	return strings.TrimSpace(key)
}
