package vertexai

type VertexAIVeo3Request struct {
	Instances  []VertexAIVeo3Instance `json:"instances"`
	Parameters VertexAIVeo3Parameters `json:"parameters"`
}

type VertexAIVeo3Instance struct {
	Prompt string             `json:"prompt"`
	Image  *VertexAIVeo3Image `json:"image,omitempty"` // 可选，图片转视频功能需要此权限
}

type VertexAIVeo3Image struct {
	BytesBase64Encoded string `json:"bytesBase64Encoded,omitempty"` // Base64编码的图片字节字符串
	GcsUri             string `json:"gcsUri,omitempty"`             // Google Cloud Storage URI
	MimeType           string `json:"mimeType,omitempty"`           // 图片MIME类型
}

type VertexAIVeo3Parameters struct {
	AspectRatio      string `json:"aspectRatio,omitempty"`      // 可选，宽高比："16:9"(默认)或"9:16"
	NegativePrompt   string `json:"negativePrompt,omitempty"`   // 可选，描述不想生成的内容
	PersonGeneration string `json:"personGeneration,omitempty"` // 可选，人物生成控制："allow_adult"(默认)或"dont_allow"
	SampleCount      int    `json:"sampleCount,omitempty"`      // 可选，输出数量，接受1-4的值
	Seed             uint32 `json:"seed,omitempty"`             // 可选，数字种子，范围0-4,294,967,295
	StorageUri       string `json:"storageUri,omitempty"`       // 可选，Cloud Storage存储桶URI
	DurationSeconds  int    `json:"durationSeconds,omitempty"`  // 必需，视频长度，接受5-8的整数值，默认为8
	EnhancePrompt    *bool  `json:"enhancePrompt,omitempty"`    // 可选，使用Gemini优化问题，默认为true
}
