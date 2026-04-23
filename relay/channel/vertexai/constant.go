package vertexai

var ModelList = []string{
	// Gemini 1.5 系列
	"gemini-1.5-flash", "gemini-1.5-pro", "gemini-1.5-pro-002", "gemini-1.5-pro-001",
	// Gemini 2.0 系列
	"gemini-2.0-flash",
	// Gemini 2.5 系列
	"gemini-2.5-flash", "gemini-2.5-flash-lite", "gemini-2.5-pro", "gemini-2.5-flash-image-preview", "gemini-2.5-flash-image",
	"gemini-2.5-flash-thinking", "gemini-2.5-pro-thinking", "gemini-2.5-flash-lite-thinking",
	"gemini-2.5-flash-nothinking", "gemini-2.5-flash-lite-nothinking", "gemini-2.5-pro-nothinking",
	// Gemini 3 系列
	"gemini-3-pro-preview", "gemini-3-pro-preview-thinking", "gemini-3-pro-image-preview",
	// Veo 3.0 系列
	"veo-3.0-generate-001", "veo-3.0-fast-generate-001", "veo-3.0-generate-preview", "veo-3.0-fast-generate-preview",
	// Veo 3.1 系列
	"veo-3.1-generate-preview", "veo-3.1-fast-generate-preview",
	"gemini-2.5-flash-preview-tts", "gemini-2.5-pro-preview-tts", "gemini-2.5-flash-tts", "gemini-2.5-pro-tts",
	"gemini-3-flash-preview-thinking", "gemini-3-flash-preview", "gemini-3-flash-preview-nothinking",
	"gemini-3.1-pro-preview-thinking", "gemini-3.1-pro-preview", "gemini-3.1-pro-preview-nothinking",
	"gemini-3.1-flash-image-preview", "gemini-3.1-flash-lite-preview", "gemini-3.1-flash-lite-preview-thinking", "gemini-3.1-flash-lite-preview-nothinking",
	// Claude on Vertex（Anthropic publisher）
	"claude-3-5-sonnet-20240620", "claude-3-5-sonnet-20241022", "claude-3-7-sonnet-20250219",
	"claude-sonnet-4-20250514", "claude-opus-4-20250514", "claude-opus-4-1-20250805",
	"claude-sonnet-4-5-20250929", "claude-haiku-4-5-20251001", "claude-opus-4-5-20251101",
	"claude-opus-4-6", "claude-opus-4-7",
	// Claude thinking variants（与 anthropic.ModelList 对齐；适配层后缀，URL 与 body 都会剥离）
	"claude-opus-4-6-thinking", "claude-opus-4-7-thinking",
	"claude-sonnet-4-5-20250929-thinking", "claude-haiku-4-5-20251001-thinking",
	"claude-opus-4-5-20251101-thinking", "claude-opus-4-1-20250805-thinking",
	"claude-sonnet-4-20250514-thinking", "claude-opus-4-20250514-thinking",
	"claude-3-7-sonnet-20250219-thinking",
}

// anthropicVersion 是 Vertex 的 Anthropic publisher endpoint 要求注入到请求体顶层的版本号。
// 参考 https://cloud.google.com/vertex-ai/generative-ai/docs/partner-models/use-claude
const anthropicVersion = "vertex-2023-10-16"

// claudeModelMap 把 Anthropic 官方模型 ID（带 "-日期" 后缀）映射到
// Vertex Anthropic publisher 要求的 URL 格式（带 "@日期" 后缀）。
// 仅用于拼 URL，请求体里仍保留官方模型名。
var claudeModelMap = map[string]string{
	"claude-3-sonnet-20240229":   "claude-3-sonnet@20240229",
	"claude-3-opus-20240229":     "claude-3-opus@20240229",
	"claude-3-haiku-20240307":    "claude-3-haiku@20240307",
	"claude-3-5-sonnet-20240620": "claude-3-5-sonnet@20240620",
	"claude-3-5-sonnet-20241022": "claude-3-5-sonnet-v2@20241022",
	"claude-3-7-sonnet-20250219": "claude-3-7-sonnet@20250219",
	"claude-sonnet-4-20250514":   "claude-sonnet-4@20250514",
	"claude-opus-4-20250514":     "claude-opus-4@20250514",
	"claude-opus-4-1-20250805":   "claude-opus-4-1@20250805",
	"claude-sonnet-4-5-20250929": "claude-sonnet-4-5@20250929",
	"claude-haiku-4-5-20251001":  "claude-haiku-4-5@20251001",
	"claude-opus-4-5-20251101":   "claude-opus-4-5@20251101",
	"claude-opus-4-6":            "claude-opus-4-6",
	"claude-opus-4-7":            "claude-opus-4-7",
}
