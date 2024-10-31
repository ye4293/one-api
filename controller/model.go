package controller

import (
	"fmt"

	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/constant"
	"github.com/songquanpeng/one-api/relay/helper"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

// https://platform.openai.com/docs/api-reference/models/list

type OpenAIModelPermission struct {
	Id                 string  `json:"id"`
	Object             string  `json:"object"`
	Created            int     `json:"created"`
	AllowCreateEngine  bool    `json:"allow_create_engine"`
	AllowSampling      bool    `json:"allow_sampling"`
	AllowLogprobs      bool    `json:"allow_logprobs"`
	AllowSearchIndices bool    `json:"allow_search_indices"`
	AllowView          bool    `json:"allow_view"`
	AllowFineTuning    bool    `json:"allow_fine_tuning"`
	Organization       string  `json:"organization"`
	Group              *string `json:"group"`
	IsBlocking         bool    `json:"is_blocking"`
}

type OpenAIModels struct {
	Id         string                  `json:"id"`
	Object     string                  `json:"object"`
	Created    int                     `json:"created"`
	OwnedBy    string                  `json:"owned_by"`
	Permission []OpenAIModelPermission `json:"permission"`
	Root       string                  `json:"root"`
	Parent     *string                 `json:"parent"`
}

var openAIModels []OpenAIModels
var openAIModelsMap map[string]OpenAIModels
var channelId2Models map[int][]string

func init() {
	var permission []OpenAIModelPermission
	permission = append(permission, OpenAIModelPermission{
		Id:                 "modelperm-LwHkVFn8AcMItP432fKKDIKJ",
		Object:             "model_permission",
		Created:            1626777600,
		AllowCreateEngine:  true,
		AllowSampling:      true,
		AllowLogprobs:      true,
		AllowSearchIndices: false,
		AllowView:          true,
		AllowFineTuning:    false,
		Organization:       "*",
		Group:              nil,
		IsBlocking:         false,
	})
	// https://platform.openai.com/docs/models/model-endpoint-compatibility
	for i := 0; i < constant.APITypeDummy; i++ {
		if i == constant.APITypeAIProxyLibrary {
			continue
		}
		adaptor := helper.GetAdaptor(i)
		channelName := adaptor.GetChannelName()
		modelNames := adaptor.GetModelList()
		for _, modelName := range modelNames {
			openAIModels = append(openAIModels, OpenAIModels{
				Id:         modelName,
				Object:     "model",
				Created:    1626777600,
				OwnedBy:    channelName,
				Permission: permission,
				Root:       modelName,
				Parent:     nil,
			})
		}
	}
	for _, channelType := range openai.CompatibleChannels {
		if channelType == common.ChannelTypeAzure {
			continue
		}
		channelName, channelModelList := openai.GetCompatibleChannelMeta(channelType)
		for _, modelName := range channelModelList {
			openAIModels = append(openAIModels, OpenAIModels{
				Id:         modelName,
				Object:     "model",
				Created:    1626777600,
				OwnedBy:    channelName,
				Permission: permission,
				Root:       modelName,
				Parent:     nil,
			})
		}
	}
	for modelName, _ := range common.MidjourneyModel2Action {
		openAIModels = append(openAIModels, OpenAIModels{
			Id:         modelName,
			Object:     "model",
			Created:    1626777600,
			OwnedBy:    "midjourney",
			Permission: permission,
			Root:       modelName,
			Parent:     nil,
		})
	}
	for modelName, _ := range common.SdModel2Action {
		openAIModels = append(openAIModels, OpenAIModels{
			Id:         modelName,
			Object:     "model",
			Created:    1626777600,
			OwnedBy:    "Stability",
			Permission: permission,
			Root:       modelName,
			Parent:     nil,
		})
	}
	openAIModelsMap = make(map[string]OpenAIModels)
	for _, model := range openAIModels {
		openAIModelsMap[model.Id] = model
	}
	channelId2Models = make(map[int][]string)
	for i := 1; i < common.ChannelTypeDummy; i++ {
		adaptor := helper.GetAdaptor(constant.ChannelType2APIType(i))
		meta := &util.RelayMeta{
			ChannelType: i,
		}
		adaptor.Init(meta)
		channelId2Models[i] = adaptor.GetModelList()
	}
}

func DashboardListModels(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channelId2Models,
	})
}

