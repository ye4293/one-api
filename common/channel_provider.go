package common

import "fmt"

// ChannelTypeDefaultProvider 按渠道类型名称提供稳定的默认值，不使用模型供应商推断。
func ChannelTypeDefaultProvider(channelType int) string {
	if name, ok := channelProviderNames[channelType]; ok {
		return name
	}
	return fmt.Sprintf("Channel Type %d", channelType)
}

var channelProviderNames = map[int]string{
	1:  "OpenAI",
	14: "Anthropic Claude",
	3:  "Azure OpenAI",
	11: "Google PaLM2",
	24: "Google Gemini",
	28: "Mistral AI",
	31: "零一万物",
	32: "midjourney-Plus",
	33: "AWS Claude",
	34: "Coze",
	35: "Cohere",
	36: "together",
	37: "Deepseek",
	38: "Stability",
	39: "Novita",
	40: "豆包",
	41: "可灵",
	42: "Runway",
	43: "Recraft",
	44: "Luma",
	45: "Pixverse",
	46: "Flux",
	47: "XAI",
	48: "Vertex AI",
	30: "Ollama",
	29: "Groq",
	15: "百度文心千帆",
	17: "阿里通义千问",
	18: "讯飞星火认知",
	16: "智谱 ChatGLM",
	19: "360 智脑",
	25: "Moonshot AI",
	23: "腾讯混元",
	26: "百川大模型",
	27: "MiniMax",
	8:  "自定义渠道",
	22: "知识库：FastGPT",
	21: "知识库：AI Proxy",
	20: "代理：OpenRouter",
	2:  "代理：API2D",
	5:  "代理：OpenAI-SB",
	7:  "代理：OhMyGPT",
	10: "代理：AI Proxy",
	4:  "代理：CloseAI",
	6:  "代理：OpenAI Max",
	9:  "代理：AI.LS",
	12: "代理：API2GPT",
	13: "代理：AIGC2D",
}
