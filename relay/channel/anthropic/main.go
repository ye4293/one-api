package anthropic

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/songquanpeng/one-api/common/render"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/image"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/model"
)

func stopReasonClaude2OpenAI(reason *string) string {
	if reason == nil {
		return ""
	}
	switch *reason {
	case "end_turn":
		return "stop"
	case "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return *reason
	}
}

func ConvertRequest(textRequest model.GeneralOpenAIRequest) *Request {
	claudeTools := make([]Tool, 0, len(textRequest.Tools))

	for _, tool := range textRequest.Tools {
		if params, ok := tool.Function.Parameters.(map[string]any); ok {
			var required []string
			if reqVal, ok := params["required"]; ok {
				if reqArr, ok := reqVal.([]interface{}); ok {
					for _, r := range reqArr {
						if s, ok := r.(string); ok {
							required = append(required, s)
						}
					}
				} else if reqStrArr, ok := reqVal.([]string); ok {
					required = reqStrArr
				}
			}
			typeStr := "object"
			if t, ok := params["type"].(string); ok {
				typeStr = t
			}
			claudeTools = append(claudeTools, Tool{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				InputSchema: InputSchema{
					Type:       typeStr,
					Properties: params["properties"],
					Required:   required,
				},
			})
		}
	}

	// 处理 MaxTokens：优先使用 MaxCompletionTokens（OpenAI 新格式），其次使用 MaxTokens
	maxTokens := textRequest.MaxTokens
	if textRequest.MaxCompletionTokens > 0 {
		maxTokens = textRequest.MaxCompletionTokens
	}

	claudeRequest := Request{
		Model:       textRequest.Model,
		MaxTokens:   maxTokens,
		Temperature: textRequest.Temperature,
		TopP:        textRequest.TopP,
		TopK:        textRequest.TopK,
		Stream:      textRequest.Stream,
		Tools:       claudeTools,
	}
	if stop, ok := textRequest.Stop.(string); ok && stop != "" {
		claudeRequest.StopSequences = []string{stop}
	}
	if len(claudeTools) > 0 {
		claudeToolChoice := &ToolChoice{Type: "auto"} // default value https://docs.anthropic.com/en/docs/build-with-claude/tool-use#controlling-claudes-output
		if choice, ok := textRequest.ToolChoice.(map[string]any); ok {
			if function, ok := choice["function"].(map[string]any); ok {
				claudeToolChoice.Type = "tool"
				if name, ok := function["name"].(string); ok {
					claudeToolChoice.Name = name
				}
			}
		} else if toolChoiceType, ok := textRequest.ToolChoice.(string); ok {
			// OpenAI tool_choice 到 Claude tool_choice 的映射：
			// "auto" -> "auto" (模型自行决定)
			// "required" -> "any" (必须调用工具)
			// "any" -> "any" (兼容已使用 Claude 格式的请求)
			// "none" -> 不设置 tool_choice (当前保持 auto，Claude 会自行判断)
			switch toolChoiceType {
			case "required", "any":
				claudeToolChoice.Type = "any"
			case "auto":
				claudeToolChoice.Type = "auto"
			case "none":
				// 对于 none，不设置 tool_choice，让 Claude 自行判断
				// 但由于有 tools，仍然可能调用，如果完全不想调用需要不传 tools
				claudeToolChoice = nil
			}
		}
		if claudeToolChoice != nil {
			claudeRequest.ToolChoice = claudeToolChoice
		}
	}
	if claudeRequest.MaxTokens == 0 {
		claudeRequest.MaxTokens = 4096
	}
	// legacy model name mapping
	if claudeRequest.Model == "claude-instant-1" {
		claudeRequest.Model = "claude-instant-1.1"
	} else if claudeRequest.Model == "claude-2" {
		claudeRequest.Model = "claude-2.1"
	}
	for _, message := range textRequest.Messages {
		if message.Role == "system" && claudeRequest.System == nil {
			claudeRequest.System = message.StringContent()
			continue
		}
		claudeMessage := Message{
			Role: message.Role,
		}
		if message.IsStringContent() {
			var contentBlocks []ContentBlockParam
			content := ContentBlockParam{
				Type: "text",
				Text: message.StringContent(),
			}
			if message.Role == "tool" {
				claudeMessage.Role = "user"
				content = ContentBlockParam{
					Type:      "tool_result",
					ToolUseID: message.ToolCallId,
					Content:   message.StringContent(),
				}
			}
			contentBlocks = append(contentBlocks, content)
			for i := range message.ToolCalls {
				inputParam := make(map[string]any)
				if args, ok := message.ToolCalls[i].Function.Arguments.(string); ok {
					_ = json.Unmarshal([]byte(args), &inputParam)
				}
				contentBlocks = append(contentBlocks, ContentBlockParam{
					Type:  "tool_use",
					Id:    message.ToolCalls[i].Id,
					Name:  message.ToolCalls[i].Function.Name,
					Input: inputParam,
				})
			}
			claudeMessage.Content = contentBlocks
			claudeRequest.Messages = append(claudeRequest.Messages, claudeMessage)
			continue
		}
		var contents []ContentBlockParam
		openaiContent := message.ParseContent()
		for _, part := range openaiContent {
			var content ContentBlockParam
			if part.Type == model.ContentTypeText {
				content.Type = "text"
				content.Text = part.Text
			} else if part.Type == model.ContentTypeImageURL {
				content.Type = "image"
				mimeType, data, err := image.GetImageFromUrl(part.ImageURL.Url)
				if err != nil {
					logger.SysError(fmt.Sprintf("Error getting image from URL: %v", err))
					continue
				}
				content.Source = Base64ImageSource{
					Type:      "base64",
					MediaType: mimeType,
					Data:      data,
				}
			}
			contents = append(contents, content)
		}
		claudeMessage.Content = contents
		claudeRequest.Messages = append(claudeRequest.Messages, claudeMessage)
	}
	return &claudeRequest
}

