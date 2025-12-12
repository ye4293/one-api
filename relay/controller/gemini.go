package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	meta := util.GetRelayMeta(c)
	
	adaptor := helper.GetAdaptor(meta.APIType)
	if adaptor == nil {
		return openai.ErrorWrapper(fmt.Errorf("invalid api type: %d", meta.APIType), "invalid_api_type", http.StatusBadRequest)
	}

	// 计算预消费配额
	groupRatio := common.GetGroupRatio(group)
	modelRatio := common.GetModelRatio(modelName)
	ratio := modelRatio * groupRatio

	// 简单估算：每次请求预扣费
	preConsumedQuota, prePromptTokens, err := CalculateGeminiQuotaFromRequest(originRequestBody, modelName, ratio)
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

	meta.PromptTokens = prePromptTokens
	//先写死透传

	adaptor.Init(meta)
	resp, err := adaptor.DoRequest(c, meta, bytes.NewBuffer(originRequestBody))
	if err != nil {
		return openai.ErrorWrapper(err, "failed_to_send_request", http.StatusBadGateway)
	}

	var usageMetadata *gemini.UsageMetadata
	var openaiErr *model.ErrorWithStatusCode

	if meta.IsStream {
		usageMetadata, openaiErr = doNativeGeminiStreamResponse(c, resp, meta)
	} else {
		usageMetadata, openaiErr = doNativeGeminiResponse(c, resp, meta)
	}

	if openaiErr != nil {
		return openaiErr
	}
	
	actualQuota,costInfo := CalculateGeminiQuotaFromUsageMetadata(usageMetadata, modelName, ratio)

	//logger.Infof(ctx, "Gemini actual quota: %d, total tokens: %d", actualQuota, usage.TotalTokens)
	// 记录消费日志
	duration := time.Since(startTime).Seconds()
	tokenName := c.GetString("token_name")
	promptTokens := usageMetadata.PromptTokenCount
	completionTokens := usageMetadata.CandidatesTokenCount + usageMetadata.ThoughtsTokenCount
	totalTokens := usageMetadata.TotalTokenCount
	cachedTokens := usageMetadata.CachedContentTokenCount

	go recordGeminiConsumption(ctx, userId, channelId, tokenId, modelName, tokenName, promptTokens, completionTokens, totalTokens, cachedTokens, actualQuota, c.Request.RequestURI, duration, meta.IsStream, c, costInfo)
	return nil
}

// recordGeminiConsumption 记录 Gemini 消费日志
func recordGeminiConsumption(ctx context.Context, userId, channelId, tokenId int, modelName, tokenName string, promptTokens, completionTokens, totalTokens, cachedTokens int, quota int64, requestPath string, duration float64,isStream bool, c *gin.Context, costInfo GeminiTokenCost) {
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
	other := common.GetJsonString(costInfo)

	dbmodel.RecordConsumeLogWithOtherAndRequestID(ctx, userId, channelId, promptTokens, completionTokens, modelName,
		tokenName, quota, logContent, duration, title, referer, isStream, 0, other, c.GetHeader("X-Request-ID"), cachedTokens)
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
				estimatedTokens += openai.CountTokenText(part.Text, modelName)
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

	// // 估算输出 tokens (根据 maxOutputTokens)
	// estimatedOutputTokens := 1000 // 默认估算
	// if geminiReq.GenerationConfig.MaxOutputTokens > 0 {
	// 	estimatedOutputTokens = geminiReq.GenerationConfig.MaxOutputTokens
	// }

	// // 总 tokens = 输入 + 预估输出
	// totalEstimatedTokens := estimatedTokens + estimatedOutputTokens

	// 计算配额 (tokens * ratio / 1000 * QuotaPerUnit)
	// 预消费按 30% 估算，避免预扣太多
	quota := int64(float64(estimatedTokens) * ratio * 0.3 / 1000 * config.QuotaPerUnit)

	return quota, estimatedTokens, nil
}

// countTextTokens 简单的 token 计数（粗略估算）
// 中文: 1个字 ≈ 1.5 tokens
// 英文: 1个词 ≈ 1.3 tokens
// 这里简化为: 每4个字符 ≈ 1 token

