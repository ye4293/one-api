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
	"ai21.jamba-instruct-v1:0": "ai21.jamba-instruct-v1:0",
	"ai21.j2-ultra-v1":         "ai21.j2-ultra-v1",
	"ai21.j2-mid-v1":           "ai21.j2-mid-v1",
}

func ConvertRequest(textRequest *model.GeneralOpenAIRequest) *AI21Request {
	req := &AI21Request{
		Temperature: textRequest.Temperature,
	}

	switch textRequest.Model {
	case "ai21.jamba-instruct-v1:0":
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
	// 在函数开始时记录入口日志

	ai21Req, ok := c.Get(ctxkey.ConvertedRequest)
	if !ok {
		logger.SysLog("Error: ConvertedRequest not found in context")
		return utils.WrapErr(errors.New("request not found")), nil
	}

	// 记录ai21Req的内容
	logger.SysLog(fmt.Sprintf("ai21Req: %+v", ai21Req))

	var err error
	awsReq.Body, err = json.Marshal(ai21Req)
	if err != nil {
		logger.SysLog(fmt.Sprintf("Error marshaling request: %v", err))
		return utils.WrapErr(errors.Wrap(err, "marshal request")), nil
	}

	// 记录序列化后的awsReq.Body
	logger.SysLog(fmt.Sprintf("Marshaled awsReq.Body: %s", string(awsReq.Body)))

	// 记录完整的awsReq
	logger.SysLog(fmt.Sprintf("Full awsReq: %+v", awsReq))

	awsResp, err := awsCli.InvokeModel(c.Request.Context(), awsReq)

	// 记录InvokeModel的结果
	if err != nil {
		logger.SysLog(fmt.Sprintf("Error from InvokeModel: %v", err))
		return utils.WrapErr(errors.Wrap(err, "InvokeModel")), nil
	} else {
		// 直接打印响应体，因为 Body 已经是 []byte 类型
		logger.SysLog(fmt.Sprintf("Raw AWS response body: %s", string(awsResp.Body)))

		logger.SysLog(fmt.Sprintf("InvokeModel successful, awsResp: %+v", awsResp))

		// 如果需要，可以在这里解析 JSON
		var parsedResp map[string]interface{}
		if err := json.Unmarshal(awsResp.Body, &parsedResp); err != nil {
			logger.SysLog(fmt.Sprintf("Error parsing AWS response: %v", err))
		} else {
			logger.SysLog(fmt.Sprintf("Parsed AWS response: %+v", parsedResp))
		}
	}

	// 继续处理响应...

	var ai21Response NonStreamingResponse
	err = json.Unmarshal(awsResp.Body, &ai21Response)
	if err != nil {
		return utils.WrapErr(errors.Wrap(err, "unmarshal response")), nil
	}

	openaiResp := ResponseAi21ToOpenAI(&ai21Response)
	openaiResp.Model = modelName
	usage := relaymodel.Usage{
		PromptTokens:     ai21Response.Usage.PromptTokens,
		CompletionTokens: ai21Response.Usage.CompletionTokens,
		TotalTokens:      ai21Response.Usage.PromptTokens + ai21Response.Usage.PromptTokens,
	}
	openaiResp.Usage = usage
	logger.SysLog(fmt.Sprint("openaiResp:%s", openaiResp))
	c.JSON(http.StatusOK, openaiResp)
	return nil, &usage
}

func ResponseAi21ToOpenAI(ai21Response *NonStreamingResponse) *openai.TextResponse {
	var choices []openai.TextResponseChoice

	for _, ai21Choice := range ai21Response.Choices {
		responseText := ai21Choice.Message.Content
		choice := openai.TextResponseChoice{
			Index: ai21Choice.Index,
			Message: relaymodel.Message{
				Role:    ai21Choice.Message.Role,
				Content: responseText,
				Name:    nil,
			},
			FinishReason: ai21Choice.FinishReason,
		}
		choices = append(choices, choice)
	}

	fullTextResponse := openai.TextResponse{
		Id:      ai21Response.ID,
		Object:  "chat.completion",
		Created: helper.GetTimestamp(),
		Choices: choices,
		Usage: model.Usage{
			PromptTokens:     ai21Response.Usage.PromptTokens,
			CompletionTokens: ai21Response.Usage.CompletionTokens,
			TotalTokens:      ai21Response.Usage.TotalTokens,
		},
	}
	return &fullTextResponse
}

func StreamHandler(c *gin.Context, awsCli *bedrockruntime.Client) (*relaymodel.ErrorWithStatusCode, *relaymodel.Usage) {
	createdTime := helper.GetTimestamp()
	awsModelId := c.GetString(ctxkey.RequestModel)

	awsReq := &bedrockruntime.InvokeModelWithResponseStreamInput{
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

	awsResp, err := awsCli.InvokeModelWithResponseStream(c.Request.Context(), awsReq)
	if err != nil {
		return utils.WrapErr(errors.Wrap(err, "InvokeModelWithResponseStream")), nil
	}
	stream := awsResp.GetStream()
	defer stream.Close()

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	var usage relaymodel.Usage
	c.Stream(func(w io.Writer) bool {
		event, ok := <-stream.Events()
		if !ok {
			c.Render(-1, common.CustomEvent{Data: "data: [DONE]"})
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

			// 检查是否是最后一个响应
			if len(openaiResp.Choices) > 0 && openaiResp.Choices[0].FinishReason != nil {
				return false
			}

			return true
		case *types.UnknownUnionMember:
			fmt.Println("unknown tag:", v.Tag)
			return false
		default:
			fmt.Println("union is nil or unknown type")
			return false
		}
	})

	return nil, &usage
}

func StreamResponseAI21ToOpenAI(ai21Resp *ai21StreamResponse) *openai.ChatCompletionsStreamResponse {
	openaiResp := &openai.ChatCompletionsStreamResponse{
		Id:      ai21Resp.ID,
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
