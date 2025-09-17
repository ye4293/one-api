package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/gemini"
	"github.com/songquanpeng/one-api/relay/channel/keling"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/channel/vertexai"
	"github.com/songquanpeng/one-api/relay/helper"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"

	"github.com/gin-gonic/gin"
)

func RelayImageHelper(c *gin.Context, relayMode int) *relaymodel.ErrorWithStatusCode {

	startTime := time.Now()
	ctx := c.Request.Context()

	channelId := c.GetInt("channel_id")
	userId := c.GetInt("id")

	logger.Infof(ctx, "RelayImageHelper START: relayMode=%d, channelId=%d, userId=%d, path=%s",
		relayMode, channelId, userId, c.Request.URL.Path)

	// 获取 meta 信息用于调试
	meta := util.GetRelayMeta(c)

	// VertexAI 配置调试信息
	if meta.ChannelType == common.ChannelTypeVertexAI {
		logger.Infof(ctx, "🔍 [VertexAI Debug] =====【VertexAI渠道配置信息】=====")
		logger.Infof(ctx, "🔍 [VertexAI Debug] ChannelId: %d, ChannelType: %d", meta.ChannelId, meta.ChannelType)
		logger.Infof(ctx, "🔍 [VertexAI Debug] IsMultiKey: %v, KeyIndex: %v", meta.IsMultiKey, meta.KeyIndex)
		logger.Infof(ctx, "🔍 [VertexAI Debug] Keys数量: %d, ActualAPIKey长度: %d", len(meta.Keys), len(meta.ActualAPIKey))
		logger.Infof(ctx, "🔍 [VertexAI Debug] Config.Region: '%s', Config.VertexAIProjectID: '%s'", meta.Config.Region, meta.Config.VertexAIProjectID)
		logger.Infof(ctx, "🔍 [VertexAI Debug] Config.VertexAIADC是否为空: %v", meta.Config.VertexAIADC == "")
		logger.Infof(ctx, "🔍 [VertexAI Debug] BaseURL: '%s'", meta.BaseURL)
		logger.Infof(ctx, "🔍 [VertexAI Debug] =============================")
	}

	// 检查函数开始时的上下文状态
	if channelHistoryInterface, exists := c.Get("admin_channel_history"); exists {
		logger.Debugf(ctx, "RelayImageHelper: ENTRY - admin_channel_history exists: %v", channelHistoryInterface)
	}
	// 检查内容类型
	contentType := c.GetHeader("Content-Type")
	isFormRequest := strings.Contains(contentType, "multipart/form-data") || strings.Contains(contentType, "application/x-www-form-urlencoded")

	// 获取基本的请求信息，但不消费请求体
	imageRequest, err := getImageRequest(c, meta.Mode)
	if err != nil {
		logger.Errorf(ctx, "getImageRequest failed: %s", err.Error())
		return openai.ErrorWrapper(err, "invalid_image_request", http.StatusBadRequest)
	}

	// map model name
	var isModelMapped bool
	meta.OriginModelName = imageRequest.Model
	imageRequest.Model, isModelMapped = util.GetMappedModelName(imageRequest.Model, meta.ModelMapping)
	meta.ActualModelName = imageRequest.Model

	imageCostRatio, err := getImageCostRatio(imageRequest)
	if err != nil {
		return openai.ErrorWrapper(err, "get_image_cost_ratio_failed", http.StatusInternalServerError)
	}

	var fullRequestURL string
	requestURL := c.Request.URL.String()
	fullRequestURL = util.GetFullRequestURL(meta.BaseURL, requestURL, meta.ChannelType)
	if meta.ChannelType == common.ChannelTypeAzure {
		apiVersion := util.GetAzureAPIVersion(c)
		fullRequestURL = fmt.Sprintf("%s/openai/deployments/%s/images/generations?api-version=%s", meta.BaseURL, imageRequest.Model, apiVersion)
	}
	if meta.ChannelType == 27 { //minimax
		fullRequestURL = fmt.Sprintf("%s/v1/image_generation", meta.BaseURL)
	}
	if meta.ChannelType == 40 { //doubao (字节跳动豆包)
		fullRequestURL = fmt.Sprintf("%s/api/v3/images/generations", meta.BaseURL)
	}

	var requestBody io.Reader
	var req *http.Request

	if isFormRequest {
		// 对于表单请求，我们需要特殊处理
		if strings.Contains(contentType, "multipart/form-data") {
			// 解析原始表单
			if err := c.Request.ParseMultipartForm(32 << 20); err != nil { // 32MB
				return openai.ErrorWrapper(err, "parse_multipart_form_failed", http.StatusBadRequest)
			}

			// 检查是否是 Gemini 模型的 form 请求，需要特殊处理转换为 JSON
			if strings.HasPrefix(imageRequest.Model, "gemini") {
				return handleGeminiFormRequest(c, ctx, imageRequest, meta, fullRequestURL)
			}

			// 对于其他模型，继续原有的 form 转发逻辑
			// 创建一个新的multipart表单
			body := &bytes.Buffer{}
			writer := multipart.NewWriter(body)

			// 添加所有表单字段
			for key, values := range c.Request.MultipartForm.Value {
				for _, value := range values {
					// 如果模型被映射，则更新model字段
					if key == "model" && isModelMapped {
						writer.WriteField(key, imageRequest.Model)
					} else {
						writer.WriteField(key, value)
					}
				}
			}

			// 添加所有文件
			for key, fileHeaders := range c.Request.MultipartForm.File {
				for _, fileHeader := range fileHeaders {
					file, err := fileHeader.Open()
					if err == nil {
						// 获取文件的MIME类型
						mimeType := fileHeader.Header.Get("Content-Type")
						if mimeType == "" || mimeType == "application/octet-stream" {
							ext := strings.ToLower(filepath.Ext(fileHeader.Filename))
							switch ext {
							case ".png":
								mimeType = "image/png"
							case ".jpg", ".jpeg":
								mimeType = "image/jpeg"
							case ".webp":
								mimeType = "image/webp"
							default:
								// 如果无法确定，默认使用image/png
								if key == "image" {
									mimeType = "image/png"
								}
							}
						}

						// 使用自定义头部创建表单部分
						h := textproto.MIMEHeader{}
						h.Set("Content-Disposition",
							fmt.Sprintf(`form-data; name="%s"; filename="%s"`,
								escapeQuotes(key), escapeQuotes(fileHeader.Filename)))
						h.Set("Content-Type", mimeType)

						// 使用CreatePart而不是CreateFormFile
						part, err := writer.CreatePart(h)
						if err == nil {
							io.Copy(part, file)
							logger.Debugf(ctx, "Added file %s with MIME type %s to form", fileHeader.Filename, mimeType)
						} else {
							logger.Errorf(ctx, "Failed to create form part for file %s: %v", fileHeader.Filename, err)
						}
						file.Close()
					} else {
						logger.Errorf(ctx, "Failed to open file %s: %v", fileHeader.Filename, err)
					}
				}
			}

			writer.Close()
			requestBody = body

			// 创建请求
			req, err = http.NewRequest(c.Request.Method, fullRequestURL, requestBody)
			if err != nil {
				return openai.ErrorWrapper(err, "new_request_failed", http.StatusInternalServerError)
			}

			// 设置Content-Type为multipart/form-data
			req.Header.Set("Content-Type", writer.FormDataContentType())

		} else if strings.Contains(contentType, "application/x-www-form-urlencoded") {
			// 解析表单
			if err := c.Request.ParseForm(); err != nil {
				return openai.ErrorWrapper(err, "parse_form_failed", http.StatusBadRequest)
			}

			// 创建新的表单数据
			formData := url.Values{}
			for key, values := range c.Request.Form {
				// 如果模型被映射，则更新model字段
				if key == "model" && isModelMapped {
					formData.Set(key, imageRequest.Model)
				} else {
					for _, value := range values {
						formData.Add(key, value)
					}
				}
			}

			// 编码表单数据
			encodedFormData := formData.Encode()
			requestBody = strings.NewReader(encodedFormData)

			// 创建请求
			req, err = http.NewRequest(c.Request.Method, fullRequestURL, requestBody)
			if err != nil {
				return openai.ErrorWrapper(err, "new_request_failed", http.StatusInternalServerError)
			}

			// 设置Content-Type为application/x-www-form-urlencoded
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	} else {
		// 对于非表单请求，使用原有逻辑
		if isModelMapped || meta.ChannelType == common.ChannelTypeAzure {
			jsonStr, err := json.Marshal(imageRequest)
			if err != nil {
				return openai.ErrorWrapper(err, "marshal_image_request_failed", http.StatusInternalServerError)
			}
			requestBody = bytes.NewBuffer(jsonStr)
		} else {
			requestBody = c.Request.Body
		}

		if strings.HasPrefix(imageRequest.Model, "gemini") {
			// Print the original request body
			bodyBytes, err := io.ReadAll(c.Request.Body)
			if err != nil {
				return openai.ErrorWrapper(err, "read_request_body_failed", http.StatusBadRequest)
			}

			// Restore the request body for further use
			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

			// Create Gemini image request structure
			geminiImageRequest := gemini.ChatRequest{
				Contents: []gemini.ChatContent{
					{
						Role: "user",
						Parts: []gemini.Part{
							{
								Text: imageRequest.Prompt,
							},
						},
					},
				},
				GenerationConfig: gemini.ChatGenerationConfig{
					ResponseModalities: []string{"TEXT", "IMAGE"},
				},
			}

			// 记录原始请求体
			bodyBytes, err = io.ReadAll(c.Request.Body)
			if err != nil {
				return openai.ErrorWrapper(err, "read_request_body_failed", http.StatusBadRequest)
			}

			// 恢复请求体以供后续使用
			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

			var requestMap map[string]interface{}
			if err := json.Unmarshal(bodyBytes, &requestMap); err != nil {
				return openai.ErrorWrapper(fmt.Errorf("请求中的 JSON 无效: %w", err), "invalid_request_json", http.StatusBadRequest)
			}

			if image, ok := requestMap["image"].(string); ok && image != "" {
				// Parse the base64 image data
				// Format is typically: data:image/png;base64,BASE64_DATA
				parts := strings.SplitN(image, ",", 2)

				var mimeType string
				var imageData string

				if len(parts) == 2 {
					// Extract mime type from the prefix
					mimeTypeParts := strings.SplitN(parts[0], ":", 2)
					if len(mimeTypeParts) == 2 {
						mimeTypeParts = strings.SplitN(mimeTypeParts[1], ";", 2)
						if len(mimeTypeParts) > 0 {
							mimeType = mimeTypeParts[0]
						}
					}
					imageData = parts[1]
				} else {
					// If no comma found, assume it's just the base64 data
					mimeType = "image/png" // Default to PNG if not specified
					imageData = image
				}

				// Add the image to the Gemini request
				geminiImageRequest.Contents[0].Parts = append(geminiImageRequest.Contents[0].Parts, gemini.Part{
					InlineData: &gemini.InlineData{
						MimeType: mimeType,
						Data:     imageData,
					},
				})
			}

			// Convert to JSON
			jsonStr, err := json.Marshal(geminiImageRequest)
			if err != nil {
				return openai.ErrorWrapper(err, "marshal_gemini_request_failed", http.StatusInternalServerError)
			}

			// Print the transformed Gemini request body for debugging（省略具体内容，避免 base64 数据占用日志）
			logger.Infof(ctx, "Gemini JSON 请求体已构建完成，包含文本提示和图片数据")

			requestBody = bytes.NewBuffer(jsonStr)

			// Update URL for Gemini API
			if meta.ChannelType == common.ChannelTypeVertexAI {
				logger.Infof(ctx, "🔧 [VertexAI Debug] 开始处理VertexAI图像请求")
				logger.Infof(ctx, "🔧 [VertexAI Debug] ChannelId: %d, ChannelType: %d", meta.ChannelId, meta.ChannelType)
				logger.Infof(ctx, "🔧 [VertexAI Debug] IsMultiKey: %v, KeyIndex: %v", meta.IsMultiKey, meta.KeyIndex)

				// 为VertexAI构建URL
				keyIndex := 0
				if meta.KeyIndex != nil {
					keyIndex = *meta.KeyIndex
					logger.Infof(ctx, "🔧 [VertexAI Debug] 使用KeyIndex: %d", keyIndex)
				}

				// 安全检查：确保keyIndex不为负数
				if keyIndex < 0 {
					logger.Errorf(ctx, "🔧 [VertexAI Debug] keyIndex为负数: %d，重置为0", keyIndex)
					keyIndex = 0
				}

				projectID := ""

				// 尝试从Key字段解析项目ID（支持多密钥）
				if meta.IsMultiKey && len(meta.Keys) > keyIndex && keyIndex >= 0 {
					logger.Infof(ctx, "🔧 [VertexAI Debug] 多密钥模式，Keys总数: %d, 当前索引: %d", len(meta.Keys), keyIndex)
					// 多密钥模式：从指定索引的密钥解析
					var credentials vertexai.Credentials
					if err := json.Unmarshal([]byte(meta.Keys[keyIndex]), &credentials); err == nil {
						projectID = credentials.ProjectID
						logger.Infof(ctx, "🔧 [VertexAI Debug] 从多密钥解析ProjectID成功: %s", projectID)
					} else {
						logger.Errorf(ctx, "🔧 [VertexAI Debug] 从多密钥解析ProjectID失败: %v", err)
					}
				} else if meta.ActualAPIKey != "" {
					logger.Infof(ctx, "🔧 [VertexAI Debug] 单密钥模式，ActualAPIKey长度: %d", len(meta.ActualAPIKey))
					// 单密钥模式：从ActualAPIKey解析
					var credentials vertexai.Credentials
					if err := json.Unmarshal([]byte(meta.ActualAPIKey), &credentials); err == nil {
						projectID = credentials.ProjectID
						logger.Infof(ctx, "🔧 [VertexAI Debug] 从ActualAPIKey解析ProjectID成功: %s", projectID)
					} else {
						logger.Errorf(ctx, "🔧 [VertexAI Debug] 从ActualAPIKey解析ProjectID失败: %v", err)
					}
				} else {
					logger.Warnf(ctx, "🔧 [VertexAI Debug] 无法获取密钥信息，IsMultiKey: %v, Keys长度: %d, ActualAPIKey是否为空: %v",
						meta.IsMultiKey, len(meta.Keys), meta.ActualAPIKey == "")
				}

				// 回退：尝试从Config获取项目ID
				if projectID == "" && meta.Config.VertexAIProjectID != "" {
					projectID = meta.Config.VertexAIProjectID
					logger.Infof(ctx, "🔧 [VertexAI Debug] 从Config获取ProjectID: %s", projectID)
				}

				if projectID == "" {
					logger.Errorf(ctx, "🔧 [VertexAI Debug] 无法获取ProjectID，所有方式都失败了")
					return openai.ErrorWrapper(fmt.Errorf("VertexAI project ID not found"), "vertex_ai_project_id_missing", http.StatusBadRequest)
				}

				region := meta.Config.Region
				if region == "" {
					region = "global"
				}
				logger.Infof(ctx, "🔧 [VertexAI Debug] 使用Region: %s", region)
				logger.Infof(ctx, "🔧 [VertexAI Debug] 使用Model: %s", meta.OriginModelName)

				// 构建VertexAI API URL - 使用generateContent而不是predict用于图像生成
				if region == "global" {
					fullRequestURL = fmt.Sprintf("https://aiplatform.googleapis.com/v1/projects/%s/locations/global/publishers/google/models/%s:generateContent", projectID, meta.OriginModelName)
				} else {
					fullRequestURL = fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s:generateContent", region, projectID, region, meta.OriginModelName)
				}
				logger.Infof(ctx, "🔧 [VertexAI Debug] 构建的完整URL: %s", fullRequestURL)
			} else {
				// 原有的Gemini官方API URL
				fullRequestURL = fmt.Sprintf("%s/v1beta/models/%s:generateContent", meta.BaseURL, meta.OriginModelName)
				logger.Infof(ctx, "Gemini API URL: %s", fullRequestURL)
			}
		}

		if meta.ChannelType == 27 {
			// 将请求体解析为 map
			var requestMap map[string]interface{}
			if err := json.NewDecoder(c.Request.Body).Decode(&requestMap); err != nil {
				return openai.ErrorWrapper(err, "decode_request_failed", http.StatusBadRequest)
			}

			// 如果存在 size 参数，将其值赋给 AspectRatio 并删除 size
			if size, ok := requestMap["size"].(string); ok {
				// 处理不同格式的 size
				if strings.Contains(size, "x") {
					// 处理分辨率格式 (如 "1024x1024")
					parts := strings.Split(size, "x")
					if len(parts) == 2 {
						width, wErr := strconv.Atoi(parts[0])
						height, hErr := strconv.Atoi(parts[1])
						if wErr == nil && hErr == nil && width > 0 && height > 0 {
							// 计算宽高比并简化
							gcd := gcd(width, height)
							aspectRatio := fmt.Sprintf("%d:%d", width/gcd, height/gcd)
							requestMap["aspect_ratio"] = aspectRatio
						} else {
							// 如果解析失败，直接使用原始值
							requestMap["aspect_ratio"] = size
						}
					} else {
						requestMap["aspect_ratio"] = size
					}
				} else {
					// 直接使用比例格式 (如 "1:1", "4:3")
					requestMap["aspect_ratio"] = size
				}
				delete(requestMap, "size")
			}

			// 重新序列化
			jsonStr, err := json.Marshal(requestMap)
			if err != nil {
				return openai.ErrorWrapper(err, "marshal_request_failed", http.StatusInternalServerError)
			}

			requestBody = bytes.NewBuffer(jsonStr)
		} else if meta.ChannelType == common.ChannelTypeRecraft {
			// 将请求体解析为 map
			var requestMap map[string]interface{}
			if err := json.NewDecoder(c.Request.Body).Decode(&requestMap); err != nil {
				return openai.ErrorWrapper(err, "decode_request_failed", http.StatusBadRequest)
			}

			// 检查 model 字段
			if model, ok := requestMap["model"].(string); ok {
				if model == "recraftv2" {
					imageRequest.Model = "recraftv2"
					meta.ActualModelName = "recraftv2"
				} else {
					// 默认设置为 recraftv3
					imageRequest.Model = "recraftv3"
					meta.ActualModelName = "recraftv3"
				}
			} else {
				// 如果没有 model 字段，默认设置为 recraftv3
				imageRequest.Model = "recraftv3"
				meta.ActualModelName = "recraftv3"
			}

			// 重新序列化
			jsonStr, err := json.Marshal(requestMap)
			if err != nil {
				return openai.ErrorWrapper(err, "marshal_request_failed", http.StatusInternalServerError)
			}
			requestBody = bytes.NewBuffer(jsonStr)
		}

		// 创建请求
		req, err = http.NewRequest(c.Request.Method, fullRequestURL, requestBody)
		if err != nil {
			return openai.ErrorWrapper(err, "new_request_failed", http.StatusInternalServerError)
		}

		// 设置Content-Type
		req.Header.Set("Content-Type", contentType)
	}

	// 在发送请求前记录详细信息
	logger.Infof(ctx, "Sending request to %s", fullRequestURL)
	logger.Infof(ctx, "Request Content-Type: %s", req.Header.Get("Content-Type"))

	// VertexAI调试信息
	if meta.ChannelType == common.ChannelTypeVertexAI && strings.HasPrefix(imageRequest.Model, "gemini") {
		logger.Infof(ctx, "📤 [VertexAI Debug] 即将发送请求到VertexAI")
		logger.Infof(ctx, "📤 [VertexAI Debug] Request Headers: Content-Type=%s, Authorization=%s",
			req.Header.Get("Content-Type"),
			func() string {
				auth := req.Header.Get("Authorization")
				if len(auth) > 20 {
					return auth[:20] + "..."
				}
				return auth
			}())
	}

	// 如果是表单请求，记录表单内容
	if isFormRequest && strings.Contains(contentType, "multipart/form-data") {
		for key, values := range c.Request.MultipartForm.Value {
			logger.Debugf(ctx, "Form field: %s = %s", key, values[0])
		}

		for key, fileHeaders := range c.Request.MultipartForm.File {
			for _, fileHeader := range fileHeaders {
				logger.Debugf(ctx, "Form file: %s, filename: %s, size: %d, content-type: %s",
					key, fileHeader.Filename, fileHeader.Size, fileHeader.Header.Get("Content-Type"))
			}
		}
	}

	adaptor := helper.GetAdaptor(meta.APIType)
	if adaptor == nil {
		return openai.ErrorWrapper(fmt.Errorf("invalid api typezz: %d", meta.APIType), "invalid_api_type", http.StatusBadRequest)
	}
	adaptor.Init(meta)
	groupRatio := common.GetGroupRatio(meta.Group)
	// userModelTypeRatio := common.GetUserModelTypeRation(meta.Group, imageRequest.Model)
	ratio := groupRatio
	userQuota, err := model.CacheGetUserQuota(ctx, meta.UserId)
	if err != nil {
		return openai.ErrorWrapper(err, "failed to get user quota", http.StatusInternalServerError)
	}

	var modelPrice float64
	defaultPrice, ok := common.DefaultModelPrice[imageRequest.Model]
	if !ok {
		modelPrice = 0.1
	} else {
		modelPrice = defaultPrice
	}
	quota := int64(modelPrice*500000*imageCostRatio*ratio) * int64(imageRequest.N)

	if userQuota-quota < 0 {
		return openai.ErrorWrapper(errors.New("user quota is not enough"), "insufficient_user_quota", http.StatusForbidden)
	}

	// 设置通用请求头
	token := c.Request.Header.Get("Authorization")
	if meta.ChannelType == common.ChannelTypeAzure {
		token = strings.TrimPrefix(token, "Bearer ")
		req.Header.Set("api-key", token)
	} else if strings.HasPrefix(imageRequest.Model, "gemini") {
		if meta.ChannelType == common.ChannelTypeVertexAI {
			logger.Infof(ctx, "🔐 [VertexAI Debug] 开始VertexAI认证流程")
			// 为VertexAI使用Bearer token认证 - 复用已有的adaptor实例
			var vertexAIAdaptor *vertexai.Adaptor
			if va, ok := adaptor.(*vertexai.Adaptor); ok {
				vertexAIAdaptor = va
			} else {
				// 如果不是VertexAI适配器，创建新实例（这种情况不应该发生）
				vertexAIAdaptor = &vertexai.Adaptor{}
				vertexAIAdaptor.Init(meta)
				logger.Warnf(ctx, "🔐 [VertexAI Debug] 警告：adaptor类型不匹配，创建新的VertexAI适配器实例")
			}

			logger.Infof(ctx, "🔐 [VertexAI Debug] 调用GetAccessToken获取访问令牌")
			accessToken, err := vertexai.GetAccessToken(vertexAIAdaptor, meta)
			if err != nil {
				logger.Errorf(ctx, "🔐 [VertexAI Debug] 获取访问令牌失败: %v", err)
				return openai.ErrorWrapper(fmt.Errorf("failed to get VertexAI access token: %v", err), "vertex_ai_auth_failed", http.StatusUnauthorized)
			}

			// 只显示令牌的前10个字符用于调试，避免完整令牌泄露
			tokenPreview := ""
			if len(accessToken) > 10 {
				tokenPreview = accessToken[:10] + "..."
			} else {
				tokenPreview = accessToken
			}
			logger.Infof(ctx, "🔐 [VertexAI Debug] 成功获取访问令牌，长度: %d, 前缀: %s", len(accessToken), tokenPreview)

			req.Header.Set("Authorization", "Bearer "+accessToken)
			logger.Infof(ctx, "🔐 [VertexAI Debug] 已设置Authorization header为Bearer token")
		} else {
			// For Gemini, set the API key in the x-goog-api-key header
			req.Header.Set("x-goog-api-key", meta.APIKey)
			logger.Infof(ctx, "Setting x-goog-api-key header for Gemini API request")
		}
	} else {
		req.Header.Set("Authorization", token)
	}

	req.Header.Set("Accept", c.Request.Header.Get("Accept"))

	resp, err := util.HTTPClient.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
	}

	// 关闭请求体，但不让关闭错误覆盖有用的响应数据
	if err := req.Body.Close(); err != nil {
		logger.Warnf(ctx, "关闭请求体失败: %v", err)
	}
	if err := c.Request.Body.Close(); err != nil {
		logger.Warnf(ctx, "关闭原始请求体失败: %v", err)
	}
	var imageResponse openai.ImageResponse
	var responseBody []byte

	// 用于保存 Gemini token 信息
	var geminiPromptTokens, geminiCompletionTokens int

	defer func(ctx context.Context) {
		if resp == nil || (resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated) {
			return
		}

		// 对于 gpt-image-1 模型，先解析响应并计算 quota
		if meta.ActualModelName == "gpt-image-1" {
			var parsedResponse openai.ImageResponse
			if err := json.Unmarshal(responseBody, &parsedResponse); err != nil {
				logger.SysError("error parsing gpt-image-1 response: " + err.Error())
			} else {
				// 先将令牌数转换为浮点数
				textTokens := float64(parsedResponse.Usage.InputTokensDetails.TextTokens)
				imageTokens := float64(parsedResponse.Usage.InputTokensDetails.ImageTokens)
				outputTokens := float64(parsedResponse.Usage.OutputTokens)

				// 保存旧的 quota 值用于日志
				oldQuota := quota

				// 修复：先乘后除，避免小数被截断为0
				textCost := textTokens * 5 / 1000000
				imageCost := imageTokens * 10 / 1000000
				outputCost := outputTokens * 40 / 1000000

				// 计算总成本并转换为quota单位
				calculatedQuota := int64((textCost + imageCost + outputCost) * 500000)
				quota = int64(float64(calculatedQuota) * ratio)

				// 记录日志
				logger.Infof(ctx, "GPT-Image-1 token usage: text=%d, image=%d, output=%d, old quota=%d, new quota=%d",
					int(textTokens), int(imageTokens), int(outputTokens), oldQuota, quota)
			}
		}

		// 对于 Gemini 模型，跳过处理（已在响应处理中直接处理）
		var promptTokens, completionTokens int
		if strings.HasPrefix(meta.ActualModelName, "gemini") || strings.HasPrefix(meta.OriginModelName, "gemini") {
			logger.Infof(ctx, "Defer 函数跳过 Gemini 模型处理（已在响应处理中完成）: ActualModelName=%s, OriginModelName=%s", meta.ActualModelName, meta.OriginModelName)
			return // 跳过 Gemini 的处理
		}

		// 然后再处理配额消费
		err := model.PostConsumeTokenQuota(meta.TokenId, quota)
		if err != nil {
			logger.SysError("error consuming token remain quota: " + err.Error())
		}

		err = model.CacheUpdateUserQuota(ctx, meta.UserId)
		if err != nil {
			logger.SysError("error update user quota cache: " + err.Error())
		}

		referer := c.Request.Header.Get("HTTP-Referer")
		title := c.Request.Header.Get("X-Title")
		rowDuration := time.Since(startTime).Seconds()
		duration := math.Round(rowDuration*1000) / 1000
		tokenName := c.GetString("token_name")
		xRequestID := c.GetString("X-Request-ID")

		// 对于 Gemini 模型，包含 token 使用信息
		var logContent string
		if strings.HasPrefix(meta.ActualModelName, "gemini") || strings.HasPrefix(meta.OriginModelName, "gemini") {
			modelPriceFloat := float64(quota) / 500000
			logContent = fmt.Sprintf("Gemini JSON Request - Model: %s, Price: $%.4f, Tokens: prompt=%d, completion=%d, total=%d",
				meta.OriginModelName, modelPriceFloat, promptTokens, completionTokens, promptTokens+completionTokens)
		} else {
			logContent = fmt.Sprintf("模型价格 $%.2f，分组倍率 %.2f", modelPrice, groupRatio)
		}

		// 记录消费日志 - 在RelayImageHelper中不需要处理other字段，这由具体的处理函数负责
		model.RecordConsumeLogWithRequestID(ctx, meta.UserId, meta.ChannelId, promptTokens, completionTokens, meta.ActualModelName, tokenName, quota, logContent, duration, title, referer, false, 0.0, xRequestID)
		model.UpdateUserUsedQuotaAndRequestCount(meta.UserId, quota)
		channelId := c.GetInt("channel_id")
		model.UpdateChannelUsedQuota(channelId, quota)

		// 更新多Key使用统计
		UpdateMultiKeyUsageFromContext(c, quota > 0)

	}(c.Request.Context())

	responseBody, err = io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}

	err = resp.Body.Close()
	if err != nil {
		return openai.ErrorWrapper(err, "close_response_body_failed", http.StatusInternalServerError)
	}

	// 检查HTTP状态码，如果不是成功状态码，直接返回错误
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		logger.Errorf(ctx, "API返回错误状态码: %d, 响应体: %s", resp.StatusCode, string(responseBody))

		// 检查错误返回时的上下文状态
		if channelHistoryInterface, exists := c.Get("admin_channel_history"); exists {
			logger.Infof(ctx, "RelayImageHelper: EXIT ERROR - admin_channel_history exists: %v", channelHistoryInterface)
		} else {
			logger.Warnf(ctx, "RelayImageHelper: EXIT ERROR - admin_channel_history NOT found")
		}

		logger.Errorf(ctx, "RelayImageHelper EXIT ERROR: returning error for status %d", resp.StatusCode)
		return openai.ErrorWrapper(
			fmt.Errorf("API请求失败，状态码: %d，响应: %s", resp.StatusCode, string(responseBody)),
			"api_error",
			resp.StatusCode,
		)
	}

	// Handle Gemini response format conversion
	if strings.HasPrefix(meta.OriginModelName, "gemini") {
		logger.Infof(ctx, "进入 Gemini 响应处理逻辑，原始模型: %s, 映射后模型: %s", meta.OriginModelName, imageRequest.Model)
		// Add debug logging for the original response body（省略具体内容，避免 base64 数据占用日志）
		logger.Infof(ctx, "Gemini 原始响应已接收，状态码: %d", resp.StatusCode)

		// VertexAI特定的调试信息
		if meta.ChannelType == common.ChannelTypeVertexAI {
			logger.Infof(ctx, "📥 [VertexAI Debug] 收到VertexAI响应，状态码: %d", resp.StatusCode)
			logger.Infof(ctx, "📥 [VertexAI Debug] 响应体长度: %d bytes", len(responseBody))

			// 检查响应头
			if contentType := resp.Header.Get("Content-Type"); contentType != "" {
				logger.Infof(ctx, "📥 [VertexAI Debug] 响应Content-Type: %s", contentType)
			}
		}

		logger.Infof(ctx, "处理 Gemini 响应，状态码: %d", resp.StatusCode)

		// Check if response is an error
		var geminiError struct {
			Error struct {
				Code    int                      `json:"code"`
				Message string                   `json:"message"`
				Status  string                   `json:"status"`
				Details []map[string]interface{} `json:"details,omitempty"`
			} `json:"error"`
		}

		if err := json.Unmarshal(responseBody, &geminiError); err != nil {
			logger.Errorf(ctx, "解析 Gemini 错误响应失败: %s", err.Error())
			// VertexAI特定的错误解析调试
			if meta.ChannelType == common.ChannelTypeVertexAI {
				logger.Errorf(ctx, "🚨 [VertexAI Debug] VertexAI错误响应解析失败，原始响应: %s", string(responseBody))
			}
		} else if geminiError.Error.Message != "" {
			if meta.ChannelType == common.ChannelTypeVertexAI {
				logger.Errorf(ctx, "🚨 [VertexAI Debug] VertexAI API 返回错误: 代码=%d, 消息=%s, 状态=%s",
					geminiError.Error.Code,
					geminiError.Error.Message,
					geminiError.Error.Status)
			} else {
				logger.Errorf(ctx, "Gemini API 返回错误: 代码=%d, 消息=%s, 状态=%s",
					geminiError.Error.Code,
					geminiError.Error.Message,
					geminiError.Error.Status)
			}

			if len(geminiError.Error.Details) > 0 {
				detailsJson, _ := json.Marshal(geminiError.Error.Details)
				if meta.ChannelType == common.ChannelTypeVertexAI {
					logger.Errorf(ctx, "🚨 [VertexAI Debug] VertexAI错误详情: %s", string(detailsJson))
				} else {
					logger.Errorf(ctx, "错误详情: %s", string(detailsJson))
				}
			}

			// Use the existing ErrorWrapper function to handle the error
			var errorMsg error
			if meta.ChannelType == common.ChannelTypeVertexAI {
				errorMsg = fmt.Errorf("VertexAI API 错误: %s (状态: %s)",
					geminiError.Error.Message,
					geminiError.Error.Status)
			} else {
				errorMsg = fmt.Errorf("Gemini API 错误: %s (状态: %s)",
					geminiError.Error.Message,
					geminiError.Error.Status)
			}

			errorCode := "gemini_" + strings.ToLower(geminiError.Error.Status)
			if meta.ChannelType == common.ChannelTypeVertexAI {
				errorCode = "vertex_ai_" + strings.ToLower(geminiError.Error.Status)
			}
			statusCode := geminiError.Error.Code
			if statusCode == 0 {
				statusCode = http.StatusBadRequest
			}

			return openai.ErrorWrapper(errorMsg, errorCode, statusCode)
		}

		var geminiResponse struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						InlineData *gemini.InlineData `json:"inlineData,omitempty"`
						Text       string             `json:"text,omitempty"`
					} `json:"parts,omitempty"`
					Role string `json:"role,omitempty"`
				} `json:"content,omitempty"`
				FinishReason string `json:"finishReason,omitempty"`
				Index        int    `json:"index,omitempty"`
			} `json:"candidates,omitempty"`
			ModelVersion  string `json:"modelVersion,omitempty"`
			UsageMetadata struct {
				PromptTokenCount     int `json:"promptTokenCount,omitempty"`
				CandidatesTokenCount int `json:"candidatesTokenCount,omitempty"`
				TotalTokenCount      int `json:"totalTokenCount,omitempty"`
			} `json:"usageMetadata,omitempty"`
		}

		err = json.Unmarshal(responseBody, &geminiResponse)
		if err != nil {
			logger.Errorf(ctx, "解析 Gemini 成功响应失败: %s", err.Error())
			return openai.ErrorWrapper(err, "unmarshal_gemini_response_failed", http.StatusInternalServerError)
		}

		// 保存 Gemini token 信息到全局变量，供 defer 函数使用
		if meta.ChannelType == common.ChannelTypeVertexAI {
			logger.Infof(ctx, "📊 [VertexAI Debug] 准备保存 VertexAI token 信息")
			logger.Infof(ctx, "📊 [VertexAI Debug] 原始 UsageMetadata: PromptTokenCount=%d, CandidatesTokenCount=%d, TotalTokenCount=%d",
				geminiResponse.UsageMetadata.PromptTokenCount,
				geminiResponse.UsageMetadata.CandidatesTokenCount,
				geminiResponse.UsageMetadata.TotalTokenCount)
		} else {
			logger.Infof(ctx, "准备保存 Gemini token 信息")
			logger.Infof(ctx, "原始 UsageMetadata: PromptTokenCount=%d, CandidatesTokenCount=%d, TotalTokenCount=%d",
				geminiResponse.UsageMetadata.PromptTokenCount,
				geminiResponse.UsageMetadata.CandidatesTokenCount,
				geminiResponse.UsageMetadata.TotalTokenCount)
		}

		geminiPromptTokens = geminiResponse.UsageMetadata.PromptTokenCount
		geminiCompletionTokens = geminiResponse.UsageMetadata.CandidatesTokenCount

		if meta.ChannelType == common.ChannelTypeVertexAI {
			logger.Infof(ctx, "📊 [VertexAI Debug] 已保存 VertexAI token 信息: geminiPromptTokens=%d, geminiCompletionTokens=%d",
				geminiPromptTokens, geminiCompletionTokens)
			logger.Infof(ctx, "📊 [VertexAI Debug] VertexAI JSON token usage: prompt=%d, completion=%d, total=%d",
				geminiPromptTokens, geminiCompletionTokens, geminiResponse.UsageMetadata.TotalTokenCount)
		} else {
			logger.Infof(ctx, "已保存 Gemini token 信息: geminiPromptTokens=%d, geminiCompletionTokens=%d",
				geminiPromptTokens, geminiCompletionTokens)
			logger.Infof(ctx, "Gemini JSON token usage: prompt=%d, completion=%d, total=%d",
				geminiPromptTokens, geminiCompletionTokens, geminiResponse.UsageMetadata.TotalTokenCount)
		}

		// Check if any candidate has a finish reason that isn't STOP
		for _, candidate := range geminiResponse.Candidates {
			if candidate.FinishReason != "" && candidate.FinishReason != "STOP" {
				logger.Errorf(ctx, "Gemini API 返回非正常完成原因: %s", candidate.FinishReason)
				errorMsg := fmt.Errorf("Gemini API 错误: 生成未正常完成 (原因: %s)", candidate.FinishReason)
				errorCode := "gemini_incomplete_generation"
				return openai.ErrorWrapper(errorMsg, errorCode, http.StatusBadRequest)
			}
		}

		// Convert to OpenAI DALL-E 3 format
		var imageData []struct {
			B64Json string `json:"b64_json"`
		}

		// Extract image data from Gemini response
		for i, candidate := range geminiResponse.Candidates {
			for j, part := range candidate.Content.Parts {
				if part.InlineData != nil {
					// Use the base64 data in b64_json field (OpenAI standard)
					imageData = append(imageData, struct {
						B64Json string `json:"b64_json"`
					}{
						B64Json: part.InlineData.Data,
					})
				} else if part.Text != "" {
					logger.Infof(ctx, "候选项 #%d 部分 #%d 包含文本: %s", i, j, part.Text)
				}
			}
		}

		// Create OpenAI compatible response data with b64_json
		var openaiCompatibleData []struct {
			Url     string `json:"url,omitempty"`
			B64Json string `json:"b64_json,omitempty"`
		}
		for _, img := range imageData {
			openaiCompatibleData = append(openaiCompatibleData, struct {
				Url     string `json:"url,omitempty"`
				B64Json string `json:"b64_json,omitempty"`
			}{
				B64Json: img.B64Json,
			})
		}

		// 为 Gemini JSON 请求构建包含 usage 信息的响应
		type GeminiImageResponse struct {
			Created int `json:"created"`
			Data    []struct {
				Url     string `json:"url,omitempty"`
				B64Json string `json:"b64_json,omitempty"`
			} `json:"data"`
			Usage struct {
				TotalTokens        int `json:"total_tokens"`
				InputTokens        int `json:"input_tokens"`
				OutputTokens       int `json:"output_tokens"`
				InputTokensDetails struct {
					TextTokens  int `json:"text_tokens"`
					ImageTokens int `json:"image_tokens"`
				} `json:"input_tokens_details"`
			} `json:"usage,omitempty"`
		}

		imageResponseWithUsage := GeminiImageResponse{
			Created: int(time.Now().Unix()),
			Data:    openaiCompatibleData,
			Usage: struct {
				TotalTokens        int `json:"total_tokens"`
				InputTokens        int `json:"input_tokens"`
				OutputTokens       int `json:"output_tokens"`
				InputTokensDetails struct {
					TextTokens  int `json:"text_tokens"`
					ImageTokens int `json:"image_tokens"`
				} `json:"input_tokens_details"`
			}{
				TotalTokens:  geminiResponse.UsageMetadata.TotalTokenCount,
				InputTokens:  geminiResponse.UsageMetadata.PromptTokenCount,
				OutputTokens: geminiResponse.UsageMetadata.CandidatesTokenCount,
				InputTokensDetails: struct {
					TextTokens  int `json:"text_tokens"`
					ImageTokens int `json:"image_tokens"`
				}{
					// Gemini 不提供详细的 token 分解，设为 0
					TextTokens:  0,
					ImageTokens: 0,
				},
			},
		}

		// Re-marshal to the OpenAI format with usage information
		responseBody, err = json.Marshal(imageResponseWithUsage)
		if err != nil {
			logger.Errorf(ctx, "序列化转换后的响应失败: %s", err.Error())
			return openai.ErrorWrapper(err, "marshal_converted_response_failed", http.StatusInternalServerError)
		}

		// 记录 usage 信息
		logger.Infof(ctx, "Gemini JSON 响应包含 usage 信息: total_tokens=%d, input_tokens=%d, output_tokens=%d, text_tokens=%d, image_tokens=%d",
			imageResponseWithUsage.Usage.TotalTokens,
			imageResponseWithUsage.Usage.InputTokens,
			imageResponseWithUsage.Usage.OutputTokens,
			0, // Gemini 不提供详细分解
			0) // Gemini 不提供详细分解

		// 对于 Gemini JSON 请求，在这里直接处理配额消费和日志记录
		err = handleGeminiTokenConsumption(c, ctx, meta, imageRequest, &geminiResponse, quota, startTime)
		if err != nil {
			logger.Warnf(ctx, "Gemini token consumption failed: %v", err)
		}
	} else if meta.ChannelType == 27 {
		// Handle channel type 27 response format conversion
		var channelResponse struct {
			ID   string `json:"id"`
			Data struct {
				ImageURLs []string `json:"image_urls"`
			} `json:"data"`
			Metadata struct {
				FailedCount  string `json:"failed_count"`
				SuccessCount string `json:"success_count"`
			} `json:"metadata"`
			BaseResp struct {
				StatusCode int    `json:"status_code"`
				StatusMsg  string `json:"status_msg"`
			} `json:"base_resp"`
		}

		err = json.Unmarshal(responseBody, &channelResponse)
		if err != nil {
			return openai.ErrorWrapper(err, "unmarshal_channel_response_failed", http.StatusInternalServerError)
		}

		// Convert to OpenAI DALL-E 3 format
		if channelResponse.BaseResp.StatusCode == 0 {
			var openaiImages []struct {
				Url string `json:"url"`
			}

			for _, url := range channelResponse.Data.ImageURLs {
				openaiImages = append(openaiImages, struct {
					Url string `json:"url"`
				}{
					Url: url,
				})
			}
			// Create a compatible slice with the correct struct type
			var compatibleImages []struct {
				Url string `json:"url"`
			}
			for _, img := range openaiImages {
				compatibleImages = append(compatibleImages, struct {
					Url string `json:"url"`
				}{
					Url: img.Url,
				})
			}

			// Create a compatible slice with the correct struct type for OpenAI response
			var openaiCompatibleImages []struct {
				Url     string `json:"url,omitempty"`
				B64Json string `json:"b64_json,omitempty"`
			}
			for _, img := range compatibleImages {
				openaiCompatibleImages = append(openaiCompatibleImages, struct {
					Url     string `json:"url,omitempty"`
					B64Json string `json:"b64_json,omitempty"`
				}{
					Url: img.Url,
				})
			}

			imageResponse = openai.ImageResponse{
				Created: int(time.Now().Unix()),
				Data:    openaiCompatibleImages,
			}

			// Re-marshal to the OpenAI format
			responseBody, err = json.Marshal(imageResponse)
			if err != nil {
				return openai.ErrorWrapper(err, "marshal_converted_response_failed", http.StatusInternalServerError)
			}
		} else {
			// If there's an error in the original response, keep it as is
			return openai.ErrorWrapper(
				fmt.Errorf("channel error: %s (code: %d)",
					channelResponse.BaseResp.StatusMsg,
					channelResponse.BaseResp.StatusCode),
				"channel_error",
				http.StatusInternalServerError,
			)
		}
	} else {
		// For other channel types, unmarshal as usual
		err = json.Unmarshal(responseBody, &imageResponse)
		if err != nil {
			return openai.ErrorWrapper(err, "unmarshal_response_body_failed", http.StatusInternalServerError)
		}
	}

	// 设置响应头，排除Content-Length（我们稍后会设置正确的值）
	for k, v := range resp.Header {
		// 跳过Content-Length，避免与我们重新计算的值冲突
		if strings.ToLower(k) != "content-length" {
			c.Writer.Header().Set(k, v[0])
		}
	}

	// 设置正确的 Content-Length（基于可能已转换的responseBody）
	c.Writer.Header().Set("Content-Length", strconv.Itoa(len(responseBody)))

	// 设置状态码 - 使用原始响应的状态码
	c.Writer.WriteHeader(resp.StatusCode)

	// 写入响应体
	_, err = c.Writer.Write(responseBody)
	if err != nil {
		return openai.ErrorWrapper(err, "write_response_body_failed", http.StatusInternalServerError)
	}

	// 检查函数结束时的上下文状态
	if channelHistoryInterface, exists := c.Get("admin_channel_history"); exists {
		logger.Infof(ctx, "RelayImageHelper: EXIT SUCCESS - admin_channel_history exists: %v", channelHistoryInterface)
	} else {
		logger.Warnf(ctx, "RelayImageHelper: EXIT SUCCESS - admin_channel_history NOT found (this is the problem!)")
	}

	logger.Infof(ctx, "RelayImageHelper EXIT SUCCESS: returning nil")
	return nil
}

