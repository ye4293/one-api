package model

const (
	ContentTypeText       = "text"
	ContentTypeImageURL   = "image_url"
	ContentTypeAudioURL   = "audio_url"   // 音频URL类型
	ContentTypeVideoURL   = "video_url"   // 视频URL类型
	ContentTypeInputAudio = "input_audio" // 输入音频类型（OpenAI格式）
	ContentTypeFileURL    = "file_url"    // 文档URL类型（PDF等）
)

type APIModel struct {
	Provider    string                 `json:"provider"`    // 提供商列表
	Name        string                 `json:"name"`        // API名称
	Tags        []string               `json:"tags"`        // 标签列表
	Description string                 `json:"description"` // 描述
	PriceType   string                 `json:"price_type"`  // 价格类型(如"按量计费")
	Prices      map[string]interface{} `json:"prices"`      // 价格列表

}
