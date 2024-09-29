package aws

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/ctxkey"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/relay/channel/aws/utils"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/model"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
)

var AwsModelIDMap = map[string]string{
	"ai21.jamba-1-5-mini-v1:0":  "ai21.jamba-1-5-mini-v1:0",
	"ai21.jamba-1-5-large-v1:0": "ai21.jamba-1-5-large-v1:0",
	"ai21.jamba-instruct-v1:0":  "ai21.jamba-instruct-v1:0",
	"ai21.j2-ultra-v1":          "ai21.j2-ultra-v1",
	"ai21.j2-mid-v1":            "ai21.j2-mid-v1",
}

func ConvertRequest(textRequest *model.GeneralOpenAIRequest) *AI21Request {
	req := &AI21Request{
		Temperature: textRequest.Temperature,
	}

	switch textRequest.Model {
	case "ai21.jamba-instruct-v1:0", "ai21.jamba-1-5-large-v1:0", "ai21.jamba-1-5-mini-v1:0":
		req.MaxTokens = textRequest.MaxTokens // 使用 max_tokens
		req.TopP = textRequest.TopP           // 使用 top_p
		req.Messages = make([]Message, len(textRequest.Messages))
		for i, msg := range textRequest.Messages {
			content, ok := msg.Content.(string)
			if !ok {
				log.Printf("Warning: message content is not a string for message %d", i)
				content = fmt.Sprintf("%v", msg.Content)
			}
			req.Messages[i] = Message{
				Role:    msg.Role,
				Content: content,
			}
		}
	case "ai21.j2-ultra-v1", "ai21.j2-mid-v1":
		req.MaxTokens2 = textRequest.MaxTokens // 使用 maxTokens
		req.TopP2 = textRequest.TopP           // 使用 topP
		var promptBuilder strings.Builder
		for _, msg := range textRequest.Messages {
			content, ok := msg.Content.(string)
			if !ok {
				log.Printf("Warning: message content is not a string")
				content = fmt.Sprintf("%v", msg.Content)
			}
			promptBuilder.WriteString(msg.Role + ": " + content + "\n")
		}
		req.Prompt = promptBuilder.String()

		logger.SysLog(fmt.Sprintf("req.Prompt: %+v", req.Prompt))

		if stop, ok := textRequest.Stop.([]string); ok {
			req.StopSequences = stop
		} else if stop, ok := textRequest.Stop.(string); ok {
			req.StopSequences = []string{stop}
		}

		if textRequest.FrequencyPenalty != 0 {
			req.FrequencyPenalty = &Penalty{Scale: textRequest.FrequencyPenalty}
		}
		if textRequest.PresencePenalty != 0 {
			req.PresencePenalty = &Penalty{Scale: textRequest.PresencePenalty}
		}
		// AI21 特定的 CountPenalty，设置默认值
		req.CountPenalty = &Penalty{Scale: 0}
	default:
		log.Printf("Unknown model: %s", textRequest.Model)
		return nil
	}

	return req
}