func ListModels(c *gin.Context) {
	c.JSON(200, gin.H{
		"object": "list",
		"data":   openAIModels,
	})
}

func RetrieveModel(c *gin.Context) {
	modelId := c.Param("model")
	if model, ok := openAIModelsMap[modelId]; ok {
		c.JSON(200, model)
	} else {
		Error := relaymodel.Error{
			Message: fmt.Sprintf("The model '%s' does not exist", modelId),
			Type:    "invalid_request_error",
			Param:   "model",
			Code:    "model_not_found",
		}
		c.JSON(200, gin.H{
			"error": Error,
		})
	}
}

// 定义返回的数据结构
func ListTypes(c *gin.Context) {
	// 定义返回的数据结构
	type ChannelOption struct {
		Key   int    `json:"key"`
		Text  string `json:"text"`
		Value int    `json:"value"`
		Color string `json:"color"`
	}

	// 创建返回数据
	channelOptions := []ChannelOption{
		{Key: 1, Text: "OpenAI", Value: 1, Color: "green"},
		{Key: 14, Text: "Anthropic Claude", Value: 14, Color: "black"},
		{Key: 3, Text: "Azure OpenAI", Value: 3, Color: "olive"},
		{Key: 11, Text: "Google PaLM2", Value: 11, Color: "orange"},
		{Key: 24, Text: "Google Gemini", Value: 24, Color: "orange"},
		{Key: 28, Text: "Mistral AI", Value: 28, Color: "orange"},
		{Key: 31, Text: "零一万物", Value: 31, Color: "green"},
		{Key: 32, Text: "midjourney-Plus", Value: 32, Color: "green"},
		{Key: 33, Text: "AWS Claude", Value: 33, Color: "black"},
		{Key: 34, Text: "Coze", Value: 34, Color: "blue"},
		{Key: 35, Text: "Cohere", Value: 35, Color: "green"},
		{Key: 36, Text: "together", Value: 36, Color: "blue"},
		{Key: 37, Text: "Deepseek", Value: 37, Color: "green"},
		{Key: 38, Text: "Stability", Value: 38, Color: "blue"},
		{Key: 39, Text: "Novita", Value: 39, Color: "blue"},
		{Key: 40, Text: "Replicate", Value: 40, Color: "blue"},
		{Key: 30, Text: "Ollama", Value: 30, Color: "orange"},
		{Key: 29, Text: "Groq", Value: 29, Color: "orange"},
		{Key: 15, Text: "百度文心千帆", Value: 15, Color: "blue"},
		{Key: 17, Text: "阿里通义千问", Value: 17, Color: "orange"},
		{Key: 18, Text: "讯飞星火认知", Value: 18, Color: "blue"},
		{Key: 16, Text: "智谱 ChatGLM", Value: 16, Color: "violet"},
		{Key: 19, Text: "360 智脑", Value: 19, Color: "blue"},
		{Key: 25, Text: "Moonshot AI", Value: 25, Color: "black"},
		{Key: 23, Text: "腾讯混元", Value: 23, Color: "teal"},
		{Key: 26, Text: "百川大模型", Value: 26, Color: "orange"},
		{Key: 27, Text: "MiniMax", Value: 27, Color: "red"},
		{Key: 8, Text: "自定义渠道", Value: 8, Color: "pink"},
		{Key: 41, Text: "可灵", Value: 41, Color: "purple"},
	}

	c.JSON(200, gin.H{
		"object": "list",
		"data":   channelOptions,
	})
}
