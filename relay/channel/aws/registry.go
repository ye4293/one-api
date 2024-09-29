package aws

import (
	ai21 "github.com/songquanpeng/one-api/relay/channel/aws/ai21"
	claude "github.com/songquanpeng/one-api/relay/channel/aws/claude"
	cohere "github.com/songquanpeng/one-api/relay/channel/aws/cohere"
	llama3 "github.com/songquanpeng/one-api/relay/channel/aws/llama3"
	mistral "github.com/songquanpeng/one-api/relay/channel/aws/mistral"
	"github.com/songquanpeng/one-api/relay/channel/aws/utils"
)

type AwsModelType int

const (
	AwsClaude AwsModelType = iota + 1
	AwsLlama3
	AwsAi21
	AwsCohere
	AwsMistral
)

var (
	adaptors = map[string]AwsModelType{}
)

func init() {
	for model := range claude.AwsModelIDMap {
		adaptors[model] = AwsClaude
	}
	for model := range llama3.AwsModelIDMap {
		adaptors[model] = AwsLlama3
	}
	for model := range ai21.AwsModelIDMap {
		adaptors[model] = AwsAi21
	}
	for model := range cohere.AwsModelIDMap {
		adaptors[model] = AwsCohere
	}
	for model := range mistral.AwsModelIDMap {
		adaptors[model] = AwsMistral
	}
}

func GetAdaptor(model string) utils.AwsAdapter {
	adaptorType := adaptors[model]
	switch adaptorType {
	case AwsClaude:
		return &claude.Adaptor{}
	case AwsLlama3:
		return &llama3.Adaptor{}
	case AwsAi21:
		return &ai21.Adaptor{}
	case AwsCohere:
		return &cohere.Adaptor{}
	case AwsMistral:
		return &mistral.Adaptor{}

	default:
		return nil
	}
}
