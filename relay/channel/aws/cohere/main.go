package aws

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"

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
	"github.com/songquanpeng/one-api/relay/channel/cohere"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/model"
)

var AwsModelIDMap = map[string]string{
	"cohere.command-r-v1:0":      "cohere.command-r-v1:0",
	"cohere.command-r-plus-v1:0": "cohere.command-r-plus-v1:0",
}

var fieldMappings = map[string]string{
	"Message":          "message",
	"ChatHistory":      "chat_history",
	"Documents":        "documents",
	"Preamble":         "preamble",
	"MaxTokens":        "max_tokens",
	"Temperature":      "temperature",
	"P":                "p",
	"K":                "k",
	"PromptTruncation": "prompt_truncation",
	"FrequencyPenalty": "frequency_penalty",
	"PresencePenalty":  "presence_penalty",
	"Seed":             "seed",
	"Tools":            "tools",
	"ToolResults":      "tool_results",
	"StopSequences":    "stop_sequences",
}

func convertCohereRequestToAWSMap(cohereReq *cohere.Request) (map[string]interface{}, error) {
	if cohereReq == nil {
		return nil, errors.New("cohereReq is nil")
	}

	requestMap := make(map[string]interface{})
	v := reflect.ValueOf(*cohereReq)
	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldName := t.Field(i).Name

		// 检查字段是否在映射表中
		if awsFieldName, exists := fieldMappings[fieldName]; exists {
			// 跳过空值字段
			if isZeroValue(field) {
				continue
			}

			// 特殊处理某些字段
			switch fieldName {
			case "K":
				requestMap[awsFieldName] = float64(field.Int())
			case "Temperature", "P", "FrequencyPenalty", "PresencePenalty":
				// 对于浮点数，我们可能想要限制精度
				requestMap[awsFieldName] = float64(int(field.Float()*1000)) / 1000
			default:
				requestMap[awsFieldName] = field.Interface()
			}
		}
	}

	// 添加 AWS 特有的字段，设置默认值
	requestMap["search_queries_only"] = false
	requestMap["return_prompt"] = false
	requestMap["raw_prompting"] = false

	return requestMap, nil
}

// 检查值是否为零值
func isZeroValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Ptr:
		return v.IsNil()
	}
	return false
}

func Handler(c *gin.Context, awsCli *bedrockruntime.Client, modelName string) (*model.ErrorWithStatusCode, *model.Usage) {
	awsModelId := modelName
	awsReq := &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(awsModelId),
		Accept:      aws.String("application/json"),
		ContentType: aws.String("application/json"),
	}

	cohereReq_, ok := c.Get(ctxkey.ConvertedRequest)
	if !ok {
		return utils.WrapErr(errors.New("request not found")), nil
	}

	cohereReq, ok := cohereReq_.(*cohere.Request)
	if !ok {
		return utils.WrapErr(errors.New("invalid request type")), nil
	}
	var err error
	requestMap, err := convertCohereRequestToAWSMap(cohereReq)
	if err != nil {
		return utils.WrapErr(err), nil
	}

	awsReq.Body, err = json.Marshal(requestMap)
	if err != nil {
		return utils.WrapErr(errors.Wrap(err, "marshal request")), nil
	}

	awsResp, err := awsCli.InvokeModel(c.Request.Context(), awsReq)
	if err != nil {
		return utils.WrapErr(errors.Wrap(err, "InvokeModel")), nil
	}

	cohereResponse := new(cohere.Response)
	err = json.Unmarshal(awsResp.Body, cohereResponse)
	if err != nil {
		return utils.WrapErr(errors.Wrap(err, "unmarshal response")), nil
	}

	openaiResp := cohere.ResponseCohere2OpenAI(cohereResponse)
	openaiResp.Model = modelName

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

func StreamHandler(c *gin.Context, awsCli *bedrockruntime.Client) (*model.ErrorWithStatusCode, *model.Usage) {
	createdTime := helper.GetTimestamp()
	awsModelId := c.GetString(ctxkey.RequestModel)

	awsReq := &bedrockruntime.InvokeModelWithResponseStreamInput{
		ModelId:     aws.String(awsModelId),
		Accept:      aws.String("application/json"),
		ContentType: aws.String("application/json"),
	}

	cohereReq_, ok := c.Get(ctxkey.ConvertedRequest)
	if !ok {
		return utils.WrapErr(errors.New("request not found")), nil
	}

	cohereReq, ok := cohereReq_.(*cohere.Request)
	if !ok {
		return utils.WrapErr(errors.New("invalid request type")), nil
	}
	var err error
	requestMap, err := convertCohereRequestToAWSMap(cohereReq)
	if err != nil {
		return utils.WrapErr(err), nil
	}
	awsReq.Body, err = json.Marshal(requestMap)
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
	var usage model.Usage

	c.Stream(func(w io.Writer) bool {
		event, ok := <-stream.Events()
		if !ok {
			c.Render(-1, common.CustomEvent{Data: "data: [DONE]"})
			return false
		}

		switch v := event.(type) {
		case *types.ResponseStreamMemberChunk:
			cohereResp := new(cohere.StreamResponse)
			err := json.NewDecoder(bytes.NewReader(v.Value.Bytes)).Decode(cohereResp)
			if err != nil {
				logger.SysError("error unmarshalling stream response: " + err.Error())
				return false
			}

			response, meta := cohere.StreamResponseCohere2OpenAI(cohereResp)
			if meta != nil {
				usage.PromptTokens = meta.Meta.Tokens.InputTokens
				usage.CompletionTokens += meta.Meta.Tokens.OutputTokens
				return true
			}
			if response == nil {
				return true
			}
			response.Id = fmt.Sprintf("chatcmpl-%d", createdTime)
			response.Model = c.GetString(ctxkey.OriginalModel)
			response.Created = createdTime
			jsonStr, err := json.Marshal(response)
			if err != nil {
				logger.SysError("error marshalling stream response: " + err.Error())
				return true
			}

			c.Render(-1, common.CustomEvent{Data: "data: " + string(jsonStr)})
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