// 计算最大公约数
func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// 添加辅助函数用于转义引号 (在文件末尾添加)
func escapeQuotes(s string) string {
	return strings.Replace(s, `"`, `\"`, -1)
}

func DoImageRequest(c *gin.Context, modelName string) *relaymodel.ErrorWithStatusCode {
	ctx := c.Request.Context()
	meta := util.GetRelayMeta(c)
	if strings.HasPrefix(modelName, "kling") {
		return handleKlingImageRequest(c, ctx, modelName, meta)
	} else if strings.HasPrefix(modelName, "flux") {
		return handleFluxImageRequest(c, ctx, modelName, meta)
	}
	// 需要添加处理其他模型类型的逻辑
	return openai.ErrorWrapper(fmt.Errorf("unsupported model: %s", modelName), "unsupported_model", http.StatusBadRequest)
}

func handleFluxImageRequest(c *gin.Context, ctx context.Context, modelName string, meta *util.RelayMeta) *relaymodel.ErrorWithStatusCode {
	baseUrl := meta.BaseURL
	// 直接使用模型名称构建URL

	fullRequestUrl := ""
	if meta.ChannelType == 46 { //flux
		fullRequestUrl = fmt.Sprintf("%s/v1/%s", baseUrl, modelName)
	} else {
		fullRequestUrl = fmt.Sprintf("%s/flux/v1/%s", baseUrl, modelName)
	}

	// Read the original request body
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_request_body_failed", http.StatusBadRequest)
	}

	// Restore the request body for further use
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	// Parse the request body
	var requestMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &requestMap); err != nil {
		return openai.ErrorWrapper(err, "unmarshal_request_body_failed", http.StatusBadRequest)
	}

	// Determine the mode based on whether an image_prompt parameter exists
	mode := "texttoimage"
	if _, hasImagePrompt := requestMap["image_prompt"]; hasImagePrompt {
		mode = "imagetoimage"
	}

	logger.Debugf(ctx, "Flux API request mode: %s, model: %s", mode, modelName)

	// Remove the 'model' parameter as Flux API doesn't need it
	delete(requestMap, "model")

	// Re-marshal the modified request
	modifiedBody, err := json.Marshal(requestMap)
	if err != nil {
		return openai.ErrorWrapper(err, "marshal_modified_request_failed", http.StatusInternalServerError)
	}

	// Create a new request with the modified body
	req, err := http.NewRequest(c.Request.Method, fullRequestUrl, bytes.NewBuffer(modifiedBody))
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_failed", http.StatusInternalServerError)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	if meta.ChannelType == 46 {
		req.Header.Set("x-key", meta.APIKey)
	} else {
		req.Header.Set("Authorization", "Bearer "+meta.APIKey)
	}

	// Send the request
	resp, err := util.HTTPClient.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	// Read the response body
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}

	// 在记录日志时使用更安全的方式
	logger.Infof(ctx, "Flux API response status: %d, body: %s", resp.StatusCode, string(responseBody))

	// Check if the request was successful
	if resp.StatusCode != http.StatusOK {
		// Handle error response (status code 422 or others)
		if resp.StatusCode == http.StatusUnprocessableEntity {
			// Parse error response format
			var fluxError struct {
				Detail []struct {
					Loc  []string `json:"loc"`
					Msg  string   `json:"msg"`
					Type string   `json:"type"`
				} `json:"detail"`
			}

			if err := json.Unmarshal(responseBody, &fluxError); err == nil && len(fluxError.Detail) > 0 {
				errorMsg := fmt.Sprintf("Flux API validation error: %s", fluxError.Detail[0].Msg)
				return openai.ErrorWrapper(
					fmt.Errorf(errorMsg),
					"flux_validation_error",
					resp.StatusCode,
				)
			}
		}

		return openai.ErrorWrapper(
			fmt.Errorf("Flux API error: status code %d, response: %s", resp.StatusCode, string(responseBody)),
			"flux_api_error",
			resp.StatusCode,
		)
	}

	// Parse the Flux API successful response
	var fluxResponse struct {
		ID         string `json:"id"`
		PollingURL string `json:"polling_url"`
	}

	if err := json.Unmarshal(responseBody, &fluxResponse); err != nil {
		return openai.ErrorWrapper(err, "unmarshal_response_body_failed", http.StatusInternalServerError)
	}

	// 计算配额（在记录日志之前）
	quota := calculateImageQuota(modelName, mode, 1)

	// 记录图像生成日志
	err = CreateImageLog(
		"flux",          // provider
		fluxResponse.ID, // taskId
		meta,            // meta
		"submitted",     // status (Flux API 提交成功后的初始状态)
		"",              // failReason (空，因为请求成功)
		mode,            // mode参数
		1,               // n参数
		quota,           // quota参数

	)
	if err != nil {
		logger.Warnf(ctx, "Failed to create image log: %v", err)
		// 继续处理，不因日志记录失败而中断响应
	}

	// Convert to the format expected by the client
	asyncResponse := relaymodel.GeneralImageResponseAsync{
		TaskId:     fluxResponse.ID,
		Message:    "Request submitted successfully",
		TaskStatus: "succeed", // 请求提交成功
	}

	// Marshal the response
	responseJSON, err := json.Marshal(asyncResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "marshal_response_failed", http.StatusInternalServerError)
	}

	// Set response headers
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.Header().Set("Content-Length", strconv.Itoa(len(responseJSON)))

	// Write the response
	c.Writer.WriteHeader(http.StatusOK)
	_, err = c.Writer.Write(responseJSON)
	if err != nil {
		return openai.ErrorWrapper(err, "write_response_failed", http.StatusInternalServerError)
	}

	// Handle billing based on mode, modelName and number of images (n)
	err = handleSuccessfulResponseImage(c, ctx, meta, modelName, mode, 1)
	if err != nil {
		logger.Warnf(ctx, "Failed to process billing: %v", err)
		// Continue processing, don't interrupt the response due to billing failure
	}

	return nil
}