func Handler(c *gin.Context, awsCli *bedrockruntime.Client, modelName string) (*relaymodel.ErrorWithStatusCode, *relaymodel.Usage) {
	awsModelId := modelName
	awsReq := &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(awsModelId),
		Accept:      aws.String("application/json"),
		ContentType: aws.String("application/json"),
	}

	ai21Req, ok := c.Get(ctxkey.ConvertedRequest)
	if !ok {
		return utils.WrapErr(errors.New("request not found")), nil
	}

	var err error
	awsReq.Body, err = json.Marshal(ai21Req)
	if err != nil {
		return utils.WrapErr(errors.Wrap(err, "marshal request")), nil
	}

	awsResp, err := awsCli.InvokeModel(c.Request.Context(), awsReq)
	if err != nil {
		return utils.WrapErr(errors.Wrap(err, "InvokeModel")), nil
	}

	var response interface{}
	switch modelName {
	case "ai21.jamba-instruct-v1:0", "ai21.jamba-1-5-large-v1:0", "ai21.jamba-1-5-mini-v1:0":
		var genericResponse GenericResponse
		if err := json.Unmarshal(awsResp.Body, &genericResponse); err != nil {
			return utils.WrapErr(errors.Wrap(err, "unmarshal response")), nil
		}
		response = &genericResponse
	case "ai21.j2-ultra-v1", "ai21.j2-mid-v1":
		var j2Response Response
		if err := json.Unmarshal(awsResp.Body, &j2Response); err != nil {
			return utils.WrapErr(errors.Wrap(err, "unmarshal response")), nil
		}
		response = &j2Response
	default:
		return utils.WrapErr(errors.New("unknown model")), nil
	}

	openaiResp := ResponseAi21ToOpenAI(response)
	if openaiResp == nil {
		return utils.WrapErr(errors.New("failed to process response")), nil
	}

	openaiResp.Model = modelName

	c.JSON(http.StatusOK, openaiResp)
	return nil, &openaiResp.Usage
}

func ResponseAi21ToOpenAI(genericResponse interface{}) *openai.TextResponse {
	var choices []openai.TextResponseChoice
	var usage model.Usage
	var idStr string

	switch resp := genericResponse.(type) {
	case *GenericResponse:
		log.Println("Processing GenericResponse")
		// 处理 jamba 系列模型的响应
		for _, ai21Choice := range resp.Choices {
			choice := openai.TextResponseChoice{
				Index: ai21Choice.Index,
				Message: relaymodel.Message{
					Role:    ai21Choice.Message.Role,
					Content: ai21Choice.Message.Content,
					Name:    nil,
				},
				FinishReason: ai21Choice.FinishReason,
			}
			choices = append(choices, choice)
		}

		usage = model.Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		}

		switch id := resp.ID.(type) {
		case string:
			idStr = id
		case float64:
			idStr = fmt.Sprintf("%d", int(id))
		default:
			idStr = fmt.Sprintf("generated-%d", helper.GetTimestamp())
		}

	case *Response:
		log.Println("Processing Response (j2 series model)")
		// 处理 j2 系列模型的响应
		if len(resp.Completions) > 0 {
			choice := openai.TextResponseChoice{
				Index: 0,
				Message: relaymodel.Message{
					Role:    "assistant",
					Content: resp.Completions[0].Data.Text,
					Name:    nil,
				},
				FinishReason: resp.Completions[0].FinishReason.Reason,
			}
			choices = append(choices, choice)
		}

		// 计算 token 数量
		promptTokens := 0
		completionTokens := 0

		// 获取 prompt 的最后一个 token 的 end 值作为 promptTokens
		if len(resp.Prompt.Tokens) > 0 {
			promptTokens = resp.Prompt.Tokens[len(resp.Prompt.Tokens)-1].TextRange.End
		}

		// 获取 completions 的最后一个 token 的 end 值作为 completionTokens
		if len(resp.Completions) > 0 && len(resp.Completions[0].Data.Tokens) > 0 {
			completionTokens = resp.Completions[0].Data.Tokens[len(resp.Completions[0].Data.Tokens)-1].TextRange.End
		}

		usage = model.Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		}

		idStr = fmt.Sprintf("%d", resp.ID)

	default:
		// 处理未知类型
		log.Printf("Unknown response type: %T", genericResponse)
		return nil
	}

	fullTextResponse := openai.TextResponse{
		Id:      idStr,
		Object:  "chat.completion",
		Created: helper.GetTimestamp(),
		Choices: choices,
		Usage:   usage,
	}
	return &fullTextResponse
}

