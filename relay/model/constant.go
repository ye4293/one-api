package model

const (
	ContentTypeText     = "text"
	ContentTypeImageURL = "image_url"
)

type APIModel struct {
	Provider    string                 `json:"provider"`    // 提供商列表
	Name        string                 `json:"name"`        // API名称
	Tags        []string               `json:"tags"`        // 标签列表
	Description string                 `json:"description"` // 描述
	PriceType   string                 `json:"price_type"`  // 价格类型(如"按量计费")
	Prices      map[string]interface{} `json:"prices"`      // 价格列表

}
