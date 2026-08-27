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
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/common/message"
	"github.com/songquanpeng/one-api/middleware"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/monitor"
	"github.com/songquanpeng/one-api/relay/channel/anthropic"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/constant"
	"github.com/songquanpeng/one-api/relay/helper"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"

	"github.com/gin-gonic/gin"
)

// testRequestMaxTokens 是普通聊天模型的探测/测活输出上限。
// 只需要确认模型能应答，不需要完整回复。
const testRequestMaxTokens = 16

// isOpenAIReasoningModel 判断是否为 OpenAI 推理模型（o 系列 / gpt-5 系列）。
// 这类模型会先产出 reasoning tokens 再产出可见内容，输出上限太小会导致
// 空响应甚至直接报错。
func isOpenAIReasoningModel(modelName string) bool {
	lower := strings.ToLower(strings.TrimSpace(modelName))
	for _, p := range []string{"o1", "o3", "o4", "gpt-5"} {
		if lower == p || strings.HasPrefix(lower, p+"-") {
			return true
		}
	}
	return false
}

// testRequestMaxTokensFor 按模型能力给出测试/探测请求的 max_tokens。
//
// 一刀切的小值会在两类模型上必然失败，且都不是「模型不可用」：
//
//   - Claude thinking 模型：thinking budget 有 1024 的下限
//     （anthropic/main.go:133-136），而 Anthropic 要求 max_tokens > budget_tokens，
//     给 16 会直接 400
//   - OpenAI 推理模型（o1/o3/o4/gpt-5）：max_completion_tokens 需要覆盖
//     reasoning tokens，16 会导致空内容或报错
//
// 另外 Claude 的 max_tokens 字段没有 omitempty（anthropic/model.go:309），
// 不设值会发出 "max_tokens": 0，Anthropic 直接拒绝 —— 所以任何情况下都必须给
// 一个 >= 1 的值，不能靠「不设置」蒙混过关。
func testRequestMaxTokensFor(modelName string) int {
	if anthropic.IsThinkingModel(modelName) {
		// 需大于 thinking budget 下限 1024，留出少量可见输出的余量
		return 1200
	}
	if isOpenAIReasoningModel(modelName) {
		return 1000
	}
	return testRequestMaxTokens
}

func buildTestRequest(modelName string) *relaymodel.GeneralOpenAIRequest {
	testRequest := &relaymodel.GeneralOpenAIRequest{
		Stream:    false,
		Model:     "gpt-3.5-turbo",
		MaxTokens: testRequestMaxTokensFor(modelName),
	}
	testMessage := relaymodel.Message{
		Role:    "user",
		Content: "hi",
	}
	testRequest.Messages = append(testRequest.Messages, testMessage)
	return testRequest
}

// 不支持通过 /v1/chat/completions 进行自动测试的渠道类型
// 这些渠道属于图像/视频/音频等专用接口，无法用聊天补全测试
var unsupportedTestChannelTypes = map[int]bool{
	common.ChannelTypeMidjourneyPlus: true,
	common.ChannelTypeKeling:         true,
	common.ChannelTypeRunway:         true,
	common.ChannelTypeRecraft:        true,
	common.ChannelTypeLuma:           true,
	common.ChannelTypePixverse:       true,
	common.ChannelTypeFlux:           true,
	common.ChannelTypeReplicate:      true,
}

// 不支持通过 /v1/chat/completions 进行自动测试的模型名关键字（小写匹配）
// 命中任一关键字即视为非聊天模型，跳过测试
var unsupportedTestModelKeywords = []string{
	"embedding",
	"embed",
	"rerank",
	"whisper",
	"tts",
	"dall-e",
	"dalle",
	"stable-diffusion",
	"flux",
	"midjourney",
	"suno",
	"kling",
	"runway",
	"luma",
	"pixverse",
	"recraft",
	"veo",
	"sora",
	"jimeng",
	"vidu",
	"doubao-video",
	"moderation",
}

// isUnsupportedTestChannel 判断渠道类型是否不支持自动测试
func isUnsupportedTestChannel(channelType int) bool {
	return unsupportedTestChannelTypes[channelType]
}