func StreamHandler(c *gin.Context, awsCli *bedrockruntime.Client) (*relaymodel.ErrorWithStatusCode, *relaymodel.Usage) {
	createdTime := helper.GetTimestamp()
	awsModelId := c.GetString(ctxkey.RequestModel)

	ai21Req, ok := c.Get(ctxkey.ConvertedRequest)
	if !ok {
		return utils.WrapErr(errors.New("request not found")), nil
	}

	var usage relaymodel.Usage
	c.Writer.Header().Set("Content-Type", "text/event-stream")

	switch awsModelId {
	case "ai21.jamba-instruct-v1:0", "ai21.jamba-1-5-large-v1:0", "ai21.jamba-1-5-mini-v1:0":
		// 流式处理逻辑
		awsReq := &bedrockruntime.InvokeModelWithResponseStreamInput{
			ModelId:     aws.String(awsModelId),
			Accept:      aws.String("application/json"),
			ContentType: aws.String("application/json"),
		}

		var err error
		awsReq.Body, err = json.Marshal(ai21Req)
		if err != nil {
			return utils.WrapErr(errors.Wrap(err, "marshal request")), nil
		}

		awsResp, err := awsCli.InvokeModelWithResponseStream(c.Request.Context(), awsReq)
		if err != nil {
			return utils.WrapErr(errors.Wrap(err, "InvokeModelWithResponseStream")), nil
		}
		stream := awsResp.GetStream()
		defer stream.Close()

		c.Stream(func(w io.Writer) bool {
			event, ok := <-stream.Events()
			if !ok {
				return false
			}

			switch v := event.(type) {
			case *types.ResponseStreamMemberChunk:
				var ai21Resp ai21StreamResponse
				err := json.NewDecoder(bytes.NewReader(v.Value.Bytes)).Decode(&ai21Resp)
				if err != nil {
					logger.SysError("error unmarshalling stream response: " + err.Error())
					return false
				}

				openaiResp := StreamResponseAI21ToOpenAI(&ai21Resp)
				openaiResp.Id = fmt.Sprintf("chatcmpl-%s", helper.GetUUID())
				openaiResp.Model = c.GetString(ctxkey.OriginalModel)
				openaiResp.Created = createdTime

				jsonStr, err := json.Marshal(openaiResp)
				if err != nil {
					logger.SysError("error marshalling stream response: " + err.Error())
					return true
				}
				c.Render(-1, common.CustomEvent{Data: "data: " + string(jsonStr)})

				// 更新 usage
				if ai21Resp.Usage != nil {
					usage.PromptTokens = ai21Resp.Usage.PromptTokens
					usage.CompletionTokens = ai21Resp.Usage.CompletionTokens
					usage.TotalTokens = ai21Resp.Usage.TotalTokens
				}

				return true
			default:
				return false
			}
		})

		// 在流结束后单独发送 [DONE] 消息
		c.Render(-1, common.CustomEvent{Data: "data: [DONE]"})

	case "ai21.j2-ultra-v1", "ai21.j2-mid-v1":
		// 非流式处理逻辑
		awsReq := &bedrockruntime.InvokeModelInput{
			ModelId:     aws.String(awsModelId),
			Accept:      aws.String("application/json"),
			ContentType: aws.String("application/json"),
		}

		var err error
		awsReq.Body, err = json.Marshal(ai21Req)
		if err != nil {
			return utils.WrapErr(errors.Wrap(err, "marshal request")), nil
		}

		awsResp, err := awsCli.InvokeModel(c.Request.Context(), awsReq)
		if err != nil {
			return utils.WrapErr(errors.Wrap(err, "InvokeModel")), nil
		}

		var j2Response Response
		if err := json.Unmarshal(awsResp.Body, &j2Response); err != nil {
			return utils.WrapErr(errors.Wrap(err, "unmarshal response")), nil
		}

		streamResponses := convertJ2ResponseToStream(&j2Response, awsModelId, createdTime)
		for _, streamResp := range streamResponses {
			streamResp.Id = fmt.Sprintf("chatcmpl-%s", helper.GetUUID())
			streamResp.Model = c.GetString(ctxkey.OriginalModel)
			streamResp.Created = createdTime

			jsonStr, err := json.Marshal(streamResp)
			if err != nil {
				logger.SysError("error marshalling stream response: " + err.Error())
				continue
			}
			c.Render(-1, common.CustomEvent{Data: "data: " + string(jsonStr)})
		}

		// 在所有响应发送完毕后，单独发送 [DONE] 消息
		c.Render(-1, common.CustomEvent{Data: "data: [DONE]"})

		// 更新 usage（使用最后一个响应中的 Usage）
		if len(streamResponses) > 0 && streamResponses[len(streamResponses)-1].Usage != nil {
			lastUsage := streamResponses[len(streamResponses)-1].Usage
			usage.PromptTokens = lastUsage.PromptTokens
			usage.CompletionTokens = lastUsage.CompletionTokens
			usage.TotalTokens = lastUsage.TotalTokens
		}

	default:
		return utils.WrapErr(errors.New("unknown model")), nil
	}

	return nil, &usage
}

