package controller

import (
	"bytes"
	"context"
	"encoding/json"

	//"errors"
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
	"github.com/songquanpeng/one-api/relay/channel/anthropic"
	"github.com/songquanpeng/one-api/relay/channel/openai"

	//relayconstant "github.com/songquanpeng/one-api/relay/constant"
	"github.com/songquanpeng/one-api/relay/helper"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

// ensureGeminiContentsRole 确保 Gemini 请求体中的 contents 数组中每个元素都有 role 字段
// Vertex AI API 要求必须指定 role 字段（值为 "user" 或 "model"），而 Gemini 原生 API 可以省略
// 此函数用于在发送请求到 Vertex AI 之前自动补全缺失的 role 字段

// RelayGeminiNative 处理 Gemini 原生 API 请求
func RelayClaudeNative(c *gin.Context) *model.ErrorWithStatusCode {
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

	if err != nil {
		return openai.ErrorWrapper(err, "failed_to_get_request_body", http.StatusInternalServerError)
	}
	meta := util.GetRelayMeta(c)
	meta.ActualModelName = meta.OriginModelName
	if meta.ModelMapping != nil && len(meta.ModelMapping) > 0 {
		if mappedModel, ok := meta.ModelMapping[meta.OriginModelName]; ok && mappedModel != "" {
			meta.ActualModelName = mappedModel
		}
	}
	adaptor := helper.GetAdaptor(meta.APIType)
	if adaptor == nil {
		return openai.ErrorWrapper(fmt.Errorf("invalid api type: %d", meta.APIType), "invalid_api_type", http.StatusBadRequest)
	}
	var claudeReq anthropic.Request
	if err := json.Unmarshal(originRequestBody, &claudeReq); err != nil {
		return openai.ErrorWrapper(fmt.Errorf("failed to parse claude request: %w", err), "failed_to_parse_request", http.StatusInternalServerError)
	}
	meta.IsStream = claudeReq.Stream
	// 计算预消费配额
	groupRatio := common.GetGroupRatio(group)
	modelRatio := common.GetModelRatio(modelName)
	ratio := modelRatio * groupRatio

	// 简单估算：每次请求预扣费
	preConsumedQuota, prePromptTokens, err := CalculateClaudeQuotaFromRequest(originRequestBody, modelName, ratio)
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

	var usageMetadata *anthropic.Usage
	var openaiErr *model.ErrorWithStatusCode

	if meta.IsStream {
		usageMetadata, openaiErr = doNativeClaudeStreamResponse(c, resp, meta)
	} else {
		usageMetadata, openaiErr = doNativeClaudeResponse(c, resp, meta)
	}

	if openaiErr != nil {
		return openaiErr
	}

	actualQuota, _ := CalculateClaudeQuotaFromUsageMetadata(usageMetadata, modelName, groupRatio)

	//logger.Infof(ctx, "Gemini actual quota: %d, total tokens: %d", actualQuota, usage.TotalTokens)
	// 记录消费日志
	duration := time.Since(startTime).Seconds()
	tokenName := c.GetString("token_name")
	promptTokens := usageMetadata.InputTokens
	completionTokens := usageMetadata.OutputTokens
	totalTokens := usageMetadata.InputTokens + usageMetadata.OutputTokens
	//cachedTokens := usageMetadata.CacheCreationInputTokens + usageMetadata.CacheReadInputTokens

	go recordClaudeConsumption(ctx, userId, channelId, tokenId, modelName, tokenName, promptTokens, completionTokens, totalTokens, 0, actualQuota, c.Request.RequestURI, duration, meta.IsStream, c, usageMetadata)

	return nil
}

// recordClaudeConsumption 记录 Claude 消费日志
func recordClaudeConsumption(ctx context.Context, userId, channelId, tokenId int, modelName, tokenName string, promptTokens, completionTokens, totalTokens, cachedTokens int, quota int64, requestPath string, duration float64, isStream bool, c *gin.Context, usageMetadata *anthropic.Usage) {
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
	logContent := fmt.Sprintf("Claude API %s", requestPath)
	referer := c.Request.Header.Get("HTTP-Referer")
	title := c.Request.Header.Get("X-Title")

	// 提取用量详情并格式化为统一格式
	//usageDetails := extractClaudeNativeUsageDetails(usageMetadata)
	//adminInfo := extractAdminInfoFromContext(c)
	//other := buildOtherInfoWithUsageDetails(adminInfo, nil)
	//logger.Infof(ctx, "usageMetadata: %+v", usageMetadata)
	var other string
	usageDetails := extractClaudeNativeUsageDetails(usageMetadata)

	if usageDetails != nil {
		if otherBytes, err := json.Marshal(usageDetails); err == nil {
			other = string(otherBytes)

		}
	}
	dbmodel.RecordConsumeLogWithOtherAndRequestID(ctx, userId, channelId, promptTokens, completionTokens, modelName,
		tokenName, quota, logContent, duration, title, referer, isStream, 0, other, c.GetHeader("X-Request-ID"), 0)
}

// ClaudeUsageDetails 用于存储从 Claude Usage 提取的详细使用信息
type ClaudeUsageDetails struct {
	InputTokens                    int `json:"input_tokens"`
	OutputTokens                   int `json:"output_tokens"`
	ClaudeCacheCreation5mTokens    int `json:"claude_cache_creation_5_m_tokens,omitempty"`
	ClaudeCacheCreation1hTokens    int `json:"claude_cache_creation_1_h_tokens,omitempty"`
	CacheCreationInputTokens       int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens           int `json:"cache_read_input_tokens,omitempty"`
	ServerToolUseWebSearchRequests int `json:"server_tool_use_web_search_requests,omitempty"`
}

// extractClaudeNativeUsageDetails 从 Claude Usage 提取详细的使用信息（用于 native 接口）
func extractClaudeNativeUsageDetails(usageMetadata *anthropic.Usage) *ClaudeUsageDetails {
	if usageMetadata == nil {
		return nil
	}

	details := &ClaudeUsageDetails{
		InputTokens:                 usageMetadata.InputTokens,
		OutputTokens:                usageMetadata.OutputTokens,
		CacheCreationInputTokens:    usageMetadata.CacheCreationInputTokens,
		CacheReadInputTokens:        usageMetadata.CacheReadInputTokens,
		ClaudeCacheCreation5mTokens: usageMetadata.ClaudeCacheCreation5mTokens,
		ClaudeCacheCreation1hTokens: usageMetadata.ClaudeCacheCreation1hTokens,
	}

	if usageMetadata.ServerToolUse != nil {
		details.ServerToolUseWebSearchRequests = usageMetadata.ServerToolUse.WebSearchRequests
	}

	return details
}

func CalculateClaudeQuotaFromRequest(requestBody []byte, modelName string, ratio float64) (int64, int, error) {
	return 0, 0, nil
}

// countTextTokens 简单的 token 计数（粗略估算）
// 中文: 1个字 ≈ 1.5 tokens
// 英文: 1个词 ≈ 1.3 tokens
// 这里简化为: 每4个字符 ≈ 1 token

// ClaudeModelPricing Claude 模型的价格结构
// 价格单位: 美元/百万tokens
type ClaudeModelPricing struct {
	BaseInputPrice         float64 // 基础输入价格
	OutputPrice            float64 // 输出价格
	CacheWrite5MinPrice    float64 // 5分钟缓存写入价格（1.25x 基础输入价格）
	CacheWrite1HourPrice   float64 // 1小时缓存写入价格（2x 基础输入价格）
	CacheReadPrice         float64 // 缓存读取价格（0.1x 基础输入价格）
	LongContextInputPrice  float64 // 长上下文输入价格（>200K tokens）
	LongContextOutputPrice float64 // 长上下文输出价格（>200K tokens）
	LongContextThreshold   int     // 长上下文阈值（200K tokens）
	BatchInputPrice        float64 // 批量处理输入价格（50%折扣）
	BatchOutputPrice       float64 // 批量处理输出价格（50%折扣）
}

// ClaudePricingTable Claude 模型价格表
// 参考: https://platform.claude.com/docs/en/about-claude/pricing
var ClaudePricingTable = map[string]ClaudeModelPricing{
	// Claude Opus 4.5
	"claude-3-5-sonnet-20241022": {
		BaseInputPrice: 3.0, OutputPrice: 15.0,
		CacheWrite5MinPrice: 3.75, CacheWrite1HourPrice: 6.0, CacheReadPrice: 0.30,
		LongContextInputPrice: 6.0, LongContextOutputPrice: 22.50, LongContextThreshold: 200000,
		BatchInputPrice: 1.50, BatchOutputPrice: 7.50,
	},
	"claude-3-5-sonnet": {
		BaseInputPrice: 3.0, OutputPrice: 15.0,
		CacheWrite5MinPrice: 3.75, CacheWrite1HourPrice: 6.0, CacheReadPrice: 0.30,
		LongContextInputPrice: 6.0, LongContextOutputPrice: 22.50, LongContextThreshold: 200000,
		BatchInputPrice: 1.50, BatchOutputPrice: 7.50,
	},

	// Claude Sonnet 4
	"claude-sonnet-4-20250514": {
		BaseInputPrice: 3.0, OutputPrice: 15.0,
		CacheWrite5MinPrice: 3.75, CacheWrite1HourPrice: 6.0, CacheReadPrice: 0.30,
		LongContextInputPrice: 6.0, LongContextOutputPrice: 22.50, LongContextThreshold: 200000,
		BatchInputPrice: 1.50, BatchOutputPrice: 7.50,
	},

	// Claude Haiku 4.5
	"claude-3-5-haiku-20241022": {
		BaseInputPrice: 1.0, OutputPrice: 5.0,
		CacheWrite5MinPrice: 1.25, CacheWrite1HourPrice: 2.0, CacheReadPrice: 0.10,
		LongContextInputPrice: 0, LongContextOutputPrice: 0, LongContextThreshold: 0, // Haiku 不支持长上下文
		BatchInputPrice: 0.50, BatchOutputPrice: 2.50,
	},
	"claude-3-5-haiku": {
		BaseInputPrice: 1.0, OutputPrice: 5.0,
		CacheWrite5MinPrice: 1.25, CacheWrite1HourPrice: 2.0, CacheReadPrice: 0.10,
		LongContextInputPrice: 0, LongContextOutputPrice: 0, LongContextThreshold: 0,
		BatchInputPrice: 0.50, BatchOutputPrice: 2.50,
	},

	// Claude Haiku 3.5
	"claude-3-haiku-20240307": {
		BaseInputPrice: 0.80, OutputPrice: 4.0,
		CacheWrite5MinPrice: 1.0, CacheWrite1HourPrice: 1.6, CacheReadPrice: 0.08,
		LongContextInputPrice: 0, LongContextOutputPrice: 0, LongContextThreshold: 0,
		BatchInputPrice: 0.40, BatchOutputPrice: 2.0,
	},

	// Claude Sonnet 3.7 (deprecated)
	"claude-3-7-sonnet-20250219": {
		BaseInputPrice: 3.0, OutputPrice: 15.0,
		CacheWrite5MinPrice: 3.75, CacheWrite1HourPrice: 6.0, CacheReadPrice: 0.30,
		LongContextInputPrice: 0, LongContextOutputPrice: 0, LongContextThreshold: 0,
		BatchInputPrice: 1.50, BatchOutputPrice: 7.50,
	},

	// Claude Opus 4 (deprecated)
	"claude-opus-4-20250514": {
		BaseInputPrice: 15.0, OutputPrice: 75.0,
		CacheWrite5MinPrice: 18.75, CacheWrite1HourPrice: 30.0, CacheReadPrice: 1.50,
		LongContextInputPrice: 0, LongContextOutputPrice: 0, LongContextThreshold: 0,
		BatchInputPrice: 7.50, BatchOutputPrice: 37.50,
	},

	// Claude Haiku 3 (deprecated)
	"claude-3-haiku": {
		BaseInputPrice: 0.25, OutputPrice: 1.25,
		CacheWrite5MinPrice: 0.30, CacheWrite1HourPrice: 0.50, CacheReadPrice: 0.03,
		LongContextInputPrice: 0, LongContextOutputPrice: 0, LongContextThreshold: 0,
		BatchInputPrice: 0.125, BatchOutputPrice: 0.625,
	},
}

// ClaudeTokenCost Claude API 的费用明细
type ClaudeTokenCost struct {
	// 输入部分 token 数量
	InputTextTokens  int // 输入文字 token 数量
	InputImageTokens int // 输入图片 token 数量
	InputAudioTokens int // 输入音频 token 数量
	// 输出部分 token 数量
	OutputTextTokens  int // 输出文字 token 数量
	OutputImageTokens int // 输出图片 token 数量
	OutputAudioTokens int // 输出音频 token 数量
	ThinkingTokens    int // 思考 token 数量
	CachedTokens      int // 缓存 token 数量
	TotalTokens       int // 总 token 数量
	// 费用明细
	InputCost      float64 // 输入费用 (美元)
	OutputCost     float64 // 输出费用 (美元)
	CacheWriteCost float64 // 缓存写入费用 (美元)
	CacheReadCost  float64 // 缓存读取费用 (美元)
	ToolCost       float64 // 工具使用费用 (美元)
	TotalCost      float64 // 总费用 (美元)
	ModelName      string  // 模型名称
	IsBatch        bool    // 是否为批量处理
	IsLongContext  bool    // 是否为长上下文
}

// CalculateClaudeTokenCost 根据 Claude API 响应计算详细费用
// 支持：缓存定价、批量处理折扣、长上下文定价、工具使用费用
func CalculateClaudeTokenCost(usageMetadata *anthropic.Usage, modelName string, isBatch bool) ClaudeTokenCost {
	result := ClaudeTokenCost{
		ModelName: modelName,
		IsBatch:   isBatch,
	}

	if usageMetadata == nil {
		return result
	}

	// 提取 token 数量
	result.InputTextTokens = usageMetadata.InputTokens
	result.OutputTextTokens = usageMetadata.OutputTokens
	result.CachedTokens = usageMetadata.CacheCreationInputTokens + usageMetadata.CacheReadInputTokens
	result.TotalTokens = usageMetadata.InputTokens + usageMetadata.OutputTokens +
		usageMetadata.CacheCreationInputTokens + usageMetadata.CacheReadInputTokens

	// 获取模型价格配置
	pricing, found := getClaudePricing(modelName)
	if !found {
		// 默认使用 Claude 3.5 Sonnet 的价格
		pricing = ClaudePricingTable["claude-3-5-sonnet"]
	}

	// 判断是否为长上下文（总输入tokens > 200K）
	totalInputTokens := usageMetadata.InputTokens + usageMetadata.CacheCreationInputTokens + usageMetadata.CacheReadInputTokens
	isLongContext := pricing.LongContextThreshold > 0 && totalInputTokens > pricing.LongContextThreshold
	result.IsLongContext = isLongContext

	// 计算费用
	if isBatch {
		// 批量处理使用50%折扣价格
		result.InputCost = float64(usageMetadata.InputTokens) / 1000000.0 * pricing.BatchInputPrice
		result.OutputCost = float64(usageMetadata.OutputTokens) / 1000000.0 * pricing.BatchOutputPrice
	} else if isLongContext {
		// 长上下文使用更高价格
		result.InputCost = float64(usageMetadata.InputTokens) / 1000000.0 * pricing.LongContextInputPrice
		result.OutputCost = float64(usageMetadata.OutputTokens) / 1000000.0 * pricing.LongContextOutputPrice
	} else {
		// 标准定价
		result.InputCost = float64(usageMetadata.InputTokens) / 1000000.0 * pricing.BaseInputPrice
		result.OutputCost = float64(usageMetadata.OutputTokens) / 1000000.0 * pricing.OutputPrice
	}

	// 计算缓存费用
	if usageMetadata.CacheCreationInputTokens > 0 {
		// 根据缓存持续时间选择价格（这里假设使用5分钟缓存）
		cacheWritePrice := pricing.CacheWrite5MinPrice
		if usageMetadata.CacheCreation != nil {
			// 如果有更具体的缓存信息，可以根据TTL调整价格
			// 这里简化处理，使用5分钟价格
		}
		result.CacheWriteCost = float64(usageMetadata.CacheCreationInputTokens) / 1000000.0 * cacheWritePrice
	}

	if usageMetadata.CacheReadInputTokens > 0 {
		result.CacheReadCost = float64(usageMetadata.CacheReadInputTokens) / 1000000.0 * pricing.CacheReadPrice
	}

	// 计算工具使用费用
	if usageMetadata.ServerToolUse != nil {
		if usageMetadata.ServerToolUse.WebSearchRequests > 0 {
			// 网络搜索：每1000次搜索 $10
			result.ToolCost = float64(usageMetadata.ServerToolUse.WebSearchRequests) / 1000.0 * 10.0
		}
	}

	// 计算总费用
	result.TotalCost = result.InputCost + result.OutputCost + result.CacheWriteCost + result.CacheReadCost + result.ToolCost

	return result
}

// getClaudePricing 根据模型名称获取价格配置
func getClaudePricing(modelName string) (ClaudeModelPricing, bool) {
	// 直接匹配
	if pricing, ok := ClaudePricingTable[modelName]; ok {
		return pricing, true
	}

	// 模糊匹配：去掉版本号等
	normalizedName := strings.ToLower(modelName)

	// 移除日期后缀 (如 -20241022, -20240307)
	for i := len(normalizedName) - 1; i >= 0; i-- {
		if normalizedName[i] == '-' {
			suffix := normalizedName[i+1:]
			if len(suffix) == 8 && strings.Contains(suffix, "20") { // 日期格式如 20241022
				normalizedName = normalizedName[:i]
				break
			}
		}
	}

	if pricing, ok := ClaudePricingTable[normalizedName]; ok {
		return pricing, true
	}

	return ClaudeModelPricing{}, false
}

// CalculateClaudeQuotaByRatio 使用动态倍率计算 Claude API 的配额消耗
//
// ==================== 计费原理说明 ====================
//
// 【背景】
// 之前的计费方式是硬编码各模型的价格，新增模型需要修改代码。
// 新方案通过前端配置的倍率动态计算，支持灵活配置新模型价格。
//
// 【倍率定义规则】（在前端"价格设置"页面配置）
//
//   - ModelRatio（模型基础价格倍率）= 官方输入价格($/1M tokens) / 2
//     例如: Claude 3.5 Sonnet 输入 $3/1M → ModelRatio = 3 / 2 = 1.5
//
//   - CompletionRatio（输出token价格倍率）= 官方输出价格 / 官方输入价格
//     例如: Claude 3.5 Sonnet 输出 $15, 输入 $3 → CompletionRatio = 15 / 3 = 5
//
//   - ImageInputRatio（图片输入倍率）= 图片输入价格 / 文字输入价格
//     如果模型图片输入价格与文字相同，则设为 1.0
//
//   - ImageOutputRatio（图片输出倍率）= 图片输出价格 / 文字输入价格
//     注意：这是相对于"输入"的倍率，不是相对于"输出文字"的倍率
//     例如: 图片输出 $6/1M, 文字输入 $0.10/1M → ImageOutputRatio = 60
//
//   - AudioInputRatio / AudioOutputRatio 同理
//
// 【配额计算公式】
//   - 输入文字配额 = inputTextTokens × ModelRatio
//   - 输入图片配额 = inputImageTokens × ModelRatio × ImageInputRatio
//   - 输入音频配额 = inputAudioTokens × ModelRatio × AudioInputRatio
//   - 输出文字配额 = outputTextTokens × ModelRatio × CompletionRatio
//   - 输出图片配额 = outputImageTokens × ModelRatio × ImageOutputRatio
//     ⚠️ 注意：输出图片不乘 CompletionRatio，因为 ImageOutputRatio 已经是相对于输入的完整倍率
//   - 输出音频配额 = outputAudioTokens × ModelRatio × AudioOutputRatio（同上）
//   - 思考配额 = thinkingTokens × ModelRatio × CompletionRatio
//
// 【最终配额】
//
//	总配额 = (各部分之和) / 1000000 × 2 × groupRatio × QuotaPerUnit
//	- 除以 1000000：因为倍率是基于每百万 token 的价格
//	- 乘以 2：因为 ModelRatio = 官方价格 / 2，需要还原真实价格
//	- groupRatio：用户分组的价格倍率
//	- QuotaPerUnit：系统配额单位（如 500000 表示 $1 = 500000 配额）
//
// 【计算示例】Gemini 2.5 Flash, 1000输入+500输出, groupRatio=1.0, QuotaPerUnit=500000
//
//	输入配额 = 1000 × 0.15 = 150
//	输出配额 = 500 × 0.15 × 8.33 = 624.75
//	总配额 = (150 + 624.75) / 1000000 × 2 × 1.0 × 500000 ≈ 775
func CalculateClaudeQuotaByRatio(usageMetadata *anthropic.Usage, modelName string, groupRatio float64) (int64, ClaudeTokenCost) {
	if usageMetadata == nil {
		return 0, ClaudeTokenCost{}
	}

	// 检测是否为批量处理
	isBatch := false

	// 计算详细费用
	cost := CalculateClaudeTokenCost(usageMetadata, modelName, isBatch)

	// 将美元费用转换为系统配额单位
	// 假设 1 美元 = QuotaPerUnit * 500 配额单位
	//quotaPerDollar := float64(config.QuotaPerUnit) * 500

	// 计算配额（包含groupRatio）
	quota := int64(cost.TotalCost * config.QuotaPerUnit * groupRatio)

	return quota, cost
}

// CalculateGeminiQuotaFromUsageMetadata 根据 UsageMetadata 计算配额
// 使用动态倍率计算各类型 token 的费用
func CalculateClaudeQuotaFromUsageMetadata(usageMetadata *anthropic.Usage, modelName string, groupRatio float64) (int64, ClaudeTokenCost) {
	return CalculateClaudeQuotaByRatio(usageMetadata, modelName, groupRatio)
}

// doNativeGeminiResponse 处理 Gemini 非流式响应
func doNativeClaudeResponse(c *gin.Context, resp *http.Response, meta *util.RelayMeta) (usageMetadata *anthropic.Usage, err *model.ErrorWithStatusCode) {
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
				Type:    "claude_api_error",
				Code:    fmt.Sprintf("status_%d", resp.StatusCode),
			},
			StatusCode: resp.StatusCode,
		}
	}

	// 解析 claude 原生响应
	var claudeResponse anthropic.Response
	if unmarshalErr := json.Unmarshal(responseBody, &claudeResponse); unmarshalErr != nil {
		return nil, openai.ErrorWrapper(unmarshalErr, "unmarshal_response_failed", http.StatusInternalServerError)
	}
	if claudeResponse.Usage.ServerToolUse != nil && claudeResponse.Usage.ServerToolUse.WebSearchRequests > 0 {
		c.Set("claude_web_search_requests", claudeResponse.Usage.ServerToolUse.WebSearchRequests)
	}
	util.IOCopyBytesGracefully(c, resp, responseBody)
	return claudeResponse.Usage, nil
}