// GeminiModelPricing 定义 Gemini 模型的价格结构
// 价格单位: 美元/百万tokens
type GeminiModelPricing struct {
	InputPriceLow      float64 // 输入价格（低阈值）
	InputPriceHigh     float64 // 输入价格（高阈值）
	OutputPriceLow     float64 // 输出价格（低阈值）
	OutputPriceHigh    float64 // 输出价格（高阈值）
	ThinkingPriceLow   float64 // 思考token价格（低阈值）- 仅 Flash 系列
	ThinkingPriceHigh  float64 // 思考token价格（高阈值）- 仅 Flash 系列
	Threshold          int     // 阈值（token数量），超过此值使用高价格
	HasThinkingPricing bool    // 是否有单独的思考token定价
}

// GeminiPricingTable Gemini 模型价格表
// 参考: https://ai.google.dev/gemini-api/docs/pricing
var GeminiPricingTable = map[string]GeminiModelPricing{
	// Gemini 2.5 Pro
	"gemini-2.5-pro": {
		InputPriceLow: 1.25, InputPriceHigh: 2.50,
		OutputPriceLow: 10.00, OutputPriceHigh: 15.00,
		Threshold: 200000, HasThinkingPricing: false,
	},
	"gemini-2.5-pro-preview": {
		InputPriceLow: 1.25, InputPriceHigh: 2.50,
		OutputPriceLow: 10.00, OutputPriceHigh: 15.00,
		Threshold: 200000, HasThinkingPricing: false,
	},
	// Gemini 2.5 Flash
	"gemini-2.5-flash": {
		InputPriceLow: 0.15, InputPriceHigh: 0.30,
		OutputPriceLow: 0.60, OutputPriceHigh: 1.20,
		ThinkingPriceLow: 3.50, ThinkingPriceHigh: 7.00,
		Threshold: 200000, HasThinkingPricing: true,
	},
	"gemini-2.5-flash-preview": {
		InputPriceLow: 0.15, InputPriceHigh: 0.30,
		OutputPriceLow: 0.60, OutputPriceHigh: 1.20,
		ThinkingPriceLow: 3.50, ThinkingPriceHigh: 7.00,
		Threshold: 200000, HasThinkingPricing: true,
	},
	// Gemini 2.0 Flash
	"gemini-2.0-flash": {
		InputPriceLow: 0.10, InputPriceHigh: 0.10,
		OutputPriceLow: 0.40, OutputPriceHigh: 0.40,
		Threshold: 0, HasThinkingPricing: false, // 无阶梯价格
	},
	"gemini-2.0-flash-exp": {
		InputPriceLow: 0.10, InputPriceHigh: 0.10,
		OutputPriceLow: 0.40, OutputPriceHigh: 0.40,
		Threshold: 0, HasThinkingPricing: false,
	},
	// Gemini 1.5 Pro
	"gemini-1.5-pro": {
		InputPriceLow: 1.25, InputPriceHigh: 2.50,
		OutputPriceLow: 5.00, OutputPriceHigh: 10.00,
		Threshold: 128000, HasThinkingPricing: false,
	},
	"gemini-1.5-pro-latest": {
		InputPriceLow: 1.25, InputPriceHigh: 2.50,
		OutputPriceLow: 5.00, OutputPriceHigh: 10.00,
		Threshold: 128000, HasThinkingPricing: false,
	},
	// Gemini 1.5 Flash
	"gemini-1.5-flash": {
		InputPriceLow: 0.075, InputPriceHigh: 0.15,
		OutputPriceLow: 0.30, OutputPriceHigh: 0.60,
		Threshold: 128000, HasThinkingPricing: false,
	},
	"gemini-1.5-flash-latest": {
		InputPriceLow: 0.075, InputPriceHigh: 0.15,
		OutputPriceLow: 0.30, OutputPriceHigh: 0.60,
		Threshold: 128000, HasThinkingPricing: false,
	},
	// Gemini 3 Pro Preview
	"gemini-3-pro-preview": {
		InputPriceLow: 2.00, InputPriceHigh: 4.00,
		OutputPriceLow: 12.00, OutputPriceHigh: 18.00,
		Threshold: 200000, HasThinkingPricing: false,
	},
}

