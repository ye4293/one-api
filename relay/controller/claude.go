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

	// 获取首字延迟（从 context 中获取，流式响应时会设置）
	var firstWordLatency float64
	if latency, exists := c.Get("first_word_latency"); exists {
		if latencyFloat, ok := latency.(float64); ok {
			firstWordLatency = latencyFloat
		}
	}

	go recordClaudeConsumption(ctx, userId, channelId, tokenId, modelName, tokenName, promptTokens, completionTokens, totalTokens, 0, actualQuota, c.Request.RequestURI, duration, meta.IsStream, c, usageMetadata, firstWordLatency)

	return nil
}

// recordClaudeConsumption 记录 Claude 消费日志
func recordClaudeConsumption(ctx context.Context, userId, channelId, tokenId int, modelName, tokenName string, promptTokens, completionTokens, totalTokens, cachedTokens int, quota int64, requestPath string, duration float64, isStream bool, c *gin.Context, usageMetadata *anthropic.Usage, firstWordLatency float64) {
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
	usageDetails := extractClaudeNativeUsageDetails(usageMetadata)
	// 提取渠道历史信息 (adminInfo)
	adminInfo := extractAdminInfoFromContext(c)
	// 构建 other 字段，包含 adminInfo 和 usageDetails
	other := buildClaudeOtherInfoWithUsageDetails(adminInfo, usageDetails)

	dbmodel.RecordConsumeLogWithOtherAndRequestID(ctx, userId, channelId, promptTokens, completionTokens, modelName,
		tokenName, quota, logContent, duration, title, referer, isStream, firstWordLatency, other, c.GetHeader("X-Request-ID"), 0)
}

// buildClaudeOtherInfoWithUsageDetails 构建包含 adminInfo 和 Claude usageDetails 的 otherInfo 字符串
func buildClaudeOtherInfoWithUsageDetails(adminInfo string, usageDetails *ClaudeUsageDetails) string {
	var parts []string

	if adminInfo != "" {
		parts = append(parts, adminInfo)
	}

	if usageDetails != nil {
		if detailsBytes, err := json.Marshal(usageDetails); err == nil {
			parts = append(parts, fmt.Sprintf("usageDetails:%s", string(detailsBytes)))
		}
	}

	return strings.Join(parts, ";")
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
		InputTokens:              usageMetadata.InputTokens,
		OutputTokens:             usageMetadata.OutputTokens,
		CacheCreationInputTokens: usageMetadata.CacheCreationInputTokens,
		CacheReadInputTokens:     usageMetadata.CacheReadInputTokens,
	}

	// 从 CacheCreation 对象中提取5分钟和1小时缓存的详细信息
	if usageMetadata.CacheCreation != nil {
		details.ClaudeCacheCreation5mTokens = usageMetadata.CacheCreation.Ephemeral5mInputTokens
		details.ClaudeCacheCreation1hTokens = usageMetadata.CacheCreation.Ephemeral1hInputTokens
	}

	if usageMetadata.ServerToolUse != nil {
		details.ServerToolUseWebSearchRequests = usageMetadata.ServerToolUse.WebSearchRequests
	}

	return details
}

func CalculateClaudeQuotaFromRequest(requestBody []byte, modelName string, ratio float64) (int64, int, error) {
	return 0, 0, nil
}

// ClaudeTokenCost Claude API 的费用明细（使用动态倍率计算）
type ClaudeTokenCost struct {
	// 输入部分 token 数量
	InputTextTokens int // 输入文字 token 数量
	// 输出部分 token 数量
	OutputTextTokens int // 输出文字 token 数量
	// 缓存相关 token 数量
	CacheCreation5mTokens int // 5分钟缓存创建 token 数量
	CacheCreation1hTokens int // 1小时缓存创建 token 数量
	CacheReadTokens       int // 缓存读取 token 数量
	TotalTokens           int // 总 token 数量
	ModelName             string
}

