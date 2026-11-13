package controller

import (
	"bufio"
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
	"github.com/songquanpeng/one-api/common/audit"
	"github.com/songquanpeng/one-api/common/config"
	commonhelper "github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/helper"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
	"github.com/songquanpeng/one-api/service"
)

// ensureGeminiContentsRole 确保 Gemini 请求体中的 contents 数组中每个元素都有 role 字段
// Vertex AI API 要求必须指定 role 字段（值为 "user" 或 "model"），而 Gemini 原生 API 可以省略
// 此函数用于在发送请求到 Vertex AI 之前自动补全缺失的 role 字段

// RelayOpenaiResponseNative 处理 openai 原生 API 请求
func RelayOpenaiResponseNative(c *gin.Context) *model.ErrorWithStatusCode {
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
	isModelMapped := false
	if len(meta.ModelMapping) > 0 {
		if mappedModel, ok := meta.ModelMapping[meta.OriginModelName]; ok && mappedModel != "" {
			meta.ActualModelName = mappedModel
			isModelMapped = true
		}
	}
	adaptor := helper.GetAdaptor(meta.APIType)
	if adaptor == nil {
		return openai.ErrorWrapper(fmt.Errorf("invalid api type: %d", meta.APIType), "invalid_api_type", http.StatusBadRequest)
	}
	//logger.SysLog(fmt.Sprintf("openai response request: %s", string(originRequestBody)))
	var openaiResponseRequest openai.OpeanaiResaponseRequest
	if err := json.Unmarshal(originRequestBody, &openaiResponseRequest); err != nil {
		return openai.ErrorWrapper(fmt.Errorf("failed to parse claude request: %w", err), "failed_to_parse_request", http.StatusInternalServerError)
	}

	// 如果模型发生了重定向，替换请求体中的 model 字段
	if isModelMapped {
		openaiResponseRequest.Model = meta.ActualModelName
		newBody, err := json.Marshal(openaiResponseRequest)
		if err != nil {
			return openai.ErrorWrapper(err, "json_marshal_failed", http.StatusInternalServerError)
		}
		originRequestBody = newBody
		logger.Infof(ctx, "model mapping applied: %s -> %s", meta.OriginModelName, meta.ActualModelName)
	}

	meta.IsStream = openaiResponseRequest.Stream
	audit.SetMeta(c, openaiResponseRequest.Stream, meta.ActualModelName)
	audit.SetConvertedBody(c, string(originRequestBody))
	// 计算预消费配额
	groupRatio := util.GetBillingGroupRatio(c, group)
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
	// 来源回退请求始终保留完整历史；其它请求沿用 Azure 密文错误的同渠道降级逻辑。
	resp, err := doOpenaiResponseRequest(c, meta, adaptor, originRequestBody)
	if err != nil {
		return openai.ErrorWrapper(err, "failed_to_send_request", http.StatusBadGateway)
	}

	var usageMetadata *openai.ResponseUsage
	var openaiErr *model.ErrorWithStatusCode

	// AWS adaptor 的 DoRequest 返回 nil, nil，因为 AWS SDK 直接处理请求
	// 这种情况下应该使用 DoResponse 来处理
	if meta.IsStream {
		usageMetadata, openaiErr = doNativeOpenaiResponseStream(c, resp, meta)
	} else {
		usageMetadata, openaiErr = doNativeOpenaiResponse(c, resp, meta)
	}

	if openaiErr != nil {
		return openaiErr
	}

	overrides := selectOpenAIPrices(modelName, c.GetString(openai.ResponseServiceTierContextKey), openAIResponsePricing, meta)
	// 基础档位也保留本次计算返回的价格，供有效缓存分项的异步账单复用。
	actualQuota, _, prices := calculateOpenaiResponseQuotaWithOverrides(usageMetadata, modelName, groupRatio, overrides)

	// 记录消费日志
	duration := time.Since(startTime).Seconds()
	tokenName := c.GetString("token_name")
	promptTokens := usageMetadata.InputTokens
	completionTokens := usageMetadata.OutputTokens
	totalTokens := usageMetadata.TotalTokens
	//cachedTokens := usageMetadata.CacheCreationInputTokens + usageMetadata.CacheReadInputTokens

	// 计算首字延迟：优先使用总请求开始时间（包含重试），否则使用 meta 中的时间
	var firstWordLatency float64
	if totalStartTime, exists := c.Get("total_request_start_time"); exists {
		if startTime, ok := totalStartTime.(time.Time); ok && !meta.FirstResponseTime.IsZero() {
			// 使用总请求开始时间到首字响应时间的间隔
			firstWordLatency = meta.FirstResponseTime.Sub(startTime).Seconds()
		} else {
			firstWordLatency = meta.GetFirstWordLatency()
		}
	} else {
		firstWordLatency = meta.GetFirstWordLatency()
	}

	cachedTokens := 0
	cacheWriteTokens := 0
	if usageMetadata != nil && usageMetadata.InputTokensDetails != nil {
		cachedTokens = usageMetadata.InputTokensDetails.CachedTokens
		cacheWriteTokens = usageMetadata.InputTokensDetails.CacheWriteTokens
	}

	if overrides.applies() || overrides.hasCacheWritePrices() || prices.cacheWrite12hRatio != nil {
		go recordOpenaiResponseConsumptionWithPrices(commonhelper.DetachCancel(ctx), userId, channelId, tokenId, modelName, tokenName, promptTokens, completionTokens, totalTokens, cachedTokens, cacheWriteTokens, actualQuota, c.Request.RequestURI, duration, meta.IsStream, c.Copy(), usageMetadata, firstWordLatency, groupRatio, modelRatio, &prices)
	} else {
		go recordOpenaiResponseConsumption(commonhelper.DetachCancel(ctx), userId, channelId, tokenId, modelName, tokenName, promptTokens, completionTokens, totalTokens, cachedTokens, cacheWriteTokens, actualQuota, c.Request.RequestURI, duration, meta.IsStream, c.Copy(), usageMetadata, firstWordLatency, groupRatio, modelRatio)
	}

	return nil
}