// isUnsupportedTestModel 判断模型名是否不适合用聊天补全测试
func isUnsupportedTestModel(modelName string) bool {
	if modelName == "" {
		return false
	}
	lower := strings.ToLower(modelName)
	for _, kw := range unsupportedTestModelKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// isCodexModel 判断是否为 OpenAI Codex 系列模型（仅支持 /v1/responses 端点）
func isCodexModel(modelName string) bool {
	return strings.Contains(strings.ToLower(modelName), "codex")
}

// testChannelViaResponses 通过 /v1/responses 端点测试渠道
// 用于 Codex 等 responses-only 模型，不经过 chat-completions adaptor
// 成功时根据返回的 usage 换算 quota 并写入 log 表（仅记录，不扣用户配额）
func testChannelViaResponses(channel *model.Channel, modelName, testKey string) (error, *relaymodel.Error) {
	baseURL := channel.GetBaseURL()
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	requestURL := strings.TrimRight(baseURL, "/") + "/v1/responses"

	payload := map[string]interface{}{
		"model":  modelName,
		"input":  "hi",
		"stream": false,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err, nil
	}

	req, err := http.NewRequest(http.MethodPost, requestURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return err, nil
	}
	req.Header.Set("Authorization", "Bearer "+testKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	tik := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return err, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	duration := time.Since(tik).Seconds()

	if resp.StatusCode != http.StatusOK {
		// 尝试解析 OpenAI 标准错误体
		var errResp struct {
			Error relaymodel.Error `json:"error"`
		}
		if jsonErr := json.Unmarshal(body, &errResp); jsonErr == nil && errResp.Error.Message != "" {
			return fmt.Errorf("status code %d: %s", resp.StatusCode, errResp.Error.Message), &errResp.Error
		}
		return fmt.Errorf("status code %d: %s", resp.StatusCode, string(body)), nil
	}

	// 解析响应体以提取 usage
	var parsed openai.OpenaiResaponseResponse
	if jsonErr := json.Unmarshal(body, &parsed); jsonErr != nil {
		logger.SysError(fmt.Sprintf("failed to parse /v1/responses body for channel #%d: %v", channel.Id, jsonErr))
	}
	if parsed.Usage != nil {
		cachedTokens := 0
		if parsed.Usage.InputTokensDetails != nil {
			cachedTokens = parsed.Usage.InputTokensDetails.CachedTokens
		}
		recordChannelTestConsumeLog(channel, modelName, parsed.Usage.InputTokens, parsed.Usage.OutputTokens, cachedTokens, duration)
	}

	logger.SysLog(fmt.Sprintf("testing channel #%d with model %s (responses), response: \n%s", channel.Id, modelName, string(body)))
	return nil, nil
}

// recordChannelTestConsumeLog 将渠道测试消耗换算成 quota 并写入 log 表
// 用于 chat-completions 和 /v1/responses 两种测试路径
// 仅写日志，不扣用户配额、不更新 channel 累计用量（避免测试污染统计）
func recordChannelTestConsumeLog(channel *model.Channel, modelName string, promptTokens, completionTokens, cachedTokens int, duration float64) {
	// 测试场景默认分组倍率 1.0
	groupRatio := 1.0
	modelPrice := common.GetModelPrice(modelName, false)
	modelRatio := common.GetModelRatio(modelName)
	completionRatio := common.GetCompletionRatio(modelName)
	cacheRatio := common.GetCacheRatio(modelName)
	ratio := modelRatio * groupRatio

	var quota int64
	var logContent string
	if modelPrice != -1 {
		// 固定价格计费（按次）
		quota = int64(modelPrice * config.QuotaPerUnit * groupRatio)
		logContent = fmt.Sprintf("模型固定价格 %.2f$，分组倍率 %.2f", modelPrice, groupRatio)
	} else {
		// token 倍率计费
		if cachedTokens > 0 {
			nonCachedPromptTokens := promptTokens - cachedTokens
			if nonCachedPromptTokens < 0 {
				nonCachedPromptTokens = 0
			}
			inputQuota := float64(nonCachedPromptTokens) * modelRatio * groupRatio
			cacheQuota := float64(cachedTokens) * modelRatio * cacheRatio * groupRatio
			outputQuota := float64(completionTokens) * modelRatio * completionRatio * groupRatio
			quota = int64(math.Ceil(inputQuota + cacheQuota + outputQuota))
		} else {
			quota = int64(math.Ceil((float64(promptTokens) + float64(completionTokens)*completionRatio) * ratio))
		}
		if ratio != 0 && quota <= 0 {
			quota = 1
		}
		if promptTokens+completionTokens == 0 {
			quota = 0
		}
		logContent = fmt.Sprintf("模型倍率 %.2f，分组倍率 %.2f，补全倍率 %.2f", modelRatio, groupRatio, completionRatio)
	}

	title := fmt.Sprintf("渠道测试: %s", channel.Name)
	model.RecordConsumeLogWithOtherAndRequestID(
		context.Background(),
		1, // userId=1 表示系统测试，不关联真实用户
		channel.Id,
		promptTokens,
		completionTokens,
		modelName,
		"channel-test", // tokenName
		quota,
		logContent,
		duration,
		title,
		"",    // httpReferer
		false, // isStream
		0,     // firstWordLatency
		"",    // other
		"",    // xRequestID
		cachedTokens,
		"", // xResponseID
	)
}

func testChannel(channel *model.Channel, specifiedModel string, auto_enable bool) (err error, openaiErr *relaymodel.Error, actualModel string, keyIndex int) {
	keyIndex = -1
	// 不支持的渠道类型：图像/视频/音频等，无法用 /v1/chat/completions 测试
	if auto_enable && isUnsupportedTestChannel(channel.Type) {
		channelTypeName, ok := common.ChannelTypeToProvider[channel.Type]
		if !ok {
			channelTypeName = fmt.Sprintf("type=%d", channel.Type)
		}
		return fmt.Errorf("channel type %s is not supported by chat-completions test, skipped", channelTypeName), nil, specifiedModel, keyIndex
	}
	testStart := time.Now()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = &http.Request{
		Method: "POST",
		URL:    &url.URL{Path: "/v1/chat/completions"},
		Body:   nil,
		Header: make(http.Header),
	}
	// 为多密钥渠道选择一个Key进行测试
	testKey := channel.Key
	keyIndex = -1
	if channel.MultiKeyInfo.IsMultiKey {
		actualKey, selectedIndex, err := channel.GetNextAvailableKey()
		if err != nil {
			return fmt.Errorf("no available key for testing: %v", err), nil, "", -1
		}
		testKey = actualKey
		keyIndex = selectedIndex
	}

	c.Request.Header.Set("Authorization", "Bearer "+testKey)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("channel", channel.Type)
	c.Set("base_url", channel.GetBaseURL())
	c.Set("test_key_index", keyIndex) // 用于日志记录
	// 复用已选中的 key，避免 SetupContextForSelectedChannel 二次轮询导致
	// actual_key 与报告的 used_key_index 错位（AWS 等渠道实际读的是 actual_key）
	if channel.MultiKeyInfo.IsMultiKey && keyIndex >= 0 {
		c.Set("cached_key_index", keyIndex)
	}
	middleware.SetupContextForSelectedChannel(c, channel, "")
	meta := util.GetRelayMeta(c)
	apiType := constant.ChannelType2APIType(channel.Type)
	adaptor := helper.GetAdaptor(apiType)
	if adaptor == nil {
		return fmt.Errorf("invalid api type: %d, adaptor is nil", apiType), nil, "", keyIndex
	}
	adaptor.Init(meta)

	var modelName string
	if specifiedModel != "" {
		// 如果指定了模型，检查渠道是否支持该模型
		if strings.Contains(channel.Models, specifiedModel) {
			modelName = specifiedModel
		} else {
			return fmt.Errorf("specified model '%s' is not supported by this channel", specifiedModel), nil, specifiedModel, keyIndex
		}
	} else {
		// 没有指定模型：优先使用渠道配置的 test_model，否则从 adaptor/channel.Models 推断
		if channel.TestModel != "" {
			modelName = channel.TestModel
		} else {
			if channel.Models == "" {
				return fmt.Errorf("channel %s has no models", channel.Name), nil, "", keyIndex
			} else {
				modelNames := strings.Split(channel.Models, ",")
				if len(modelNames) > 0 {
					modelName = strings.TrimSpace(modelNames[0])
				}
			}
		}
	}
	// 非聊天类模型（embedding/rerank/tts/whisper/图像/视频等）跳过，避免误判
	if auto_enable && isUnsupportedTestModel(modelName) {
		return fmt.Errorf("model %s is not supported by chat-completions test, skipped", modelName), nil, modelName, keyIndex
	}
	// Codex 系列模型仅支持 /v1/responses 端点，单独走直连测试
	if isCodexModel(modelName) {
		err, openaiErr = testChannelViaResponses(channel, modelName, testKey)
		return err, openaiErr, modelName, keyIndex
	}
	request := buildTestRequest(modelName)
	request.Model = modelName
	meta.OriginModelName = modelName
	request.Model, _ = util.GetMappedModelName(modelName, meta.ModelMapping)
	meta.ActualModelName = request.Model
	convertedRequest, err := adaptor.ConvertRequest(c, constant.RelayModeChatCompletions, request)
	if err != nil {
		return err, nil, modelName, keyIndex
	}
	jsonData, err := json.Marshal(convertedRequest)
	if err != nil {
		return err, nil, modelName, keyIndex
	}
	requestBody := bytes.NewBuffer(jsonData)
	c.Request.Body = io.NopCloser(requestBody)
	resp, err := adaptor.DoRequest(c, meta, requestBody)
	if err != nil {
		return err, nil, modelName, keyIndex
	}
	// 处理 resp 为 nil 的情况（如 AWS SDK 直接处理请求，不返回 HTTP 响应）
	if resp != nil {
		if resp.StatusCode != http.StatusOK {
			err := util.RelayErrorHandler(resp)
			return fmt.Errorf("status code %d: %s", resp.StatusCode, err.Error.Message), &err.Error, modelName, keyIndex
		}
	}
	usage, respErr := adaptor.DoResponse(c, resp, meta)
	if respErr != nil {
		return fmt.Errorf("%s", respErr.Error.Message), &respErr.Error, modelName, keyIndex
	}
	if usage == nil {
		return errors.New("usage is nil"), nil, modelName, keyIndex
	}

	// 将本次测试消耗写入 log 表（仅记录，不扣配额、不累计用量）
	if auto_enable {
		recordChannelTestConsumeLog(
			channel,
			modelName,
			usage.PromptTokens,
			usage.CompletionTokens,
			usage.PromptTokensDetails.CachedTokens,
			time.Since(testStart).Seconds(),
		)
	}
	result := w.Result()
	// print result.Body
	respBody, err := io.ReadAll(result.Body)
	if err != nil {
		return err, nil, modelName, keyIndex
	}
	logger.SysLog(fmt.Sprintf("testing channel #%d with model %s, response: \n%s", channel.Id, modelName, string(respBody)))
	return nil, nil, modelName, keyIndex
}

func TestChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// 获取请求体中的模型参数（可选）
	var requestBody struct {
		Model string `json:"model"`
	}
	c.ShouldBindJSON(&requestBody)
	specifiedModel := strings.TrimSpace(requestBody.Model)

	channel, err := model.GetChannelById(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	tik := time.Now()
	err, _, actualModel, usedKeyIndex := testChannel(channel, specifiedModel, false)
	tok := time.Now()
	milliseconds := tok.Sub(tik).Milliseconds()
	go channel.UpdateResponseTime(milliseconds)
	consumedTime := float64(milliseconds) / 1000.0

	// 构建详细的测试结果信息
	testResult := gin.H{
		"channel_id":   channel.Id,
		"channel_name": channel.Name,
		"model":        actualModel,
		"time":         consumedTime,
		"timestamp":    time.Now().Unix(),
	}

	// 为多密钥渠道添加额外信息
	if channel.MultiKeyInfo.IsMultiKey {
		testResult["is_multi_key"] = true
		testResult["used_key_index"] = usedKeyIndex
		testResult["total_keys"] = channel.MultiKeyInfo.KeyCount
	}

	if err != nil {
		testResult["success"] = false
		testResult["message"] = err.Error()

		// 增强错误提示
		if channel.MultiKeyInfo.IsMultiKey {
			if specifiedModel != "" {
				testResult["message"] = fmt.Sprintf("Test failed for model '%s' on multi-key channel '%s' (using key #%d): %s",
					actualModel, channel.Name, usedKeyIndex, err.Error())
			} else {
				testResult["message"] = fmt.Sprintf("Test failed for multi-key channel '%s' with model '%s' (using key #%d): %s",
					channel.Name, actualModel, usedKeyIndex, err.Error())
			}
		} else {
			if specifiedModel != "" {
				testResult["message"] = fmt.Sprintf("Test failed for model '%s' on channel '%s': %s",
					actualModel, channel.Name, err.Error())
			} else {
				testResult["message"] = fmt.Sprintf("Test failed for channel '%s' with model '%s': %s",
					channel.Name, actualModel, err.Error())
			}
		}

		logger.Info(c.Request.Context(),
			fmt.Sprintf("Channel #%d (%s) test failed with model %s: %s",
				channel.Id, channel.Name, actualModel, err.Error()))
	} else {
		testResult["success"] = true

		// 增强成功提示
		if channel.MultiKeyInfo.IsMultiKey {
			if specifiedModel != "" {
				testResult["message"] = fmt.Sprintf("Test succeeded for specified model '%s' on multi-key channel '%s' (using key #%d), took %.2fs",
					actualModel, channel.Name, usedKeyIndex, consumedTime)
			} else {
				testResult["message"] = fmt.Sprintf("Test succeeded for multi-key channel '%s' with model '%s' (using key #%d), took %.2fs",
					channel.Name, actualModel, usedKeyIndex, consumedTime)
			}
		} else {
			if specifiedModel != "" {
				testResult["message"] = fmt.Sprintf("Test succeeded for specified model '%s' on channel '%s', took %.2fs",
					actualModel, channel.Name, consumedTime)
			} else {
				testResult["message"] = fmt.Sprintf("Test succeeded for channel '%s' with model '%s', took %.2fs",
					channel.Name, actualModel, consumedTime)
			}
		}

		logger.Info(c.Request.Context(),
			fmt.Sprintf("Channel #%d (%s) test succeeded with model %s, took %.2fs",
				channel.Id, channel.Name, actualModel, consumedTime))
	}

	c.JSON(http.StatusOK, testResult)
}

var testAllChannelsLock sync.Mutex
var testAllChannelsRunning bool = false

// filterCheckLock/Running 与 testAllChannelsLock 分离，让精准巡检（scope=filter）
// 与全量测试（scope=all/disabled/auto_disabled）能并发运行，互不阻塞。
// 参见 docs/plans/2026-08-27-channel-filter-healthcheck.md
var filterCheckLock sync.Mutex
var filterCheckRunning bool = false

func testChannels(notify bool, scope string, keyword string, channelType *int, statusList []int, specifiedModel string) error {
	if config.RootUserEmail == "" {
		config.RootUserEmail = model.GetRootUserEmail()
	}

	// 独立 lock：filter 模式与其他 scope 分离
	lock := &testAllChannelsLock
	running := &testAllChannelsRunning
	if scope == "filter" {
		lock = &filterCheckLock
		running = &filterCheckRunning
	}
	lock.Lock()
	if *running {
		lock.Unlock()
		return errors.New("测试已在运行中")
	}
	*running = true
	lock.Unlock()

	channels, err := model.GetAllChannelsForTest(0, 0, scope, keyword, channelType, statusList)
	if err != nil {
		lock.Lock()
		*running = false
		lock.Unlock()
		return err
	}
	var disableThreshold = int64(config.ChannelDisableThreshold * 1000)
	if disableThreshold == 0 {
		disableThreshold = 10000000 // a impossible value
	}
	go func() {
		for _, channel := range channels {
			if !channel.AutoEnabled {
				logger.SysLog(fmt.Sprintf(
					"skip auto-enable channel #%d (%s): channel auto_enabled is disabled",
					channel.Id, channel.Name,
				))
				continue
			}
			isChannelEnabled := channel.Status == common.ChannelStatusEnabled
			tik := time.Now()
			err, openaiErr, _, _ := testChannel(channel, specifiedModel, true)
			tok := time.Now()
			milliseconds := tok.Sub(tik).Milliseconds()

			if scope == "filter" {
				// 精准巡检分派（主动调用，禁用+恢复均不受 Automatic*Enabled 门控）：
				//   测失败 && enabled  → 禁用为 auto_disabled
				//   测通    && !enabled → 恢复为 enabled
				//   其他组合 no-op
				timeout := isChannelEnabled && milliseconds > disableThreshold
				failed := (err != nil) || util.ShouldDisableChannel(openaiErr, -1) || timeout
				if isChannelEnabled && failed {
					reason := ""
					if err != nil {
						reason = err.Error()
					} else if timeout {
						reason = fmt.Sprintf("响应时间 %.2fs 超过阈值 %.2fs", float64(milliseconds)/1000.0, float64(disableThreshold)/1000.0)
					} else {
						reason = "测试失败"
					}
					monitor.DisableChannelSafelyWithStatusCode(channel.Id, channel.Name, reason, "N/A (Filter Test)", -1)
				} else if !isChannelEnabled && !failed {
					if e := model.UpdateChannelStatusById(channel.Id, common.ChannelStatusEnabled); e != nil {
						logger.SysError(fmt.Sprintf("filter check: enable channel %d failed: %s", channel.Id, e.Error()))
					} else {
						logger.SysLog(fmt.Sprintf("filter check: channel #%d (%s) re-enabled after test success", channel.Id, channel.Name))
					}
				}
			} else {
				// 现有 scope=all/disabled/auto_disabled 逻辑：只对 enabled 渠道做超时/关键词禁用，
				// 不做恢复动作，保持向后兼容。
				if isChannelEnabled && milliseconds > disableThreshold {
					err = fmt.Errorf("响应时间 %.2fs 超过阈值 %.2fs", float64(milliseconds)/1000.0, float64(disableThreshold)/1000.0)
					if config.AutomaticDisableChannelEnabled {
						monitor.DisableChannelSafelyWithStatusCode(channel.Id, channel.Name, err.Error(), "N/A (Test)", 0)
					} else {
						_ = message.Notify(message.ByAll, fmt.Sprintf("渠道 %s （%d）测试超时", channel.Name, channel.Id), "", err.Error())
					}
				}
				if isChannelEnabled && util.ShouldDisableChannel(openaiErr, -1) {
					monitor.DisableChannelSafelyWithStatusCode(channel.Id, channel.Name, err.Error(), "N/A (Test)", -1)
				}
			}
			// 方案 A：不再由渠道级测通触发"整渠道恢复"（原逻辑会连带清零 auto_disabled 洗白坏模型）。
			// 被自动禁用的渠道统一由 recoverAutoDisabledModels 逐模型恢复：测通哪个模型就恢复哪个，
			// 单模型恢复内联提升渠道 status；不可探测的模型只能通过前端「模型自动禁用」批量启用救火。
			channel.UpdateResponseTime(milliseconds)
			time.Sleep(config.RequestInterval)
		}
		lock.Lock()
		*running = false
		lock.Unlock()
		if notify {
			err := message.Notify(message.ByAll, "渠道测试完成", "", "渠道测试完成，如果没有收到禁用通知，说明所有渠道都正常")
			if err != nil {
				logger.SysError(fmt.Sprintf("failed to send email: %s", err.Error()))
			}
		}
	}()
	return nil
}

func TestChannels(c *gin.Context) {
	scope := c.Query("scope")
	if scope == "" {
		scope = "all"
	}

	// filter 模式参数解析
	var (
		keyword       string
		channelType   *int
		statusList    []int
		specifiedModel string
	)
	if scope == "filter" {
		keyword = c.Query("keyword")
		typeStr := c.Query("type")
		statusStr := c.DefaultQuery("status", strconv.Itoa(common.ChannelStatusEnabled))
		specifiedModel = strings.TrimSpace(c.Query("model")) // 可选：强制用此 model 测试，避免服务端选到老模型被 404 误判

		if typeStr != "" {
			t, terr := strconv.Atoi(typeStr)
			if terr != nil || t < 0 {
				c.JSON(http.StatusBadRequest, gin.H{
					"success": false,
					"message": "invalid type: " + typeStr,
				})
				return
			}
			channelType = &t
		}
		if keyword == "" && channelType == nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": "filter mode requires keyword or type",
			})
			return
		}
		for _, s := range strings.Split(statusStr, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			v, verr := strconv.Atoi(s)
			if verr != nil || v < common.ChannelStatusEnabled || v > common.ChannelStatusAutoDisabled {
				c.JSON(http.StatusBadRequest, gin.H{
					"success": false,
					"message": "invalid status: " + s,
				})
				return
			}
			statusList = append(statusList, v)
		}
	}

	err := testChannels(true, scope, keyword, channelType, statusList, specifiedModel)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

