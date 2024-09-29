package aws

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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
	"mistral.mistral-7b-instruct-v0:2":   "mistral.mistral-7b-instruct-v0:2",
	"mistral.mixtral-8x7b-instruct-v0:1": "mistral.mixtral-8x7b-instruct-v0:1",
	"mistral.mistral-large-2402-v1:0":    "mistral.mistral-large-2402-v1:0",
	"mistral.mistral-small-2402-v1:0":    "mistral.mistral-small-2402-v1:0",
}

func ConvertRequest(textRequest *model.GeneralOpenAIRequest) *MistralRequest {
	mistralReq := &MistralRequest{}

	// 构建 prompt
	var prompt strings.Builder
	for i, msg := range textRequest.Messages {
		if i > 0 {
			prompt.WriteString("")
		}
		prompt.WriteString(fmt.Sprintf("%s: %v", msg.Role, msg.Content))
	}
	mistralReq.Prompt = prompt.String()

	// 转换其他参数
	mistralReq.MaxTokens = textRequest.MaxTokens
	mistralReq.Temperature = textRequest.Temperature
	mistralReq.TopP = textRequest.TopP
	mistralReq.TopK = textRequest.TopK

	// 处理 Stop 参数
	if stop, ok := textRequest.Stop.([]string); ok {
		mistralReq.Stop = stop
	} else if stop, ok := textRequest.Stop.(string); ok {
		mistralReq.Stop = []string{stop}
	}

	// 设置默认值和参数范围
	if mistralReq.MaxTokens == 0 {
		mistralReq.MaxTokens = 200 // 默认值
	}

	if mistralReq.Temperature == 0 {
		mistralReq.Temperature = 0.7 // 默认值
	} else if mistralReq.Temperature < 0 {
		mistralReq.Temperature = 0
	} else if mistralReq.Temperature > 1 {
		mistralReq.Temperature = 1
	}

	if mistralReq.TopP == 0 {
		mistralReq.TopP = 1.0 // 默认值
	} else if mistralReq.TopP < 0 {
		mistralReq.TopP = 0
	} else if mistralReq.TopP > 1 {
		mistralReq.TopP = 1
	}

	if mistralReq.TopK == 0 {
		mistralReq.TopK = 50 // 默认值
	}

	return mistralReq
}

func Handler(c *gin.Context, awsCli *bedrockruntime.Client, modelName string) (*relaymodel.ErrorWithStatusCode, *relaymodel.Usage) {
	awsModelId := modelName
	awsReq := &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(awsModelId),
		Accept:      aws.String("application/json"),
		ContentType: aws.String("application/json"),
	}

	mistralReq_, ok := c.Get(ctxkey.ConvertedRequest)
	if !ok {
		return utils.WrapErr(errors.New("request not found")), nil
	}
	mistralReq := mistralReq_.(*MistralRequest)

	var err error
	awsReq.Body, err = json.Marshal(mistralReq)
	if err != nil {
		return utils.WrapErr(errors.Wrap(err, "marshal request")), nil
	}
	fmt.Printf("Request Body: %s\n", string(awsReq.Body))

	awsResp, err := awsCli.InvokeModel(c.Request.Context(), awsReq)
	if err != nil {
		return utils.WrapErr(errors.Wrap(err, "InvokeModel")), nil
	}

	mistralResponse := new(MistralResponse)
	err = json.Unmarshal(awsResp.Body, mistralResponse)
	if err != nil {
		return utils.WrapErr(errors.Wrap(err, "unmarshal response")), nil
	}

	openaiResp := openai.TextResponse{
		Id:      uuid.New().String(), // Generate a new UUID for the id
		Model:   modelName,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
	}

	for i, output := range mistralResponse.Outputs {
		choice := openai.TextResponseChoice{
			Index: i,
			Message: model.Message{
				Role:    "assistant",
				Content: output.Text,
			},
			FinishReason: output.StopReason,
		}
		openaiResp.Choices = append(openaiResp.Choices, choice)
	}

	var usage model.Usage
	request_, ok := c.Get("request")
	if !ok {
		return utils.WrapErr(errors.New("invalid request type")), nil
	}

	// 类型断言
	generalRequest, ok := request_.(model.GeneralOpenAIRequest)
	if !ok {
		return utils.WrapErr(errors.New("request is not of type GeneralOpenAIRequest")), nil
	}

	logger.SysLog(fmt.Sprintf("request: %+v", generalRequest))

	// 确保 Messages 切片不为空
	if len(generalRequest.Messages) == 0 {
		return utils.WrapErr(errors.New("no messages in request")), nil
	}

	messages1 := []model.Message{
		{
			Role:    "assistant", // 假设这是助手的回复，如果不是，请相应调整
			Content: generalRequest.Messages[0].Content,
		},
	}

	usage.PromptTokens = openai.CountTokenMessages(messages1, modelName)

	messages2 := []model.Message{
		{
			Role:    "assistant", // 假设这是助手的回复，如果不是，请相应调整
			Content: openaiResp.Choices[0].Content,
		},
	}

	usage.CompletionTokens = openai.CountTokenMessages(messages2, modelName)
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens

	openaiResp.Usage = usage

	c.JSON(http.StatusOK, openaiResp)
	return nil, &usage
}