// doNativeClaudeStreamResponse 处理 claude 流式响应
// claude 流式响应格式为 SSE，每行以 "data: " 开头，后跟 JSON 对象
func doNativeClaudeStreamResponse(c *gin.Context, resp *http.Response, meta *util.RelayMeta) (usageMetadata *anthropic.Usage, err *model.ErrorWithStatusCode) {
	defer util.CloseResponseBodyGracefully(resp)
	// 检查响应状态码

	// 设置 SSE 响应头
	//common.SetEventStreamHeaders(c)
	// 用于保存最后的 UsageMetadata
	var lastUsageMetadata = &anthropic.Usage{}
	var openaiErr *model.ErrorWithStatusCode

	helper.StreamScannerHandler(c, resp, meta, func(data string) bool {
		var claudeResponse anthropic.StreamResponse
		err := json.Unmarshal([]byte(data), &claudeResponse)
		if err != nil {
			openaiErr = openai.ErrorWrapper(err, "unmarshal_response_failed", http.StatusInternalServerError)
			return false
		}
		if claudeResponse.Type == "error" && claudeResponse.Error != nil {
			openaiErr = openai.ErrorWrapper(fmt.Errorf("claude error: %s", claudeResponse.Error.Message), "claude_api_error", http.StatusInternalServerError)
			return false
		}

		helper.ClaudeChunkData(c, claudeResponse, data)

		// 更新使用量统计
		if claudeResponse.Type == "message_start" {
			lastUsageMetadata = claudeResponse.Message.Usage
		} else if claudeResponse.Type == "content_block_delta" {
		} else if claudeResponse.Type == "message_delta" {
			// 最终的usage获取
			if claudeResponse.Usage.InputTokens > 0 {
				// 不叠加，只取最新的
				lastUsageMetadata.InputTokens = claudeResponse.Usage.InputTokens
			}
			if claudeResponse.Usage.InputTokens > 0 {
				// 不叠加，只取最新的
				lastUsageMetadata.InputTokens = claudeResponse.Usage.InputTokens
			}
			if claudeResponse.Usage.OutputTokens > 0 {
				// 不叠加，只取最新的
				lastUsageMetadata.OutputTokens = claudeResponse.Usage.OutputTokens
			}
			if claudeResponse.Usage.CacheCreationInputTokens > 0 {
				// 不叠加，只取最新的
				lastUsageMetadata.CacheCreationInputTokens = claudeResponse.Usage.CacheCreationInputTokens
			}
			if claudeResponse.Usage.CacheReadInputTokens > 0 {
				// 不叠加，只取最新的
				lastUsageMetadata.CacheReadInputTokens = claudeResponse.Usage.CacheReadInputTokens
			}
			if claudeResponse.Usage.ClaudeCacheCreation5mTokens > 0 {
				// 不叠加，只取最新的
				lastUsageMetadata.ClaudeCacheCreation5mTokens = claudeResponse.Usage.ClaudeCacheCreation5mTokens
			}
			if claudeResponse.Usage.ClaudeCacheCreation1hTokens > 0 {
				// 不叠加，只取最新的
				lastUsageMetadata.ClaudeCacheCreation1hTokens = claudeResponse.Usage.ClaudeCacheCreation1hTokens
			}
			if claudeResponse.Usage.ServerToolUse != nil && claudeResponse.Usage.ServerToolUse.WebSearchRequests > 0 {
				// 不叠加，只取最新的
				lastUsageMetadata.ServerToolUse.WebSearchRequests = claudeResponse.Usage.ServerToolUse.WebSearchRequests
			}
		} else if claudeResponse.Type == "content_block_start" {
		} else {
		}
		return true
	})

	if openaiErr != nil {
		return nil, openaiErr
	}

	return lastUsageMetadata, nil
}