// GeminiTokenCost 表示 Gemini API 的费用明细
type GeminiTokenCost struct {
	InputTokens    int     // 输入 token 数量
	OutputTokens   int     // 输出 token 数量 (不含思考)
	ThinkingTokens int     // 思考 token 数量
	CachedTokens   int     // 缓存 token 数量
	TotalTokens    int     // 总 token 数量
	InputCost      float64 // 输入费用 (美元)
	OutputCost     float64 // 输出费用 (美元)
	ThinkingCost   float64 // 思考 token 费用 (美元)
	CachedCost     float64 // 缓存 token 费用 (美元)
	TotalCost      float64 // 总费用 (美元)
	ModelName      string  // 模型名称
}

// CalculateGeminiTokenCost 根据 Gemini 响应体的 UsageMetadata 计算 token 费用
// 参数:
//   - usageMetadata: Gemini API 响应中的 UsageMetadata
//   - modelName: 使用的模型名称
//
// 返回:
//   - GeminiTokenCost: 费用明细
//
// 定价规则（参考 https://ai.google.dev/gemini-api/docs/pricing）:
//  1. 部分模型有阶梯定价：输入token超过阈值后使用更高价格
//  2. Flash 2.5 系列有单独的思考token定价（thinking tokens）
//  3. 缓存的token享有 90% 折扣（价格为正常价格的 1/10）
//  4. 不同模型的输入/输出价格不同，需根据模型名称匹配价格表
func CalculateGeminiTokenCost(usageMetadata *gemini.UsageMetadata, modelName string) GeminiTokenCost {
	result := GeminiTokenCost{
		ModelName: modelName,
	}

	if usageMetadata == nil {
		return result
	}

	// 提取 token 数量
	result.InputTokens = usageMetadata.PromptTokenCount
	result.OutputTokens = usageMetadata.CandidatesTokenCount
	result.ThinkingTokens = usageMetadata.ThoughtsTokenCount
	result.TotalTokens = usageMetadata.TotalTokenCount
	result.CachedTokens = usageMetadata.CachedContentTokenCount

	// 获取模型价格，如果找不到则使用默认价格（gemini-2.0-flash）
	pricing, found := getGeminiPricing(modelName)
	if !found {
		// 默认使用 gemini-2.0-flash 的价格
		pricing = GeminiPricingTable["gemini-2.0-flash"]
	}

	// 根据输入 token 数量判断使用高/低价格
	// 注意：阈值判断应该基于非缓存的输入token数量
	nonCachedInputTokens := result.InputTokens - result.CachedTokens
	useHighPrice := pricing.Threshold > 0 && result.InputTokens > pricing.Threshold

	// 计算输入费用（分为缓存和非缓存部分）
	inputPrice := pricing.InputPriceLow
	if useHighPrice {
		inputPrice = pricing.InputPriceHigh
	}

	// 非缓存输入token按正常价格计算
	result.InputCost = float64(nonCachedInputTokens) / 1000000.0 * inputPrice

	// 缓存token享有90%折扣（价格为正常价格的1/10）
	if result.CachedTokens > 0 {
		cachedPrice := inputPrice * 0.1 // 10% 的正常价格
		result.CachedCost = float64(result.CachedTokens) / 1000000.0 * cachedPrice
	}

	// 计算输出费用（不含思考 token）
	outputPrice := pricing.OutputPriceLow
	if useHighPrice {
		outputPrice = pricing.OutputPriceHigh
	}
	result.OutputCost = float64(result.OutputTokens) / 1000000.0 * outputPrice

	// 计算思考 token 费用（如果模型支持且有思考 token）
	if pricing.HasThinkingPricing && result.ThinkingTokens > 0 {
		thinkingPrice := pricing.ThinkingPriceLow
		if useHighPrice {
			thinkingPrice = pricing.ThinkingPriceHigh
		}
		result.ThinkingCost = float64(result.ThinkingTokens) / 1000000.0 * thinkingPrice
	} else if result.ThinkingTokens > 0 {
		// 对于不区分思考token价格的模型，思考token按输出价格计算
		result.ThinkingCost = float64(result.ThinkingTokens) / 1000000.0 * outputPrice
	}

	// 总费用 = 输入费用 + 缓存费用 + 输出费用 + 思考token费用
	result.TotalCost = result.InputCost + result.CachedCost + result.OutputCost + result.ThinkingCost

	return result
}

