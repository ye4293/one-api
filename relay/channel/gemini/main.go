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
	}

	// 深拷贝Contents并截断base64数据
	for i, content := range original.Contents {
		printableRequest.Contents[i] = ChatContent{
			Role:  content.Role,
			Parts: make([]Part, len(content.Parts)),
		}

		for j, part := range content.Parts {
			printableRequest.Contents[i].Parts[j] = Part{
				Text: part.Text,
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
		var budget int
		if textRequest.ThinkingTokens > 0 {
			budget = textRequest.ThinkingTokens
		} else if textRequest.MaxTokens > 0 {
			budget = int(float64(textRequest.MaxTokens) * 0.6)
		} else {
			budget = -1 // Enable dynamic thinking
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
			ThinkingBudget:  budget,
			IncludeThoughts: true,
		}
	} else if strings.HasSuffix(textRequest.Model, "-nothinking") {
		budget := 0
		if baseModel == "gemini-2.5-pro" {
			budget = -1
		}
		geminiRequest.GenerationConfig.ThinkingConfig = &ThinkingConfig{
			ThinkingBudget: budget,
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
	if textRequest.Functions != nil {
		geminiRequest.Tools = []ChatTools{
			{
				FunctionDeclarations: textRequest.Functions,
			},
		}
	}
	for _, message := range messages {

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

		// there's no assistant role in gemini and API shall vomit if Role is not user or model
		if content.Role == "assistant" {
			content.Role = "model"
		}
		// system和developer消息已经在上面转换为system_instruction，这里不再处理
		geminiRequest.Contents = append(geminiRequest.Contents, content)
	}

	// 打印转换后的geminiRequest为JSON格式（截断base64数据）
	printableRequest := createPrintableRequest(geminiRequest)
	requestJSON, err := json.MarshalIndent(printableRequest, "", "  ")
	if err != nil {
		logger.SysError("error marshalling gemini request: " + err.Error())
	} else {
		logger.SysLog("Converted Gemini Request: " + string(requestJSON))
	}

	return &geminiRequest, nil
}

type ChatResponse struct {
	Candidates     []ChatCandidate    `json:"candidates"`
	PromptFeedback ChatPromptFeedback `json:"promptFeedback"`
	UsageMetadata  *UsageMetadata     `json:"usageMetadata,omitempty"`
	ModelVersion   string             `json:"modelVersion,omitempty"`
}

type UsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
	ThoughtsTokenCount   int `json:"thoughtsTokenCount,omitempty"`
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

// GetReasoningContent 提取思考内容
func (g *ChatResponse) GetReasoningContent() string {
	if g == nil {
		return ""
	}
	if len(g.Candidates) > 0 && len(g.Candidates[0].Content.Parts) == 2 {
		// 当parts长度为2时，第一个通常是思考内容，第二个是实际回答
		return g.Candidates[0].Content.Parts[0].Text
	}
	return ""
}

// GetActualContent 提取实际回答内容（非思考内容）
func (g *ChatResponse) GetActualContent() string {
	if g == nil {
		return ""
	}
	if len(g.Candidates) > 0 {
		parts := g.Candidates[0].Content.Parts
		if len(parts) == 2 {
			// 当parts长度为2时，第二个是实际回答内容
			return parts[1].Text
		} else if len(parts) > 0 {
			// 当parts长度为1时，直接返回第一个内容
			return parts[0].Text
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
	fullTextResponse := openai.TextResponse{
		Id:      fmt.Sprintf("chatcmpl-%s", helper.GetUUID()),
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

		// 处理Parts，分离思考内容和实际回答内容
		var reasoningContent string
		var actualContent string

		parts := candidate.Content.Parts
		if len(parts) == 2 {
			// 当parts长度为2时，第一个是思考内容，第二个是实际回答
			reasoningContent = parts[0].Text
			actualContent = parts[1].Text
		} else if len(parts) > 0 {
			// 当parts长度为1时，直接使用第一个内容作为实际回答
			actualContent = parts[0].Text
		}

		choice.Message.Content = actualContent
		choice.Message.ReasoningContent = reasoningContent

		fullTextResponse.Choices = append(fullTextResponse.Choices, choice)
	}
	return &fullTextResponse
}

func streamResponseGeminiChat2OpenAI(geminiResponse *ChatResponse, modelName string) *openai.ChatCompletionsStreamResponse {
	var choice openai.ChatCompletionsStreamResponseChoice
	choice.Delta.Content = geminiResponse.GetActualContent()
	choice.Delta.ReasoningContent = geminiResponse.GetReasoningContent()
	var response openai.ChatCompletionsStreamResponse
	response.Id = fmt.Sprintf("chatcmpl-%s", helper.GetUUID())
	response.Created = helper.GetTimestamp()
	response.Object = "chat.completion.chunk"
	response.Model = modelName // 使用实际的模型名
	response.Choices = []openai.ChatCompletionsStreamResponseChoice{choice}
	return &response
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
				errorResponse := &openai.ChatCompletionsStreamResponse{
					Id:      fmt.Sprintf("chatcmpl-%s", helper.GetUUID()),
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
			reasoningContent := response.Choices[0].Delta.ReasoningContent

			// 记录思考内容（如果有的话）
			if reasoningContent != "" {
				logger.SysLog(fmt.Sprintf("Gemini reasoning content: %s", reasoningContent))
			}

			if content != "" && firstWordTime == nil {
				// 记录首字时间
				now := time.Now()
				firstWordTime = &now
				contentPreview := content
				if len(content) > 50 {
					contentPreview = content[:50] + "..."
				}
				logger.SysLog(fmt.Sprintf("Gemini first word detected at: %v, content: %s", now, contentPreview))
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

					finalResponse := &openai.ChatCompletionsStreamResponse{
						Id:      response.Id,
						Object:  "chat.completion.chunk",
						Created: response.Created,
						Model:   modelName,
						Choices: []openai.ChatCompletionsStreamResponseChoice{}, // 空的 choices 数组
						Usage:   usage,
					}
					finalJson, _ := json.Marshal(finalResponse)
					c.Render(-1, common.CustomEvent{Data: "data: " + string(finalJson)})
					logger.SysLog(fmt.Sprintf("Gemini final chunk with usage: prompt=%d, completion=%d, total=%d, reasoning=%d",
						lastUsageMetadata.PromptTokenCount, lastUsageMetadata.CandidatesTokenCount, lastUsageMetadata.TotalTokenCount, lastUsageMetadata.ThoughtsTokenCount))
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
	}

	usage := model.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens, // 使用包含推理token的completionTokens
	}

	// 如果有推理 token，添加详细信息
	if geminiResponse.UsageMetadata != nil && geminiResponse.UsageMetadata.ThoughtsTokenCount > 0 {
		usage.CompletionTokensDetails.ReasoningTokens = geminiResponse.UsageMetadata.ThoughtsTokenCount
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
