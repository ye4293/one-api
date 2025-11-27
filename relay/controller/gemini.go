package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/gemini"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/helper"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

// RelayGeminiNative 处理 Gemini 原生 API 请求
func RelayGeminiNative(c *gin.Context) *model.ErrorWithStatusCode {
	ctx := c.Request.Context()
	startTime := time.Now()

	// 获取基本信息
	tokenId := c.GetInt("token_id")
	userId := c.GetInt("id")
	group := c.GetString("group")
	channelId := c.GetInt("channel_id")
	modelName := c.GetString("original_model")

	//获取原生requestbody
	originRequestBody, err := common.GetRequestBody(c)
	logger.Infof(ctx, "originRequestBody: %s", string(originRequestBody))
	if err != nil {
		return openai.ErrorWrapper(err, "failed_to_get_request_body", http.StatusInternalServerError)
	}

	// 获取请求body
	geminiRequest := gemini.ChatRequest{}
	err = c.ShouldBindJSON(&geminiRequest)
	if err != nil {
		return openai.ErrorWrapper(err, "failed_to_bind_request", http.StatusBadRequest)
	}
	// logger.Infof(ctx, "geminiRequest: %+v", geminiRequest)

	// requestBody, err := json.Marshal(geminiRequest)
	// if err != nil {
	// 	return openai.ErrorWrapper(err, "failed_to_marshal_request", http.StatusInternalServerError)
	// }
	meta := util.GetRelayMeta(c)
    logger.Infof(ctx, "meta: %+v", meta.APIKey)
	adaptor := helper.GetAdaptor(meta.APIType)
	if adaptor == nil {
		return openai.ErrorWrapper(fmt.Errorf("invalid api type: %d", meta.APIType), "invalid_api_type", http.StatusBadRequest)
	}

	// 计算预消费配额
	groupRatio := common.GetGroupRatio(group)
	modelRatio := common.GetModelRatio(modelName)
	ratio := modelRatio * groupRatio

	// 简单估算：每次请求预扣费
	preConsumedQuota, promptTokens, err := CalculateGeminiQuotaFromRequest(originRequestBody, modelName, ratio)
	if err != nil {
		return openai.ErrorWrapper(err, "failed_to_calculate_pre_consumed_quota", http.StatusInternalServerError)
	}

	userQuota, err := dbmodel.CacheGetUserQuota(ctx, userId)
	if err != nil {
		return openai.ErrorWrapper(err, "failed_to_get_user_quota", http.StatusInternalServerError)
	}

	if userQuota < preConsumedQuota {
		return openai.ErrorWrapper(fmt.Errorf("insufficient quota"), "insufficient_quota", http.StatusForbidden)
	}
	meta.PreConsumedQuota = preConsumedQuota
	meta.PromptTokens = promptTokens
	//先写死透传

	adaptor.Init(meta)
	resp, err := adaptor.DoRequest(c, meta, bytes.NewBuffer(originRequestBody))
	if err != nil {
		return openai.ErrorWrapper(err, "failed_to_send_request", http.StatusBadGateway)
	}

	var usage *model.Usage
	var openaiErr *model.ErrorWithStatusCode

	if meta.IsStream {
		usage, openaiErr = doNativeGeminiStreamResponse(c, resp, meta)
	} else {
		usage, openaiErr = doNativeGeminiResponse(c, resp, meta)
	}

	if openaiErr != nil {
		return openaiErr
	}

	// 使用从响应中解析的 usage 计算实际配额
	actualQuota := int64(0)
	if usage != nil && usage.TotalTokens > 0 {
		actualQuota = int64(float64(usage.TotalTokens) * ratio / 1000 * config.QuotaPerUnit)
	} else {
		// 如果无法从响应获取usage，使用预消费配额
		actualQuota = preConsumedQuota
	}

	logger.Infof(ctx, "Gemini actual quota: %d, total tokens: %d", actualQuota, usage.TotalTokens)
	// 记录消费日志
	duration := time.Since(startTime).Seconds()
	tokenName := c.GetString("token_name")

	go recordGeminiConsumption(ctx, userId, channelId, tokenId, modelName, tokenName, actualQuota, c.Request.RequestURI, duration, c)
	return nil
}