func StreamResponseClaude2OpenAI(mistralResp *MistralStreamResponse, id string, created int64, modelName string) *openai.ChatCompletionsStreamResponse {
	if mistralResp == nil || len(mistralResp.Outputs) == 0 {
		return nil
	}

	output := mistralResp.Outputs[0]
	openAIResp := &openai.ChatCompletionsStreamResponse{
		Id:      id, // 生成一个唯一ID
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   modelName, // 或其他适当的模型名称
		Choices: []openai.ChatCompletionsStreamResponseChoice{
			{
				Index: 0,
				Delta: model.Message{
					Role:    "assistant",
					Content: output.Text,
				},
			},
		},
	}

	if output.StopReason != "" {
		finishReason := output.StopReason
		openAIResp.Choices[0].FinishReason = &finishReason
	}

	// 如果有usage信息，可以在这里添加
	if mistralResp.AmazonBedrockInvocationMetrics != nil {
		openAIResp.Usage = &model.Usage{
			PromptTokens:     mistralResp.AmazonBedrockInvocationMetrics.InputTokenCount,
			CompletionTokens: mistralResp.AmazonBedrockInvocationMetrics.OutputTokenCount,
			TotalTokens:      mistralResp.AmazonBedrockInvocationMetrics.InputTokenCount + mistralResp.AmazonBedrockInvocationMetrics.OutputTokenCount,
		}
	}

	return openAIResp
}

func StreamHandler(c *gin.Context, awsCli *bedrockruntime.Client) (*relaymodel.ErrorWithStatusCode, *relaymodel.Usage) {
	createdTime := helper.GetTimestamp()
	awsModelId := c.GetString(ctxkey.RequestModel)

	awsReq := &bedrockruntime.InvokeModelWithResponseStreamInput{
		ModelId:     aws.String(awsModelId),
		Accept:      aws.String("application/json"),
		ContentType: aws.String("application/json"),
	}

	mistralReq_, ok := c.Get(ctxkey.ConvertedRequest)
	if !ok {
		return utils.WrapErr(errors.New("request not found")), nil
	}

	var err error
	mistralReq := mistralReq_.(*MistralRequest)
	awsReq.Body, err = json.Marshal(mistralReq)
	if err != nil {
		return utils.WrapErr(errors.Wrap(err, "marshal request")), nil
	}

	// 打印请求体
	logger.SysLog("AWS Request Body: " + string(awsReq.Body))

	awsResp, err := awsCli.InvokeModelWithResponseStream(c.Request.Context(), awsReq)
	if err != nil {
		return utils.WrapErr(errors.Wrap(err, "InvokeModelWithResponseStream")), nil
	}
	stream := awsResp.GetStream()
	defer stream.Close()

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	var usage relaymodel.Usage
	responseId := "chatcmpl-" + uuid.New().String() // 为整个流生成一个唯一ID
	c.Stream(func(w io.Writer) bool {
		event, ok := <-stream.Events()
		if !ok {
			logger.SysLog("Stream closed")
			c.Render(-1, common.CustomEvent{Data: "data: [DONE]"})
			return false
		}

		switch v := event.(type) {

		case *types.ResponseStreamMemberChunk:
			// 打印原始响应

			mistralResp := new(MistralStreamResponse)
			err := json.NewDecoder(bytes.NewReader(v.Value.Bytes)).Decode(mistralResp)
			if err != nil {
				logger.SysError("error unmarshalling stream response: " + err.Error())
				return false
			}

			openAIResp := StreamResponseClaude2OpenAI(mistralResp, responseId, createdTime, awsModelId)
			if openAIResp == nil {
				logger.SysLog("Response is nil")
				return true
			}

			// 更新usage
			if openAIResp.Usage != nil {
				usage.PromptTokens += openAIResp.Usage.PromptTokens
				usage.CompletionTokens += openAIResp.Usage.CompletionTokens
			}

			jsonStr, err := json.Marshal(openAIResp)
			if err != nil {
				logger.SysError("error marshalling stream response: " + err.Error())
				return true
			}

			c.Render(-1, common.CustomEvent{Data: "data: " + string(jsonStr)})
			return true
		case *types.UnknownUnionMember:
			logger.SysError("unknown tag: " + v.Tag)
			return false
		default:
			logger.SysError("union is nil or unknown type")
			return false
		}
	})

	return nil, &usage
}
