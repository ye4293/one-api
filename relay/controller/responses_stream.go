package controller

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/audit"
	"github.com/songquanpeng/one-api/common/config"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
	"github.com/songquanpeng/one-api/service"
)

func mapResponsesRequestModel(body []byte, modelName string) ([]byte, error) {
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil || request == nil {
		return nil, fmt.Errorf("Responses 请求必须为 JSON 对象")
	}
	request["model"], _ = json.Marshal(modelName)
	return json.Marshal(request)
}

func responsesOutputError(err error) *model.ErrorWithStatusCode {
	code, status := "responses_output_invalid", http.StatusBadGateway
	var stateErr *dbmodel.ResponsesStateError
	if errors.As(err, &stateErr) {
		code, status = stateErr.Code, stateErr.Status
	}
	return &model.ErrorWithStatusCode{Error: model.Error{Message: err.Error(), Type: "responses_state_error", Code: code}, StatusCode: status}
}

// readResponseEvent 按完整 SSE 事件读取，保留原始字节及多行 data，限制单事件内存。
func readResponseEvent(reader *bufio.Reader) ([]byte, []byte, string, error) {
	const maxEventSize = 16 << 20
	var frame, line []byte
	var data [][]byte
	var event string
	for {
		part, err := reader.ReadSlice('\n')
		if len(frame)+len(part) > maxEventSize {
			return nil, nil, "", fmt.Errorf("Responses SSE 事件超过 16 MiB")
		}
		frame = append(frame, part...)
		line = append(line, part...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			if err == io.EOF && len(frame) > 0 {
				err = io.ErrUnexpectedEOF
			}
			return nil, nil, "", err
		}
		content := bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r"))
		if len(content) == 0 {
			return frame, bytes.Join(data, []byte("\n")), event, nil
		}
		if bytes.HasPrefix(content, []byte("data:")) {
			value := bytes.TrimPrefix(content[5:], []byte(" "))
			data = append(data, bytes.Clone(value))
		} else if bytes.HasPrefix(content, []byte("event:")) {
			event = strings.TrimSpace(string(content[6:]))
		}
		line = nil
	}
}

