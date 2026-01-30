package openai

import (
	"bufio"
	"bytes"
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
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/constant"
	"github.com/songquanpeng/one-api/relay/model"
)

func StreamHandler(c *gin.Context, resp *http.Response, relayMode int) (*model.ErrorWithStatusCode, string, *model.Usage) {
	responseText := ""
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
	dataChan := make(chan string)
	stopChan := make(chan bool)
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
	// ===== 新增：追踪 response ID 和缓存状态 =====
	var responseId string
	var idCached bool = false
	// ===== 新增结束 =====

	go func() {
		for scanner.Scan() {
			data := scanner.Text()
			if len(data) < 6 { // ignore blank line or wrong format
				continue
			}
			if data[:6] != "data: " && data[:6] != "[DONE]" {
				continue
			}
			dataChan <- data
			data = data[6:]
			if !strings.HasPrefix(data, "[DONE]") {
				switch relayMode {
				case constant.RelayModeChatCompletions:
					var streamResponse ChatCompletionsStreamResponse
					err := json.Unmarshal([]byte(data), &streamResponse)
					if err != nil {
						logger.SysError("error unmarshalling stream response: " + err.Error())
						continue // just ignore the error
					}

					// ===== 新增：提取 Chat Completions 的 response ID 并缓存 =====
					if streamResponse.Id != "" && responseId == "" {
						responseId = streamResponse.Id
					}
					// 在收到第一个非空内容时缓存（确保响应正常）
					if responseId != "" && !idCached {
						for _, choice := range streamResponse.Choices {
							content := conv.AsString(choice.Delta.Content)
							if content != "" {
								// 第一次有内容输出时缓存 ID
								channelId := c.GetInt("channel_id")
								if channelId > 0 {
									expireMinutes := int64(1440)
									if writeErr := dbmodel.SetClaudeCacheIdToRedis(responseId, fmt.Sprintf("%d", channelId), expireMinutes); writeErr != nil {
										logger.SysLog(fmt.Sprintf("[Chat Completions Stream Cache] Failed to cache response_id=%s to channel_id=%d: %v",
											responseId, channelId, writeErr))
									} else {
										logger.SysLog(fmt.Sprintf("[Chat Completions Stream Cache] Cached response_id=%s -> channel_id=%d (TTL: 24h)",
											responseId, channelId))
									}
								}
								idCached = true
								break // 已缓存，跳出循环
							}
						}
					}
					// ===== 新增结束 =====

					for _, choice := range streamResponse.Choices {
						content := conv.AsString(choice.Delta.Content)
						if content != "" && firstWordTime == nil {
							// 记录首字时间
							now := time.Now()
							firstWordTime = &now
						}
						responseText += content
					}
					if streamResponse.Usage != nil {
						usage = streamResponse.Usage
					}
				case constant.RelayModeCompletions:
					var streamResponse CompletionsStreamResponse
					err := json.Unmarshal([]byte(data), &streamResponse)
					if err != nil {
						logger.SysError("error unmarshalling stream response: " + err.Error())
						continue
					}

					// ===== 新增：提取 Completions 的 response ID 并缓存 =====
					if streamResponse.Id != "" && responseId == "" {
						responseId = streamResponse.Id
					}
					// 在收到第一个非空文本时缓存（确保响应正常）
					if responseId != "" && !idCached {
						for _, choice := range streamResponse.Choices {
							if choice.Text != "" {
								// 第一次有内容输出时缓存 ID
								channelId := c.GetInt("channel_id")
								if channelId > 0 {
									expireMinutes := int64(1440)
									if writeErr := dbmodel.SetClaudeCacheIdToRedis(responseId, fmt.Sprintf("%d", channelId), expireMinutes); writeErr != nil {
										logger.SysLog(fmt.Sprintf("[Text Completions Stream Cache] Failed to cache response_id=%s to channel_id=%d: %v",
											responseId, channelId, writeErr))
									} else {
										logger.SysLog(fmt.Sprintf("[Text Completions Stream Cache] Cached response_id=%s -> channel_id=%d (TTL: 24h)",
											responseId, channelId))
									}
								}
								idCached = true
								break // 已缓存，跳出循环
							}
						}
					}
					// ===== 新增结束 =====

					for _, choice := range streamResponse.Choices {
						if choice.Text != "" && firstWordTime == nil {
							// 记录首字时间
							now := time.Now()
							firstWordTime = &now
						}
						responseText += choice.Text
					}
				}
			}
		}
		stopChan <- true
	}()
	common.SetEventStreamHeaders(c)
	c.Stream(func(w io.Writer) bool {
		select {
		case data := <-dataChan:
			if strings.HasPrefix(data, "data: [DONE]") {
				data = data[:12]
			}
			// some implementations may add \r at the end of data
			data = strings.TrimSuffix(data, "\r")
			c.Render(-1, common.CustomEvent{Data: data})
			return true
		case <-stopChan:
			return false
		}
	})
	err := resp.Body.Close()
	if err != nil {
		return ErrorWrapper(err, "close_response_body_failed", http.StatusInternalServerError), "", nil
	}

	// 计算首字延迟并存储到 context 中
	if firstWordTime != nil {
		firstWordLatency := firstWordTime.Sub(startTime).Seconds()
		c.Set("first_word_latency", firstWordLatency)
	}

	return nil, responseText, usage
}

