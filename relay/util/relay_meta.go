package util

import (
	"strconv"
	"strings"

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
	ActualModelName         string
	RequestURLPath          string
	PromptTokens            int // only for DoResponse
	ChannelRatio            float64
	UserChannelTypeRatio    float64
	UserChannelTypeRatioMap string
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
		APIKey:                  strings.TrimPrefix(c.Request.Header.Get("Authorization"), "Bearer "),
		RequestURLPath:          c.Request.URL.String(),
		OriginModelName:         c.GetString("original_model"),
		ChannelRatio:            c.GetFloat64("channel_ratio"),
		UserChannelTypeRatioMap: c.GetString("user_channel_type_ratio_map"),
		UserChannelTypeRatio:    GetChannelTypeRatio(c.GetString("user_channel_type_ratio_map"), c.GetInt("channel")),
	}
	cfg, ok := c.Get("Config")
	if ok {
		meta.Config = cfg.(model.ChannelConfig)
	}
	if meta.BaseURL == "" {
		meta.BaseURL = common.ChannelBaseURLs[meta.ChannelType]
	}
	meta.APIType = constant.ChannelType2APIType(meta.ChannelType)
	return &meta
}

// GetChannelTypeRatio 根据 ChannelType 从 UserChannelTypeRatioMap 中获取对应的倍率
// ratioMapStr 格式: "{41:0.2,42:0.6}"
// 如果找不到对应的 ChannelType，返回默认值 1.0
func GetChannelTypeRatio(ratioMapStr string, channelType int) float64 {
	if ratioMapStr == "" {
		return 1.0
	}

	// 移除大括号
	ratioMapStr = strings.Trim(ratioMapStr, "{}")

	// 按逗号分割键值对
	pairs := strings.Split(ratioMapStr, ",")

	for _, pair := range pairs {
		// 按冒号分割键和值
		kv := strings.Split(strings.TrimSpace(pair), ":")
		if len(kv) != 2 {
			continue
		}

		// 解析 ChannelType
		key, err := strconv.Atoi(strings.TrimSpace(kv[0]))
		if err != nil {
			continue
		}

		// 如果找到匹配的 ChannelType
		if key == channelType {
			// 解析倍率值
			ratio, err := strconv.ParseFloat(strings.TrimSpace(kv[1]), 64)
			if err != nil {
				return 1.0
			}
			return ratio
		}
	}

	// 如果没有找到对应的 ChannelType，返回默认值
	return 1.0
}