// CalculateClaudeQuotaByRatio 使用动态倍率计算 Claude API 的配额消耗
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
//   - 5分钟缓存创建：输入价格 × 1.25
//   - 1小时缓存创建：输入价格 × 2.0
//   - 缓存读取：输入价格 × 0.1
//
// 【配额计算公式】
//   - 输入配额 = inputTokens × ModelRatio
//   - 输出配额 = outputTokens × ModelRatio × CompletionRatio
//   - 5分钟缓存创建配额 = cache5mTokens × ModelRatio × 1.25
//   - 1小时缓存创建配额 = cache1hTokens × ModelRatio × 2.0
//   - 缓存读取配额 = cacheReadTokens × ModelRatio × 0.1
//
// 【最终配额】
//
//	总配额 = (各部分之和) / 1000000 × 2 × groupRatio × QuotaPerUnit
func CalculateClaudeQuotaByRatio(usageMetadata *anthropic.Usage, modelName string, groupRatio float64) (int64, ClaudeTokenCost) {
	cost := ClaudeTokenCost{
		ModelName: modelName,
	}

	if usageMetadata == nil {
		return 0, cost
	}

	// 提取 token 数量
	cost.InputTextTokens = usageMetadata.InputTokens
	cost.OutputTextTokens = usageMetadata.OutputTokens
	cost.CacheReadTokens = usageMetadata.CacheReadInputTokens

	// 提取缓存创建详情（5分钟和1小时）
	if usageMetadata.CacheCreation != nil {
		cost.CacheCreation5mTokens = usageMetadata.CacheCreation.Ephemeral5mInputTokens
		cost.CacheCreation1hTokens = usageMetadata.CacheCreation.Ephemeral1hInputTokens
	} else if usageMetadata.CacheCreationInputTokens > 0 {
		// 没有详细信息，默认全部算作5分钟缓存
		cost.CacheCreation5mTokens = usageMetadata.CacheCreationInputTokens
	}

	cost.TotalTokens = usageMetadata.InputTokens + usageMetadata.OutputTokens +
		usageMetadata.CacheCreationInputTokens + usageMetadata.CacheReadInputTokens

	// ========== 获取各类型的倍率 ==========
	modelRatio := common.GetModelRatio(modelName)
	completionRatio := common.GetCompletionRatio(modelName)

	// 打印倍率信息
	logger.SysLog(fmt.Sprintf("[Claude计费] 模型: %s, 倍率配置: ModelRatio=%.4f, CompletionRatio=%.4f",
		modelName, modelRatio, completionRatio))

	// 打印 token 数量
	logger.SysLog(fmt.Sprintf("[Claude计费] Token数量: 输入=%d, 输出=%d, 5分钟缓存创建=%d, 1小时缓存创建=%d, 缓存读取=%d, 总计=%d",
		cost.InputTextTokens, cost.OutputTextTokens,
		cost.CacheCreation5mTokens, cost.CacheCreation1hTokens,
		cost.CacheReadTokens, cost.TotalTokens))

	// ========== 计算各部分的等效 ratio tokens ==========
	// 输入部分：tokens × modelRatio
	inputQuota := float64(cost.InputTextTokens) * modelRatio

	// 输出部分：tokens × modelRatio × completionRatio
	outputQuota := float64(cost.OutputTextTokens) * modelRatio * completionRatio

	// 缓存创建部分
	// 5分钟缓存：tokens × modelRatio × 1.25
	cache5mQuota := float64(cost.CacheCreation5mTokens) * modelRatio * 1.25
	// 1小时缓存：tokens × modelRatio × 2.0
	cache1hQuota := float64(cost.CacheCreation1hTokens) * modelRatio * 2.0

	// 缓存读取部分：tokens × modelRatio × 0.1
	cacheReadQuota := float64(cost.CacheReadTokens) * modelRatio * 0.1

	// 打印各部分配额计算
	logger.SysLog(fmt.Sprintf("[Claude计费] 各部分Ratio Tokens: 输入=%.2f (%d×%.4f), 输出=%.2f (%d×%.4f×%.4f)",
		inputQuota, cost.InputTextTokens, modelRatio,
		outputQuota, cost.OutputTextTokens, modelRatio, completionRatio))

	logger.SysLog(fmt.Sprintf("[Claude计费] 缓存Ratio Tokens: 5分钟创建=%.2f (%d×%.4f×1.25), 1小时创建=%.2f (%d×%.4f×2.0), 读取=%.2f (%d×%.4f×0.1)",
		cache5mQuota, cost.CacheCreation5mTokens, modelRatio,
		cache1hQuota, cost.CacheCreation1hTokens, modelRatio,
		cacheReadQuota, cost.CacheReadTokens, modelRatio))

	// ========== 计算最终配额 ==========
	// 公式: 总RatioTokens / 1000000 × 2 × groupRatio × QuotaPerUnit
	// 乘以 2 是因为 ModelRatio = 官方价格 / 2，需要还原真实价格
	totalRatioTokens := inputQuota + outputQuota + cache5mQuota + cache1hQuota + cacheReadQuota

	quota := int64(totalRatioTokens / 1000000 * 2 * groupRatio * config.QuotaPerUnit)

	// 打印最终配额计算
	logger.SysLog(fmt.Sprintf("[Claude计费] 最终计算: 总RatioTokens=%.2f, 公式=%.2f/1000000×2×%.4f×%.2f, 最终配额=%d",
		totalRatioTokens, totalRatioTokens, groupRatio, config.QuotaPerUnit, quota))

	return quota, cost
}