// getGeminiPricing 根据模型名称获取价格配置
func getGeminiPricing(modelName string) (GeminiModelPricing, bool) {
	// 直接匹配
	if pricing, ok := GeminiPricingTable[modelName]; ok {
		return pricing, true
	}

	// 模糊匹配：去掉后缀版本号等
	normalizedName := normalizeGeminiModelName(modelName)
	if pricing, ok := GeminiPricingTable[normalizedName]; ok {
		return pricing, true
	}

	// 前缀匹配
	for key, pricing := range GeminiPricingTable {
		if strings.HasPrefix(modelName, key) {
			return pricing, true
		}
	}

	return GeminiModelPricing{}, false
}

// normalizeGeminiModelName 规范化模型名称
func normalizeGeminiModelName(modelName string) string {
	// 移除常见后缀如 -001, -002, -latest, -exp 等
	name := strings.ToLower(modelName)

	// 移除日期后缀 (如 -0801, -1206)
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '-' {
			suffix := name[i+1:]
			if isDateSuffix(suffix) {
				name = name[:i]
				break
			}
		}
	}

	return name
}

// isDateSuffix 检查是否是日期后缀
func isDateSuffix(s string) bool {
	if len(s) != 4 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// CalculateGeminiQuotaFromUsageMetadata 根据 UsageMetadata 计算配额
// 此方法将费用转换为系统配额单位
func CalculateGeminiQuotaFromUsageMetadata(usageMetadata *gemini.UsageMetadata, modelName string, groupRatio float64) (int64,GeminiTokenCost) {
	cost := CalculateGeminiTokenCost(usageMetadata, modelName)

	// 将美元费用转换为配额
	// 假设 1 美元 = QuotaPerUnit * 1000 配额 (可根据实际情况调整)
	// 这里使用 config.QuotaPerUnit 作为基准
	quotaPerDollar := float64(config.QuotaPerUnit) * 500 // 1美元 = 500K 配额单位

	quota := int64(cost.TotalCost * quotaPerDollar * groupRatio)

	return quota,cost
}

// doNativeGeminiResponse 处理 Gemini 非流式响应
func doNativeGeminiResponse(c *gin.Context, resp *http.Response, meta *util.RelayMeta) (usageMetadata *gemini.UsageMetadata, err *model.ErrorWithStatusCode) {
	defer util.CloseResponseBodyGracefully(resp)

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
	// usage = &model.Usage{
	// 	PromptTokens:     geminiResponse.UsageMetadata.PromptTokenCount,
	// 	CompletionTokens: geminiResponse.UsageMetadata.CandidatesTokenCount + geminiResponse.UsageMetadata.ThoughtsTokenCount,
	// 	TotalTokens:      geminiResponse.UsageMetadata.TotalTokenCount,
	// }

	// 如果响应中有 usageMetadata，使用真实数据
	// Gemini 原生响应可能在不同位置包含 usage 信息
	// 这里需要手动解析 JSON 来获取
	// var rawResponse map[string]interface{}
	// if json.Unmarshal(responseBody, &rawResponse) == nil {
	// 	if usageMetadata, ok := rawResponse["usageMetadata"].(map[string]interface{}); ok {  //输入token
	// 		if promptTokens, ok := usageMetadata["promptTokenCount"].(float64); ok {
	// 			usage.PromptTokens = int(promptTokens)
	// 		}
	// 		if candidatesTokens, ok := usageMetadata["candidatesTokenCount"].(float64); ok {  //补全token
	// 			usage.CompletionTokens = int(candidatesTokens)
	// 		}
	// 		if totalTokens, ok := usageMetadata["totalTokenCount"].(float64); ok {
	// 			usage.TotalTokens = int(totalTokens)
	// 		}
	// 	}
	// }
	// for _, detail := range geminiResponse.UsageMetadata.PromptTokensDetails {
	// 	if detail.Modality == "AUDIO" {
	// 		usage.PromptTokensDetails.AudioTokens = detail.TokenCount
	// 	} else if detail.Modality == "TEXT" {
	// 		usage.PromptTokensDetails.TextTokens = detail.TokenCount
	// 	}
	// }

	// 直接返回原生 Gemini 响应格式
	util.IOCopyBytesGracefully(c, resp, responseBody)
	return geminiResponse.UsageMetadata, nil

}

// doNativeGeminiStreamResponse 处理 Gemini 流式响应
// Gemini 流式响应格式为 SSE，每行以 "data: " 开头，后跟 JSON 对象
func doNativeGeminiStreamResponse(c *gin.Context, resp *http.Response, meta *util.RelayMeta) (usageMetadata *gemini.UsageMetadata, err *model.ErrorWithStatusCode) {
	defer util.CloseResponseBodyGracefully(resp)
	// 检查响应状态码
	if resp.StatusCode != http.StatusOK {
		responseBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, openai.ErrorWrapper(readErr, "read_error_response_failed", http.StatusInternalServerError)
		}
		return nil, &model.ErrorWithStatusCode{
			Error: model.Error{
				Message: string(responseBody),
				Type:    "gemini_api_error",
				Code:    fmt.Sprintf("status_%d", resp.StatusCode),
			},
			StatusCode: resp.StatusCode,
		}
	}
	// 设置 SSE 响应头
	common.SetEventStreamHeaders(c)
	// 用于保存最后的 UsageMetadata
	var lastUsageMetadata = &gemini.UsageMetadata{}

	var imageCount int
	var SendResponseCount int
	responseText := strings.Builder{}

	helper.StreamScannerHandler(c, resp, meta, func(data string) bool {
		var geminiResponse gemini.ChatResponse
		//var data1 := `{"candidates": [{"content": {"role": "model","parts": [{"text": "**AI learns from data to find patterns and make predictions.**"}]},"finishReason": "STOP"}],"usageMetadata": {"promptTokenCount": 8,"candidatesTokenCount": 12,"totalTokenCount": 1191,"trafficType": "ON_DEMAND","promptTokensDetails": [{"modality": "TEXT","tokenCount": 8}],"candidatesTokensDetails": [{"modality": "TEXT","tokenCount": 12}],"thoughtsTokenCount": 1171},"modelVersion": "gemini-2.5-pro","createTime": "2025-12-11T20:48:01.865079Z","responseId": "AS47abfmNISn998PvLHjwAs"}`
		//err := json.Unmarshal([]byte(data1), &geminiResponse)
		err := json.Unmarshal([]byte(data), &geminiResponse)

		if err != nil {
			logger.Error(c, "error unmarshalling stream response: "+err.Error())
			return false
		}

		// 统计图片数量
		for _, candidate := range geminiResponse.Candidates {
			for _, part := range candidate.Content.Parts {
				if part.InlineData != nil && part.InlineData.MimeType != "" {
					imageCount++
				}
				if part.Text != "" {
					responseText.WriteString(part.Text)
				}
			}
		}

		// 更新使用量统计
		if geminiResponse.UsageMetadata != nil {
			lastUsageMetadata = geminiResponse.UsageMetadata
		}

		// 直接发送 GeminiChatResponse 响应
		err = helper.StringData(c, data)
		if err != nil {
			logger.Error(c, err.Error())
		}
		SendResponseCount++
		return true
	})

	if SendResponseCount == 0 {
		return nil, openai.ErrorWrapper(errors.New("no response received from Gemini API"), "write_stream_failed", http.StatusInternalServerError)
	}

	if imageCount != 0 {
		if lastUsageMetadata.CandidatesTokenCount == 0 {
			lastUsageMetadata.CandidatesTokenCount = imageCount * 258
		}
	}

	// 如果usage.CompletionTokens为0，则使用本地统计的completion tokens
	if lastUsageMetadata != nil && lastUsageMetadata.CandidatesTokenCount == 0 {
		str := responseText.String()
		if len(str) > 0 {
			lastUsageMetadata.CandidatesTokenCount = openai.CountTokenText(responseText.String(), meta.OriginModelName)
		}
	}

	return lastUsageMetadata, nil
}