// FormatClaudeCost 格式化输出 Claude 费用明细（用于日志或调试）
func FormatClaudeCost(cost ClaudeTokenCost) string {
	var builder strings.Builder

	builder.WriteString(fmt.Sprintf("Model: %s", cost.ModelName))

	// 特殊标识
	if cost.IsBatch {
		builder.WriteString(" [BATCH]")
	}
	if cost.IsLongContext {
		builder.WriteString(" [LONG_CONTEXT]")
	}
	builder.WriteString(" | ")

	// 输入费用明细
	builder.WriteString(fmt.Sprintf("Input: %d tokens ($%.6f)",
		cost.InputTextTokens, cost.InputCost))

	// 输出费用明细
	builder.WriteString(fmt.Sprintf(" | Output: %d tokens ($%.6f)",
		cost.OutputTextTokens, cost.OutputCost))

	// 缓存费用明细
	if cost.CacheWriteCost > 0 || cost.CacheReadCost > 0 {
		builder.WriteString(fmt.Sprintf(" | Cache: W$%.6f R$%.6f",
			cost.CacheWriteCost, cost.CacheReadCost))
	}

	// 工具费用明细
	if cost.ToolCost > 0 {
		builder.WriteString(fmt.Sprintf(" | Tools: $%.6f", cost.ToolCost))
	}

	// 总费用
	builder.WriteString(fmt.Sprintf(" | Total: %d tokens ($%.6f)",
		cost.TotalTokens, cost.TotalCost))

	return builder.String()
}

