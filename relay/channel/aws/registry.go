package aws

import (
	claude "github.com/songquanpeng/one-api/relay/channel/aws/claude"
	"github.com/songquanpeng/one-api/relay/channel/aws/utils"
)

type AwsModelType int

const (
	AwsClaude AwsModelType = iota + 1
)

var (
	adaptors = map[string]AwsModelType{}
)

func init() {
	for model := range claude.AwsModelIDMap {
		adaptors[model] = AwsClaude
	}
}

func GetAdaptor(model string) utils.AwsAdapter {
	adaptorType := adaptors[model]
	switch adaptorType {
	case AwsClaude:
		return &claude.Adaptor{}
	default:
		return nil
	}
}