var recoverModelsLock sync.Mutex
var recoverModelsRunning bool

// recoverModelsMaxPerRound 每轮模型级恢复处理的最大 (channel, model) 数。
// 生产环境可能出现单模型挂几百渠道、每渠道又累积多个被禁模型的场景，
// 无上限时一轮会发几百次真实付费请求且串行 sleep，占满整个恢复周期。
// 100 是经验值：按 RequestInterval~500ms 估算，一轮上限约 50s，与 5min 周期相容。
const recoverModelsMaxPerRound = 100

// recoverAutoDisabledModels 模型级恢复：测试被模型级禁用的模型，成功则重新启用该 (channel, model)。
//
// 与方案 A 匹配的语义：
//   - 覆盖 channel.status ∈ {enabled, auto_disabled} 两类渠道；manually_disabled 不动。
//   - 恢复动作走 EnableModelOnChannel，内联把 auto_disabled 渠道 status 提升回 enabled。
//   - 按 auto_disabled_time 升序（优先恢复最久的），每轮上限 recoverModelsMaxPerRound。
//   - 不可 chat 探测的模型/渠道类型（image/embedding/video 等）在发请求前跳过，只能靠前端人工批量启用。
//
// 受 config.AutomaticEnableChannelEnabled 与单渠道 AutoEnabled 约束，仅主节点调用。
func recoverAutoDisabledModels() {
	if !config.AutomaticEnableChannelEnabled {
		return
	}
	recoverModelsLock.Lock()
	if recoverModelsRunning {
		recoverModelsLock.Unlock()
		return
	}
	recoverModelsRunning = true
	recoverModelsLock.Unlock()
	defer func() {
		recoverModelsLock.Lock()
		recoverModelsRunning = false
		recoverModelsLock.Unlock()
	}()

	items, err := model.GetAutoDisabledAbilities()
	if err != nil {
		logger.SysError(fmt.Sprintf("failed to load auto-disabled abilities for recovery: %s", err.Error()))
		return
	}
	if len(items) == 0 {
		return
	}
	if len(items) > recoverModelsMaxPerRound {
		logger.SysLog(fmt.Sprintf("model recovery: %d candidates found, processing first %d (by auto_disabled_time ASC); rest defer to next round",
			len(items), recoverModelsMaxPerRound))
		items = items[:recoverModelsMaxPerRound]
	}

	// 按渠道缓存，避免同渠道多模型重复取库
	channelCache := make(map[int]*model.Channel)
	processed, recovered, skipped := 0, 0, 0
	for _, it := range items {
		channel, ok := channelCache[it.ChannelId]
		if !ok {
			channel, err = model.GetChannelById(it.ChannelId, true)
			if err != nil {
				logger.SysError(fmt.Sprintf("model recovery: failed to get channel %d: %s", it.ChannelId, err.Error()))
				channelCache[it.ChannelId] = nil
				continue
			}
			channelCache[it.ChannelId] = channel
		}
		if channel == nil {
			continue
		}
		// 渠道关闭自动启用则跳过其模型级恢复
		if !channel.AutoEnabled {
			skipped++
			continue
		}
		// 24h 熔断退避：本渠道近期已触发过整渠道自动禁用，未过退避窗口就不再探测。
		// 参见 docs/plans/2026-08-26-auto-disable-circuit-breaker.md
		if channel.AutoDisableCount > 0 && channel.AutoDisabledTime != nil {
			idx := channel.AutoDisableCount - 1
			if idx >= len(common.ChannelAutoDisableProbeBackoff) {
				idx = len(common.ChannelAutoDisableProbeBackoff) - 1
			}
			minWait := int64(common.ChannelAutoDisableProbeBackoff[idx].Seconds())
			if time.Now().Unix()-*channel.AutoDisabledTime < minWait {
				skipped++
				continue
			}
		}
		// 预过滤：不可 chat 探测的渠道类型 / 模型名，直接跳过避免浪费真实 API 调用。
		// 这类模型只能通过前端「模型自动禁用」批量启用手工恢复。
		if isUnsupportedTestChannel(channel.Type) || isUnsupportedTestModel(it.Model) {
			skipped++
			continue
		}

		processed++
		testErr, openaiErr, _, _ := testChannel(channel, it.Model, true)
		if util.ShouldEnableChannel(testErr, openaiErr) {
			if e := model.EnableModelOnChannel(it.ChannelId, it.Model); e != nil {
				logger.SysError(fmt.Sprintf("model recovery: failed to enable channel %d model %s: %s", it.ChannelId, it.Model, e.Error()))
			} else {
				recovered++
				logger.SysLog(fmt.Sprintf("model recovery: channel #%d model %s re-enabled", it.ChannelId, it.Model))
			}
		}
		time.Sleep(config.RequestInterval)
	}
	logger.SysLog(fmt.Sprintf("model recovery round done: candidates=%d processed=%d recovered=%d skipped=%d",
		len(items), processed, recovered, skipped))

	// 整渠道「最近使用模型全禁」的收尾判定已剥离到 evaluateUsageBasedChannelDisable
	// 独立评估，不再挂本函数尾部。避免因 recoverModelsMaxPerRound=100 截断而漏评估。
	// 参见 docs/plans/2026-08-27-auto-disable-refactor.md
}