// ExampleCalculateGeminiCost 示例：如何计算 Gemini API 的费用
// 此函数展示了完整的费用计算流程
func ExampleCalculateGeminiCost() {
	// 假设我们从 Gemini API 响应中获取了以下 UsageMetadata
	usageMetadata := &gemini.UsageMetadata{
		PromptTokenCount:        10000, // 输入token
		CandidatesTokenCount:    5000,  // 输出token（不含思考token）
		ThoughtsTokenCount:      1000,  // 思考token（仅Flash 2.5系列）
		TotalTokenCount:         16000, // 总token
		CachedContentTokenCount: 2000,  // 缓存的token（享有90%折扣）
	}

	modelName := "gemini-2.5-flash"

	// 计算费用
	cost := CalculateGeminiTokenCost(usageMetadata, modelName)

	// 输出费用明细
	fmt.Printf("===== Gemini 费用计算明细 =====\n")
	fmt.Printf("模型名称: %s\n", cost.ModelName)
	fmt.Printf("输入 tokens: %d\n", cost.InputTokens)
	fmt.Printf("  - 非缓存: %d tokens, 费用: $%.6f\n", cost.InputTokens-cost.CachedTokens, cost.InputCost)
	fmt.Printf("  - 缓存: %d tokens, 费用: $%.6f (享90%%折扣)\n", cost.CachedTokens, cost.CachedCost)
	fmt.Printf("输出 tokens: %d, 费用: $%.6f\n", cost.OutputTokens, cost.OutputCost)
	if cost.ThinkingTokens > 0 {
		fmt.Printf("思考 tokens: %d, 费用: $%.6f\n", cost.ThinkingTokens, cost.ThinkingCost)
	}
	fmt.Printf("总计 tokens: %d\n", cost.TotalTokens)
	fmt.Printf("总费用: $%.6f\n", cost.TotalCost)
	fmt.Printf("===============================\n")

	// 示例输出:
	// ===== Gemini 费用计算明细 =====
	// 模型名称: gemini-2.5-flash
	// 输入 tokens: 10000
	//   - 非缓存: 8000 tokens, 费用: $0.001200
	//   - 缓存: 2000 tokens, 费用: $0.000030 (享90%折扣)
	// 输出 tokens: 5000, 费用: $0.003000
	// 思考 tokens: 1000, 费用: $0.003500
	// 总计 tokens: 16000
	// 总费用: $0.007730
	// ===============================
}