// recordOpenaiResponseConsumption 记录 OpenAI Response API 消费日志
func recordOpenaiResponseConsumption(ctx context.Context, userId, channelId, tokenId int, modelName, tokenName string, promptTokens, completionTokens, totalTokens, cachedTokens, cacheWriteTokens int, quota int64, requestPath string, duration float64, isStream bool, c *gin.Context, usageMetadata *openai.ResponseUsage, firstWordLatency float64, groupRatio float64, modelRatio float64) {
	recordOpenaiResponseConsumptionWithPrices(ctx, userId, channelId, tokenId, modelName, tokenName, promptTokens, completionTokens, totalTokens, cachedTokens, cacheWriteTokens, quota, requestPath, duration, isStream, c, usageMetadata, firstWordLatency, groupRatio, modelRatio, nil)
}

// 显式档位、自动倍率或有效缓存分项使用计算时的价格快照；旧总量路径保持原记录方式。
func recordOpenaiResponseConsumptionWithPrices(ctx context.Context, userId, channelId, tokenId int, modelName, tokenName string, promptTokens, completionTokens, totalTokens, cachedTokens, cacheWriteTokens int, quota int64, requestPath string, duration float64, isStream bool, c *gin.Context, usageMetadata *openai.ResponseUsage, firstWordLatency float64, groupRatio float64, modelRatio float64, prices *openAITokenPrices) {
	err := dbmodel.PostConsumeTokenQuota(tokenId, quota)
	if err != nil {
		logger.Error(ctx,
			"error consuming token remain quota: "+err.Error())
	}

	err = dbmodel.CacheUpdateUserQuota(ctx, userId)
	if err != nil {
		logger.Error(ctx,
			"error update user quota cache: "+err.Error())
	}

	dbmodel.UpdateUserUsedQuotaAndRequestCount(userId, quota)
	dbmodel.UpdateChannelUsedQuota(channelId, quota)

	// 记录日志
	logContent := buildOpenaiResponseLogContent(requestPath, modelName, promptTokens)
	referer := c.Request.Header.Get("HTTP-Referer")
	title := c.Request.Header.Get("X-Title")

	// 提取用量详情并格式化为统一格式
	usageDetails := extractOpenaiReseponseNativeUsageDetails(usageMetadata)
	if usageDetails != nil {
		usageDetails.ServiceTier = c.GetString(openai.ResponseServiceTierContextKey)
		// 复用已校验或补全的创建总量，使计费明细与用量明细一致。
		if usageDetails.CacheCreationTotalTokens != nil {
			cacheWriteTokens = int(*usageDetails.CacheCreationTotalTokens)
		}
	}
	// 提取渠道历史信息 (adminInfo)
	adminInfo := extractAdminInfoFromContext(c)
	// 构建 other 字段，包含 adminInfo 和 usageDetails
	other := buildOpenaiResponseOtherInfoWithUsageDetails(adminInfo, usageDetails)
	// 追加模型重定向信息
	originModel := c.GetString("original_model")
	modelMapping := c.GetStringMapString("model_mapping")
	if mappedModel, ok := modelMapping[originModel]; ok && mappedModel != "" {
		other = appendModelMappingInfo(other, originModel, mappedModel)
	}
	// 追加计费详情
	var completionRatio float64
	tierPricing := openAIPriceOverrides{}.pricingInfo()
	if prices != nil {
		modelRatio, completionRatio = prices.modelRatio, prices.completionRatio
		tierPricing = prices.tierPricing
	} else {
		completionRatio = common.GetCompletionRatio(modelName)
	}
	billingDetails := map[string]interface{}{
		"billing_type":     "token",
		"model_ratio":      modelRatio,
		"completion_ratio": completionRatio,
		"group_ratio":      groupRatio,
	}
	tierPricing.appendBillingDetails(billingDetails)
	billingDetails = enrichBillingDetailsFromContext(c, billingDetails)
	if cachedTokens > 0 {
		billingDetails["cached_tokens"] = cachedTokens
		if prices != nil {
			billingDetails["cache_ratio"] = prices.cacheRatio
			billingDetails["cache_read_ratio"] = prices.cacheRatio
		} else {
			billingDetails["cache_ratio"] = common.GetCacheRatio(modelName)
			billingDetails["cache_read_ratio"] = common.GetCacheRatio(modelName)
		}
	}
	if cacheWriteTokens > 0 {
		billingDetails["cache_write_tokens"] = cacheWriteTokens
		if prices != nil {
			billingDetails["cache_write_ratio"] = prices.cacheWriteRatio
			billingDetails["cache_creation_ratio"] = prices.cacheWriteRatio
		} else {
			tier := c.GetString(openai.ResponseServiceTierContextKey)
			ratio, _ := resolveOpenAICacheWritePrices(modelName, lookupOpenAIPriceOverrides(modelName, tier, openAIResponsePricing))
			billingDetails["cache_write_ratio"] = ratio
			billingDetails["cache_creation_ratio"] = ratio
		}
	}
	if prices != nil && prices.cacheWrite12hRatio != nil {
		billingDetails["cache_write_30m_ratio"] = prices.cacheWriteRatio
		billingDetails["cache_write_12h_ratio"] = *prices.cacheWrite12hRatio
	}
	other = appendBillingDetails(ctx, other, billingDetails)
	other = util.AppendRetryHistoryOther(c, other, duration)

	dbmodel.RecordConsumeLogWithOtherAndRequestID(ctx, userId, channelId, promptTokens, completionTokens, modelName,
		tokenName, quota, logContent, duration, title, referer, isStream, firstWordLatency, other, c.GetHeader("X-Request-ID"), 0, c.GetString("x_response_id"))
}

