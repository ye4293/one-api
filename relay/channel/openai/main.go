package openai

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/conv"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/constant"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/service"
)

func StreamHandler(c *gin.Context, resp *http.Response, relayMode int) (*model.ErrorWithStatusCode, string, *model.Usage) {
	c.Set(ResponseServiceTierContextKey, "")
	responseText := ""
	var usage *model.Usage
	startTime := time.Now()
	if value, ok := c.Get("request_start_time"); ok {
		if t, ok := value.(time.Time); ok {
			startTime = t
		}
	}
	var firstWordTime *time.Time
	responseID := ""
	cached := false
	origin := dbmodel.RelayResponseOrigin(c, resp, "")
	timeout := time.Duration(config.StreamingTimeout) * time.Second
	common.SetEventStreamHeaders(c)
	err := common.ScanSSEOwned(c.Request.Context(), resp.Body, timeout, 0, func(data string) bool {
		if data == "[DONE]" {
			if responseID != "" && !cached {
				if err := dbmodel.RegisterResponseOrigin(origin); err != nil {
					logger.Warnf(c.Request.Context(), "chat origin registration failed: %v", err)
				}
			}
			c.Render(-1, common.CustomEvent{Data: "data: [DONE]"})
			return false
		}
		if relayMode == constant.RelayModeChatCompletions {
			var chunk ChatCompletionsStreamResponse
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				logger.Warnf(c.Request.Context(), "invalid chat stream chunk: %v", err)
				return true
			}
			if chunk.Id != "" {
				responseID = chunk.Id
				origin.ResponseID = responseID
				c.Set("x_response_id", responseID)
			}
			if chunk.ServiceTier != "" {
				c.Set(ResponseServiceTierContextKey, chunk.ServiceTier)
			}
			for _, choice := range chunk.Choices {
				content := conv.AsString(choice.Delta.Content)
				if content != "" && firstWordTime == nil {
					now := time.Now()
					firstWordTime = &now
				}
				responseText += content
			}
			if chunk.Usage != nil {
				usage = chunk.Usage
			}
		} else {
			var chunk CompletionsStreamResponse
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				logger.Warnf(c.Request.Context(), "invalid text stream chunk: %v", err)
				return true
			}
			if chunk.Id != "" {
				responseID = chunk.Id
				origin.ResponseID = responseID
				c.Set("x_response_id", responseID)
			}
			if chunk.ServiceTier != "" {
				c.Set(ResponseServiceTierContextKey, chunk.ServiceTier)
			}
			for _, choice := range chunk.Choices {
				if choice.Text != "" && firstWordTime == nil {
					now := time.Now()
					firstWordTime = &now
				}
				responseText += choice.Text
			}
		}
		if responseID != "" && !cached {
			if err := dbmodel.RegisterResponseOrigin(origin); err != nil {
				logger.Warnf(c.Request.Context(), "chat origin registration failed: %v", err)
			} else {
				cached = true
			}
		}
		c.Render(-1, common.CustomEvent{Data: "data: " + data})
		c.Writer.Flush()
		service.RecordGeminiResponseSignatures(c, []byte(data))
		return true
	}, nil)
	if firstWordTime != nil {
		c.Set("first_word_latency", firstWordTime.Sub(startTime).Seconds())
	}
	if err != nil && err != io.EOF && c.Request.Context().Err() == nil {
		return ErrorWrapper(err, "stream_read_failed", http.StatusBadGateway), responseText, usage
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
	c.Set(ResponseServiceTierContextKey, textResponse.ServiceTier)
	// Reset response body
	resp.Body = io.NopCloser(bytes.NewBuffer(responseBody))

	if err := dbmodel.RegisterResponseOrigin(dbmodel.RelayResponseOrigin(c, resp, textResponse.Id)); err != nil {
		logger.Warnf(c.Request.Context(), "chat origin registration failed: %v", err)
	}
	c.Set("x_response_id", textResponse.Id)

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
	service.RecordGeminiResponseSignatures(c, responseBody)
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

	return nil, &textResponse.Usage
}
