package aws

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

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
	"github.com/songquanpeng/one-api/relay/model"
)

var AwsModelIDMap = map[string]string{
	"cohere.command-r-v1:0": "cohere.command-r-v1:0",
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

	// 创建一个新的map来存储我们想要的字段
	requestMap := map[string]interface{}{
		"message":         cohereReq.Message,
		"return_metadata": true,
	}

	// 添加其他非空字段
	if len(cohereReq.ChatHistory) > 0 {
		requestMap["chat_history"] = cohereReq.ChatHistory
	}

	var err error
	awsReq.Body, err = json.Marshal(requestMap)
	if err != nil {
		return utils.WrapErr(errors.Wrap(err, "marshal request")), nil
	}

	// 打印请求体
	fmt.Printf("Request Body: %s\n", string(awsReq.Body))

	awsResp, err := awsCli.InvokeModel(c.Request.Context(), awsReq)
	if err != nil {
		return utils.WrapErr(errors.Wrap(err, "InvokeModel")), nil
	}

	// 打印原始响应体
	fmt.Printf("Raw Response Body: %s\n", string(awsResp.Body))

	cohereResponse := new(cohere.Response)
	err = json.Unmarshal(awsResp.Body, cohereResponse)
	if err != nil {
		return utils.WrapErr(errors.Wrap(err, "unmarshal response")), nil
	}

	openaiResp := cohere.ResponseCohere2OpenAI(cohereResponse)
	openaiResp.Model = modelName
	usage := model.Usage{
		PromptTokens:     cohereResponse.Meta.BilledUnits.InputTokens,
		CompletionTokens: cohereResponse.Meta.BilledUnits.OutputTokens,
		TotalTokens:      cohereResponse.Meta.BilledUnits.InputTokens + cohereResponse.Meta.BilledUnits.OutputTokens,
	}
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

	requestMap := map[string]interface{}{
		"message": cohereReq.Message,
	}

	// 添加其他非空字段
	if len(cohereReq.ChatHistory) > 0 {
		requestMap["chat_history"] = cohereReq.ChatHistory
	}
	var err error
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
				usage.PromptTokens += meta.Meta.Tokens.InputTokens
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
