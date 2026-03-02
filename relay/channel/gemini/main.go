package gemini

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/image"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/constant"
	"github.com/songquanpeng/one-api/relay/model"

	"github.com/gin-gonic/gin"
)

// https://ai.google.dev/docs/gemini_api_overview?hl=zh-cn

const (
	VisionMaxImageNum = 100
)

// addMediaPart 添加媒体部分到parts列表的辅助函数
func addMediaPart(parts *[]Part, mimeType, data string) {
	*parts = append(*parts, Part{
		InlineData: &InlineData{
			MimeType: mimeType,
			Data:     data,
		},
	})
}

// createPrintableRequest 创建用于打印的请求副本，截断base64数据
func createPrintableRequest(original ChatRequest) ChatRequest {
	printableRequest := ChatRequest{
		Contents:          make([]ChatContent, len(original.Contents)),
		SafetySettings:    original.SafetySettings,
		GenerationConfig:  original.GenerationConfig,
		SystemInstruction: original.SystemInstruction,
		Tools:             original.Tools,
	}

	// 深拷贝Contents并截断base64数据
	for i, content := range original.Contents {
		printableRequest.Contents[i] = ChatContent{
			Role:  content.Role,
			Parts: make([]Part, len(content.Parts)),
		}

		for j, part := range content.Parts {
			printableRequest.Contents[i].Parts[j] = Part{
				Text:             part.Text,
				Thought:          part.Thought,
				ThoughtSignature: part.ThoughtSignature,
				FunctionCall:     part.FunctionCall,
				FunctionResponse: part.FunctionResponse,
			}

			// 如果有InlineData，截断Data字段
			if part.InlineData != nil {
				data := part.InlineData.Data
				if len(data) > 100 {
					data = data[:100] + "...[truncated " + fmt.Sprintf("%d", len(part.InlineData.Data)-100) + " chars]"
				}
				printableRequest.Contents[i].Parts[j].InlineData = &InlineData{
					MimeType: part.InlineData.MimeType,
					Data:     data,
				}
			}

			// 如果有ThoughtSignature，截断
			if part.ThoughtSignature != "" && len(part.ThoughtSignature) > 100 {
				printableRequest.Contents[i].Parts[j].ThoughtSignature = part.ThoughtSignature[:100] + "...[truncated]"
			}
		}
	}

	return printableRequest
}

