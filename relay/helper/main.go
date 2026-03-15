package helper

import (
	"strings"

	"github.com/songquanpeng/one-api/relay/channel"
	"github.com/songquanpeng/one-api/relay/channel/ali"
	"github.com/songquanpeng/one-api/relay/channel/anthropic"
	"github.com/songquanpeng/one-api/relay/channel/aws"
	"github.com/songquanpeng/one-api/relay/channel/baidu"
	"github.com/songquanpeng/one-api/relay/channel/cohere"
	"github.com/songquanpeng/one-api/relay/channel/flux"
	"github.com/songquanpeng/one-api/relay/channel/gemini"
	"github.com/songquanpeng/one-api/relay/channel/minimax"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/channel/runway"
	"github.com/songquanpeng/one-api/relay/channel/tencent"
	"github.com/songquanpeng/one-api/relay/channel/vertexai"
	"github.com/songquanpeng/one-api/relay/channel/xai"
	"github.com/songquanpeng/one-api/relay/channel/xunfei"
	"github.com/songquanpeng/one-api/relay/channel/zhipu"
	"github.com/songquanpeng/one-api/relay/constant"
)

// GetVideoAdaptor 根据模型名称返回对应的视频适配器
func GetVideoAdaptor(modelName string) channel.VideoAdaptor {
	lower := strings.ToLower(modelName)
	switch {
	// Minimax
	case strings.HasPrefix(modelName, "video-01"),
		strings.HasPrefix(modelName, "S2V-01"),
		strings.HasPrefix(modelName, "T2V-01"),
		strings.HasPrefix(modelName, "I2V-01"),
		strings.HasPrefix(lower, "minimax"):
		return &minimax.VideoAdaptor{}
	// Zhipu
	case modelName == "cogvideox":
		return &zhipu.VideoAdaptor{}
	// Runway
	case modelName == "gen3a_turbo":
		return &runway.VideoAdaptor{}
	// TODO: Phase 2/3 – kling、luma、ali、pixverse、doubao、grok、sora、vertexai
	}
	return nil
}

// GetVideoAdaptorByProvider 根据供应商名称返回对应的视频适配器（用于结果查询）
func GetVideoAdaptorByProvider(provider string) channel.VideoAdaptor {
	switch provider {
	case "minimax":
		return &minimax.VideoAdaptor{}
	case "zhipu":
		return &zhipu.VideoAdaptor{}
	case "runway":
		return &runway.VideoAdaptor{}
	// TODO: Phase 2/3 – kling、luma、ali、pixverse、doubao、grok、sora、vertexai
	}
	return nil
}

func GetAdaptor(apiType int) channel.Adaptor {
	switch apiType {
	case constant.APITypeAli:
		return &ali.Adaptor{}
	case constant.APITypeAnthropic:
		return &anthropic.Adaptor{}
	case constant.APITypeBaidu:
		return &baidu.Adaptor{}
	case constant.APITypeGemini:
		return &gemini.Adaptor{}
	case constant.APITypeOpenAI:
		return &openai.Adaptor{}
	case constant.APITypeTencent:
		return &tencent.Adaptor{}
	case constant.APITypeXunfei:
		return &xunfei.Adaptor{}
	case constant.APITypeZhipu:
		return &zhipu.Adaptor{}
	case constant.APITypeAwsClaude:
		return &aws.Adaptor{}
	case constant.APITypeCohere:
		return &cohere.Adaptor{}
	case constant.APITypeMinimax:
		return &minimax.Adaptor{}
	case constant.APITypeXAI:
		return &xai.Adaptor{}
	case constant.APITypeVertexAI:
		return &vertexai.Adaptor{}
	case constant.APITypeFlux:
		return &flux.Adaptor{}
	}
	return nil
}
