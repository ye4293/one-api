package xai

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/conv"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/constant"
	"github.com/songquanpeng/one-api/relay/model"
)

type XaiStreamResponse struct {
	openai.ChatCompletionsStreamResponse
	Usage *XaiUsage `json:"usage,omitempty"`
}

type XaiResponse struct {
	openai.SlimTextResponse
	Usage XaiUsage `json:"usage"`
}

// XAI 自定义的 Usage 结构体（去掉 omitempty，确保所有字段都显示）
type XaiCompleteUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
		AudioTokens  int `json:"audio_tokens"`
		TextTokens   int `json:"text_tokens"`
		ImageTokens  int `json:"image_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens          int `json:"reasoning_tokens"`
		AudioTokens              int `json:"audio_tokens"`
		AcceptedPredictionTokens int `json:"accepted_prediction_tokens"`
		RejectedPredictionTokens int `json:"rejected_prediction_tokens"`
	} `json:"completion_tokens_details"`
}

// 转换 XAI usage 为完整的 Usage 格式（显示所有字段包括0值）
func convertXaiUsageToComplete(xaiUsage *XaiUsage) *XaiCompleteUsage {
	if xaiUsage == nil {
		return nil
	}

	// 按照 OpenAI 格式：CompletionTokens = TotalTokens - PromptTokens
	completionTokens := xaiUsage.TotalTokens - xaiUsage.PromptTokens

	// 创建完整的 Usage，所有字段都会显示（包括0值）
	usage := &XaiCompleteUsage{
		PromptTokens:     xaiUsage.PromptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      xaiUsage.TotalTokens,
	}

	// 保留 prompt_tokens_details
	usage.PromptTokensDetails.CachedTokens = xaiUsage.PromptTokensDetails.CachedTokens
	usage.PromptTokensDetails.AudioTokens = xaiUsage.PromptTokensDetails.AudioTokens
	usage.PromptTokensDetails.TextTokens = xaiUsage.PromptTokensDetails.TextTokens
	usage.PromptTokensDetails.ImageTokens = xaiUsage.PromptTokensDetails.ImageTokens

	// 保留 completion_tokens_details
	usage.CompletionTokensDetails.ReasoningTokens = xaiUsage.CompletionTokensDetails.ReasoningTokens
	usage.CompletionTokensDetails.AudioTokens = xaiUsage.CompletionTokensDetails.AudioTokens
	usage.CompletionTokensDetails.AcceptedPredictionTokens = xaiUsage.CompletionTokensDetails.AcceptedPredictionTokens
	usage.CompletionTokensDetails.RejectedPredictionTokens = xaiUsage.CompletionTokensDetails.RejectedPredictionTokens

	return usage
}

// 转换 XAI usage 为 OpenAI 兼容格式
func convertXaiUsageToOpenAI(xaiUsage *XaiUsage) *model.Usage {
	if xaiUsage == nil {
		return nil
	}

	// 按照 OpenAI 格式：CompletionTokens = TotalTokens - PromptTokens
	completionTokens := xaiUsage.TotalTokens - xaiUsage.PromptTokens

	// 创建 Usage 并保留详细信息
	usage := &model.Usage{
		PromptTokens:     xaiUsage.PromptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      xaiUsage.TotalTokens,
	}

	// 保留 prompt_tokens_details
	usage.PromptTokensDetails.CachedTokens = xaiUsage.PromptTokensDetails.CachedTokens
	usage.PromptTokensDetails.AudioTokens = xaiUsage.PromptTokensDetails.AudioTokens
	usage.PromptTokensDetails.TextTokens = xaiUsage.PromptTokensDetails.TextTokens
	usage.PromptTokensDetails.ImageTokens = xaiUsage.PromptTokensDetails.ImageTokens

	// 保留 completion_tokens_details
	usage.CompletionTokensDetails.ReasoningTokens = xaiUsage.CompletionTokensDetails.ReasoningTokens
	usage.CompletionTokensDetails.AudioTokens = xaiUsage.CompletionTokensDetails.AudioTokens
	usage.CompletionTokensDetails.AcceptedPredictionTokens = xaiUsage.CompletionTokensDetails.AcceptedPredictionTokens
	usage.CompletionTokensDetails.RejectedPredictionTokens = xaiUsage.CompletionTokensDetails.RejectedPredictionTokens

	return usage
}

// 生成随机 ID（类似 OpenAI 格式）
func generateRandomID() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)[:29] // OpenAI ID 通常是 29 个字符
}

func StreamHandler(c *gin.Context, resp *http.Response, relayMode int) (*model.ErrorWithStatusCode, string, *model.Usage) {
	responseText := ""
	var usage *model.Usage

	// 获取请求开始时间用于计算首字延迟
	var startTime time.Time
	if requestStartTime, exists := c.Get("request_start_time"); exists {
		if t, ok := requestStartTime.(time.Time); ok {
			startTime = t
		} else {
			startTime = time.Now() // fallback
		}
	} else {
		startTime = time.Now() // fallback
	}

	var firstWordTime *time.Time

	// 设置流式响应头
	common.SetEventStreamHeaders(c)

	// 立即写入头部
	c.Writer.WriteHeader(http.StatusOK)

	// 确保支持 flushing
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return openai.ErrorWrapper(fmt.Errorf("streaming not supported"), "streaming_not_supported", http.StatusInternalServerError), "", nil
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		if i := strings.Index(string(data), "\n"); i >= 0 {
			return i + 1, data[0:i], nil
		}
		if atEOF {
			return len(data), data, nil
		}
		return 0, nil, nil
	})

	for scanner.Scan() {
		data := scanner.Text()
		if len(data) < 6 { // ignore blank line or wrong format
			continue
		}
		if data[:6] != "data: " && data[:6] != "[DONE]" {
			continue
		}

		originalData := data
		dataContent := data[6:]

		if !strings.HasPrefix(dataContent, "[DONE]") {
			switch relayMode {
			case constant.RelayModeChatCompletions:
				var streamResponse XaiStreamResponse
				err := json.Unmarshal([]byte(dataContent), &streamResponse)
				if err != nil {
					// 如果解析失败，检查是否是XAI错误格式
					var xaiError map[string]interface{}
					if unmarshalErr := json.Unmarshal([]byte(dataContent), &xaiError); unmarshalErr == nil {
						// 检查是否是XAI错误格式（包含code和error字段）
						if code, hasCode := xaiError["code"]; hasCode {
							if errorMsg, hasError := xaiError["error"]; hasError {
								// 转换为OpenAI错误格式的流式响应
								openaiError := map[string]interface{}{
									"error": map[string]interface{}{
										"message": fmt.Sprintf("%v", errorMsg),
										"type":    "api_error",
										"param":   "",
										"code":    fmt.Sprintf("%v", code),
									},
								}

								// 将转换后的错误序列化为JSON
								if convertedData, marshalErr := json.Marshal(openaiError); marshalErr == nil {
									writeLine(c.Writer, "data: "+string(convertedData))
									flusher.Flush()
									continue
								}
							}
						}
					}

					logger.SysError("error unmarshalling XAI stream response: " + err.Error())
					// 发送原始数据
					writeLine(c.Writer, originalData)
					flusher.Flush()
					continue
				}
				for _, choice := range streamResponse.Choices {
					content := conv.AsString(choice.Delta.Content)
					if content != "" && firstWordTime == nil {
						// 记录首字时间（与其他渠道保持一致）
						now := time.Now()
						firstWordTime = &now
					}
					responseText += content
				}
				if streamResponse.Usage != nil {
					// 转换 XAI usage 为 OpenAI 兼容格式（用于内部记录）
					usage = convertXaiUsageToOpenAI(streamResponse.Usage)

					// 转换为完整格式用于客户端响应（显示所有字段包括0值）
					completeUsage := convertXaiUsageToComplete(streamResponse.Usage)

					// 创建转换后的流式响应，使用完整的 Usage 格式
					openaiStreamResponse := struct {
						openai.ChatCompletionsStreamResponse
						Usage *XaiCompleteUsage `json:"usage,omitempty"`
					}{
						ChatCompletionsStreamResponse: streamResponse.ChatCompletionsStreamResponse,
						Usage:                         completeUsage,
					}

					// 重新序列化
					modifiedData, err := json.Marshal(openaiStreamResponse)
					if err != nil {
						logger.SysError("error marshalling modified stream response: " + err.Error())
						writeLine(c.Writer, originalData)
					} else {
						writeLine(c.Writer, "data: "+string(modifiedData))
					}
				} else {
					writeLine(c.Writer, originalData)
				}
			case constant.RelayModeCompletions:
				var streamResponse openai.CompletionsStreamResponse
				err := json.Unmarshal([]byte(dataContent), &streamResponse)
				if err != nil {
					logger.SysError("error unmarshalling XAI stream response: " + err.Error())
					writeLine(c.Writer, originalData)
					flusher.Flush()
					continue
				}

				for _, choice := range streamResponse.Choices {
					if choice.Text != "" && firstWordTime == nil {
						// 记录首字时间（与其他渠道保持一致）
						now := time.Now()
						firstWordTime = &now
					}
					responseText += choice.Text
				}
				writeLine(c.Writer, originalData)
			}
		} else {
			// [DONE] 消息
			if strings.HasPrefix(originalData, "data: [DONE]") {
				originalData = originalData[:12]
			}
			writeLine(c.Writer, originalData)
		}

		// 立即 flush 每个数据块
		flusher.Flush()
	}

	err := resp.Body.Close()
	if err != nil {
		return openai.ErrorWrapper(err, "close_response_body_failed", http.StatusInternalServerError), "", nil
	}

	// 计算首字延迟并存储到 context 中
	if firstWordTime != nil {
		firstWordLatency := firstWordTime.Sub(startTime).Seconds()
		c.Set("first_word_latency", firstWordLatency)
	}

	return nil, responseText, usage
}

// 辅助函数：写入一行数据到响应体
func writeLine(w io.Writer, data string) {
	// 清理数据末尾的回车符
	data = strings.TrimSuffix(data, "\r")
	// 写入数据和换行符
	fmt.Fprintf(w, "%s\n\n", data)
}

func Handler(c *gin.Context, resp *http.Response, promptTokens int, modelName string) (*model.ErrorWithStatusCode, *model.Usage) {
	var xaiResponse XaiResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError), nil
	}
	err = resp.Body.Close()
	if err != nil {
		return openai.ErrorWrapper(err, "close_response_body_failed", http.StatusInternalServerError), nil
	}

	// 首先尝试解析为正常的XAI响应
	err = json.Unmarshal(responseBody, &xaiResponse)
	if err != nil {
		// 如果解析失败，可能是XAI特有的错误格式，尝试解析并转换
		var xaiError map[string]interface{}
		if unmarshalErr := json.Unmarshal(responseBody, &xaiError); unmarshalErr == nil {
			// 检查是否是XAI错误格式（包含code和error字段）
			if code, hasCode := xaiError["code"]; hasCode {
				if errorMsg, hasError := xaiError["error"]; hasError {
					// 转换为OpenAI错误格式
					convertedError := model.Error{
						Message: fmt.Sprintf("%v", errorMsg),
						Type:    "api_error",
						Param:   "",
						Code:    fmt.Sprintf("%v", code),
					}
					return &model.ErrorWithStatusCode{
						Error:      convertedError,
						StatusCode: resp.StatusCode,
					}, nil
				}
			}
		}
		return openai.ErrorWrapper(err, "unmarshal_response_body_failed", http.StatusInternalServerError), nil
	}

	// 检查是否有OpenAI格式的错误
	if xaiResponse.Error.Type != "" {
		return &model.ErrorWithStatusCode{
			Error:      xaiResponse.Error,
			StatusCode: resp.StatusCode,
		}, nil
	}

	// 转换 XAI usage 为完整格式（显示所有字段包括0值）
	completeUsage := convertXaiUsageToComplete(&xaiResponse.Usage)

	// 同时创建标准 Usage 用于内部记录
	usage := convertXaiUsageToOpenAI(&xaiResponse.Usage)

	// 如果没有有效的 usage 信息，则按照 OpenAI 的逻辑计算
	if usage == nil || usage.TotalTokens == 0 || (usage.PromptTokens == 0 && usage.CompletionTokens == 0) {
		completionTokens := 0
		for _, choice := range xaiResponse.Choices {
			completionTokens += openai.CountTokenText(choice.Message.StringContent(), modelName)
		}
		usage = &model.Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		}
	}

	// 创建标准的 OpenAI 响应格式，使用完整的 Usage（显示所有字段）
	openaiResponse := struct {
		Id      string                      `json:"id"`
		Object  string                      `json:"object"`
		Created int64                       `json:"created"`
		Model   string                      `json:"model"`
		Choices []openai.TextResponseChoice `json:"choices"`
		Usage   XaiCompleteUsage            `json:"usage"`
	}{
		Id:      "chatcmpl-" + generateRandomID(),
		Object:  "chat.completion",
		Created: int64(time.Now().Unix()),
		Model:   modelName,
		Choices: xaiResponse.Choices,
		Usage:   *completeUsage,
	}

	// 将修改后的响应序列化
	modifiedResponseBody, err := json.Marshal(openaiResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "marshal_modified_response_failed", http.StatusInternalServerError), nil
	}

	// We shouldn't set the header before we parse the response body, because the parse part may fail.
	// And then we will have to send an error response, but in this case, the header has already been set.
	// So the HTTPClient will be confused by the response.
	// For example, Postman will report error, and we cannot check the response at all.
	for k, v := range resp.Header {
		c.Writer.Header().Set(k, v[0])
	}

	// 更新 Content-Length 头
	c.Writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(modifiedResponseBody)))
	c.Writer.WriteHeader(resp.StatusCode)

	// 发送修改后的响应体
	_, err = c.Writer.Write(modifiedResponseBody)
	if err != nil {
		return openai.ErrorWrapper(err, "write_modified_response_failed", http.StatusInternalServerError), nil
	}

	return nil, usage
}