func handleKlingImageRequest(c *gin.Context, ctx context.Context, modelName string, meta *util.RelayMeta) *relaymodel.ErrorWithStatusCode {
	baseUrl := meta.BaseURL
	var fullRequestUrl string

	if meta.ChannelType == 41 {
		fullRequestUrl = fmt.Sprintf("%s/v1/images/generations", baseUrl)
	} else {
		fullRequestUrl = fmt.Sprintf("%s/kling/v1/images/generations", baseUrl)
	}

	// Read the original request body
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_request_body_failed", http.StatusBadRequest)
	}

	// Restore the request body for further use
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	// Parse the request body
	var requestMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &requestMap); err != nil {
		return openai.ErrorWrapper(err, "unmarshal_request_body_failed", http.StatusBadRequest)
	}

	// Extract the 'n' parameter (number of images) from the request
	n := 1 // Default to 1 if not specified
	if nValue, ok := requestMap["n"]; ok {
		// Try to convert to float64 first (JSON numbers are decoded as float64)
		if nFloat, ok := nValue.(float64); ok {
			n = int(nFloat)
		} else if nInt, ok := nValue.(int); ok {
			// Also try int just in case
			n = nInt
		} else if nString, ok := nValue.(string); ok {
			// Also try string conversion
			if nInt, err := strconv.Atoi(nString); err == nil {
				n = nInt
			}
		}
	}

	// Ensure n is at least 1
	if n < 1 {
		n = 1
	}

	// Determine the mode based on whether an image parameter exists
	mode := "texttoimage"
	if _, hasImage := requestMap["image"]; hasImage {
		mode = "imagetoimage"
	}

	logger.Debugf(ctx, "Kling API request mode: %s, generating %d images", mode, n)

	// Transform 'model' to 'model_name'
	if model, ok := requestMap["model"]; ok {
		requestMap["model_name"] = model
		delete(requestMap, "model")
	}

	// Re-marshal the modified request
	modifiedBody, err := json.Marshal(requestMap)
	if err != nil {
		return openai.ErrorWrapper(err, "marshal_modified_request_failed", http.StatusInternalServerError)
	}

	// Create a new request with the modified body
	req, err := http.NewRequest(c.Request.Method, fullRequestUrl, bytes.NewBuffer(modifiedBody))
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_failed", http.StatusInternalServerError)
	}

	var token string

	if meta.ChannelType == 41 {
		ak := meta.Config.AK
		sk := meta.Config.SK

		// Generate JWT token
		token = EncodeJWTToken(ak, sk)
	} else {
		token = meta.APIKey
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	// Send the request
	resp, err := util.HTTPClient.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	// Read the response body
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}

	// 在记录日志时使用更安全的方式
	logger.Infof(ctx, "Kling API modified request: %s", string(responseBody))
	// Parse the Kling API response
	var klingImageResponse keling.KlingImageResponse

	if err := json.Unmarshal(responseBody, &klingImageResponse); err != nil {
		return openai.ErrorWrapper(err, "unmarshal_response_body_failed", http.StatusInternalServerError)
	}

	// 检查错误时提供更详细的信息
	if klingImageResponse.Code != 0 {
		return openai.ErrorWrapper(
			fmt.Errorf("Kling API error: %s (code: %d, task_id: %s)",
				klingImageResponse.Message,
				klingImageResponse.Code,
				klingImageResponse.Data.TaskID),
			"kling_api_error",
			http.StatusBadRequest,
		)
	}

	// 计算配额（在记录日志之前）
	quota := calculateImageQuota(modelName, mode, n)

	// 记录图像生成日志，传递mode参数
	err = CreateImageLog(
		"kling",                            // provider
		klingImageResponse.Data.TaskID,     // taskId
		meta,                               // meta
		klingImageResponse.Data.TaskStatus, // status
		"",                                 // failReason (空，因为请求成功)
		mode,                               // 新增的mode参数
		n,                                  // 新增的n参数
		quota,                              // 新增的quota参数
	)
	if err != nil {
		logger.Warnf(ctx, "Failed to create image log: %v", err)
		// 继续处理，不因日志记录失败而中断响应
	}

	// Convert to the format expected by the client
	asyncResponse := relaymodel.GeneralImageResponseAsync{
		TaskId:  klingImageResponse.Data.TaskID,
		Message: klingImageResponse.Message,
	}

	// Normalize task status to match the expected format (only "failed" or "succeed")
	switch klingImageResponse.Data.TaskStatus {
	case "failed":
		asyncResponse.TaskStatus = "failed"
	default:
		asyncResponse.TaskStatus = "succeed"
	}

	// Marshal the response
	responseJSON, err := json.Marshal(asyncResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "marshal_response_failed", http.StatusInternalServerError)
	}

	// Set response headers
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.Header().Set("Content-Length", strconv.Itoa(len(responseJSON)))

	// Write the response
	c.Writer.WriteHeader(http.StatusOK)
	_, err = c.Writer.Write(responseJSON)
	if err != nil {
		return openai.ErrorWrapper(err, "write_response_failed", http.StatusInternalServerError)
	}

	// Handle billing based on mode, modelName and number of images (n)
	err = handleSuccessfulResponseImage(c, ctx, meta, modelName, mode, n)
	if err != nil {
		logger.Warnf(ctx, "Failed to process billing: %v", err)
		// Continue processing, don't interrupt the response due to billing failure
	}

	return nil
}