// Setting safety to the lowest possible values since Gemini is already powerless enough
func ConvertRequest(textRequest model.GeneralOpenAIRequest) (*ChatRequest, error) {
	geminiRequest := ChatRequest{
		Contents: make([]ChatContent, 0, len(textRequest.Messages)),
		SafetySettings: []ChatSafetySettings{
			{
				Category:  "HARM_CATEGORY_HARASSMENT",
				Threshold: config.GeminiSafetySetting,
			},
			{
				Category:  "HARM_CATEGORY_HATE_SPEECH",
				Threshold: config.GeminiSafetySetting,
			},
			{
				Category:  "HARM_CATEGORY_SEXUALLY_EXPLICIT",
				Threshold: config.GeminiSafetySetting,
			},
			{
				Category:  "HARM_CATEGORY_DANGEROUS_CONTENT",
				Threshold: config.GeminiSafetySetting,
			},
		},
		GenerationConfig: ChatGenerationConfig{
			Temperature:     textRequest.Temperature,
			TopP:            textRequest.TopP,
			MaxOutputTokens: textRequest.MaxTokens,
		},
	}
	// Handle thinking models
	baseModel := textRequest.Model
	if strings.HasSuffix(baseModel, "-thinking") {
		baseModel = strings.TrimSuffix(baseModel, "-thinking")
	} else if strings.HasSuffix(baseModel, "-nothinking") {
		baseModel = strings.TrimSuffix(baseModel, "-nothinking")
	}

	if strings.HasSuffix(textRequest.Model, "-thinking") {
		budget := -1 // Enable dynamic thinking by default
		if textRequest.ThinkingTokens > 0 {
			budget = textRequest.ThinkingTokens
		} else if textRequest.MaxTokens > 0 {
			budget = int(float64(textRequest.MaxTokens) * 0.6)
		}

		// Clamp the budget based on the model's supported range
		switch baseModel {
		case "gemini-2.5-pro":
			if budget != -1 {
				if budget < 128 {
					budget = 128
				}
				if budget > 32768 {
					budget = 32768
				}
			}
		case "gemini-2.5-flash":
			if budget != -1 {
				if budget < 0 {
					budget = 0
				}
				if budget > 24576 {
					budget = 24576
				}
			}
		case "gemini-2.5-flash-lite":
			if budget != -1 {
				if budget < 512 {
					budget = 512
				}
				if budget > 24576 {
					budget = 24576
				}
			}
		}
		geminiRequest.GenerationConfig.ThinkingConfig = &ThinkingConfig{
			ThinkingBudget:  &budget,
			IncludeThoughts: true,
		}
	} else if strings.HasSuffix(textRequest.Model, "-nothinking") {
		// 禁用思考：设置 thinkingBudget = 0
		// 注意：gemini-2.5-pro 不支持禁用思考，但我们仍然传递 0
		budget := 0
		geminiRequest.GenerationConfig.ThinkingConfig = &ThinkingConfig{
			ThinkingBudget: &budget,
		}
	}

	// Handle reasoning_effort -> thinking_level mapping for Gemini 3
	// Reference: https://ai.google.dev/gemini-api/docs/gemini-3?hl=zh_cn#thinking_level
	// Gemini 3 thinking_level values: "none", "minimal", "low", "medium", "high"
	// Note: OpenAI "medium" maps to Gemini "high" per Google's documentation
	if textRequest.ReasoningEffort != "" {
		thinkingLevel := ""
		switch strings.ToLower(textRequest.ReasoningEffort) {
		case "none":
			thinkingLevel = "none"
		case "minimal":
			thinkingLevel = "minimal"
		case "low":
			thinkingLevel = "low"
		case "medium":
			// Per Gemini docs: reasoning_effort medium maps to thinking_level high
			thinkingLevel = "high"
		case "high":
			thinkingLevel = "high"
		}

		if thinkingLevel != "" {
			if geminiRequest.GenerationConfig.ThinkingConfig == nil {
				geminiRequest.GenerationConfig.ThinkingConfig = &ThinkingConfig{}
			}
			geminiRequest.GenerationConfig.ThinkingConfig.ThinkingLevel = thinkingLevel
			logger.SysLog(fmt.Sprintf("Mapped reasoning_effort '%s' to thinking_level '%s'", textRequest.ReasoningEffort, thinkingLevel))
		}
	}

	// 检测是否有system或developer消息，如果有则转换为system_instruction
	var systemMessages []model.Message
	var nonSystemMessages []model.Message

	for _, message := range textRequest.Messages {
		if message.Role == "system" || message.Role == "developer" {
			systemMessages = append(systemMessages, message)
		} else {
			nonSystemMessages = append(nonSystemMessages, message)
		}
	}

	// 如果有system/developer消息，将其合并为system_instruction
	if len(systemMessages) > 0 {
		var systemParts []Part
		for _, sysMsg := range systemMessages {
			systemParts = append(systemParts, Part{
				Text: sysMsg.StringContent(),
			})
		}
		geminiRequest.SystemInstruction = &SystemInstruction{
			Parts: systemParts,
		}
		logger.SysLog(fmt.Sprintf("Converted %d system/developer messages to system_instruction", len(systemMessages)))
	}

	// 使用非system消息构建contents
	messages := nonSystemMessages
	if textRequest.Model == "gemini-2.0-flash-exp-image-generation" {
		geminiRequest.GenerationConfig.ResponseModalities = []string{"TEXT", "IMAGE"}
	}
	// Handle functions (legacy format)
	if textRequest.Functions != nil {
		geminiRequest.Tools = []ChatTools{
			{
				FunctionDeclarations: textRequest.Functions,
			},
		}
	}

	// Handle tools (OpenAI format) - convert to Gemini FunctionDeclarations
	if len(textRequest.Tools) > 0 {
		var functionDeclarations []map[string]any
		for _, tool := range textRequest.Tools {
			if tool.Type == "function" {
				funcDecl := map[string]any{
					"name":        tool.Function.Name,
					"description": tool.Function.Description,
				}
				if tool.Function.Parameters != nil {
					funcDecl["parameters"] = tool.Function.Parameters
				}
				functionDeclarations = append(functionDeclarations, funcDecl)
			}
		}
		if len(functionDeclarations) > 0 {
			geminiRequest.Tools = []ChatTools{
				{
					FunctionDeclarations: functionDeclarations,
				},
			}
		}
	}

	// Build a mapping from tool_call_id to function name
	// This is needed because OpenAI tool responses only have tool_call_id, but Gemini requires function name
	toolCallIdToFuncName := make(map[string]string)
	for _, message := range messages {
		if len(message.ToolCalls) > 0 {
			for _, toolCall := range message.ToolCalls {
				if toolCall.Id != "" {
					toolCallIdToFuncName[toolCall.Id] = toolCall.Function.Name
				}
			}
		}
	}

	for _, message := range messages {
		// Handle tool role messages (function responses)
		if message.Role == "tool" {
			// Parse the content as function response
			// IMPORTANT: Gemini requires response to be an object (Struct), NOT an array
			var responseData any

			// Check if content is already structured data (array or object)
			switch v := message.Content.(type) {
			case string:
				// Try to parse string as JSON
				var parsed any
				if err := json.Unmarshal([]byte(v), &parsed); err != nil {
					// If not valid JSON, wrap string in object
					responseData = map[string]any{"result": v}
				} else {
					// Check if parsed result is array - Gemini requires object
					if arr, isArray := parsed.([]any); isArray {
						responseData = map[string]any{"result": arr}
					} else {
						responseData = parsed
					}
				}
			case []any:
				// Array must be wrapped in object for Gemini
				responseData = map[string]any{"result": v}
			case map[string]any:
				// Object can be used directly
				responseData = v
			default:
				// For any other type, try StringContent as fallback
				contentStr := message.StringContent()
				if contentStr != "" {
					var parsed any
					if err := json.Unmarshal([]byte(contentStr), &parsed); err != nil {
						responseData = map[string]any{"result": contentStr}
					} else if arr, isArray := parsed.([]any); isArray {
						responseData = map[string]any{"result": arr}
					} else {
						responseData = parsed
					}
				} else {
					// If all else fails, use empty object (Gemini requires Struct type)
					responseData = map[string]any{}
				}
			}

			// Get function name from the message or from tool_call_id mapping
			funcName := ""
			if message.Name != nil && *message.Name != "" {
				funcName = *message.Name
			} else if message.ToolCallId != "" {
				// Look up function name from tool_call_id
				if name, ok := toolCallIdToFuncName[message.ToolCallId]; ok {
					funcName = name
				}
			}

			content := ChatContent{
				Role: "user", // In Gemini, function responses are sent as user role
				Parts: []Part{
					{
						FunctionResponse: &FunctionResponse{
							Name:     funcName,
							Response: responseData,
						},
					},
				},
			}
			geminiRequest.Contents = append(geminiRequest.Contents, content)
			continue
		}

		// Handle assistant/model messages with tool_calls
		if len(message.ToolCalls) > 0 {
			content := ChatContent{
				Role:  "model",
				Parts: []Part{},
			}

			// Convert each tool call to a FunctionCall part
			for i, toolCall := range message.ToolCalls {
				// Parse arguments from string to map
				var args map[string]any
				if toolCall.Function.Arguments != nil {
					switch v := toolCall.Function.Arguments.(type) {
					case string:
						if err := json.Unmarshal([]byte(v), &args); err != nil {
							logger.SysLog(fmt.Sprintf("Error parsing function arguments: %v", err))
							args = make(map[string]any)
						}
					case map[string]any:
						args = v
					}
				}

				part := Part{
					FunctionCall: &FunctionCall{
						Name: toolCall.Function.Name,
						Args: args,
					},
				}

				// Add thought signature from extra_content (only on first function call per Gemini spec)
				if i == 0 && toolCall.ExtraContent != nil && toolCall.ExtraContent.Google != nil {
					part.ThoughtSignature = toolCall.ExtraContent.Google.ThoughtSignature
				}

				content.Parts = append(content.Parts, part)
			}

			geminiRequest.Contents = append(geminiRequest.Contents, content)
			continue
		}

		content := ChatContent{
			Role: message.Role,
			Parts: []Part{
				{
					Text: message.StringContent(),
				},
			},
		}
		openaiContent := message.ParseContent()
		var parts []Part
		imageNum := 0
		for _, part := range openaiContent {
			if part.Type == model.ContentTypeText {
				parts = append(parts, Part{
					Text: part.Text,
				})
			} else if part.Type == model.ContentTypeImageURL {
				imageNum += 1
				if imageNum > VisionMaxImageNum {
					continue
				}
				// 使用智能媒体检测函数，支持图片、音频、视频
				mimeType, data, mediaType, err := image.GetGeminiMediaInfo(part.ImageURL.Url)
				if err != nil {
					logger.SysLog(fmt.Sprintf("Error in GetGeminiMediaInfo for image_url: %v", err))
					continue
				}

				// 所有支持的媒体类型都使用相同的InlineData结构
				if mediaType == "image" || mediaType == "audio" || mediaType == "video" || mediaType == "document" {
					addMediaPart(&parts, mimeType, data)
				} else {
					logger.SysLog(fmt.Sprintf("Unsupported media type for image_url: %s", mediaType))
				}
			} else if part.Type == model.ContentTypeAudioURL {
				// 处理audio_url类型
				mimeType, data, mediaType, err := image.GetGeminiMediaInfo(part.AudioURL.Url)
				if err != nil {
					logger.SysLog(fmt.Sprintf("Error in GetGeminiMediaInfo for audio_url: %v", err))
					continue
				}
				if mediaType == "audio" {
					addMediaPart(&parts, mimeType, data)
				} else {
					logger.SysLog(fmt.Sprintf("Expected audio type but got: %s", mediaType))
				}
			} else if part.Type == model.ContentTypeVideoURL {
				// 处理video_url类型
				mimeType, data, mediaType, err := image.GetGeminiMediaInfo(part.VideoURL.Url)
				if err != nil {
					logger.SysLog(fmt.Sprintf("Error in GetGeminiMediaInfo for video_url: %v", err))
					continue
				}
				if mediaType == "video" {
					addMediaPart(&parts, mimeType, data)
				} else {
					logger.SysLog(fmt.Sprintf("Expected video type but got: %s", mediaType))
				}
			} else if part.Type == model.ContentTypeInputAudio {
				// 处理input_audio类型（OpenAI格式）
				if part.InputAudio != nil && part.InputAudio.Data != "" {
					// 检测音频格式
					detectedType, err := image.GetMediaTypeFromBase64(part.InputAudio.Data)
					if err != nil {
						logger.SysLog(fmt.Sprintf("Error detecting media type from base64: %v", err))
						continue
					}

					// 验证是否为音频类型
					if !image.IsAudioType(detectedType) {
						logger.SysLog(fmt.Sprintf("Expected audio type but got: %s", detectedType))
						continue
					}

					addMediaPart(&parts, detectedType, part.InputAudio.Data)
				}
			} else if part.Type == model.ContentTypeFileURL {
				// 处理file_url类型（PDF文档等）
				mimeType, data, mediaType, err := image.GetGeminiMediaInfo(part.FileURL.Url)
				if err != nil {
					logger.SysLog(fmt.Sprintf("Error in GetGeminiMediaInfo for file_url: %v", err))
					continue
				}

				// 根据Gemini文档处理规范，主要支持PDF
				if mediaType == "document" {
					addMediaPart(&parts, mimeType, data)
				} else {
					logger.SysLog(fmt.Sprintf("Expected document type but got: %s for file_url", mediaType))
				}
			}
		}
		content.Parts = parts

		// Add ThoughtSignature if present (for backward compatibility)
		if message.ThoughtSignature != "" {
			if len(content.Parts) > 0 {
				content.Parts[0].ThoughtSignature = message.ThoughtSignature
			} else {
				content.Parts = append(content.Parts, Part{ThoughtSignature: message.ThoughtSignature})
			}
		}

		// there's no assistant role in gemini and API shall vomit if Role is not user or model
		if content.Role == "assistant" {
			content.Role = "model"
		}
		// system和developer消息已经在上面转换为system_instruction，这里不再处理
		geminiRequest.Contents = append(geminiRequest.Contents, content)
	}

	return &geminiRequest, nil
}

