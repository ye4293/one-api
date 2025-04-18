package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/gemini"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/helper"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"

	"github.com/gin-gonic/gin"
)

func RelayImageHelper(c *gin.Context, relayMode int) *relaymodel.ErrorWithStatusCode {
	startTime := time.Now()
	ctx := c.Request.Context()
	meta := util.GetRelayMeta(c)
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

	var requestBody io.Reader
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
		logger.Infof(ctx, "处理 Gemini 图像请求，模型: %s", imageRequest.Model)

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
		bodyBytes, err := io.ReadAll(c.Request.Body)
		if err != nil {
			logger.Errorf(ctx, "读取请求体失败: %s", err.Error())
			return openai.ErrorWrapper(err, "read_request_body_failed", http.StatusBadRequest)
		}

		// 恢复请求体以供后续使用
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		logger.Infof(ctx, "原始请求体: %s", string(bodyBytes))

		var requestMap map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &requestMap); err != nil {
			logger.Errorf(ctx, "解析请求 JSON 失败: %s，请求体: %s", err.Error(), string(bodyBytes))
			return openai.ErrorWrapper(fmt.Errorf("请求中的 JSON 无效: %w", err), "invalid_request_json", http.StatusBadRequest)
		}

		logger.Infof(ctx, "解析后的请求映射: %+v", requestMap)

		if image, ok := requestMap["image"].(string); ok && image != "" {
			logger.Infof(ctx, "检测到图像数据，长度: %d", len(image))

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
				logger.Infof(ctx, "解析图像数据成功，MIME 类型: %s，数据长度: %d", mimeType, len(imageData))
			} else {
				// If no comma found, assume it's just the base64 data
				mimeType = "image/png" // Default to PNG if not specified
				imageData = image
				logger.Infof(ctx, "未找到 MIME 类型分隔符，使用默认 MIME 类型: %s，数据长度: %d", mimeType, len(imageData))
			}

			// Add the image to the Gemini request
			geminiImageRequest.Contents[0].Parts = append(geminiImageRequest.Contents[0].Parts, gemini.Part{
				InlineData: &gemini.InlineData{
					MimeType: mimeType,
					Data:     imageData,
				},
			})
			logger.Infof(ctx, "已将图像添加到 Gemini 请求中")
		} else {
			logger.Infof(ctx, "请求中未包含图像数据")
		}

		// Convert to JSON
		jsonStr, err := json.Marshal(geminiImageRequest)
		if err != nil {
			logger.Errorf(ctx, "序列化 Gemini 请求失败: %s", err.Error())
			return openai.ErrorWrapper(err, "marshal_gemini_request_failed", http.StatusInternalServerError)
		}
		logger.Infof(ctx, "序列化后的 Gemini 请求: %s", string(jsonStr))

		requestBody = bytes.NewBuffer(jsonStr)

		// Update URL for Gemini API
		fullRequestURL = fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash-exp:generateContent?key=%s", meta.APIKey)
		logger.Infof(ctx, "Gemini API 请求 URL: %s", fullRequestURL)
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

	adaptor := helper.GetAdaptor(meta.APIType)
	if adaptor == nil {
		return openai.ErrorWrapper(fmt.Errorf("invalid api type: %d", meta.APIType), "invalid_api_type", http.StatusBadRequest)
	}
	adaptor.Init(meta)
	groupRatio := common.GetGroupRatio(meta.Group)
	userModelTypeRatio := common.GetUserModelTypeRation(meta.Group, imageRequest.Model)
	ratio := groupRatio * userModelTypeRatio
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

	req, err := http.NewRequest(c.Request.Method, fullRequestURL, requestBody)
	if err != nil {
		return openai.ErrorWrapper(err, "new_request_failed", http.StatusInternalServerError)
	}

	token := c.Request.Header.Get("Authorization")
	if meta.ChannelType == common.ChannelTypeAzure {
		token = strings.TrimPrefix(token, "Bearer ")
		req.Header.Set("api-key", token)
	} else if strings.HasPrefix(imageRequest.Model, "gemini") {
		// For Gemini, we're using the API key in the URL, so don't set Authorization header
		logger.Infof(ctx, "Skipping Authorization header for Gemini API request")
	} else {
		req.Header.Set("Authorization", token)
	}

	req.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))
	req.Header.Set("Accept", c.Request.Header.Get("Accept"))

	resp, err := util.HTTPClient.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
	}

	err = req.Body.Close()
	if err != nil {
		return openai.ErrorWrapper(err, "close_request_body_failed", http.StatusInternalServerError)
	}
	err = c.Request.Body.Close()
	if err != nil {
		return openai.ErrorWrapper(err, "close_request_body_failed", http.StatusInternalServerError)
	}
	var imageResponse openai.ImageResponse

	defer func(ctx context.Context) {
		if resp == nil || (resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated) {
			return
		}

		err := model.PostConsumeTokenQuota(meta.TokenId, quota)
		if err != nil {
			logger.SysError("error consuming token remain quota: " + err.Error())
		}
		err = model.CacheUpdateUserQuota(ctx, meta.UserId)
		if err != nil {
			logger.SysError("error update user quota cache: " + err.Error())
		}
		if quota != 0 {
			referer := c.Request.Header.Get("HTTP-Referer")
			title := c.Request.Header.Get("X-Title")
			rowDuration := time.Since(startTime).Seconds()
			duration := math.Round(rowDuration*1000) / 1000
			tokenName := c.GetString("token_name")
			logContent := fmt.Sprintf("模型价格 $%.2f，分组倍率 %.2f 用户模型倍率 %.2f", modelPrice, groupRatio, userModelTypeRatio)
			model.RecordConsumeLog(ctx, meta.UserId, meta.ChannelId, 0, 0, meta.ActualModelName, tokenName, quota, logContent, duration, title, referer)
			model.UpdateUserUsedQuotaAndRequestCount(meta.UserId, quota)
			channelId := c.GetInt("channel_id")
			model.UpdateChannelUsedQuota(channelId, quota)
		}
	}(c.Request.Context())

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}
	err = resp.Body.Close()
	if err != nil {
		return openai.ErrorWrapper(err, "close_response_body_failed", http.StatusInternalServerError)
	}

	// Handle Gemini response format conversion
	if strings.HasPrefix(imageRequest.Model, "gemini") {
		logger.Infof(ctx, "处理 Gemini 响应，状态码: %d", resp.StatusCode)
		logger.Infof(ctx, "原始响应体: %s", string(responseBody))

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
		} else if geminiError.Error.Message != "" {
			logger.Errorf(ctx, "Gemini API 返回错误: 代码=%d, 消息=%s, 状态=%s",
				geminiError.Error.Code,
				geminiError.Error.Message,
				geminiError.Error.Status)

			if len(geminiError.Error.Details) > 0 {
				detailsJson, _ := json.Marshal(geminiError.Error.Details)
				logger.Errorf(ctx, "错误详情: %s", string(detailsJson))
			}

			// Use the existing ErrorWrapper function to handle the error
			errorMsg := fmt.Errorf("Gemini API 错误: %s (状态: %s)",
				geminiError.Error.Message,
				geminiError.Error.Status)

			errorCode := "gemini_" + strings.ToLower(geminiError.Error.Status)
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
				PromptTokenCount int `json:"promptTokenCount,omitempty"`
				TotalTokenCount  int `json:"totalTokenCount,omitempty"`
			} `json:"usageMetadata,omitempty"`
		}

		err = json.Unmarshal(responseBody, &geminiResponse)
		if err != nil {
			logger.Errorf(ctx, "解析 Gemini 成功响应失败: %s", err.Error())
			return openai.ErrorWrapper(err, "unmarshal_gemini_response_failed", http.StatusInternalServerError)
		}

		logger.Infof(ctx, "成功解析 Gemini 响应，候选项数量: %d", len(geminiResponse.Candidates))

		// Convert to OpenAI DALL-E 3 format
		var imageData []struct {
			Url string `json:"url"`
		}

		// Extract image data from Gemini response
		for i, candidate := range geminiResponse.Candidates {
			logger.Infof(ctx, "处理候选项 #%d, 部分数量: %d", i, len(candidate.Content.Parts))
			for j, part := range candidate.Content.Parts {
				if part.InlineData != nil {
					logger.Infof(ctx, "候选项 #%d 部分 #%d 包含内联数据，MIME 类型: %s, 数据长度: %d",
						i, j, part.InlineData.MimeType, len(part.InlineData.Data))
					// Use the base64 data as the URL (for DALL-E compatibility)
					imageData = append(imageData, struct {
						Url string `json:"url"`
					}{
						Url: "data:" + part.InlineData.MimeType + ";base64," + part.InlineData.Data,
					})
				} else if part.Text != "" {
					logger.Infof(ctx, "候选项 #%d 部分 #%d 包含文本: %s", i, j, part.Text)
				}
			}
		}

		// Use the existing OpenAI ImageResponse struct
		imageResponse = openai.ImageResponse{
			Created: int(time.Now().Unix()),
			Data:    imageData,
		}

		logger.Infof(ctx, "转换后的图像数量: %d", len(imageData))

		// Re-marshal to the OpenAI format
		responseBody, err = json.Marshal(imageResponse)
		if err != nil {
			logger.Errorf(ctx, "序列化转换后的响应失败: %s", err.Error())
			return openai.ErrorWrapper(err, "marshal_converted_response_failed", http.StatusInternalServerError)
		}

		logger.Infof(ctx, "转换后的响应体: %s", string(responseBody))
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

			imageResponse = openai.ImageResponse{
				Created: int(time.Now().Unix()),
				Data:    openaiImages,
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

	// 设置响应头
	for k, v := range resp.Header {
		c.Writer.Header().Set(k, v[0])
	}

	// 设置新的 Content-Length
	c.Writer.Header().Set("Content-Length", strconv.Itoa(len(responseBody)))

	// 设置状态码
	c.Writer.WriteHeader(http.StatusOK)

	// 写入响应体
	_, err = c.Writer.Write(responseBody)
	if err != nil {
		return openai.ErrorWrapper(err, "write_response_body_failed", http.StatusInternalServerError)
	}

	return nil
}

// 计算最大公约数
func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
