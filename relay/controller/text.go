package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/constant"
	"github.com/songquanpeng/one-api/relay/helper"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

func RelayTextHelper(c *gin.Context) *model.ErrorWithStatusCode {
	ctx := c.Request.Context()
	startTime := time.Now()

	// 记录请求开始时间用于首字延迟计算（应该在最开始记录）
	c.Set("request_start_time", startTime)

	meta := util.GetRelayMeta(c)
	// get & validate textRequest
	textRequest, err := getAndValidateTextRequest(c, meta.Mode)
	if err != nil {
		logger.Errorf(ctx, "getAndValidateTextRequest failed: %s", err.Error())
		return openai.ErrorWrapper(err, "invalid_text_request", http.StatusBadRequest)
	}
	meta.IsStream = textRequest.Stream

	// map model name
	var isModelMapped bool
	meta.OriginModelName = textRequest.Model
	textRequest.Model, isModelMapped = util.GetMappedModelName(textRequest.Model, meta.ModelMapping)
	meta.ActualModelName = textRequest.Model
	// get model ratio & group ratio
	modelRatio := common.GetModelRatio(textRequest.Model)
	groupRatio := common.GetGroupRatio(meta.Group)
	// userModelTypeRatio := common.GetUserModelTypeRation(meta.Group, textRequest.Model)
	ratio := modelRatio * groupRatio
	// pre-consume quota
	promptTokens := getPromptTokens(textRequest, meta.Mode)
	meta.PromptTokens = promptTokens
	preConsumedQuota, bizErr := preConsumeQuota(ctx, textRequest, promptTokens, ratio, meta)
	if bizErr != nil {
		logger.Warnf(ctx, "preConsumeQuota failed: %+v", *bizErr)
		return bizErr
	}

	adaptor := helper.GetAdaptor(meta.APIType)
	if adaptor == nil {
		return openai.ErrorWrapper(fmt.Errorf("invalid api type: %d", meta.APIType), "invalid_api_type", http.StatusBadRequest)
	}

	adaptor.Init(meta)

	// get request body
	var requestBody io.Reader
	// 在主要代码流程中
	if meta.APIType == constant.APITypeOpenAI {
		// 始终通过 ConvertRequest 处理请求
		convertedRequest, err := adaptor.ConvertRequest(c, meta.Mode, textRequest)
		if err != nil {
			return openai.ErrorWrapper(err, "convert_request_failed", http.StatusInternalServerError)
		}

		shouldResetRequestBody := isModelMapped || meta.ChannelType == common.ChannelTypeBaichuan ||
			(strings.Contains(strings.ToLower(textRequest.Model), "audio") && textRequest.Stream)

		if shouldResetRequestBody {
			jsonStr, err := json.Marshal(convertedRequest) // 使用转换后的请求
			if err != nil {
				return openai.ErrorWrapper(err, "json_marshal_failed", http.StatusInternalServerError)
			}
			requestBody = bytes.NewBuffer(jsonStr)
		} else {
			requestBody = c.Request.Body
		}
	} else {
		// 其他API类型的处理保持不变
		convertedRequest, err := adaptor.ConvertRequest(c, meta.Mode, textRequest)
		if err != nil {
			return openai.ErrorWrapper(err, "convert_request_failed", http.StatusInternalServerError)
		}
		jsonData, err := json.Marshal(convertedRequest)
		if err != nil {
			return openai.ErrorWrapper(err, "json_marshal_failed", http.StatusInternalServerError)
		}
		logger.Debugf(ctx, "converted request: \n%s", string(jsonData))
		requestBody = bytes.NewBuffer(jsonData)
	}

	// do request
	resp, err := adaptor.DoRequest(c, meta, requestBody)
	if err != nil {
		logger.Errorf(ctx, "DoRequest failed: %s", err.Error())
		return openai.ErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
	}
	if resp != nil {
		errorHappened := (resp.StatusCode != http.StatusOK) || (meta.IsStream && resp.Header.Get("Content-Type") == "application/json")
		if errorHappened {
			util.ReturnPreConsumedQuota(ctx, preConsumedQuota, meta.TokenId)
			return util.RelayErrorHandlerWithAdaptor(resp, adaptor)
		}
	}

	// 记录响应开始时间（用于计算首字延迟）
	responseStartTime := time.Now()

	// do response
	usage, respErr := adaptor.DoResponse(c, resp, meta)
	if respErr != nil {
		logger.Errorf(ctx, "respErr is not nil: %+v", respErr)
		util.ReturnPreConsumedQuota(ctx, preConsumedQuota, meta.TokenId)
		return respErr
	}

	rowDuration := time.Since(startTime).Seconds() // 计算总耗时
	duration := math.Round(rowDuration*1000) / 1000

	// 获取首字延迟（如果是流式响应）
	var firstWordLatency float64
	if meta.IsStream {
		// 首先尝试从 context 获取（OpenAI、Gemini 等渠道会设置这个值）
		if latency, exists := c.Get("first_word_latency"); exists {
			if latencyFloat, ok := latency.(float64); ok {
				firstWordLatency = math.Round(latencyFloat*1000) / 1000 // 保留3位小数
				logger.Debugf(ctx, "First word latency from context: %.3f seconds", firstWordLatency)
			}
		} else {
			// 对于其他渠道，使用响应开始到现在的时间作为近似值
			// 这不是真正的首字延迟，但可以作为响应延迟的指标
			firstWordLatency = math.Round(time.Since(responseStartTime).Seconds()*1000) / 1000
			logger.Debugf(ctx, "First word latency fallback: %.3f seconds", firstWordLatency)
		}
	}

	referer := c.Request.Header.Get("HTTP-Referer")

	// 获取X-Title header
	title := c.Request.Header.Get("X-Title")

	// post-consume quota
	go postConsumeQuota(ctx, usage, meta, textRequest, ratio, preConsumedQuota, modelRatio, groupRatio, duration, title, referer, firstWordLatency)
	return nil
}