type ChatResponse struct {
	Candidates     []ChatCandidate    `json:"candidates"`
	PromptFeedback ChatPromptFeedback `json:"promptFeedback"`
	UsageMetadata  *UsageMetadata     `json:"usageMetadata,omitempty"`
	ModelVersion   string             `json:"modelVersion,omitempty"`
	ResponseId     string             `json:"responseId,omitempty"`
}

type UsageMetadata struct {
	PromptTokenCount        int            `json:"promptTokenCount"`
	CandidatesTokenCount    int            `json:"candidatesTokenCount"`
	TotalTokenCount         int            `json:"totalTokenCount"`
	ThoughtsTokenCount      int            `json:"thoughtsTokenCount,omitempty"`
	ToolUsePromptTokenCount int            `json:"toolUsePromptTokenCount,omitempty"`
	CachedContentTokenCount int            `json:"cachedContentTokenCount,omitempty"`
	PromptTokensDetails     []TokenDetails `json:"promptTokensDetails,omitempty"`
	CandidatesTokensDetails []TokenDetails `json:"candidatesTokensDetails,omitempty"`
}
type TokenDetails struct {
	Modality   string `json:"modality"`
	TokenCount int    `json:"tokenCount"`
}

func (g *ChatResponse) GetResponseText() string {
	if g == nil {
		return ""
	}
	if len(g.Candidates) > 0 && len(g.Candidates[0].Content.Parts) > 0 {
		return g.Candidates[0].Content.Parts[0].Text
	}
	return ""
}