func buildOpenaiResponseLogContent(requestPath string, modelName string, inputTokens int) string {
	longMults := common.GetLongContextMultipliers(modelName, inputTokens)
	return fmt.Sprintf("openai response API %s，long输入倍率 %.1f，long输出倍率 %.1f", requestPath, longMults.InputMultiplier, longMults.OutputMultiplier)
}

// buildClaudeOtherInfoWithUsageDetails 构建包含 adminInfo 和 Claude usageDetails 的 otherInfo 字符串
func buildOpenaiResponseOtherInfoWithUsageDetails(adminInfo string, usageDetails *OpenaiReseponseUsageDetails) string {
	var parts []string

	if adminInfo != "" {
		parts = append(parts, adminInfo)
	}

	if usageDetails != nil {
		var payload interface{} = usageDetails
		if usageDetails.CacheCreationTotalTokens != nil {
			// 仅 12h 创建时也明确记录旧字段为 0，不改变旧日志的省略规则。
			payload = struct {
				*OpenaiReseponseUsageDetails
				CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			}{usageDetails, usageDetails.CacheCreationInputTokens}
		}
		if detailsBytes, err := json.Marshal(payload); err == nil {
			parts = append(parts, fmt.Sprintf("usageDetails:%s", string(detailsBytes)))
		}
	}

	return strings.Join(parts, ";")
}

