package openai

import (
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/relay/channel/baichuan"
	"github.com/songquanpeng/one-api/relay/channel/deepseek"
	"github.com/songquanpeng/one-api/relay/channel/groq"
	"github.com/songquanpeng/one-api/relay/channel/lingyiwanwu"
	"github.com/songquanpeng/one-api/relay/channel/mistral"
	"github.com/songquanpeng/one-api/relay/channel/moonshot"
	"github.com/songquanpeng/one-api/relay/channel/togetherai"
)

var CompatibleChannels = []int{
	common.ChannelTypeAzure,
	common.ChannelTypeMoonshot,
	common.ChannelTypeBaichuan,
	common.ChannelTypeMistral,
	common.ChannelTypeGroq,
	common.ChannelTypeLingYiWanWu,
	common.ChannelTypeTogetherAi,
}

func GetCompatibleChannelMeta(channelType int) (string, []string) {
	switch channelType {
	case common.ChannelTypeAzure:
		return "azure", ModelList
	case common.ChannelTypeMoonshot:
		return "moonshot", moonshot.ModelList
	case common.ChannelTypeBaichuan:
		return "baichuan", baichuan.ModelList
	case common.ChannelTypeMistral:
		return "mistralai", mistral.ModelList
	case common.ChannelTypeGroq:
		return "groq", groq.ModelList
	case common.ChannelTypeLingYiWanWu:
		return "lingyiwanwu", lingyiwanwu.ModelList
	case common.ChannelTypeTogetherAi:
		return "togetherai", togetherai.ModelList
	case common.ChannelTypeDeepseek:
		return "deepseek", deepseek.ModelList
	default:
		return "openai", ModelList
	}
}