// GetReasoningContent 提取思考内容（通过 thought 字段标识）
func (g *ChatResponse) GetReasoningContent() string {
	if g == nil {
		return ""
	}
	if len(g.Candidates) > 0 {
		for _, part := range g.Candidates[0].Content.Parts {
			if part.Thought {
				return part.Text
			}
		}
	}
	return ""
}

// GetActualContent 提取实际回答内容（非思考内容）
func (g *ChatResponse) GetActualContent() string {
	if g == nil {
		return ""
	}
	if len(g.Candidates) > 0 {
		for _, part := range g.Candidates[0].Content.Parts {
			// 返回第一个非思考内容的文本
			if !part.Thought && part.Text != "" {
				return part.Text
			}
		}
	}
	return ""
}

type ChatCandidate struct {
	Content       ChatContent        `json:"content"`
	FinishReason  string             `json:"finishReason"`
	Index         int64              `json:"index"`
	SafetyRatings []ChatSafetyRating `json:"safetyRatings"`
}

type ChatSafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
}

type ChatPromptFeedback struct {
	SafetyRatings []ChatSafetyRating `json:"safetyRatings"`
	BlockReason   string             `json:"blockReason,omitempty"`
}

func responseGeminiChat2OpenAI(response *ChatResponse) *openai.TextResponse {
	// 优先使用 Gemini 返回的 responseId，否则生成 UUID
	responseId := response.ResponseId
	if responseId == "" {
		responseId = helper.GetUUID()
	}
	fullTextResponse := openai.TextResponse{
		Id:      responseId,
		Object:  "chat.completion",
		Created: helper.GetTimestamp(),
		Choices: make([]openai.TextResponseChoice, 0, len(response.Candidates)),
	}
	for i, candidate := range response.Candidates {
		choice := openai.TextResponseChoice{
			Index: i,
			Message: model.Message{
				Role:    "assistant",
				Content: "",
			},
			FinishReason: constant.StopFinishReason,
		}

		parts := candidate.Content.Parts

		// 单次遍历：收集所有信息
		var reasoningBuilder, actualBuilder strings.Builder
		var thoughtSignature string
		var toolCalls []model.Tool
		funcCallIdx := 0

		for _, part := range parts {
			// 收集 thoughtSignature
			if part.ThoughtSignature != "" {
				thoughtSignature = part.ThoughtSignature
			}

			// 处理 FunctionCall
			if part.FunctionCall != nil {
				argsJSON, err := json.Marshal(part.FunctionCall.Args)
				if err != nil {
					argsJSON = []byte("{}")
				}

				tool := model.Tool{
					Id:   fmt.Sprintf("function-call-%d", helper.GetTimestamp()+int64(funcCallIdx)),
					Type: "function",
					Function: model.Function{
						Name:      part.FunctionCall.Name,
						Arguments: string(argsJSON),
					},
				}

				// 第一个 function call 添加 thought_signature
				if funcCallIdx == 0 && part.ThoughtSignature != "" {
					tool.ExtraContent = &model.ExtraContent{
						Google: &model.GoogleExtraContent{
							ThoughtSignature: part.ThoughtSignature,
						},
					}
				}

				toolCalls = append(toolCalls, tool)
				funcCallIdx++
				continue
			}

			// 处理文本内容
			if part.Thought {
				reasoningBuilder.WriteString(part.Text)
			} else if part.Text != "" {
				actualBuilder.WriteString(part.Text)
			}
		}

		// 如果有 function calls，补充第一个的 thoughtSignature（如果还没设置）
		if len(toolCalls) > 0 {
			if toolCalls[0].ExtraContent == nil && thoughtSignature != "" {
				toolCalls[0].ExtraContent = &model.ExtraContent{
					Google: &model.GoogleExtraContent{
						ThoughtSignature: thoughtSignature,
					},
				}
			}
			choice.Message.ToolCalls = toolCalls
			choice.FinishReason = "tool_calls"
		} else {
			choice.Message.Content = actualBuilder.String()
			choice.Message.ReasoningContent = reasoningBuilder.String()
			choice.Message.ThoughtSignature = thoughtSignature
		}

		fullTextResponse.Choices = append(fullTextResponse.Choices, choice)
	}
	return &fullTextResponse
}