// OpenaiReseponseUsageDetails 用于存储从 Openai Response Usage 提取的详细使用信息
type OpenaiReseponseUsageDetails struct {
	ServiceTier              string `json:"service_tier,omitempty"`
	InputTokens              int    `json:"input_tokens"`
	OutputTokens             int    `json:"output_tokens"`
	TotalTokens              int    `json:"total_tokens"`
	CacheReadInputTokens     int    `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int    `json:"cache_creation_input_tokens,omitempty"` // 旧字段记录 30m 创建量
	CacheCreation12hTokens   *int64 `json:"cache_creation_12h_tokens,omitempty"`
	CacheCreationTotalTokens *int64 `json:"cache_creation_total_tokens,omitempty"`
	ReasoningTokens          int    `json:"reasoning_tokens"`
}

// extractOpenaiResponseNativeUsageDetails 从 Openai Response Usage 提取详细的使用信息（用于 native 接口）
func extractOpenaiReseponseNativeUsageDetails(usageMetadata *openai.ResponseUsage) *OpenaiReseponseUsageDetails {
	if usageMetadata == nil {
		return nil
	}

	details := &OpenaiReseponseUsageDetails{
		InputTokens:  usageMetadata.InputTokens,
		OutputTokens: usageMetadata.OutputTokens,
		TotalTokens:  usageMetadata.TotalTokens,
	}
	// 创建缓存和推理缓存
	if usageMetadata.InputTokensDetails != nil {
		details.CacheReadInputTokens = usageMetadata.InputTokensDetails.CachedTokens
		details.CacheCreationInputTokens = usageMetadata.InputTokensDetails.CacheWriteTokens
		cacheWriteUsage, _ := util.SplitOpenAICacheWriteTokens(usageMetadata.InputTokens, *usageMetadata.InputTokensDetails)
		if cacheWriteUsage.Total > 0 || cacheWriteUsage.HasBreakdown() {
			// 旧总量回退按 30m 记录；有效分项使用上游明确报告的 30m 数量。
			if cacheWriteUsage.HasBreakdown() {
				details.CacheCreationInputTokens = int(cacheWriteUsage.Short)
			}
			longTokens := cacheWriteUsage.LongTokens()
			details.CacheCreation12hTokens = &longTokens
			details.CacheCreationTotalTokens = &cacheWriteUsage.Total
		}
	}
	if usageMetadata.OutputTokensDetails != nil {
		details.ReasoningTokens = usageMetadata.OutputTokensDetails.ReasoningTokens
	}
	return details
}

func CalculateOpenaiResponseQuotaFromRequest(requestBody []byte, modelName string, ratio float64) (int64, int, error) {
	return 0, 0, nil
}

// ClaudeTokenCost Claude API 的费用明细（使用动态倍率计算）
type OpenaiResponseTokenCost struct {
	// 输入部分 token 数量
	InputTextTokens int // 输入文字 token 数量
	// 输出部分 token 数量
	OutputTextTokens int // 输出文字 token 数量
	// 缓存相关 token 数量
	CacheTokens      int // 缓存读取token 数量（总计）
	CacheWriteTokens int // 缓存写入token 数量
	ReasoningTokens  int // 推理token 数量
	TotalTokens      int // 总 token 数量
	ModelName        string
}

// CalculateOpenaiResponseQuotaByRatio 使用动态倍率计算 Openai Response API 的配额消耗
//
// ==================== 计费原理说明 ====================
//
// 【背景】
// 通过前端配置的倍率动态计算，支持灵活配置新模型价格。
//
// 【倍率定义规则】（在前端"价格设置"页面配置）
//
//   - ModelRatio（模型基础价格倍率）= 官方输入价格($/1M tokens) / 2
//     例如: Claude 3.5 Sonnet 输入 $3/1M → ModelRatio = 3 / 2 = 1.5
//
//   - CompletionRatio（输出token价格倍率）= 官方输出价格 / 官方输入价格
//     例如: Claude 3.5 Sonnet 输出 $15, 输入 $3 → CompletionRatio = 15 / 3 = 5
//
// 【缓存计费规则】
//   - 30m 和未拆分创建量：输入价格 × 配置倍率，未配置沿用原创建倍率
//   - 响应明确报告的 12h 创建量：输入价格 × 配置倍率，未配置使用 2.5
//   - 缓存读取：输入价格 × 原选定 CacheRatio
//
// 【配额计算公式】
//   - 输入配额 = (inputTokens - cacheReadTokens - cacheWriteTokens) × ModelRatio
//   - 输出配额 = outputTokens × ModelRatio × CompletionRatio
//   - 完整有效明细的 30m 创建配额 = cache30mTokens × ModelRatio × CacheWriteRatio
//   - 12h 创建配额 = cache12hTokens × ModelRatio × CacheWrite12hRatio
//   - 明细缺失或不一致时回退：创建配额 = cacheWriteTokens × ModelRatio × CacheWriteRatio
//   - 缓存读取配额 = cacheReadTokens × ModelRatio × CacheRatio
//   - 输入及缓存应用原长上下文输入倍率，输出应用原长上下文输出倍率
//
// 【最终配额】
//
//	总配额 = (各部分之和) / 1000000 × 2 × groupRatio × QuotaPerUnit
func CalculateOpenaiResponseQuotaByRatio(usageMetadata *openai.ResponseUsage, modelName string, groupRatio float64) (int64, OpenaiResponseTokenCost) {
	quota, cost, _ := calculateOpenaiResponseQuotaWithOverrides(usageMetadata, modelName, groupRatio, openAIPriceOverrides{})
	return quota, cost
}

func calculateOpenaiResponseQuotaWithOverrides(usageMetadata *openai.ResponseUsage, modelName string, groupRatio float64, overrides openAIPriceOverrides) (int64, OpenaiResponseTokenCost, openAITokenPrices) {
	cost := OpenaiResponseTokenCost{
		ModelName: modelName,
	}

	if usageMetadata == nil {
		return 0, cost, openAITokenPrices{}
	}

	// 提取 token 数量
	cost.InputTextTokens = usageMetadata.InputTokens
	cost.OutputTextTokens = usageMetadata.OutputTokens
	cost.TotalTokens = usageMetadata.TotalTokens

	// 仅拆分响应已经提供的缓存创建量，不改变响应获取、估算及档位选择。
	var cacheWriteUsage util.OpenAICacheWriteUsage
	if usageMetadata.InputTokensDetails != nil {
		var cacheErr error
		cacheWriteUsage, cacheErr = util.SplitOpenAICacheWriteTokens(usageMetadata.InputTokens, *usageMetadata.InputTokensDetails)
		if cacheErr != nil {
			detailsJSON, _ := json.Marshal(usageMetadata.InputTokensDetails)
			logger.SysError(fmt.Sprintf("openai_response_cache_creation_invalid model=%s details=%s error=%+v", modelName, detailsJSON, cacheErr))
		}
		cost.CacheTokens = usageMetadata.InputTokensDetails.CachedTokens
		cost.CacheWriteTokens = int(cacheWriteUsage.Total)
	}
	if usageMetadata.OutputTokensDetails != nil {
		cost.ReasoningTokens = usageMetadata.OutputTokensDetails.ReasoningTokens
	}

	// ========== 获取各类型的倍率 ==========
	modelRatio := overrides.modelRatio.resolve(modelName, common.GetModelRatio)
	if overrides.defaultMultiplier > 0 {
		modelRatio *= overrides.defaultMultiplier
	}
	completionRatio := overrides.completionRatio.resolve(modelName, common.GetCompletionRatio)
	cacheRatio := overrides.cacheRatio.resolve(modelName, common.GetCacheRatio)
	cacheWriteRatio, cacheWrite12hRatio := resolveOpenAICacheWritePrices(modelName, overrides)
	// 阈值按基础模型判断，系数作用于本次选出的价格。
	longMults := common.GetLongContextMultipliers(modelName, cost.InputTextTokens)

	// 打印倍率信息
	logger.SysLog(fmt.Sprintf("[openairesponse计费] 模型: %s, 倍率配置: ModelRatio=%.4f, CompletionRatio=%.4f, CacheRatio=%.4f, CacheWriteRatio=%.4f, Long输入倍率=%.1f, Long输出倍率=%.1f",
		modelName, modelRatio, completionRatio, cacheRatio, cacheWriteRatio, longMults.InputMultiplier, longMults.OutputMultiplier))

	// 打印 token 数量
	logger.SysLog(fmt.Sprintf("[openairesponse计费] Token数量: 输入=%d, 输出=%d, 输入缓存读取=%d, 缓存写入=%d, 推理缓存=%d, 总计=%d",
		cost.InputTextTokens, cost.OutputTextTokens,
		cost.CacheTokens, cost.CacheWriteTokens, cost.ReasoningTokens,
		cost.TotalTokens))

	// ========== 计算各部分的等效 ratio tokens ==========
	// 真正的输入 token = 总输入 token - 缓存读取 token - 缓存写入 token
	realInputTokens := cost.InputTextTokens - cost.CacheTokens - cost.CacheWriteTokens
	if realInputTokens < 0 {
		realInputTokens = cost.InputTextTokens
	}

	// 输入部分（非缓存）：tokens × modelRatio × longInputMultiplier
	inputTextQuota := float64(realInputTokens) * modelRatio * longMults.InputMultiplier

	// 缓存读取部分：cacheTokens × modelRatio × cacheRatio × longInputMultiplier
	cacheQuota := float64(cost.CacheTokens) * modelRatio * cacheRatio * longMults.InputMultiplier

	var cacheWriteQuota float64
	if cacheWriteUsage.HasBreakdown() {
		// 30m/12h 直接使用响应分项，不从创建总量反推。
		cache30mQuota := float64(cacheWriteUsage.Short) * modelRatio * cacheWriteRatio * longMults.InputMultiplier
		cache12hQuota := float64(cacheWriteUsage.LongTokens()) * modelRatio * cacheWrite12hRatio * longMults.InputMultiplier
		cacheWriteQuota = cache30mQuota + cache12hQuota
	} else {
		// 无完整有效明细时单独回退原总量计费。
		cacheWriteQuota = float64(cost.CacheWriteTokens) * modelRatio * cacheWriteRatio * longMults.InputMultiplier
	}

	// 输出部分：tokens × modelRatio × completionRatio × longOutputMultiplier
	outputTextQuota := float64(cost.OutputTextTokens) * modelRatio * completionRatio * longMults.OutputMultiplier

	// 图片部分计费
	imageGenerationCallQuota := float64(0)

	// websearchtoolcall计费
	webSearchToolCallQuota := float64(0)

	// 计算最终配额
	quota := int64((inputTextQuota + cacheQuota + cacheWriteQuota + outputTextQuota + imageGenerationCallQuota + webSearchToolCallQuota) / 1000000 * 2 * groupRatio * config.QuotaPerUnit)

	prices := openAITokenPrices{
		modelRatio: modelRatio, completionRatio: completionRatio,
		cacheRatio: cacheRatio, cacheWriteRatio: cacheWriteRatio,
		tierPricing: overrides.pricingInfo(),
	}
	if cacheWriteUsage.HasBreakdown() {
		prices.cacheWrite12hRatio = &cacheWrite12hRatio
	}
	return quota, cost, prices
}

// CalculateResponseQuotaFromUsageMetadata 根据 UsageMetadata 计算配额
// 使用动态倍率计算各类型 token 的费用
func CalculateResponseQuotaFromUsageMetadata(usageMetadata *openai.ResponseUsage, modelName string, groupRatio float64) (int64, OpenaiResponseTokenCost) {
	return CalculateOpenaiResponseQuotaByRatio(usageMetadata, modelName, groupRatio)
}

// doNativeGeminiResponse 处理 Gemini 非流式响应
// parseUpstreamErrorMessage 解析上游错误响应，提取具体的错误消息
// 支持多种格式：{"error":{"message":"..."}} 或 {"message":"..."} 或纯文本

func doNativeOpenaiResponse(c *gin.Context, resp *http.Response, meta *util.RelayMeta) (usageMetadata *openai.ResponseUsage, err *model.ErrorWithStatusCode) {
	defer util.CloseResponseBodyGracefully(resp)

	// 读取响应体
	responseBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, openai.ErrorWrapper(readErr, "read_response_body_failed", http.StatusInternalServerError)
	}

	// 检查响应状态码
	if resp.StatusCode != http.StatusOK {
		// 获取系统内部的 requestID
		requestID := c.GetHeader("X-Request-ID")
		message, errType := parseUpstreamErrorMessage(responseBody, requestID)
		return nil, &model.ErrorWithStatusCode{
			Error: model.Error{
				Message: message,
				Type:    errType,
				Code:    fmt.Sprintf("status_%d", resp.StatusCode),
			},
			StatusCode: resp.StatusCode,
		}
	}

	audit.SetUpstreamResponse(c, responseBody)

	// 解析 openai response 原生响应
	var openaiResponse openai.OpenaiResaponseResponse
	if unmarshalErr := json.Unmarshal(responseBody, &openaiResponse); unmarshalErr != nil {
		return nil, openai.ErrorWrapper(unmarshalErr, "unmarshal_response_failed", http.StatusInternalServerError)
	}
	origin := dbmodel.RelayResponseOrigin(c, resp, openaiResponse.ID)
	dbmodel.CaptureCurrentResponseOrigin(c, origin)
	ids := make([]string, 0, len(openaiResponse.Output))
	for _, item := range openaiResponse.Output {
		ids = append(ids, item.ID)
	}
	if err := dbmodel.RegisterResponseOrigin(origin, ids...); err != nil {
		logger.Warnf(c.Request.Context(), "response origin registration failed: %v", err)
	}
	c.Set("x_response_id", openaiResponse.ID)
	util.IOCopyBytesGracefully(c, resp, responseBody)
	service.RecordGeminiResponseSignatures(c, responseBody)

	c.Set(openai.ResponseServiceTierContextKey, openaiResponse.ServiceTier)

	return openaiResponse.Usage, nil
}

// doNativeOpenaiResponseStream 处理 openai response 流式响应
// claude 流式响应格式为 SSE，每行以 "data: " 开头，后跟 JSON 对象
func doNativeOpenaiResponseStream(c *gin.Context, resp *http.Response, meta *util.RelayMeta) (usageMetadata *openai.ResponseUsage, err *model.ErrorWithStatusCode) {
	defer util.CloseResponseBodyGracefully(resp)
	c.Set(openai.ResponseServiceTierContextKey, "")

	// 检查响应状态码 - 如果不是200，读取错误信息并返回
	if resp.StatusCode != http.StatusOK {
		responseBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, openai.ErrorWrapper(readErr, "read_error_response_failed", http.StatusInternalServerError)
		}
		// 获取系统内部的 requestID
		requestID := c.GetHeader("X-Request-ID")
		message, errType := parseUpstreamErrorMessage(responseBody, requestID)
		return nil, &model.ErrorWithStatusCode{
			Error: model.Error{
				Message: message,
				Type:    errType,
				Code:    fmt.Sprintf("status_%d", resp.StatusCode),
			},
			StatusCode: resp.StatusCode,
		}
	}

	// 缓冲流的前几个事件，检测流内错误（如 insufficient_quota）
	// 如果检测到错误，直接返回让上层重试逻辑通过 RetryKeywords 判断是否重试
	buffered, streamErr := bufferStreamPrefix(resp.Body)
	// The prefix may contain a generated ID even when no response is published.
	prefixScanner := bufio.NewScanner(bytes.NewReader(buffered))
	prefixScanner.Buffer(make([]byte, helper.InitialScannerBufferSize), helper.MaxScannerBufferSize)
	for prefixScanner.Scan() {
		line := prefixScanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var event openai.OpenaiResponseStreamResponse
		if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event) == nil && event.Response != nil && event.Response.ID != "" {
			c.Set("x_response_id", event.Response.ID)
			dbmodel.CaptureCurrentResponseOrigin(c, dbmodel.RelayResponseOrigin(c, resp, event.Response.ID))
		}
	}
	if streamErr != nil {
		logger.Warnf(c.Request.Context(), "stream prefix error detected: %s", streamErr.Error.Message)
		return nil, streamErr
	}
	// 重组 resp.Body：已缓冲的字节 + 剩余流，保留原始 body 的 Close 能力
	if len(buffered) > 0 {
		originalBody := resp.Body
		resp.Body = &combinedReadCloser{
			Reader: io.MultiReader(bytes.NewReader(buffered), originalBody),
			Closer: originalBody,
		}
	}

	// 用于保存最后的 UsageMetadata 和文本内容
	var lastUsageMetadata = &openai.ResponseUsage{}
	var openaiErr *model.ErrorWithStatusCode
	var fullText strings.Builder // 累积完整文本
	webSearchToolCallCount := 0
	sawTerminalEvent := false
	responseIdCached := false
	responseID := ""
	origin := dbmodel.RelayResponseOrigin(c, resp, "")
	audit.WrapUpstreamBody(c, resp)
	helper.StreamScannerHandlerOwnedWithEvent(c, resp, meta, func(eventType, data string) bool {
		var streamResponse openai.OpenaiResponseStreamResponse
		err := json.Unmarshal([]byte(data), &streamResponse)
		if err != nil {
			openaiErr = openai.ErrorWrapper(err, "unmarshal_response_failed", http.StatusInternalServerError)
			return false
		}
		if streamResponse.Type == "" {
			streamResponse.Type = eventType
		}
		if streamResponse.Response != nil && streamResponse.Response.ServiceTier != "" {
			c.Set(openai.ResponseServiceTierContextKey, streamResponse.Response.ServiceTier)
		}
		if streamResponse.Response != nil && streamResponse.Response.ID != "" {
			responseID = streamResponse.Response.ID
			origin.ResponseID = responseID
			c.Set("x_response_id", responseID)
			dbmodel.CaptureCurrentResponseOrigin(c, origin)
		}
		ids := []string{}
		if streamResponse.Response != nil {
			for _, item := range streamResponse.Response.Output {
				ids = append(ids, item.ID)
			}
		}
		if streamResponse.Item != nil && streamResponse.Item.ID != "" {
			ids = append(ids, streamResponse.Item.ID)
		}
		if responseID != "" && (!responseIdCached || len(ids) > 0 || streamResponse.Type == "response.completed") {
			if err := dbmodel.RegisterResponseOrigin(origin, ids...); err != nil {
				responseIdCached = false
				logger.Warnf(c.Request.Context(), "stream origin registration failed: %v", err)
			} else {
				responseIdCached = true
			}
		}
		helper.OpenaiResponseChunkData(c, streamResponse, data)
		service.RecordGeminiResponseSignatures(c, []byte(data))

		switch streamResponse.Type {
		case "error", "response.failed":
			openaiErr = parseResponseStreamError(data, streamResponse.Type)
			meta.StreamStatus.RecordError(openaiErr.Message)
			return false
		case "response.completed", "response.incomplete":
			sawTerminalEvent = true
			if streamResponse.Response != nil {
				if streamResponse.Response.Usage != nil {
					lastUsageMetadata = streamResponse.Response.Usage
				}

				if len(streamResponse.Response.Output) > 0 {
					for _, output := range streamResponse.Response.Output {
						if output.Type == "image_generation_call" {
							c.Set("image_generation_call", true)
							c.Set("image_generation_call_quality", output.Quality)
							c.Set("image_generation_call_size", output.Size)
						}
					}
				}
			}
		case "response.output_text.delta":
			// 处理输出文本
			fullText.WriteString(streamResponse.Delta)
		case "response.output_item.done":
			// 函数调用处理
			if streamResponse.Item != nil {
				switch streamResponse.Item.Type {
				case "web_search_call":
					webSearchToolCallCount++
					c.Set("web_search_tool_call_count", webSearchToolCallCount)
				}
			}
		}
		return true
	})
	if openaiErr != nil {
		return lastUsageMetadata, openaiErr
	}
	if meta.StreamStatus.HasErrors() {
		return lastUsageMetadata, openai.ErrorWrapper(fmt.Errorf("%s", meta.StreamStatus.Summary()), "stream_read_failed", http.StatusBadGateway)
	}
	if !sawTerminalEvent {
		openaiErr = openai.ErrorWrapper(fmt.Errorf("upstream response stream ended before completion: %s", meta.StreamStatus.Summary()), "stream_incomplete", http.StatusBadGateway)
		meta.StreamStatus.RecordError(openaiErr.Message)
		logger.Warn(c.Request.Context(), openaiErr.Message)
		return lastUsageMetadata, openaiErr
	}
	if lastUsageMetadata.OutputTokens == 0 {
		// 计算输出文本的 token 数量
		tempStr := fullText.String()
		if len(tempStr) > 0 {
			// 非正常结束，使用输出文本的 token 数量
			completionTokens := openai.CountTokenText(tempStr, meta.ActualModelName)
			lastUsageMetadata.OutputTokens = completionTokens
			lastUsageMetadata.TotalTokens = lastUsageMetadata.InputTokens + lastUsageMetadata.OutputTokens
		}
	}
	//特殊情况预估输入token 用于补全输入token

	if lastUsageMetadata.InputTokens == 0 && lastUsageMetadata.OutputTokens != 0 {
	}

	return lastUsageMetadata, nil
}

// combinedReadCloser 包装 MultiReader 并委托 Close 给原始 body
type combinedReadCloser struct {
	io.Reader
	io.Closer
}

// bufferStreamPrefix 同步读取 SSE 流的前几个事件，检测流内错误。
// 如果发现错误事件（如 insufficient_quota），返回 ErrorWithStatusCode 以触发上层重试。
// 如果流正常，返回已缓冲的字节用于 MultiReader 重组。
// 注意：此函数同步阻塞，依赖上游在合理时间内发送前几个事件（由 HTTP transport 超时保证）。
func bufferStreamPrefix(body io.Reader) (bufferedData []byte, streamErr *model.ErrorWithStatusCode) {
	var buf bytes.Buffer
	scanner := bufio.NewScanner(io.TeeReader(body, &buf))
	// response.created/in_progress 会回显全部工具定义，合法事件可超过 64 KiB。
	// 与后续流扫描使用同一上限，且任何读取错误都必须进入失败路径。
	scanner.Buffer(make([]byte, helper.InitialScannerBufferSize), helper.MaxScannerBufferSize)

	var currentEventType string
	var dataEventCount int

	for scanner.Scan() {
		line := scanner.Text()

		// 空行表示 SSE 消息边界，重置事件类型
		if line == "" {
			currentEventType = ""
			continue
		}

		if strings.HasPrefix(line, "event:") {
			currentEventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}

		if !strings.HasPrefix(line, "data:") {
			continue
		}

		dataEventCount++
		dataStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		eventType := currentEventType
		if eventType == "" {
			var event struct {
				Type string `json:"type"`
			}
			if json.Unmarshal([]byte(dataStr), &event) == nil {
				eventType = event.Type
			}
		}
		if streamErr := parseResponseStreamError(dataStr, eventType); streamErr != nil {
			return buf.Bytes(), streamErr
		}

		if isContentProducingEvent(eventType) || dataEventCount >= 5 {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return buf.Bytes(), openai.ErrorWrapper(err, "stream_prefix_read_failed", http.StatusBadGateway)
	}

	return buf.Bytes(), nil
}

// 前缀与后续流共用错误解析，保留上游错误字段，兼容嵌套和扁平 error。
func parseResponseStreamError(data, eventType string) *model.ErrorWithStatusCode {
	if eventType != "error" && eventType != "response.failed" {
		return nil
	}
	var event struct {
		Error    *model.Error `json:"error"`
		Response *struct {
			Error *model.Error `json:"error"`
		} `json:"response"`
		Message string `json:"message"`
		Code    any    `json:"code"`
		Param   string `json:"param"`
	}
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return openai.ErrorWrapper(err, "unmarshal_stream_error_failed", http.StatusBadGateway)
	}
	upstreamErr := event.Error
	if eventType == "response.failed" && event.Response != nil {
		upstreamErr = event.Response.Error
	}
	if upstreamErr == nil {
		upstreamErr = &model.Error{Message: event.Message, Code: event.Code, Param: event.Param}
	}
	if upstreamErr.Message == "" {
		upstreamErr.Message = "upstream response stream failed"
	}
	if upstreamErr.Type == "" {
		upstreamErr.Type, _ = upstreamErr.Code.(string)
		if upstreamErr.Type == "" {
			upstreamErr.Type = "upstream_error"
		}
	}
	return &model.ErrorWithStatusCode{Error: *upstreamErr, StatusCode: http.StatusTooManyRequests}
}

func isContentProducingEvent(eventType string) bool {
	switch eventType {
	case "response.output_text.delta",
		"response.output_item.added",
		"response.content_part.added",
		"response.content_part.delta",
		"response.audio.delta",
		"response.file_search_call.searching",
		"response.mcp_call.executing":
		return true
	}
	return false
}