// https://docs.anthropic.com/claude/reference/messages-streaming
func StreamResponseClaude2OpenAI(claudeResponse *StreamResponse) (*openai.ChatCompletionsStreamResponse, *Response) {
	var response *Response
	var responseText string
	var stopReason string
	tools := make([]model.Tool, 0)

	switch claudeResponse.Type {
	case "message_start":
		return nil, claudeResponse.Message
	case "content_block_start":
		if claudeResponse.ContentBlock != nil {
			responseText = claudeResponse.ContentBlock.Text
			if claudeResponse.ContentBlock.Type == "tool_use" {
				tools = append(tools, model.Tool{
					Id:   claudeResponse.ContentBlock.Id,
					Type: "function",
					Function: model.Function{
						Name:      claudeResponse.ContentBlock.Name,
						Arguments: "",
					},
				})
			}
		}
	case "content_block_delta":
		if claudeResponse.Delta != nil {
			responseText = claudeResponse.Delta.Text
			if claudeResponse.Delta.Type == "input_json_delta" {
				tools = append(tools, model.Tool{
					Function: model.Function{
						Arguments: claudeResponse.Delta.PartialJson,
					},
				})
			}
		}
	case "message_delta":
		if claudeResponse.Delta != nil {
			response = &Response{
				Usage: claudeResponse.Usage,
			}
		}
		if claudeResponse.Delta != nil && claudeResponse.Delta.StopReason != nil {
			stopReason = *claudeResponse.Delta.StopReason
		}
	}
	var choice openai.ChatCompletionsStreamResponseChoice
	choice.Delta.Content = responseText
	if len(tools) > 0 {
		choice.Delta.Content = nil // compatible with other OpenAI derivative applications, like LobeOpenAICompatibleFactory ...
		choice.Delta.ToolCalls = tools
	}
	choice.Delta.Role = "assistant"
	finishReason := stopReasonClaude2OpenAI(&stopReason)
	if finishReason != "null" {
		choice.FinishReason = &finishReason
	}
	var openaiResponse openai.ChatCompletionsStreamResponse
	openaiResponse.Object = "chat.completion.chunk"
	openaiResponse.Choices = []openai.ChatCompletionsStreamResponseChoice{choice}
	return &openaiResponse, response
}

func ResponseClaude2OpenAI(claudeResponse *Response) *openai.TextResponse {
	var responseText string
	if len(claudeResponse.Content) > 0 {
		responseText = claudeResponse.Content[0].Text
	}
	tools := make([]model.Tool, 0)
	for _, v := range claudeResponse.Content {
		if v.Type == "tool_use" {
			args, _ := json.Marshal(v.Input)
			tools = append(tools, model.Tool{
				Id:   v.Id,
				Type: "function", // compatible with other OpenAI derivative applications
				Function: model.Function{
					Name:      v.Name,
					Arguments: string(args),
				},
			})
		}
	}
	choice := openai.TextResponseChoice{
		Index: 0,
		Message: model.Message{
			Role:      "assistant",
			Content:   responseText,
			Name:      nil,
			ToolCalls: tools,
		},
		FinishReason: stopReasonClaude2OpenAI(claudeResponse.StopReason),
	}
	fullTextResponse := openai.TextResponse{
		Id:      fmt.Sprintf("chatcmpl-%s", claudeResponse.Id),
		Model:   claudeResponse.Model,
		Object:  "chat.completion",
		Created: helper.GetTimestamp(),
		Choices: []openai.TextResponseChoice{choice},
	}
	return &fullTextResponse
}