// ExampleCalculateClaudeCost 示例：如何计算 Claude API 的费用
// 此函数展示了完整的费用计算流程，包括缓存、批量处理、长上下文等场景
// func ExampleCalculateClaudeCost() {
// 	// 示例 1: 标准请求（Claude 3.5 Sonnet）
// 	fmt.Println("=== 示例 1: 标准请求 ===")
// 	usage1 := &anthropic.Usage{
// 		InputTokens:  10000,
// 		OutputTokens: 5000,
// 	}
// 	cost1 := CalculateClaudeTokenCost(usage1, "claude-3-5-sonnet", false)
// 	fmt.Printf("费用明细: %s\n", FormatClaudeCost(cost1))
// 	quota1, _ := CalculateClaudeQuotaByRatio(usage1, "claude-3-5-sonnet", 1.0)
// 	fmt.Printf("应计配额: %d\n\n", quota1)

// 	// 示例 2: 带缓存的请求
// 	fmt.Println("=== 示例 2: 带缓存的请求 ===")
// 	usage2 := &anthropic.Usage{
// 		InputTokens:              8000,
// 		OutputTokens:             3000,
// 		CacheCreationInputTokens: 2000, // 5分钟缓存写入
// 		CacheReadInputTokens:     1000, // 缓存读取
// 	}
// 	cost2 := CalculateClaudeTokenCost(usage2, "claude-3-5-sonnet", false)
// 	fmt.Printf("费用明细: %s\n", FormatClaudeCost(cost2))
// 	quota2, _ := CalculateClaudeQuotaByRatio(usage2, "claude-3-5-sonnet", 1.0)
// 	fmt.Printf("应计配额: %d\n\n", quota2)