// 更新 CreateImageLog 函数以接受 mode 参数
func CreateImageLog(provider string, taskId string, meta *util.RelayMeta, status string, failReason string, mode string, n int, quota int64) error {
	// 创建新的 Image 实例
	image := &model.Image{
		Username:   model.GetUsernameById(meta.UserId),
		ChannelId:  meta.ChannelId,
		UserId:     meta.UserId,
		Model:      meta.OriginModelName,
		Status:     status,
		FailReason: failReason,
		Provider:   provider,
		CreatedAt:  time.Now().Unix(), // 使用当前时间戳
		TaskId:     taskId,
		Mode:       mode, // 添加 mode 字段
		N:          n,    // 添加 n 字段
		Quota:      quota,
		Detail:     "",
	}

	// 调用 Insert 方法插入记录
	err := image.Insert()
	if err != nil {
		return fmt.Errorf("failed to insert image log: %v", err)
	}

	return nil
}

// calculateImageQuota 计算图像生成的配额
func calculateImageQuota(modelName string, mode string, n int) int64 {
	var modelPrice float64

	// Flux API official pricing - https://bfl.ai/pricing/api
	switch modelName {
	// FLUX Models
	case "flux-kontext-max":
		modelPrice = 0.08
	case "flux-kontext-pro":
		modelPrice = 0.04
	case "flux-pro-1.1-ultra":
		modelPrice = 0.06
	case "flux-pro-1.1":
		modelPrice = 0.04
	case "flux-pro":
		modelPrice = 0.05
	case "flux-dev":
		modelPrice = 0.025
	// FLUX.1 Tools
	case "flux-pro-1.0-fill":
		modelPrice = 0.05
	case "flux-pro-1.0-canny":
		modelPrice = 0.05
	case "flux-pro-1.0-depth":
		modelPrice = 0.05
	// Legacy Kling models (keep existing logic for compatibility)
	default:
		if strings.Contains(modelName, "kling") {
			// Keep original Kling pricing logic
			basePrice := 0.025
			var multiplier float64 = 1.0

			if strings.Contains(modelName, "v1.0") {
				multiplier = 1.0
			} else if strings.Contains(modelName, "v1.5") {
				if mode == "texttoimage" {
					multiplier = 4.0
				} else if mode == "imagetoimage" {
					multiplier = 8.0
				}
			} else if strings.Contains(modelName, "v2") {
				multiplier = 4.0
			} else {
				multiplier = 4.0
			}
			modelPrice = basePrice * multiplier
		} else {
			// Default price for unknown models
			modelPrice = 0.05
		}
	}

	// Calculate quota based on model price and number of images
	quota := int64(modelPrice*500000) * int64(n)
	return quota
}

