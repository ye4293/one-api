package openai

import "github.com/songquanpeng/one-api/relay/model"

// ResponseServiceTierContextKey 保存上游实际返回的档位，供消费日志使用；独立于 token 补算。
const ResponseServiceTierContextKey = "openai_response_service_tier"

func ResponseText2Usage(responseText string, modeName string, promptTokens int) *model.Usage {
	usage := &model.Usage{}
	usage.PromptTokens = promptTokens
	usage.CompletionTokens = CountTokenText(responseText, modeName)
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return usage
}
