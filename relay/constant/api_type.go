package constant

import (
	"github.com/songquanpeng/one-api/common"
)

const (
	APITypeOpenAI = iota
	APITypeAnthropic
	APITypePaLM
	APITypeBaidu
	APITypeZhipu
	APITypeAli
	APITypeXunfei
	APITypeAIProxyLibrary
	APITypeTencent
	APITypeGemini
	APITypeOllama
	APITypeAwsClaude
	APITypeCoze
	APITypeCohere
	APITypeReplicate

	APITypeDummy // this one is only for count, do not add any channel after this
)

func ChannelType2APIType(channelType int) int {
	apiType := APITypeOpenAI
	switch channelType {
	case common.ChannelTypeAnthropic:
		apiType = APITypeAnthropic
	case common.ChannelTypeBaidu:
		apiType = APITypeBaidu
	case common.ChannelTypePaLM:
		apiType = APITypePaLM
	case common.ChannelTypeZhipu:
		apiType = APITypeZhipu
	case common.ChannelTypeAli:
		apiType = APITypeAli
	case common.ChannelTypeXunfei:
		apiType = APITypeXunfei
	case common.ChannelTypeAIProxyLibrary:
		apiType = APITypeAIProxyLibrary
	case common.ChannelTypeTencent:
		apiType = APITypeTencent
	case common.ChannelTypeGemini:
		apiType = APITypeGemini
	case common.ChannelTypeOllama:
		apiType = APITypeOllama
	case common.ChannelTypeCohere:
		apiType = APITypeCohere
	case common.ChannelTypeCoze:
		apiType = APITypeCoze
	case common.ChannelTypeReplicate:
		apiType = APITypeReplicate
	case common.ChannelTypeAwsClaude:
		apiType = APITypeAwsClaude

	}
	return apiType
}
