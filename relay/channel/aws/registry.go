package aws

import (
	"strings"

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
		// 支持 AWS 原生模型 ID 格式，如 anthropic.xxx, us.anthropic.xxx, global.anthropic.xxx 等
		if strings.Contains(model, "anthropic.") {
			return &claude.Adaptor{}
		}
		// 支持 ARN 格式（通过模型重定向配置的 inference profile）
		if strings.HasPrefix(model, "arn:") {
			return &claude.Adaptor{}
		}
		return nil
	}
}
