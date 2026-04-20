package ali

import "github.com/songquanpeng/one-api/relay/model"

var ModelList = []string{
	// 旗舰
	"qwen3-max", "qwen3-max-preview",
	"qwen-max", "qwen-max-latest", "qwen-max-longcontext",
	// 通用
	"qwen-plus", "qwen-plus-latest",
	"qwen-flash",
	"qwen-turbo", "qwen-turbo-latest",
	// 推理 / 代码
	"qwq-plus", "qwq-32b",
	"qwen3-coder-plus",
	// 视觉
	"qwen-vl-plus", "qwen-vl-max",
	// 嵌入
	"text-embedding-v1", "text-embedding-v2", "text-embedding-v3",
}

var ModelDetails = []model.APIModel{}