func streamResponsesEvents(c *gin.Context, resp *http.Response, meta *util.RelayMeta) (usage *openai.ResponseUsage, result *model.ErrorWithStatusCode) {
	if resp == nil || resp.Body == nil {
		return nil, openai.ErrorWrapper(fmt.Errorf("上游未返回响应"), "empty_response", 502)
	}
	defer util.CloseResponseBodyGracefully(resp)
	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, openai.ErrorWrapper(err, "read_error_response_failed", 502)
		}
		message, kind := parseUpstreamErrorMessage(body, c.GetHeader("X-Request-ID"))
		return nil, &model.ErrorWithStatusCode{Error: model.Error{Message: message, Type: kind, Code: fmt.Sprintf("status_%d", resp.StatusCode)}, StatusCode: resp.StatusCode}
	}
	audit.WrapUpstreamBody(c, resp)
	stopCancel := context.AfterFunc(c.Request.Context(), func() { _ = resp.Body.Close() })
	defer stopCancel()
	timeout := time.Duration(config.StreamingTimeout) * time.Second
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	var timedOut atomic.Bool
	timer := time.AfterFunc(timeout, func() { timedOut.Store(true); _ = resp.Body.Close() })
	defer timer.Stop()
	reader := bufio.NewReaderSize(resp.Body, 64<<10)
	var fullText strings.Builder
	webSearchCalls := 0
	defer func() {
		if usage == nil && (c.Writer.Written() || fullText.Len() > 0 || c.GetBool("responses_upstream_executed")) {
			usage = &openai.ResponseUsage{InputTokens: meta.PromptTokens, TotalTokens: meta.PromptTokens}
			c.Set("responses_usage_estimated", true)
		}
		if usage != nil && usage.OutputTokens == 0 && fullText.Len() > 0 {
			usage.OutputTokens = openai.CountTokenText(fullText.String(), meta.ActualModelName)
			if usage.InputTokens == 0 {
				usage.InputTokens = meta.PromptTokens
			}
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens
			c.Set("responses_usage_estimated", true)
		}
	}()
	for {
		if err := c.Request.Context().Err(); err != nil {
			return usage, openai.ErrorWrapper(err, "responses_stream_cancelled", 499)
		}
		frame, data, event, err := readResponseEvent(reader)
		if err != nil {
			if timedOut.Load() {
				err = fmt.Errorf("读取 Responses 流超时")
			}
			if c.Request.Context().Err() != nil {
				err = c.Request.Context().Err()
			}
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return usage, openai.ErrorWrapper(err, "responses_stream_interrupted", 502)
		}
		timer.Reset(timeout)
		// 上游注释不提前提交 200，首个有效事件登记成功后才开始输出。
		if len(data) == 0 {
			if c.Writer.Written() {
				if _, err := c.Writer.Write(frame); err != nil {
					c.Set("responses_client_write_failed", true)
					return usage, responsesOutputError(err)
				}
				c.Writer.Flush()
			}
			continue
		}
		if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
			return usage, openai.ErrorWrapper(io.ErrUnexpectedEOF, "responses_missing_terminal_event", 502)
		}
		var chunk openai.OpenaiResponseStreamResponse
		if err := json.Unmarshal(data, &chunk); err != nil {
			return usage, responsesOutputError(err)
		}
		if chunk.Type == "" {
			chunk.Type = event
		}
		if chunk.Response != nil && chunk.Response.Usage != nil {
			usage = chunk.Response.Usage
		}
		if chunk.Type == "error" || chunk.Type == "response.failed" || event == "error" || event == "response.failed" {
			message, code := "上游流式请求失败", "upstream_stream_error"
			if parsed := parseStreamErrorEvent(string(data)); parsed != nil {
				message, code = parsed.Error.Message, parsed.Error.Code
			}
			if parsed := parseResponseFailedEvent(string(data)); parsed != nil {
				message, code = parsed.Response.Error.Message, parsed.Response.Error.Code
			}
			return usage, &model.ErrorWithStatusCode{Error: model.Error{Message: message, Type: "upstream_error", Code: code}, StatusCode: 502}
		}
		c.Set("responses_upstream_executed", true)
		if err := service.RegisterResponseOutput(c, data); err != nil {
			return usage, responsesOutputError(err)
		}
		if chunk.Type == "response.output_text.delta" {
			fullText.WriteString(chunk.Delta)
		}
		if chunk.Type == "response.output_item.done" && chunk.Item != nil && chunk.Item.Type == "web_search_call" {
			webSearchCalls++
			c.Set("web_search_tool_call_count", webSearchCalls)
		}
		if chunk.Response != nil {
			c.Set("x_response_id", chunk.Response.ID)
			for _, output := range chunk.Response.Output {
				if output.Type == "image_generation_call" {
					c.Set("image_generation_call", true)
					c.Set("image_generation_call_quality", output.Quality)
					c.Set("image_generation_call_size", output.Size)
				}
			}
		}
		if meta.FirstResponseTime.IsZero() {
			meta.FirstResponseTime = time.Now()
		}
		common.SetEventStreamHeaders(c)
		if _, err := c.Writer.Write(frame); err != nil {
			c.Set("responses_client_write_failed", true)
			return usage, responsesOutputError(err)
		}
		c.Writer.Flush()
		if chunk.Type == "response.completed" || chunk.Type == "response.incomplete" {
			c.Set("responses_terminal_status", chunk.Type)
			if usage == nil {
				usage = &openai.ResponseUsage{InputTokens: meta.PromptTokens, TotalTokens: meta.PromptTokens}
				c.Set("responses_usage_estimated", true)
			}
			return usage, nil
		}
	}
}