// CalculateClaudeQuotaFromUsageMetadata 根据 UsageMetadata 计算配额
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

	// 记录开始时间，用于计算首字延迟
	startTime := time.Now()
	var firstWordTime *time.Time

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
			// 记录首字时间：当收到第一个 content_block_delta 时
			if firstWordTime == nil {
				// 检查是否有实际内容
				if claudeResponse.Delta != nil && claudeResponse.Delta.Text != "" {
					now := time.Now()
					firstWordTime = &now
				}
			}
		} else if claudeResponse.Type == "message_delta" {
			// 最终的usage获取
			if claudeResponse.Usage != nil {
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
				// 提取缓存创建详情（5分钟和1小时缓存）
				if claudeResponse.Usage.CacheCreation != nil {
					if lastUsageMetadata.CacheCreation == nil {
						lastUsageMetadata.CacheCreation = &anthropic.CacheCreation{}
					}
					if claudeResponse.Usage.CacheCreation.Ephemeral5mInputTokens > 0 {
						lastUsageMetadata.CacheCreation.Ephemeral5mInputTokens = claudeResponse.Usage.CacheCreation.Ephemeral5mInputTokens
					}
					if claudeResponse.Usage.CacheCreation.Ephemeral1hInputTokens > 0 {
						lastUsageMetadata.CacheCreation.Ephemeral1hInputTokens = claudeResponse.Usage.CacheCreation.Ephemeral1hInputTokens
					}
				}
				if claudeResponse.Usage.ServerToolUse != nil && claudeResponse.Usage.ServerToolUse.WebSearchRequests > 0 {
					if lastUsageMetadata.ServerToolUse == nil {
						lastUsageMetadata.ServerToolUse = &anthropic.ServerToolUsage{}
					}
					lastUsageMetadata.ServerToolUse.WebSearchRequests = claudeResponse.Usage.ServerToolUse.WebSearchRequests
				}
			}
		} else if claudeResponse.Type == "content_block_start" {
		} else {
		}
		return true
	})

	if openaiErr != nil {
		return nil, openaiErr
	}

	// 计算并设置首字延迟
	if firstWordTime != nil {
		firstWordLatency := firstWordTime.Sub(startTime).Seconds()
		c.Set("first_word_latency", firstWordLatency)
	}

	return lastUsageMetadata, nil
}
