package aws

import (
	"encoding/json"

	"github.com/songquanpeng/one-api/relay/channel/anthropic"
)

// Request is the request to AWS Claude
//
// https://docs.aws.amazon.com/bedrock/latest/userguide/model-parameters-anthropic-claude-messages.html
type Request struct {
	// AnthropicVersion should be "bedrock-2023-05-31"
	AnthropicVersion string              `json:"anthropic_version"`
	AnthropicBeta    json.RawMessage     `json:"anthropic_beta,omitempty"`
	Messages         []anthropic.Message `json:"messages"`
	System           any                 `json:"system,omitempty"`
	MaxTokens        int                 `json:"max_tokens,omitempty"`
	Temperature      *float64            `json:"temperature,omitempty"`
	TopP             float64             `json:"top_p,omitempty"`
	TopK             int                 `json:"top_k,omitempty"`
	StopSequences    []string            `json:"stop_sequences,omitempty"`
	Tools            []anthropic.Tool    `json:"tools,omitempty"`
	ToolChoice       any                 `json:"tool_choice,omitempty"`
	// Thinking 扩展思考配置（用于 Claude 3.5+ thinking 模式）
	Thinking any `json:"thinking,omitempty"`
}

// AwsModelCanCrossRegionMap 定义哪些模型支持跨区域调用
var AwsModelCanCrossRegionMap = map[string]map[string]bool{
	"anthropic.claude-3-sonnet-20240229-v1:0": {
		"us": true,
		"eu": true,
		"ap": true,
	},
	"anthropic.claude-3-opus-20240229-v1:0": {
		"us": true,
	},
	"anthropic.claude-3-haiku-20240307-v1:0": {
		"us": true,
		"eu": true,
		"ap": true,
	},
	"anthropic.claude-3-5-sonnet-20240620-v1:0": {
		"us": true,
		"eu": true,
		"ap": true,
	},
	"anthropic.claude-3-5-sonnet-20241022-v2:0": {
		"us": true,
		"ap": true,
	},
	"anthropic.claude-3-5-haiku-20241022-v1:0": {
		"us": true,
	},
	"anthropic.claude-3-7-sonnet-20250219-v1:0": {
		"us": true,
		"ap": true,
		"eu": true,
	},
	"anthropic.claude-sonnet-4-20250514-v1:0": {
		"us": true,
		"ap": true,
		"eu": true,
	},
	"anthropic.claude-opus-4-20250514-v1:0": {
		"us": true,
	},
	"anthropic.claude-opus-4-1-20250805-v1:0": {
		"us": true,
	},
	"anthropic.claude-sonnet-4-5-20250929-v1:0": {
		"us": true,
		"ap": true,
		"eu": true,
	},
	"anthropic.claude-opus-4-5-20251101-v1:0": {
		"us": true,
		"ap": true,
		"eu": true,
	},
	"anthropic.claude-haiku-4-5-20251001-v1:0": {
		"us": true,
		"ap": true,
		"eu": true,
	},
}

// AwsRegionCrossModelPrefixMap 区域前缀映射
var AwsRegionCrossModelPrefixMap = map[string]string{
	"us": "us",
	"eu": "eu",
	"ap": "apac",
}