func StreamHandler(c *gin.Context, resp *http.Response) (*model.ErrorWithStatusCode, *model.Usage) {
	createdTime := helper.GetTimestamp()

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

	common.SetEventStreamHeaders(c)

	var usage model.Usage
	var modelName string
	var id string
	var lastToolCallChoice openai.ChatCompletionsStreamResponseChoice

	for scanner.Scan() {
		data := scanner.Text()
		if len(data) < 6 || !strings.HasPrefix(data, "data:") {
			continue
		}
		data = strings.TrimPrefix(data, "data:")
		data = strings.TrimSpace(data)

		var claudeResponse StreamResponse
		err := json.Unmarshal([]byte(data), &claudeResponse)
		if err != nil {
			logger.SysError("error unmarshalling stream response: " + err.Error())
			continue
		}

		response, meta := StreamResponseClaude2OpenAI(&claudeResponse)
		if meta != nil {
			usage.PromptTokens += meta.Usage.InputTokens
			usage.CompletionTokens += meta.Usage.OutputTokens
			if len(meta.Id) > 0 { // only message_start has an id, otherwise it's a finish_reason event.
				modelName = meta.Model
				id = fmt.Sprintf("chatcmpl-%s", meta.Id)
				continue
			} else { // finish_reason case
				if len(lastToolCallChoice.Delta.ToolCalls) > 0 {
					lastArgs := &lastToolCallChoice.Delta.ToolCalls[len(lastToolCallChoice.Delta.ToolCalls)-1].Function
					if len(lastArgs.Arguments.(string)) == 0 { // compatible with OpenAI sending an empty object `{}` when no arguments.
						lastArgs.Arguments = "{}"
						response.Choices[len(response.Choices)-1].Delta.Content = nil
						response.Choices[len(response.Choices)-1].Delta.ToolCalls = lastToolCallChoice.Delta.ToolCalls
					}
				}
			}
		}
		if response == nil {
			continue
		}

		// 检查是否有内容并记录首字时间
		for _, choice := range response.Choices {
			content := ""
			if choice.Delta.Content != nil {
				if contentStr, ok := choice.Delta.Content.(string); ok {
					content = contentStr
				}
			}
			if content != "" && firstWordTime == nil {
				// 记录首字时间
				now := time.Now()
				firstWordTime = &now
			}
		}

		response.Id = id
		response.Model = modelName
		response.Created = createdTime

		for _, choice := range response.Choices {
			if len(choice.Delta.ToolCalls) > 0 {
				lastToolCallChoice = choice
			}
		}
		err = render.ObjectData(c, response)
		if err != nil {
			logger.SysError(err.Error())
		}
	}

	if err := scanner.Err(); err != nil {
		logger.SysError("error reading stream: " + err.Error())
	}

	render.Done(c)

	err := resp.Body.Close()
	if err != nil {
		return openai.ErrorWrapper(err, "close_response_body_failed", http.StatusInternalServerError), nil
	}

	// 计算首字延迟并存储到 context 中
	if firstWordTime != nil {
		firstWordLatency := firstWordTime.Sub(startTime).Seconds()
		c.Set("first_word_latency", firstWordLatency)
	}

	return nil, &usage
}

func Handler(c *gin.Context, resp *http.Response, promptTokens int, modelName string) (*model.ErrorWithStatusCode, *model.Usage) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError), nil
	}
	err = resp.Body.Close()
	if err != nil {
		return openai.ErrorWrapper(err, "close_response_body_failed", http.StatusInternalServerError), nil
	}
	var claudeResponse Response
	err = json.Unmarshal(responseBody, &claudeResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "unmarshal_response_body_failed", http.StatusInternalServerError), nil
	}
	if claudeResponse.Error != nil && claudeResponse.Error.Type != "" {
		return &model.ErrorWithStatusCode{
			Error: model.Error{
				Message: claudeResponse.Error.Message,
				Type:    claudeResponse.Error.Type,
				Param:   "",
				Code:    claudeResponse.Error.Type,
			},
			StatusCode: resp.StatusCode,
		}, nil
	}
	fullTextResponse := ResponseClaude2OpenAI(&claudeResponse)
	fullTextResponse.Model = modelName
	usage := model.Usage{
		PromptTokens:     claudeResponse.Usage.InputTokens,
		CompletionTokens: claudeResponse.Usage.OutputTokens,
		TotalTokens:      claudeResponse.Usage.InputTokens + claudeResponse.Usage.OutputTokens,
	}
	fullTextResponse.Usage = usage
	jsonResponse, err := json.Marshal(fullTextResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "marshal_response_body_failed", http.StatusInternalServerError), nil
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, err = c.Writer.Write(jsonResponse)
	return nil, &usage
}