// Update handleSuccessfulResponseImage to accept mode and n parameters
func handleSuccessfulResponseImage(c *gin.Context, ctx context.Context, meta *util.RelayMeta, modelName string, mode string, n int) error {
	// Calculate quota using the new function
	quota := calculateImageQuota(modelName, mode, n)

	referer := c.Request.Header.Get("HTTP-Referer")
	title := c.Request.Header.Get("X-Title")

	err := model.PostConsumeTokenQuota(meta.TokenId, quota)
	if err != nil {
		logger.SysError("error consuming token remain quota: " + err.Error())
		return err
	}

	err = model.CacheUpdateUserQuota(ctx, meta.UserId)
	if err != nil {
		logger.SysError("error update user quota cache: " + err.Error())
		return err
	}

	if quota != 0 {
		tokenName := c.GetString("token_name")
		xRequestID := c.GetString("X-Request-ID")
		// Include pricing details in log content
		totalCost := float64(quota) / 500000
		logContent := fmt.Sprintf("Mode: %s, Images: %d, Total cost: $%.3f",
			mode, n, totalCost)

		// 获取渠道历史信息并记录日志
		var otherInfo string
		if channelHistoryInterface, exists := c.Get("admin_channel_history"); exists {
			if channelHistory, ok := channelHistoryInterface.([]int); ok && len(channelHistory) > 0 {
				if channelHistoryBytes, err := json.Marshal(channelHistory); err == nil {
					otherInfo = fmt.Sprintf("adminInfo:%s", string(channelHistoryBytes))
				}
			}
		}

		if otherInfo != "" {
			model.RecordConsumeLogWithOtherAndRequestID(ctx, meta.UserId, meta.ChannelId, 0, 0, modelName, tokenName, quota, logContent, 0, title, referer, false, 0.0, otherInfo, xRequestID)
		} else {
			model.RecordConsumeLogWithRequestID(ctx, meta.UserId, meta.ChannelId, 0, 0, modelName, tokenName, quota, logContent, 0, title, referer, false, 0.0, xRequestID)
		}
		model.UpdateUserUsedQuotaAndRequestCount(meta.UserId, quota)
		channelId := c.GetInt("channel_id")
		model.UpdateChannelUsedQuota(channelId, quota)
	}

	return nil
}