func convertJ2ResponseToStream(j2Resp *Response, modelName string, createdTime int64) []openai.ChatCompletionsStreamResponse {
	var streamResponses []openai.ChatCompletionsStreamResponse

	for i, token := range j2Resp.Completions[0].Data.Tokens {
		streamResp := openai.ChatCompletionsStreamResponse{
			Id:     fmt.Sprintf("chatcmpl-%s", helper.GetUUID()),
			Object: "chat.completion.chunk",
			Model:  modelName, // 或者使用实际的模型名称
			Choices: []openai.ChatCompletionsStreamResponseChoice{
				{
					Index: 0,
					Delta: model.Message{
						Content: token.GeneratedToken.Token,
					},
				},
			},
		}

		// 设置最后一个token的FinishReason
		if i == len(j2Resp.Completions[0].Data.Tokens)-1 {
			finishReason := j2Resp.Completions[0].FinishReason.Reason
			streamResp.Choices[0].FinishReason = &finishReason
		}

		streamResponses = append(streamResponses, streamResp)
	}

	// 添加一个额外的响应，包含完整的usage信息
	finalResp := openai.ChatCompletionsStreamResponse{
		Id:      fmt.Sprintf("chatcmpl-%s", helper.GetUUID()),
		Object:  "chat.completion.chunk",
		Created: createdTime,
		Model:   "ai21.j2-ultra-v1", // 或者使用实际的模型名称
		Choices: []openai.ChatCompletionsStreamResponseChoice{
			{
				Index: 0,
				Delta: model.Message{},
			},
		},
		Usage: &model.Usage{
			PromptTokens:     len(j2Resp.Prompt.Tokens),
			CompletionTokens: len(j2Resp.Completions[0].Data.Tokens),
			TotalTokens:      len(j2Resp.Prompt.Tokens) + len(j2Resp.Completions[0].Data.Tokens),
		},
	}
	streamResponses = append(streamResponses, finalResp)

	return streamResponses
}

func StreamResponseAI21ToOpenAI(ai21Resp *ai21StreamResponse) *openai.ChatCompletionsStreamResponse {
	openaiResp := &openai.ChatCompletionsStreamResponse{
		Id:      string(ai21Resp.ID),
		Object:  "chat.completion.chunk",
		Created: ai21Resp.Created,
		Model:   ai21Resp.Model,
		Choices: make([]openai.ChatCompletionsStreamResponseChoice, len(ai21Resp.Choices)),
	}

	for i, choice := range ai21Resp.Choices {
		openaiResp.Choices[i] = openai.ChatCompletionsStreamResponseChoice{
			Index: choice.Index,
			Delta: model.Message{
				Role:    choice.Delta.Role,
				Content: choice.Delta.Content,
			},
			FinishReason: choice.FinishReason,
		}
	}

	if ai21Resp.Usage != nil {
		openaiResp.Usage = &model.Usage{
			PromptTokens:     ai21Resp.Usage.PromptTokens,
			CompletionTokens: ai21Resp.Usage.CompletionTokens,
			TotalTokens:      ai21Resp.Usage.TotalTokens,
		}
	}

	return openaiResp
}