// recordGeminiConsumption 记录 Gemini 消费日志
func recordGeminiConsumption(ctx context.Context, userId, channelId, tokenId int, modelName, tokenName string, quota int64, requestPath string, duration float64, c *gin.Context) {
	err := dbmodel.PostConsumeTokenQuota(tokenId, quota)
	if err != nil {
		logger.SysError("error consuming token remain quota: " + err.Error())
	}

	err = dbmodel.CacheUpdateUserQuota(ctx, userId)
	if err != nil {
		logger.SysError("error update user quota cache: " + err.Error())
	}

	dbmodel.UpdateUserUsedQuotaAndRequestCount(userId, quota)
	dbmodel.UpdateChannelUsedQuota(channelId, quota)

	// 记录日志
	logContent := fmt.Sprintf("Gemini API %s", requestPath)
	referer := c.Request.Header.Get("HTTP-Referer")
	title := c.Request.Header.Get("X-Title")

	dbmodel.RecordConsumeLog(ctx, userId, channelId, 0, 0, modelName,
		tokenName, quota, logContent, duration, title, referer)
}
func CalculateGeminiQuotaFromRequest(requestBody []byte, modelName string, ratio float64) (int64, int, error) {
	var geminiReq gemini.ChatRequest
	if err := json.Unmarshal(requestBody, &geminiReq); err != nil {
		return 0, 0, fmt.Errorf("failed to parse gemini request: %w", err)
	}

	// 估算输入 tokens
	estimatedTokens := 0

	// 计算内容的 tokens
	for _, content := range geminiReq.Contents {
		for _, part := range content.Parts {
			if part.Text != "" {
				estimatedTokens += countTextTokens(part.Text)
			}
			// 图片大约 258 tokens (Gemini 固定值)
			if part.InlineData != nil && strings.HasPrefix(part.InlineData.MimeType, "image") {
				estimatedTokens += 258
			}
			// 视频按文件大小估算（这里简化处理）
			if part.InlineData != nil && strings.HasPrefix(part.InlineData.MimeType, "video") {
				estimatedTokens += 1000 // 视频大约1000+ tokens
			}
			// 音频文件
			if part.InlineData != nil && strings.HasPrefix(part.InlineData.MimeType, "audio") {
				estimatedTokens += 500
			}
			// PDF 文件
			if part.InlineData != nil && part.InlineData.MimeType == "application/pdf" {
				estimatedTokens += 500 // PDF 按页数估算，这里简化处理
			}
		}
	}

	// 估算输出 tokens (根据 maxOutputTokens)
	estimatedOutputTokens := 1000 // 默认估算
	if geminiReq.GenerationConfig.MaxOutputTokens > 0 {
		estimatedOutputTokens = geminiReq.GenerationConfig.MaxOutputTokens
	}

	// 总 tokens = 输入 + 预估输出
	totalEstimatedTokens := estimatedTokens + estimatedOutputTokens

	// 计算配额 (tokens * ratio / 1000 * QuotaPerUnit)
	// 预消费按 30% 估算，避免预扣太多
	quota := int64(float64(totalEstimatedTokens) * ratio * 0.3 / 1000 * config.QuotaPerUnit)

	return quota, estimatedTokens, nil
}

// countTextTokens 简单的 token 计数（粗略估算）
// 中文: 1个字 ≈ 1.5 tokens
// 英文: 1个词 ≈ 1.3 tokens
// 这里简化为: 每4个字符 ≈ 1 token
func countTextTokens(text string) int {
	if text == "" {
		return 0
	}
	// 粗略估算：每4个字符约等于1个token
	return len([]rune(text))/4 + 1
}

// 辅助函数：从 usage 计算配额
func calculateQuotaFromUsage(usage *model.Usage, ratio float64) int64 {
	if usage == nil || usage.TotalTokens == 0 {
		return 0
	}
	return int64(float64(usage.TotalTokens) * ratio / 1000 * config.QuotaPerUnit)
}