// GetGeminiModelPricing 获取指定模型的价格信息（供外部调用）
// 返回模型的详细定价配置，如果模型不存在则返回默认配置
func GetGeminiModelPricing(modelName string) GeminiModelPricing {
	pricing, found := getGeminiPricing(modelName)
	if !found {
		// 返回默认配置
		return GeminiPricingTable["gemini-2.0-flash"]
	}
	return pricing
}

// FormatGeminiCost 格式化输出 Gemini 费用明细（用于日志或调试）
func FormatGeminiCost(cost GeminiTokenCost) string {
	var builder strings.Builder

	builder.WriteString(fmt.Sprintf("Model: %s | ", cost.ModelName))
	builder.WriteString(fmt.Sprintf("Input: %d tokens ($%.6f) | ", cost.InputTokens, cost.InputCost))

	if cost.CachedTokens > 0 {
		builder.WriteString(fmt.Sprintf("Cached: %d tokens ($%.6f) | ", cost.CachedTokens, cost.CachedCost))
	}

	builder.WriteString(fmt.Sprintf("Output: %d tokens ($%.6f) | ", cost.OutputTokens, cost.OutputCost))

	if cost.ThinkingTokens > 0 {
		builder.WriteString(fmt.Sprintf("Thinking: %d tokens ($%.6f) | ", cost.ThinkingTokens, cost.ThinkingCost))
	}

	builder.WriteString(fmt.Sprintf("Total: %d tokens ($%.6f)", cost.TotalTokens, cost.TotalCost))

	return builder.String()
}