// disableChannelByRecentUsageFn 抽为包变量以便测试注入。生产环境始终使用默认值。
// 参见 evaluate_usage_disable_test.go
var disableChannelByRecentUsageFn = monitor.DisableChannelByRecentUsage

// evaluateUsageBasedChannelDisable 独立执行「最近使用中的模型全部被自动禁用」的收尾判定。
//
// 与 recoverAutoDisabledModels 解耦：
//   - 不依赖 AutomaticEnableChannelEnabled（禁用是保护动作，不该被「启用」开关阉割）
//   - 不受 recoverModelsMaxPerRound 硬顶截断（判定纯 SQL 无 API 调用，成本可忽略）
//   - 不参与恢复队列的抖动 / 挤位
//
// 触发时机：主循环独立 tick，周期同 AutoTestChannelFrequency。
// 参见 docs/plans/2026-08-27-auto-disable-refactor.md
func evaluateUsageBasedChannelDisable() {
	if !config.IsMasterNode {
		return
	}
	if !config.AutomaticDisableChannelEnabled {
		return
	}
	channelIds, err := model.GetChannelsWithAutoDisabledAbilities()
	if err != nil {
		logger.SysError(fmt.Sprintf("evaluate usage-based disable: query channels failed: %s", err.Error()))
		return
	}
	triggered := 0
	for _, cid := range channelIds {
		should, used, disabled, jerr := model.ShouldDisableChannelByRecentUsage(cid)
		if jerr != nil {
			logger.SysError(fmt.Sprintf("channel #%d usage-based disable judge failed: %s", cid, jerr.Error()))
			continue
		}
		if !should {
			continue
		}
		logger.SysLog(fmt.Sprintf("channel #%d usage-based disable triggered: used=%d disabled=%d", cid, used, disabled))
		disableChannelByRecentUsageFn(cid, used)
		triggered++
	}
	logger.SysLog(fmt.Sprintf("usage-based disable round done: candidates=%d triggered=%d", len(channelIds), triggered))
}