func Handler(c *gin.Context, resp *http.Response, promptTokens int, modelName string) (*model.ErrorWithStatusCode, *model.Usage) {
	var textResponse SlimTextResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError), nil
	}
	err = resp.Body.Close()
	if err != nil {
		return ErrorWrapper(err, "close_response_body_failed", http.StatusInternalServerError), nil
	}
	err = json.Unmarshal(responseBody, &textResponse)
	if err != nil {
		return ErrorWrapper(err, "unmarshal_response_body_failed", http.StatusInternalServerError), nil
	}
	if textResponse.Error.Type != "" {
		return &model.ErrorWithStatusCode{
			Error:      textResponse.Error,
			StatusCode: resp.StatusCode,
		}, nil
	}
	// Reset response body
	resp.Body = io.NopCloser(bytes.NewBuffer(responseBody))

	// We shouldn't set the header before we parse the response body, because the parse part may fail.
	// And then we will have to send an error response, but in this case, the header has already been set.
	// So the HTTPClient will be confused by the response.
	// For example, Postman will report error, and we cannot check the response at all.
	for k, v := range resp.Header {
		c.Writer.Header().Set(k, v[0])
	}
	c.Writer.WriteHeader(resp.StatusCode)
	_, err = io.Copy(c.Writer, resp.Body)
	if err != nil {
		return ErrorWrapper(err, "copy_response_body_failed", http.StatusInternalServerError), nil
	}
	err = resp.Body.Close()
	if err != nil {
		return ErrorWrapper(err, "close_response_body_failed", http.StatusInternalServerError), nil
	}

	if textResponse.Usage.TotalTokens == 0 || (textResponse.Usage.PromptTokens == 0 && textResponse.Usage.CompletionTokens == 0) {
		completionTokens := 0
		for _, choice := range textResponse.Choices {
			completionTokens += CountTokenText(choice.Message.StringContent(), modelName)
		}
		textResponse.Usage = model.Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		}
	}

	// ===== 新增：缓存 response_id 到 Redis =====
	if textResponse.Id != "" {
		channelId := c.GetInt("channel_id")
		if channelId > 0 {
			// 使用 24 小时 TTL (1440 分钟)
			expireMinutes := int64(1440)
			if writeErr := dbmodel.SetClaudeCacheIdToRedis(textResponse.Id, fmt.Sprintf("%d", channelId), expireMinutes); writeErr != nil {
				// Redis 写入失败不影响主流程，只记录日志
				logger.SysLog(fmt.Sprintf("[Text Completions Cache] Failed to cache response_id=%s to channel_id=%d: %v",
					textResponse.Id, channelId, writeErr))
			} else {
				logger.SysLog(fmt.Sprintf("[Text Completions Cache] Cached response_id=%s -> channel_id=%d (TTL: 24h)",
					textResponse.Id, channelId))
			}
		}
	}
	// ===== 新增结束 =====

	return nil, &textResponse.Usage
}