func GetImageResult(c *gin.Context, taskId string) *relaymodel.ErrorWithStatusCode {
	image, err := model.GetImageByTaskId(taskId)
	if err != nil {
		return openai.ErrorWrapper(err, "failed to get image", http.StatusInternalServerError)
	}
	channel, err := model.GetChannelById(image.ChannelId, true)
	cfg, _ := channel.LoadConfig()
	if err != nil {
		return openai.ErrorWrapper(err, "failed to get channel", http.StatusInternalServerError)
	}

	var fullRequestUrl string
	var req *http.Request

	switch image.Provider {
	case "kling":
		fullRequestUrl = fmt.Sprintf("%s/kling/v1/images/generations/%s", *channel.BaseURL, taskId)
		req, err = http.NewRequest("GET", fullRequestUrl, nil)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to create request: %v", err),
				"api_error",
				http.StatusInternalServerError,
			)
		}
	case "flux":
		// Flux API 使用 GET 请求查询结果，带查询参数 id
		if channel.Type == 46 {
			fullRequestUrl = fmt.Sprintf("%s/v1/get_result?id=%s", *channel.BaseURL, taskId)
		} else {
			fullRequestUrl = fmt.Sprintf("%s/flux/v1/get_result?id=%s", *channel.BaseURL, taskId)
		}

		req, err = http.NewRequest("GET", fullRequestUrl, nil)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to create request: %v", err),
				"api_error",
				http.StatusInternalServerError,
			)
		}
	default:
		req, err = http.NewRequest("GET", fullRequestUrl, nil)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to create request: %v", err),
				"api_error",
				http.StatusInternalServerError,
			)
		}
	}
	if image.Provider == "kling" && channel.Type == 41 {
		token := EncodeJWTToken(cfg.AK, cfg.SK)

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

	} else if image.Provider == "flux" && channel.Type == 46 {
		req.Header.Set("x-key", channel.Key)
	} else {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+channel.Key)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openai.ErrorWrapper(
			fmt.Errorf("failed to fetch video result: %v", err),
			"api_error",
			http.StatusInternalServerError,
		)
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(
			fmt.Errorf("failed to read response body: %v", err),
			"api_error",
			http.StatusInternalServerError,
		)
	}

	// Log the original response body for debugging
	logger.Infof(c.Request.Context(), "%s image result original response: %s", image.Provider, string(body))

	if resp.StatusCode != http.StatusOK {
		return openai.ErrorWrapper(
			fmt.Errorf("API error: %s", string(body)),
			"api_error",
			resp.StatusCode,
		)
	}

	// Create the final response
	finalResponse := relaymodel.GeneralFinalImageResponseAsync{
		TaskId: taskId,
	}

	switch image.Provider {
	case "kling":
		var klingImageResult keling.KlingImageResult
		if err := json.Unmarshal(body, &klingImageResult); err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to unmarshal response body: %v", err),
				"api_error",
				http.StatusInternalServerError,
			)
		}

		if klingImageResult.Code != 0 {
			return openai.ErrorWrapper(
				fmt.Errorf("Kling API error: %s (code: %d)", klingImageResult.Message, klingImageResult.Code),
				"api_error",
				http.StatusInternalServerError,
			)
		}

		finalResponse.Message = klingImageResult.Message

		// 处理任务状态，将 submitted 也处理为 processing
		if klingImageResult.Data.TaskStatus == "submitted" {
			finalResponse.TaskStatus = "processing"
		} else {
			finalResponse.TaskStatus = klingImageResult.Data.TaskStatus
		}

		// Check if there are images in the result and the task is completed
		if klingImageResult.Data.TaskStatus == "succeed" &&
			len(klingImageResult.Data.TaskResult.Images) > 0 {
			// Create an array to store all image URLs
			var imageUrls []string
			for _, image := range klingImageResult.Data.TaskResult.Images {
				if image.URL != "" {
					imageUrls = append(imageUrls, image.URL)
				}
			}

			// Add all image URLs to the response
			finalResponse.ImageUrls = imageUrls
			finalResponse.ImageId = klingImageResult.Data.TaskID
		}

	case "flux":
		// Check for error response first (422 status code)
		if resp.StatusCode == http.StatusUnprocessableEntity {
			var fluxError struct {
				Detail []struct {
					Loc  []string `json:"loc"`
					Msg  string   `json:"msg"`
					Type string   `json:"type"`
				} `json:"detail"`
			}

			if err := json.Unmarshal(body, &fluxError); err == nil && len(fluxError.Detail) > 0 {
				errorMsg := fmt.Sprintf("Flux API validation error: %s", fluxError.Detail[0].Msg)
				return openai.ErrorWrapper(
					fmt.Errorf(errorMsg),
					"flux_validation_error",
					resp.StatusCode,
				)
			}
		}

		var fluxImageResult struct {
			ID       string                 `json:"id"`
			Status   string                 `json:"status"`
			Result   interface{}            `json:"result,omitempty"`
			Progress int                    `json:"progress,omitempty"`
			Details  map[string]interface{} `json:"details,omitempty"`
			Preview  map[string]interface{} `json:"preview,omitempty"`
		}

		if err := json.Unmarshal(body, &fluxImageResult); err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to unmarshal flux response body: %v", err),
				"api_error",
				http.StatusInternalServerError,
			)
		}

		// 处理任务状态映射和消息
		switch fluxImageResult.Status {
		case "Ready":
			finalResponse.TaskStatus = "succeed"
			finalResponse.Message = "Image generation completed"
			// 当任务完成时，result 字段包含图像URL
			if fluxImageResult.Result != nil {
				if resultMap, ok := fluxImageResult.Result.(map[string]interface{}); ok {
					if sample, exists := resultMap["sample"]; exists {
						if sampleStr, ok := sample.(string); ok && sampleStr != "" {
							finalResponse.ImageUrls = []string{sampleStr}
							finalResponse.ImageId = fluxImageResult.ID
						}
					}
				} else if resultStr, ok := fluxImageResult.Result.(string); ok && resultStr != "" {
					// 如果 result 直接是字符串（图像URL）
					finalResponse.ImageUrls = []string{resultStr}
					finalResponse.ImageId = fluxImageResult.ID
				}
			}
		case "Task not found":
			finalResponse.TaskStatus = "failed"
			finalResponse.Message = "Task not found"
		case "Pending":
			finalResponse.TaskStatus = "processing"
			finalResponse.Message = "Task is pending, please check later"
		case "Request Moderated":
			finalResponse.TaskStatus = "failed"
			// 提取请求审核失败的具体原因
			if fluxImageResult.Details != nil {
				if moderationReasons, exists := fluxImageResult.Details["Moderation Reasons"]; exists {
					if reasons, ok := moderationReasons.([]interface{}); ok && len(reasons) > 0 {
						finalResponse.Message = fmt.Sprintf("Request moderated: %v", reasons[0])
					} else {
						finalResponse.Message = "Request moderated"
					}
				} else {
					finalResponse.Message = "Request moderated"
				}
			} else {
				finalResponse.Message = "Request moderated"
			}
		case "Content Moderated":
			finalResponse.TaskStatus = "failed"
			// 提取内容审核失败的具体原因
			if fluxImageResult.Details != nil {
				if moderationReasons, exists := fluxImageResult.Details["Moderation Reasons"]; exists {
					if reasons, ok := moderationReasons.([]interface{}); ok && len(reasons) > 0 {
						finalResponse.Message = fmt.Sprintf("Content moderated: %v", reasons[0])
					} else {
						finalResponse.Message = "Content moderated"
					}
				} else {
					finalResponse.Message = "Content moderated"
				}
			} else {
				finalResponse.Message = "Content moderated"
			}
		case "Error":
			finalResponse.TaskStatus = "failed"
			finalResponse.Message = "Image generation failed"
		default:
			// 其他未知状态
			finalResponse.TaskStatus = "processing"
			finalResponse.Message = fmt.Sprintf("Task processing (status: %s)", fluxImageResult.Status)
		}

	default:
		return openai.ErrorWrapper(
			fmt.Errorf("unsupported provider: %s", image.Provider),
			"unsupported_provider",
			http.StatusBadRequest,
		)
	}

	// Marshal and send the response
	responseJSON, err := json.Marshal(finalResponse)
	if err != nil {
		return openai.ErrorWrapper(
			fmt.Errorf("failed to marshal response: %v", err),
			"api_error",
			http.StatusInternalServerError,
		)
	}

	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(http.StatusOK)
	_, err = c.Writer.Write(responseJSON)
	if err != nil {
		return openai.ErrorWrapper(
			fmt.Errorf("failed to write response: %v", err),
			"api_error",
			http.StatusInternalServerError,
		)
	}

	return nil
}