// doNativeGeminiResponse 处理 Gemini 非流式响应
func doNativeGeminiResponse(c *gin.Context, resp *http.Response, meta *util.RelayMeta) (usage *model.Usage, err *model.ErrorWithStatusCode) {
	defer resp.Body.Close()

	// 读取响应体
	responseBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, openai.ErrorWrapper(readErr, "read_response_body_failed", http.StatusInternalServerError)
	}

	// 检查响应状态码
	if resp.StatusCode != http.StatusOK {
		return nil, &model.ErrorWithStatusCode{
			Error: model.Error{
				Message: string(responseBody),
				Type:    "gemini_api_error",
				Code:    fmt.Sprintf("status_%d", resp.StatusCode),
			},
			StatusCode: resp.StatusCode,
		}
	}

	// 解析 Gemini 原生响应
	var geminiResponse gemini.ChatResponse
	if unmarshalErr := json.Unmarshal(responseBody, &geminiResponse); unmarshalErr != nil {
		return nil, openai.ErrorWrapper(unmarshalErr, "unmarshal_response_failed", http.StatusInternalServerError)
	}

	// 提取 usage 信息
	usage = &model.Usage{
		PromptTokens:     meta.PromptTokens,
		CompletionTokens: 0,
		TotalTokens:      meta.PromptTokens,
	}

	// 如果响应中有 usageMetadata，使用真实数据
	// Gemini 原生响应可能在不同位置包含 usage 信息
	// 这里需要手动解析 JSON 来获取
	var rawResponse map[string]interface{}
	if json.Unmarshal(responseBody, &rawResponse) == nil {
		if usageMetadata, ok := rawResponse["usageMetadata"].(map[string]interface{}); ok {  //输入token
			if promptTokens, ok := usageMetadata["promptTokenCount"].(float64); ok {
				usage.PromptTokens = int(promptTokens)
			}
			if candidatesTokens, ok := usageMetadata["candidatesTokenCount"].(float64); ok {  //补全token
				usage.CompletionTokens = int(candidatesTokens)
			}
			if totalTokens, ok := usageMetadata["totalTokenCount"].(float64); ok {
				usage.TotalTokens = int(totalTokens)
			}
		}
	}

	// 如果没有获取到 completion tokens，根据响应文本估算
	if usage.CompletionTokens == 0 {
		responseText := geminiResponse.GetResponseText()
		if responseText != "" {
			usage.CompletionTokens = countTextTokens(responseText)
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		}
	}

	// 直接返回原生 Gemini 响应格式
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, writeErr := c.Writer.Write(responseBody)
	if writeErr != nil {
		return nil, openai.ErrorWrapper(writeErr, "write_response_failed", http.StatusInternalServerError)
	}

	return usage, nil
}

// doNativeGeminiStreamResponse 处理 Gemini 流式响应
func doNativeGeminiStreamResponse(c *gin.Context, resp *http.Response, meta *util.RelayMeta) (usage *model.Usage, err *model.ErrorWithStatusCode) {
	defer resp.Body.Close()

	// 设置 SSE 响应头
	common.SetEventStreamHeaders(c)

	usage = &model.Usage{
		PromptTokens:     meta.PromptTokens,
		CompletionTokens: 0,
		TotalTokens:      meta.PromptTokens,
	}

	responseText := strings.Builder{}
	scanner := io.Reader(resp.Body)
	buffer := make([]byte, 4096)

	for {
		n, readErr := scanner.Read(buffer)
		if n > 0 {
			data := buffer[:n]

			// 直接转发数据到客户端
			_, writeErr := c.Writer.Write(data)
			if writeErr != nil {
				return nil, openai.ErrorWrapper(writeErr, "write_stream_failed", http.StatusInternalServerError)
			}
			c.Writer.Flush()

			// 尝试解析 usage 信息
			dataStr := string(data)
			if strings.Contains(dataStr, "usageMetadata") {
				// 解析最后一块包含 usage 的数据
				lines := strings.Split(dataStr, "\n")
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "data: ") {
						jsonData := strings.TrimPrefix(line, "data: ")
						var geminiResp map[string]interface{}
						if json.Unmarshal([]byte(jsonData), &geminiResp) == nil {
							if usageMetadata, ok := geminiResp["usageMetadata"].(map[string]interface{}); ok {
								if promptTokens, ok := usageMetadata["promptTokenCount"].(float64); ok {
									usage.PromptTokens = int(promptTokens)
								}
								if candidatesTokens, ok := usageMetadata["candidatesTokenCount"].(float64); ok {
									usage.CompletionTokens = int(candidatesTokens)
								}
								if totalTokens, ok := usageMetadata["totalTokenCount"].(float64); ok {
									usage.TotalTokens = int(totalTokens)
								}
							}

							// 收集响应文本
							if candidates, ok := geminiResp["candidates"].([]interface{}); ok {
								for _, candidate := range candidates {
									if candMap, ok := candidate.(map[string]interface{}); ok {
										if content, ok := candMap["content"].(map[string]interface{}); ok {
											if parts, ok := content["parts"].([]interface{}); ok {
												for _, part := range parts {
													if partMap, ok := part.(map[string]interface{}); ok {
														if text, ok := partMap["text"].(string); ok {
															responseText.WriteString(text)
														}
													}
												}
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}

		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return nil, openai.ErrorWrapper(readErr, "read_stream_failed", http.StatusInternalServerError)
		}
	}

	// 如果没有获取到 completion tokens，根据响应文本估算
	if usage.CompletionTokens == 0 {
		text := responseText.String()
		if text != "" {
			usage.CompletionTokens = countTextTokens(text)
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		}
	}

	return usage, nil
}