func streamResponseGeminiChat2OpenAI(geminiResponse *ChatResponse, modelName string) *openai.ChatCompletionsStreamResponse {
	var choice openai.ChatCompletionsStreamResponseChoice

	if len(geminiResponse.Candidates) > 0 {
		parts := geminiResponse.Candidates[0].Content.Parts

		// 单次遍历处理所有内容
		var toolCalls []model.Tool
		var thoughtSignature string
		var reasoningContent, actualContent string
		funcCallIdx := 0

		for _, part := range parts {
			if part.ThoughtSignature != "" {
				thoughtSignature = part.ThoughtSignature
			}

			if part.FunctionCall != nil {
				argsJSON, err := json.Marshal(part.FunctionCall.Args)
				if err != nil {
					argsJSON = []byte("{}")
				}

				tool := model.Tool{
					Id:   fmt.Sprintf("function-call-%d", helper.GetTimestamp()+int64(funcCallIdx)),
					Type: "function",
					Function: model.Function{
						Name:      part.FunctionCall.Name,
						Arguments: string(argsJSON),
					},
				}

				if funcCallIdx == 0 && part.ThoughtSignature != "" {
					tool.ExtraContent = &model.ExtraContent{
						Google: &model.GoogleExtraContent{
							ThoughtSignature: part.ThoughtSignature,
						},
					}
				}

				toolCalls = append(toolCalls, tool)
				funcCallIdx++
				continue
			}

			// 处理文本内容
			if part.Thought {
				reasoningContent = part.Text
			} else if part.Text != "" {
				actualContent = part.Text
			}
		}

		if len(toolCalls) > 0 {
			// 补充第一个 tool call 的 thoughtSignature
			if toolCalls[0].ExtraContent == nil && thoughtSignature != "" {
				toolCalls[0].ExtraContent = &model.ExtraContent{
					Google: &model.GoogleExtraContent{
						ThoughtSignature: thoughtSignature,
					},
				}
			}
			choice.Delta.ToolCalls = toolCalls
		} else {
			choice.Delta.Content = actualContent
			choice.Delta.ReasoningContent = reasoningContent
			choice.Delta.ThoughtSignature = thoughtSignature
		}
	}

	// 优先使用 Gemini 返回的 responseId，否则生成 UUID
	responseId := geminiResponse.ResponseId
	if responseId == "" {
		responseId = helper.GetUUID()
	}
	return &openai.ChatCompletionsStreamResponse{
		Id:      responseId,
		Created: helper.GetTimestamp(),
		Object:  "chat.completion.chunk",
		Model:   modelName,
		Choices: []openai.ChatCompletionsStreamResponseChoice{choice},
	}
}