// handleGeminiFormRequest 处理 Gemini 模型的 form 请求，转换为 JSON 格式
func handleGeminiFormRequest(c *gin.Context, ctx context.Context, imageRequest *relaymodel.ImageRequest, meta *util.RelayMeta, fullRequestURL string) *relaymodel.ErrorWithStatusCode {

	// 记录开始时间用于计算耗时
	startTime := time.Now()

	// 计算配额 - 对于 Gemini 模型需要根据实际 token 使用量计算，这里先用默认值
	var modelPrice float64
	defaultPrice, ok := common.DefaultModelPrice[imageRequest.Model]
	if !ok {
		modelPrice = 0.1
	} else {
		modelPrice = defaultPrice
	}

	groupRatio := common.GetGroupRatio(meta.Group)
	quota := int64(modelPrice*500000*groupRatio) * int64(imageRequest.N)

	// 注意：Gemini Form 请求的实际配额将在响应处理后根据真实 token 使用重新计算

	// 检查用户配额是否足够
	userQuota, err := model.CacheGetUserQuota(ctx, meta.UserId)
	if err != nil {
		return openai.ErrorWrapper(err, "failed to get user quota", http.StatusInternalServerError)
	}

	if userQuota-quota < 0 {
		return openai.ErrorWrapper(errors.New("user quota is not enough"), "insufficient_user_quota", http.StatusForbidden)
	}

	// 从 form 中获取 prompt
	prompt := ""
	if prompts, ok := c.Request.MultipartForm.Value["prompt"]; ok && len(prompts) > 0 {
		prompt = prompts[0]
	}
	if prompt == "" {
		return openai.ErrorWrapper(fmt.Errorf("prompt 字段不能为空"), "missing_prompt", http.StatusBadRequest)
	}

	// 从 form 中获取图片文件（支持多个图片）
	var imageParts []gemini.Part

	// 支持两种字段名格式：image 和 image[]
	var fileHeaders []*multipart.FileHeader
	if headers, ok := c.Request.MultipartForm.File["image"]; ok && len(headers) > 0 {
		fileHeaders = headers
	} else if headers, ok := c.Request.MultipartForm.File["image[]"]; ok && len(headers) > 0 {
		fileHeaders = headers
	}

	if len(fileHeaders) > 0 {
		// 遍历所有图片文件
		for i, fileHeader := range fileHeaders {
			file, err := fileHeader.Open()
			if err != nil {
				return openai.ErrorWrapper(fmt.Errorf("open_image_file_%d_failed: %v", i+1, err), "open_image_file_failed", http.StatusBadRequest)
			}

			// 使用匿名函数和defer确保文件正确关闭
			fileErr := func() error {
				defer func() {
					if closeErr := file.Close(); closeErr != nil {
						logger.Warnf(ctx, "关闭文件 %s 失败: %v", fileHeader.Filename, closeErr)
					}
				}()

				// 读取文件内容
				fileBytes, err := io.ReadAll(file)
				if err != nil {
					return fmt.Errorf("read_image_file_%d_failed: %v", i+1, err)
				}

				// 将文件内容转换为 base64
				imageBase64 := base64.StdEncoding.EncodeToString(fileBytes)

				// 获取 MIME 类型
				mimeType := fileHeader.Header.Get("Content-Type")
				if mimeType == "" || mimeType == "application/octet-stream" {
					// 根据文件扩展名推断 MIME 类型
					ext := strings.ToLower(filepath.Ext(fileHeader.Filename))
					switch ext {
					case ".png":
						mimeType = "image/png"
					case ".jpg", ".jpeg":
						mimeType = "image/jpeg"
					case ".webp":
						mimeType = "image/webp"
					case ".gif":
						mimeType = "image/gif"
					default:
						// 默认为 jpeg
						mimeType = "image/jpeg"
					}
				}

				// 创建图片部分
				imagePart := gemini.Part{
					InlineData: &gemini.InlineData{
						MimeType: mimeType,
						Data:     imageBase64,
					},
				}
				imageParts = append(imageParts, imagePart)
				return nil
			}()

			// 检查是否有处理错误
			if fileErr != nil {
				return openai.ErrorWrapper(fileErr, "read_image_file_failed", http.StatusBadRequest)
			}
		}
	} else {
		return openai.ErrorWrapper(fmt.Errorf("image 或 image[] 文件不能为空"), "missing_image_file", http.StatusBadRequest)
	}

	// 构建 Gemini API 请求格式
	// 按照顺序：先添加所有图片，最后添加文本提示
	var parts []gemini.Part

	// 添加所有图片部分
	parts = append(parts, imageParts...)

	// 最后添加文本提示
	textPart := gemini.Part{
		Text: prompt,
	}
	parts = append(parts, textPart)

	geminiRequest := gemini.ChatRequest{
		Contents: []gemini.ChatContent{
			{
				Role:  "user",
				Parts: parts,
			},
		},
		GenerationConfig: gemini.ChatGenerationConfig{
			ResponseModalities: []string{"TEXT", "IMAGE"},
		},
	}

	// 转换为 JSON
	jsonBytes, err := json.Marshal(geminiRequest)
	if err != nil {
		return openai.ErrorWrapper(err, "marshal_gemini_request_failed", http.StatusInternalServerError)
	}

	// 更新 URL 为 Gemini API（API key 应该在 header 中，不是 URL 参数）
	// 对于 Gemini API，我们应该使用原始模型名称，而不是映射后的名称
	if meta.ChannelType == common.ChannelTypeVertexAI {
		logger.Infof(ctx, "🔧 [VertexAI Debug] Form请求处理 - 开始构建VertexAI URL")
		// 为VertexAI构建URL
		keyIndex := 0
		if meta.KeyIndex != nil {
			keyIndex = *meta.KeyIndex
			logger.Infof(ctx, "🔧 [VertexAI Debug] Form请求 - 使用KeyIndex: %d", keyIndex)
		}

		// 安全检查：确保keyIndex不为负数
		if keyIndex < 0 {
			logger.Errorf(ctx, "🔧 [VertexAI Debug] Form请求 - keyIndex为负数: %d，重置为0", keyIndex)
			keyIndex = 0
		}

		projectID := ""

		// 尝试从Key字段解析项目ID（支持多密钥）
		if meta.IsMultiKey && len(meta.Keys) > keyIndex && keyIndex >= 0 {
			logger.Infof(ctx, "🔧 [VertexAI Debug] Form请求 - 多密钥模式，Keys总数: %d", len(meta.Keys))
			// 多密钥模式：从指定索引的密钥解析
			var credentials vertexai.Credentials
			if err := json.Unmarshal([]byte(meta.Keys[keyIndex]), &credentials); err == nil {
				projectID = credentials.ProjectID
				logger.Infof(ctx, "🔧 [VertexAI Debug] Form请求 - 从多密钥解析ProjectID成功: %s", projectID)
			} else {
				logger.Errorf(ctx, "🔧 [VertexAI Debug] Form请求 - 从多密钥解析ProjectID失败: %v", err)
			}
		} else if meta.ActualAPIKey != "" {
			logger.Infof(ctx, "🔧 [VertexAI Debug] Form请求 - 单密钥模式，ActualAPIKey长度: %d", len(meta.ActualAPIKey))
			// 单密钥模式：从ActualAPIKey解析
			var credentials vertexai.Credentials
			if err := json.Unmarshal([]byte(meta.ActualAPIKey), &credentials); err == nil {
				projectID = credentials.ProjectID
				logger.Infof(ctx, "🔧 [VertexAI Debug] Form请求 - 从ActualAPIKey解析ProjectID成功: %s", projectID)
			} else {
				logger.Errorf(ctx, "🔧 [VertexAI Debug] Form请求 - 从ActualAPIKey解析ProjectID失败: %v", err)
			}
		} else {
			logger.Warnf(ctx, "🔧 [VertexAI Debug] Form请求 - 无法获取密钥信息")
		}

		// 回退：尝试从Config获取项目ID
		if projectID == "" && meta.Config.VertexAIProjectID != "" {
			projectID = meta.Config.VertexAIProjectID
			logger.Infof(ctx, "🔧 [VertexAI Debug] Form请求 - 从Config获取ProjectID: %s", projectID)
		}

		if projectID == "" {
			logger.Errorf(ctx, "🔧 [VertexAI Debug] Form请求 - 无法获取ProjectID")
			return openai.ErrorWrapper(fmt.Errorf("VertexAI project ID not found"), "vertex_ai_project_id_missing", http.StatusBadRequest)
		}

		region := meta.Config.Region
		if region == "" {
			region = "global"
		}
		logger.Infof(ctx, "🔧 [VertexAI Debug] Form请求 - 使用Region: %s, Model: %s", region, meta.OriginModelName)

		// 构建VertexAI API URL - 使用generateContent而不是predict用于图像生成
		if region == "global" {
			fullRequestURL = fmt.Sprintf("https://aiplatform.googleapis.com/v1/projects/%s/locations/global/publishers/google/models/%s:generateContent", projectID, meta.OriginModelName)
		} else {
			fullRequestURL = fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s:generateContent", region, projectID, region, meta.OriginModelName)
		}
		logger.Infof(ctx, "🔧 [VertexAI Debug] Form请求 - 构建的完整URL: %s", fullRequestURL)
	} else {
		// 原有的Gemini官方API URL
		fullRequestURL = fmt.Sprintf("%s/v1beta/models/%s:generateContent", meta.BaseURL, meta.OriginModelName)
	}

	// 创建请求
	req, err := http.NewRequest("POST", fullRequestURL, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return openai.ErrorWrapper(err, "new_request_failed", http.StatusInternalServerError)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	if meta.ChannelType == common.ChannelTypeVertexAI {
		logger.Infof(ctx, "🔐 [VertexAI Debug] Form请求 - 开始VertexAI认证流程")
		// 为VertexAI使用Bearer token认证 - 创建新的adaptor实例（Form请求处理时没有预先创建的adaptor）
		vertexAIAdaptor := &vertexai.Adaptor{}
		vertexAIAdaptor.Init(meta)

		logger.Infof(ctx, "🔐 [VertexAI Debug] Form请求 - 调用GetAccessToken获取访问令牌")
		accessToken, err := vertexai.GetAccessToken(vertexAIAdaptor, meta)
		if err != nil {
			logger.Errorf(ctx, "🔐 [VertexAI Debug] Form请求 - 获取访问令牌失败: %v", err)
			return openai.ErrorWrapper(fmt.Errorf("failed to get VertexAI access token: %v", err), "vertex_ai_auth_failed", http.StatusUnauthorized)
		}

		// 只显示令牌的前10个字符用于调试，避免完整令牌泄露
		tokenPreview := ""
		if len(accessToken) > 10 {
			tokenPreview = accessToken[:10] + "..."
		} else {
			tokenPreview = accessToken
		}
		logger.Infof(ctx, "🔐 [VertexAI Debug] Form请求 - 成功获取访问令牌，长度: %d, 前缀: %s", len(accessToken), tokenPreview)

		req.Header.Set("Authorization", "Bearer "+accessToken)
		logger.Infof(ctx, "🔐 [VertexAI Debug] Form请求 - 已设置Authorization header为Bearer token")
	} else {
		// Gemini API 正确的 header 格式
		req.Header.Set("x-goog-api-key", meta.APIKey)
	}

	// 发送请求
	resp, err := util.HTTPClient.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	// 处理响应
	return handleGeminiResponse(c, ctx, resp, imageRequest, meta, quota, startTime)
}

// handleGeminiResponse 处理 Gemini API 的响应
func handleGeminiResponse(c *gin.Context, ctx context.Context, resp *http.Response, imageRequest *relaymodel.ImageRequest, meta *util.RelayMeta, quota int64, startTime time.Time) *relaymodel.ErrorWithStatusCode {
	// 读取响应体
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}

	// 记录原始响应（省略具体内容，避免 base64 数据占用日志）
	logger.Infof(ctx, "Gemini Form API 响应已接收，状态码: %d", resp.StatusCode)

	// 检查HTTP状态码
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		logger.Errorf(ctx, "Gemini API返回错误状态码: %d, 响应体: %s", resp.StatusCode, string(responseBody))

		// 尝试解析错误响应
		var geminiError struct {
			Error struct {
				Code    int                      `json:"code"`
				Message string                   `json:"message"`
				Status  string                   `json:"status"`
				Details []map[string]interface{} `json:"details,omitempty"`
			} `json:"error"`
		}

		if err := json.Unmarshal(responseBody, &geminiError); err == nil && geminiError.Error.Message != "" {
			// 包含原始响应体，这样重试逻辑可以正确识别 API key 错误
			errorMsg := fmt.Errorf("API请求失败，状态码: %d，响应: %s", resp.StatusCode, string(responseBody))
			errorCode := "gemini_" + strings.ToLower(geminiError.Error.Status)
			statusCode := geminiError.Error.Code
			if statusCode == 0 {
				statusCode = http.StatusBadRequest
			}
			return openai.ErrorWrapper(errorMsg, errorCode, statusCode)
		}

		// 直接使用原始响应体作为错误消息，这样重试逻辑可以正确识别 API key 错误
		return openai.ErrorWrapper(
			fmt.Errorf("API请求失败，状态码: %d，响应: %s", resp.StatusCode, string(responseBody)),
			"gemini_api_error",
			resp.StatusCode,
		)
	}

	// 解析 Gemini 成功响应
	var geminiResponse struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					InlineData *gemini.InlineData `json:"inlineData,omitempty"`
					Text       string             `json:"text,omitempty"`
				} `json:"parts,omitempty"`
				Role string `json:"role,omitempty"`
			} `json:"content,omitempty"`
			FinishReason string `json:"finishReason,omitempty"`
			Index        int    `json:"index,omitempty"`
		} `json:"candidates,omitempty"`
		ModelVersion  string `json:"modelVersion,omitempty"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount,omitempty"`
			CandidatesTokenCount int `json:"candidatesTokenCount,omitempty"`
			TotalTokenCount      int `json:"totalTokenCount,omitempty"`
			PromptTokensDetails  []struct {
				Modality   string `json:"modality"`
				TokenCount int    `json:"tokenCount"`
			} `json:"promptTokensDetails,omitempty"`
			CandidatesTokensDetails []struct {
				Modality   string `json:"modality"`
				TokenCount int    `json:"tokenCount"`
			} `json:"candidatesTokensDetails,omitempty"`
		} `json:"usageMetadata,omitempty"`
	}

	err = json.Unmarshal(responseBody, &geminiResponse)
	if err != nil {
		logger.Errorf(ctx, "解析 Gemini 成功响应失败: %s", err.Error())
		return openai.ErrorWrapper(err, "unmarshal_gemini_response_failed", http.StatusInternalServerError)
	}

	// 检查是否有非正常完成的候选项
	for _, candidate := range geminiResponse.Candidates {
		if candidate.FinishReason != "" && candidate.FinishReason != "STOP" {
			logger.Errorf(ctx, "Gemini API 返回非正常完成原因: %s", candidate.FinishReason)
			errorMsg := fmt.Errorf("Gemini API 错误: 生成未正常完成 (原因: %s)", candidate.FinishReason)
			return openai.ErrorWrapper(errorMsg, "gemini_incomplete_generation", http.StatusBadRequest)
		}
	}

	// 转换为 OpenAI DALL-E 兼容格式
	var imageData []struct {
		B64Json string `json:"b64_json"`
	}

	// 从 Gemini 响应中提取图像数据
	for i, candidate := range geminiResponse.Candidates {
		for j, part := range candidate.Content.Parts {
			if part.InlineData != nil {
				// 使用 b64_json 字段（OpenAI 标准）
				imageData = append(imageData, struct {
					B64Json string `json:"b64_json"`
				}{
					B64Json: part.InlineData.Data,
				})
			} else if part.Text != "" {
				logger.Infof(ctx, "候选项 #%d 部分 #%d 包含文本: %s", i, j, part.Text)
			}
		}
	}

	// 创建兼容 OpenAI 格式的响应数据
	var openaiCompatibleData []struct {
		Url     string `json:"url,omitempty"`
		B64Json string `json:"b64_json,omitempty"`
	}
	for _, img := range imageData {
		openaiCompatibleData = append(openaiCompatibleData, struct {
			Url     string `json:"url,omitempty"`
			B64Json string `json:"b64_json,omitempty"`
		}{
			B64Json: img.B64Json,
		})
	}

	// 构建包含完整 usage 信息的响应结构体
	type ImageResponseWithUsage struct {
		Created int `json:"created"`
		Data    []struct {
			Url     string `json:"url,omitempty"`
			B64Json string `json:"b64_json,omitempty"`
		} `json:"data"`
		Usage struct {
			TotalTokens        int `json:"total_tokens"`
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			InputTokensDetails struct {
				TextTokens  int `json:"text_tokens"`
				ImageTokens int `json:"image_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage,omitempty"`
	}

	// 构建最终响应
	imageResponse := ImageResponseWithUsage{
		Created: int(time.Now().Unix()),
		Data:    openaiCompatibleData,
		Usage: struct {
			TotalTokens        int `json:"total_tokens"`
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			InputTokensDetails struct {
				TextTokens  int `json:"text_tokens"`
				ImageTokens int `json:"image_tokens"`
			} `json:"input_tokens_details"`
		}{
			TotalTokens:  geminiResponse.UsageMetadata.TotalTokenCount,
			InputTokens:  geminiResponse.UsageMetadata.PromptTokenCount,
			OutputTokens: geminiResponse.UsageMetadata.CandidatesTokenCount,
			InputTokensDetails: struct {
				TextTokens  int `json:"text_tokens"`
				ImageTokens int `json:"image_tokens"`
			}{
				// Gemini 不提供详细的 token 分解，设为 0
				TextTokens:  0,
				ImageTokens: 0,
			},
		},
	}

	// 重新序列化为 OpenAI 格式
	finalResponseBody, err := json.Marshal(imageResponse)
	if err != nil {
		logger.Errorf(ctx, "序列化转换后的响应失败: %s", err.Error())
		return openai.ErrorWrapper(err, "marshal_converted_response_failed", http.StatusInternalServerError)
	}

	// 记录 usage 信息
	logger.Infof(ctx, "Gemini Form 响应包含 usage 信息: total_tokens=%d, input_tokens=%d, output_tokens=%d, text_tokens=%d, image_tokens=%d",
		imageResponse.Usage.TotalTokens,
		imageResponse.Usage.InputTokens,
		imageResponse.Usage.OutputTokens,
		0, // Gemini 不提供详细分解
		0) // Gemini 不提供详细分解

	// 设置响应头
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.Header().Set("Content-Length", strconv.Itoa(len(finalResponseBody)))

	// 设置状态码
	c.Writer.WriteHeader(http.StatusOK)

	// 写入响应体
	_, err = c.Writer.Write(finalResponseBody)
	if err != nil {
		return openai.ErrorWrapper(err, "write_response_body_failed", http.StatusInternalServerError)
	}

	// 计算请求耗时
	rowDuration := time.Since(startTime).Seconds()
	duration := math.Round(rowDuration*1000) / 1000

	// 使用 Gemini 实际定价重新计算配额
	groupRatio := common.GetGroupRatio(meta.Group)
	promptTokens := geminiResponse.UsageMetadata.PromptTokenCount
	completionTokens := geminiResponse.UsageMetadata.CandidatesTokenCount
	actualQuota := calculateGeminiQuota(promptTokens, completionTokens, groupRatio)

	logger.Infof(ctx, "Gemini Form 定价计算: 输入=%d tokens, 输出=%d tokens, 分组倍率=%.2f, 计算配额=%d, 耗时=%.3fs",
		promptTokens, completionTokens, groupRatio, actualQuota, duration)

	// 处理配额消费（使用重新计算的配额）
	err = model.PostConsumeTokenQuota(meta.TokenId, actualQuota)
	if err != nil {
		logger.SysError("error consuming token remain quota: " + err.Error())
	}

	err = model.CacheUpdateUserQuota(ctx, meta.UserId)
	if err != nil {
		logger.SysError("error update user quota cache: " + err.Error())
	}

	// 记录消费日志
	referer := c.Request.Header.Get("HTTP-Referer")
	title := c.Request.Header.Get("X-Title")
	tokenName := c.GetString("token_name")
	xRequestID := c.GetString("X-Request-ID")

	// 计算详细的成本信息
	inputCost := float64(promptTokens) / 1000000.0 * 0.3
	outputCost := float64(completionTokens) / 1000000.0 * 30.0
	totalCost := inputCost + outputCost

	logContent := fmt.Sprintf("Gemini Form Request - Model: %s, 输入成本: $%.6f (%d tokens), 输出成本: $%.6f (%d tokens), 总成本: $%.6f, 分组倍率: %.2f, 配额: %d, 耗时: %.3fs",
		meta.OriginModelName, inputCost, promptTokens, outputCost, completionTokens, totalCost, groupRatio, actualQuota, duration)

	// 记录详细的 token 使用情况
	logger.Infof(ctx, "Gemini Form Token Usage - Prompt: %d, Candidates: %d, Total: %d, Duration: %.3fs",
		promptTokens, completionTokens, geminiResponse.UsageMetadata.TotalTokenCount, duration)

	// 获取渠道历史信息并记录日志
	var otherInfo string
	if channelHistoryInterface, exists := c.Get("admin_channel_history"); exists {
		if channelHistory, ok := channelHistoryInterface.([]int); ok && len(channelHistory) > 0 {
			if channelHistoryBytes, err := json.Marshal(channelHistory); err == nil {
				otherInfo = fmt.Sprintf("adminInfo:%s", string(channelHistoryBytes))
			}
		}
	}

	if otherInfo != "" {
		model.RecordConsumeLogWithOtherAndRequestID(ctx, meta.UserId, meta.ChannelId, promptTokens, completionTokens, meta.OriginModelName, tokenName, actualQuota, logContent, duration, title, referer, false, 0.0, otherInfo, xRequestID)
	} else {
		model.RecordConsumeLogWithRequestID(ctx, meta.UserId, meta.ChannelId, promptTokens, completionTokens, meta.OriginModelName, tokenName, actualQuota, logContent, duration, title, referer, false, 0.0, xRequestID)
	}
	model.UpdateUserUsedQuotaAndRequestCount(meta.UserId, actualQuota)
	channelId := c.GetInt("channel_id")
	model.UpdateChannelUsedQuota(channelId, actualQuota)

	return nil
}

// handleGeminiTokenConsumption 处理 Gemini JSON 请求的 token 消费和日志记录
func handleGeminiTokenConsumption(c *gin.Context, ctx context.Context, meta *util.RelayMeta, imageRequest *relaymodel.ImageRequest, geminiResponse interface{}, quota int64, startTime time.Time) error {
	// 计算请求耗时
	rowDuration := time.Since(startTime).Seconds()
	duration := math.Round(rowDuration*1000) / 1000

	// 从 geminiResponse 中提取 token 信息
	var promptTokens, completionTokens int

	// 使用类型断言来获取 UsageMetadata
	if respStruct, ok := geminiResponse.(*struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					InlineData *gemini.InlineData `json:"inlineData,omitempty"`
					Text       string             `json:"text,omitempty"`
				} `json:"parts,omitempty"`
				Role string `json:"role,omitempty"`
			} `json:"content,omitempty"`
			FinishReason string `json:"finishReason,omitempty"`
			Index        int    `json:"index,omitempty"`
		} `json:"candidates,omitempty"`
		ModelVersion  string `json:"modelVersion,omitempty"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount,omitempty"`
			CandidatesTokenCount int `json:"candidatesTokenCount,omitempty"`
			TotalTokenCount      int `json:"totalTokenCount,omitempty"`
		} `json:"usageMetadata,omitempty"`
	}); ok {
		promptTokens = respStruct.UsageMetadata.PromptTokenCount
		completionTokens = respStruct.UsageMetadata.CandidatesTokenCount

		logger.Infof(ctx, "Gemini JSON 直接处理 token: prompt=%d, completion=%d, total=%d",
			promptTokens, completionTokens, respStruct.UsageMetadata.TotalTokenCount)
	} else {
		logger.Warnf(ctx, "Failed to extract token info from Gemini response")
		return fmt.Errorf("failed to extract token info")
	}

	// 使用 Gemini 实际定价重新计算配额
	groupRatio := common.GetGroupRatio(meta.Group)
	actualQuota := calculateGeminiQuota(promptTokens, completionTokens, groupRatio)

	logger.Infof(ctx, "Gemini JSON 定价计算: 输入=%d tokens, 输出=%d tokens, 分组倍率=%.2f, 计算配额=%d, 耗时=%.3fs",
		promptTokens, completionTokens, groupRatio, actualQuota, duration)

	// 处理配额消费（使用重新计算的配额）
	err := model.PostConsumeTokenQuota(meta.TokenId, actualQuota)
	if err != nil {
		logger.SysError("error consuming token remain quota: " + err.Error())
		return err
	}

	err = model.CacheUpdateUserQuota(ctx, meta.UserId)
	if err != nil {
		logger.SysError("error update user quota cache: " + err.Error())
		return err
	}

	// 记录消费日志
	referer := c.Request.Header.Get("HTTP-Referer")
	title := c.Request.Header.Get("X-Title")
	tokenName := c.GetString("token_name")
	xRequestID := c.GetString("X-Request-ID")

	// 计算详细的成本信息
	inputCost := float64(promptTokens) / 1000000.0 * 0.3
	outputCost := float64(completionTokens) / 1000000.0 * 30.0
	totalCost := inputCost + outputCost

	logContent := fmt.Sprintf("Gemini JSON Request - Model: %s, 输入成本: $%.6f (%d tokens), 输出成本: $%.6f (%d tokens), 总成本: $%.6f, 分组倍率: %.2f, 配额: %d, 耗时: %.3fs",
		meta.OriginModelName, inputCost, promptTokens, outputCost, completionTokens, totalCost, groupRatio, actualQuota, duration)

	// 获取渠道历史信息并记录日志
	otherInfo := extractChannelHistoryInfo(ctx, c)

	if otherInfo != "" {
		model.RecordConsumeLogWithOtherAndRequestID(ctx, meta.UserId, meta.ChannelId, promptTokens, completionTokens, meta.OriginModelName, tokenName, actualQuota, logContent, duration, title, referer, false, 0.0, otherInfo, xRequestID)
	} else {
		model.RecordConsumeLogWithRequestID(ctx, meta.UserId, meta.ChannelId, promptTokens, completionTokens, meta.OriginModelName, tokenName, actualQuota, logContent, duration, title, referer, false, 0.0, xRequestID)
	}
	model.UpdateUserUsedQuotaAndRequestCount(meta.UserId, actualQuota)
	channelId := c.GetInt("channel_id")
	model.UpdateChannelUsedQuota(channelId, actualQuota)

	logger.Infof(ctx, "Gemini JSON token consumption completed: prompt=%d, completion=%d, duration=%.3fs", promptTokens, completionTokens, duration)
	return nil
}