// 	// 示例 3: 批量处理请求
// 	fmt.Println("=== 示例 3: 批量处理请求 ===")
// 	usage3 := &anthropic.Usage{
// 		InputTokens:  50000,
// 		OutputTokens: 25000,
// 		ServiceTier:  "batch", // 批量处理
// 	}
// 	cost3 := CalculateClaudeTokenCost(usage3, "claude-3-5-sonnet", true)
// 	fmt.Printf("费用明细: %s\n", FormatClaudeCost(cost3))
// 	quota3, _ := CalculateClaudeQuotaByRatio(usage3, "claude-3-5-sonnet", 1.0)
// 	fmt.Printf("应计配额: %d\n\n", quota3)

// 	// 示例 4: 长上下文请求（超过200K tokens）
// 	fmt.Println("=== 示例 4: 长上下文请求 ===")
// 	usage4 := &anthropic.Usage{
// 		InputTokens:  250000, // 超过200K阈值
// 		OutputTokens: 10000,
// 	}
// 	cost4 := CalculateClaudeTokenCost(usage4, "claude-3-5-sonnet", false)
// 	fmt.Printf("费用明细: %s\n", FormatClaudeCost(cost4))
// 	quota4, _ := CalculateClaudeQuotaByRatio(usage4, "claude-3-5-sonnet", 1.0)
// 	fmt.Printf("应计配额: %d\n\n", quota4)

// 	// 示例 5: 带工具使用的请求（网络搜索）
// 	fmt.Println("=== 示例 5: 带网络搜索工具的请求 ===")
// 	usage5 := &anthropic.Usage{
// 		InputTokens:  5000,
// 		OutputTokens: 2000,
// 		ServerToolUse: &anthropic.ServerToolUsage{
// 			WebSearchRequests: 2, // 2次网络搜索
// 		},
// 	}
// 	cost5 := CalculateClaudeTokenCost(usage5, "claude-3-5-sonnet", false)
// 	fmt.Printf("费用明细: %s\n", FormatClaudeCost(cost5))
// 	quota5, _ := CalculateClaudeQuotaByRatio(usage5, "claude-3-5-sonnet", 1.0)
// 	fmt.Printf("应计配额: %d\n", quota5)
// }