// AutomaticallyTestChannels 仅主节点执行：周期性测试并自动启用符合条件的渠道
// 频率读取自 config.AutoTestChannelFrequency（分钟），<=0 表示未启用
func AutomaticallyTestChannels() {
	logger.SysLog(fmt.Sprintf("automatically testing all channels every %d minutes, config.IsMasterNode: %v", config.AutoTestChannelFrequency, config.IsMasterNode))
	if !config.IsMasterNode {
		return
	}
	// 启动时立即执行一次，与 upstream sync 行为对齐，避免重启后等待完整周期
	if config.AutoTestChannelFrequency > 0 {
		logger.SysLog("automatically testing all channels (startup run)")
		if err := testChannels(false, "auto_disabled", "", nil, nil, ""); err != nil {
			logger.SysLog(fmt.Sprintf("startup auto-test skipped: %s", err.Error()))
		}
		recoverAutoDisabledModels()
	}
	for {
		frequency := config.AutoTestChannelFrequency
		if frequency <= 0 {
			// 未启用，每分钟轮询一次等待开启
			time.Sleep(time.Minute)
			continue
		}

		time.Sleep(time.Duration(frequency) * time.Minute)
		// 再次读取，防止睡眠期间被关闭
		if config.AutoTestChannelFrequency <= 0 {
			continue
		}
		logger.SysLog("automatically testing all channels")
		if err := testChannels(false, "auto_disabled", "", nil, nil, ""); err != nil {
			logger.SysLog(fmt.Sprintf("auto-test skipped (previous run still in progress): %s", err.Error()))
		}
		recoverAutoDisabledModels()
		logger.SysLog("automatically channel test finished")
	}
}