// calculateGeminiQuota 根据 Gemini 的实际定价计算配额
// 输入: 1M tokens = $0.3, 输出: 1M tokens = $30
// 平台换算: $1 = 500,000 quota
func calculateGeminiQuota(promptTokens, completionTokens int, groupRatio float64) int64 {
	// Gemini 定价
	const inputPricePerMillion = 0.3   // $0.3 per 1M input tokens
	const outputPricePerMillion = 30.0 // $30 per 1M output tokens
	const quotaPerDollar = 500000.0    // 500,000 quota per $1

	// 计算成本
	inputCost := float64(promptTokens) / 1000000.0 * inputPricePerMillion
	outputCost := float64(completionTokens) / 1000000.0 * outputPricePerMillion
	totalCost := inputCost + outputCost

	// 转换为配额并应用分组倍率
	quota := int64(totalCost * quotaPerDollar * groupRatio)

	return quota
}

// extractChannelHistoryInfo 从gin上下文中提取渠道历史信息
func extractChannelHistoryInfo(ctx context.Context, c *gin.Context) string {
	channelHistoryInterface, exists := c.Get("admin_channel_history")
	if !exists {
		return ""
	}

	channelHistory, ok := channelHistoryInterface.([]int)
	if !ok || len(channelHistory) == 0 {
		logger.Debugf(ctx, "Invalid channel history type or empty: %T", channelHistoryInterface)
		return ""
	}

	channelHistoryBytes, err := json.Marshal(channelHistory)
	if err != nil {
		logger.Warnf(ctx, "Failed to marshal channel history %v: %v", channelHistory, err)
		return ""
	}

	return fmt.Sprintf("adminInfo:%s", string(channelHistoryBytes))
}
