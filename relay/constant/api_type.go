package constant

import (
	"github.com/songquanpeng/one-api/common"
)

const (
	APITypeOpenAI = iota
	APITypeAnthropic
	APITypeBaidu
	APITypeZhipu
	APITypeAli
	APITypeXunfei
	APITypeTencent
	APITypeGemini
	APITypeAwsClaude
	APITypeCohere
	APITypeMinimax

	APITypeDummy // this one is only for count, do not add any channel after this
)

func ChannelType2APIType(channelType int) int {
	apiType := APITypeOpenAI
	switch channelType {
	case common.ChannelTypeAnthropic:
		apiType = APITypeAnthropic
	case common.ChannelTypeBaidu:
		apiType = APITypeBaidu
	case common.ChannelTypeZhipu:
		apiType = APITypeZhipu
	case common.ChannelTypeAli:
		apiType = APITypeAli
	case common.ChannelTypeXunfei:
		apiType = APITypeXunfei
	case common.ChannelTypeTencent:
		apiType = APITypeTencent
	case common.ChannelTypeGemini:
		apiType = APITypeGemini
	case common.ChannelTypeCohere:
		apiType = APITypeCohere
	case common.ChannelTypeAwsClaude:
		apiType = APITypeAwsClaude
	case common.ChannelTypeMinimax:
		apiType = APITypeMinimax

	}
	return apiType
}