func StreamHandler(c *gin.Context, resp *http.Response, modelName string) (*model.ErrorWithStatusCode, string) {
	responseText := ""

	// 获取请求开始时间用于计算首字延迟
	var startTime time.Time
	if requestStartTime, exists := c.Get("request_start_time"); exists {
		if t, ok := requestStartTime.(time.Time); ok {
			startTime = t
			logger.SysLog(fmt.Sprintf("Gemini using request start time: %v", startTime))
		} else {
			startTime = time.Now() // fallback
			logger.SysLog("Gemini using fallback start time (type error)")
		}
	} else {
		startTime = time.Now() // fallback
		logger.SysLog("Gemini using fallback start time (not found)")
	}

	var firstWordTime *time.Time
	var lastUsageMetadata *UsageMetadata

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
	go func() {
		for scanner.Scan() {
			data := scanner.Text()
			data = strings.TrimSpace(data)
			if !strings.HasPrefix(data, "data: ") {
				continue
			}
			data = strings.TrimPrefix(data, "data: ")
			data = strings.TrimSuffix(data, "\"")
			dataChan <- data
		}
		stopChan <- true
	}()
	common.SetEventStreamHeaders(c)
	c.Stream(func(w io.Writer) bool {
		select {
		case data := <-dataChan:
			var geminiResponse ChatResponse
			err := json.Unmarshal([]byte(data), &geminiResponse)
			if err != nil {
				logger.SysError("error unmarshalling stream response: " + err.Error())
				return true
			}

			// 检查是否有内容阻止原因
			if geminiResponse.PromptFeedback.BlockReason != "" {
				// 发送错误响应
				finishReason := "content_filter"
				errResponseId := geminiResponse.ResponseId
				if errResponseId == "" {
					errResponseId = helper.GetUUID()
				}
				errorResponse := &openai.ChatCompletionsStreamResponse{
					Id:      errResponseId,
					Object:  "chat.completion.chunk",
					Created: helper.GetTimestamp(),
					Model:   modelName,
					Choices: []openai.ChatCompletionsStreamResponseChoice{
						{
							Index: 0,
							Delta: model.Message{
								Role:    "assistant",
								Content: geminiResponse.PromptFeedback.BlockReason,
							},
							FinishReason: &finishReason,
						},
					},
				}

				jsonResponse, _ := json.Marshal(errorResponse)
				c.Render(-1, common.CustomEvent{Data: "data: " + string(jsonResponse)})
				logger.SysLog(fmt.Sprintf("Gemini stream blocked: %s", geminiResponse.PromptFeedback.BlockReason))
				return false
			}

			// 保存最新的 usage metadata
			if geminiResponse.UsageMetadata != nil {
				lastUsageMetadata = geminiResponse.UsageMetadata
			}

			response := streamResponseGeminiChat2OpenAI(&geminiResponse, modelName)
			if response == nil {
				return true
			}
			content := response.Choices[0].Delta.StringContent()

			if content != "" && firstWordTime == nil {
				// 记录首字时间
				now := time.Now()
				firstWordTime = &now
			}
			responseText += content

			// 检查是否是最后一个 chunk（有 finishReason）
			if len(geminiResponse.Candidates) > 0 && geminiResponse.Candidates[0].FinishReason != "" {
				// 按照 OpenAI 格式：倒数第二条有 finish_reason，最后一条发送 usage
				// 先发送带有 finish_reason 的 chunk
				response.Choices[0].FinishReason = &geminiResponse.Candidates[0].FinishReason
				jsonResponse, err := json.Marshal(response)
				if err != nil {
					logger.SysError("error marshalling stream response: " + err.Error())
					return true
				}
				c.Render(-1, common.CustomEvent{Data: "data: " + string(jsonResponse)})

				// 然后发送带有 usage 信息的最后一个 chunk（choices 为空）
				if lastUsageMetadata != nil {
					// 构建符合 OpenAI 格式的 usage 信息
					completionTokens := lastUsageMetadata.CandidatesTokenCount
					// 如果有推理 token，添加到 completion_tokens 中
					if lastUsageMetadata.ThoughtsTokenCount > 0 {
						completionTokens += lastUsageMetadata.ThoughtsTokenCount
					}

					usage := &model.Usage{
						PromptTokens:     lastUsageMetadata.PromptTokenCount,
						CompletionTokens: completionTokens,
						TotalTokens:      lastUsageMetadata.TotalTokenCount, // 保持原始 total 值不变
					}

					// 如果有推理 token，添加详细信息
					if lastUsageMetadata.ThoughtsTokenCount > 0 {
						usage.CompletionTokensDetails.ReasoningTokens = lastUsageMetadata.ThoughtsTokenCount
					}

					for _, detail := range lastUsageMetadata.PromptTokensDetails {
						switch detail.Modality {
						case "TEXT":
							usage.PromptTokensDetails.TextTokens = detail.TokenCount
						case "IMAGE":
							usage.PromptTokensDetails.ImageTokens = detail.TokenCount
						case "AUDIO":
							usage.PromptTokensDetails.AudioTokens = detail.TokenCount
						}
					}
					for _, detail := range lastUsageMetadata.CandidatesTokensDetails {
						switch detail.Modality {
						case "IMAGE":
							usage.CompletionTokensDetails.ImageTokens = detail.TokenCount
						case "AUDIO":
							usage.CompletionTokensDetails.AudioTokens = detail.TokenCount
						}
					}

					finalResponse := &openai.ChatCompletionsStreamResponse{
						Id:      response.Id,
						Object:  "chat.completion.chunk",
						Created: response.Created,
						Model:   modelName,
						Choices: []openai.ChatCompletionsStreamResponseChoice{},
						Usage:   usage,
					}
					finalJson, _ := json.Marshal(finalResponse)
					c.Render(-1, common.CustomEvent{Data: "data: " + string(finalJson)})
				}
				return true
			}

			jsonResponse, err := json.Marshal(response)
			if err != nil {
				logger.SysError("error marshalling stream response: " + err.Error())
				return true
			}
			c.Render(-1, common.CustomEvent{Data: "data: " + string(jsonResponse)})
			return true
		case <-stopChan:
			c.Render(-1, common.CustomEvent{Data: "data: [DONE]"})
			return false
		}
	})
	err := resp.Body.Close()
	if err != nil {
		return openai.ErrorWrapper(err, "close_response_body_failed", http.StatusInternalServerError), ""
	}

	// 计算首字延迟并存储到 context 中
	if firstWordTime != nil {
		firstWordLatency := firstWordTime.Sub(startTime).Seconds()
		c.Set("first_word_latency", firstWordLatency)
		logger.SysLog(fmt.Sprintf("Gemini first word latency calculated: %.3f seconds", firstWordLatency))
	} else {
		logger.SysLog("Gemini: No first word time recorded")
	}

	return nil, responseText
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
	var geminiResponse ChatResponse
	err = json.Unmarshal(responseBody, &geminiResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "unmarshal_response_body_failed", http.StatusInternalServerError), nil
	}
	// 检查是否有内容阻止原因
	if geminiResponse.PromptFeedback.BlockReason != "" {
		return &model.ErrorWithStatusCode{
			Error: model.Error{
				Message: geminiResponse.PromptFeedback.BlockReason,
				Type:    "content_policy_violation",
				Param:   "",
				Code:    400,
			},
			StatusCode: 400,
		}, nil
	}

	// 然后检查是否没有候选项返回
	if len(geminiResponse.Candidates) == 0 {
		return &model.ErrorWithStatusCode{
			Error: model.Error{
				Message: "No candidates returned",
				Type:    "server_error",
				Param:   "",
				Code:    500,
			},
			StatusCode: resp.StatusCode,
		}, nil
	}
	fullTextResponse := responseGeminiChat2OpenAI(&geminiResponse)
	fullTextResponse.Model = modelName

	// 计算 completion tokens
	baseCompletionTokens := openai.CountTokenText(geminiResponse.GetActualContent(), modelName)
	completionTokens := baseCompletionTokens

	// 如果有 usage metadata，使用官方数据并处理推理 token
	if geminiResponse.UsageMetadata != nil {
		baseCompletionTokens = geminiResponse.UsageMetadata.CandidatesTokenCount
		completionTokens = baseCompletionTokens
		// 如果有推理 token，添加到 completion_tokens 中
		if geminiResponse.UsageMetadata.ThoughtsTokenCount > 0 {
			completionTokens += geminiResponse.UsageMetadata.ThoughtsTokenCount
		}
		promptTokens = geminiResponse.UsageMetadata.PromptTokenCount
	}

	usage := model.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens, // 使用包含推理token的completionTokens
	}

	// 如果有 usage metadata，处理详细信息
	if geminiResponse.UsageMetadata != nil {
		if geminiResponse.UsageMetadata.ThoughtsTokenCount > 0 {
			usage.CompletionTokensDetails.ReasoningTokens = geminiResponse.UsageMetadata.ThoughtsTokenCount
		}
		for _, detail := range geminiResponse.UsageMetadata.PromptTokensDetails {
			switch detail.Modality {
			case "TEXT":
				usage.PromptTokensDetails.TextTokens = detail.TokenCount
			case "IMAGE":
				usage.PromptTokensDetails.ImageTokens = detail.TokenCount
			case "AUDIO":
				usage.PromptTokensDetails.AudioTokens = detail.TokenCount
			}
		}
		for _, detail := range geminiResponse.UsageMetadata.CandidatesTokensDetails {
			switch detail.Modality {
			case "IMAGE":
				usage.CompletionTokensDetails.ImageTokens = detail.TokenCount
			case "AUDIO":
				usage.CompletionTokensDetails.AudioTokens = detail.TokenCount
			}
		}
	}
	fullTextResponse.Usage = usage
	jsonResponse, err := json.Marshal(fullTextResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "marshal_response_body_failed", http.StatusInternalServerError), nil
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	if _, err = c.Writer.Write(jsonResponse); err != nil {
		return openai.ErrorWrapper(err, "write_response_body_failed", http.StatusInternalServerError), nil
	}
	return nil, &usage
}