// StartUsageBasedDisableEvaluator 独立 tick，周期同 AutoTestChannelFrequency。
// 与 AutomaticallyTestChannels 并行运行、无相互依赖，专门执行整渠道「最近使用模型全禁」
// 的收尾判定，把 evaluateUsageBasedChannelDisable 从原来的恢复探针尾部解耦出来。
//
// 参见 docs/plans/2026-08-27-auto-disable-refactor.md
func StartUsageBasedDisableEvaluator() {
	logger.SysLog(fmt.Sprintf("usage-based disable evaluator starting, config.IsMasterNode: %v", config.IsMasterNode))
	if !config.IsMasterNode {
		return
	}
	// 启动即跑一次，对齐 AutomaticallyTestChannels 的 startup run 语义
	if config.AutoTestChannelFrequency > 0 {
		logger.SysLog("usage-based disable evaluator (startup run)")
		evaluateUsageBasedChannelDisable()
	}
	for {
		frequency := config.AutoTestChannelFrequency
		if frequency <= 0 {
			// 未启用，每分钟轮询一次等待开启
			time.Sleep(time.Minute)
			continue
		}
		time.Sleep(time.Duration(frequency) * time.Minute)
		// 再次读取，防止睡眠期间被关闭
		if config.AutoTestChannelFrequency <= 0 {
			continue
		}
		evaluateUsageBasedChannelDisable()
	}
}
