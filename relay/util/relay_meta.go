package util

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/constant"
)

type RelayMeta struct {
	Mode         int
	ChannelType  int
	ChannelId    int
	TokenId      int
	TokenName    string
	UserId       int
	Group        string
	ModelMapping map[string]string
	// BaseURL is the proxy url set in the channel config
	BaseURL  string
	APIKey   string
	APIType  int
	Config   model.ChannelConfig
	IsStream bool
	// OriginModelName is the model name from the raw user request
	OriginModelName string
	// ActualModelName is the model name after mapping
	ActualModelName string
	RequestURLPath  string
	PromptTokens    int // only for DoResponse
	// 用于计算首字延迟
	FirstWordLatency  float64
	StartTime         time.Time // 请求开始时间
	FirstResponseTime time.Time // 首字响应时间
	isFirstResponse   bool      // 标记是否是第一个响应
	// 多Key相关信息
	ActualAPIKey string
	KeyIndex     *int // 使用指针以支持nil值
	IsMultiKey   bool
	Keys         []string // 存储所有解析的密钥
	DisablePing  bool
	// 流式响应是否包含 usage 信息
	ShouldIncludeUsage bool
}

// SetFirstResponseTime 设置首字响应时间（只设置一次）
func (m *RelayMeta) SetFirstResponseTime() {
	if m.isFirstResponse {
		m.FirstResponseTime = time.Now()
		m.isFirstResponse = false
		// 计算首字延迟（秒）
		if !m.StartTime.IsZero() {
			m.FirstWordLatency = m.FirstResponseTime.Sub(m.StartTime).Seconds()
		}
	}
}

// GetFirstWordLatency 获取首字延迟（秒）
func (m *RelayMeta) GetFirstWordLatency() float64 {
	return m.FirstWordLatency
}

func GetRelayMeta(c *gin.Context) *RelayMeta {
	// channelId := c.GetInt("channel_id")
	// channel, _ := model.GetChannelById(channelId, false)
	meta := RelayMeta{
		Mode:         constant.Path2RelayMode(c.Request.URL.Path),
		ChannelType:  c.GetInt("channel"),
		ChannelId:    c.GetInt("channel_id"),
		TokenId:      c.GetInt("token_id"),
		TokenName:    c.GetString("token_name"),
		UserId:       c.GetInt("id"),
		Group:        c.GetString("group"),
		ModelMapping: c.GetStringMapString("model_mapping"),
		BaseURL:      c.GetString("base_url"),
		// APIVersion:     c.GetString(common.ConfigKeyAPIVersion),
		// APIKey:          channel.Key,
		APIKey:          strings.TrimPrefix(c.Request.Header.Get("Authorization"), "Bearer "),
		RequestURLPath:  c.Request.URL.String(),
		OriginModelName: c.GetString("original_model"),
		ActualAPIKey:    c.GetString("actual_key"),
		IsMultiKey:      c.GetBool("is_multi_key"),
		StartTime:       time.Now(), // 记录请求开始时间
		isFirstResponse: true,       // 初始化为 true，等待第一个响应
	}

	// 处理多密钥索引
	if keyIndexValue, exists := c.Get("key_index"); exists {
		if keyIndex, ok := keyIndexValue.(int); ok {
			meta.KeyIndex = &keyIndex
		}
	}

	// 处理多密钥列表（如果是多密钥渠道）
	if meta.IsMultiKey {
		if channelId := c.GetInt("channel_id"); channelId > 0 {
			if channel, err := model.GetChannelById(channelId, true); err == nil {
				meta.Keys = channel.ParseKeys()
			}
		}
	}
	cfg, ok := c.Get("Config")
	if ok {
		meta.Config = cfg.(model.ChannelConfig)
	}
	if meta.BaseURL == "" {
		meta.BaseURL = common.ChannelBaseURLs[meta.ChannelType]
	}
	if meta.Mode == constant.RelayModeGeminiGenerateContent || meta.Mode == constant.RelayModeGeminiStreamGenerateContent {
		// 应用模型映射：如果有映射规则，使用映射后的模型名称
		meta.ActualModelName = meta.OriginModelName
		if meta.ModelMapping != nil && len(meta.ModelMapping) > 0 {
			if mappedModel, ok := meta.ModelMapping[meta.OriginModelName]; ok && mappedModel != "" {
				meta.ActualModelName = mappedModel
			}
		}
		if meta.Mode == constant.RelayModeGeminiStreamGenerateContent {
			meta.IsStream = true
		}
	}
	meta.APIType = constant.ChannelType2APIType(meta.ChannelType)
	return &meta
}
