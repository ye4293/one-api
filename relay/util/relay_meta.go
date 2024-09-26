package util

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/constant"
)

// type RelayMeta struct {
// 	Mode            int
// 	ChannelType     int
// 	ChannelId       int
// 	TokenId         int
// 	TokenName       string
// 	UserId          int
// 	Group           string
// 	ModelMapping    map[string]string
// 	BaseURL         string
// 	APIVersion      string
// 	APIKey          string
// 	APIType         int
// 	Config          map[string]string
// 	IsStream        bool
// 	OriginModelName string
// 	ActualModelName string
// 	RequestURLPath  string
// 	PromptTokens    int // only for DoResponse
// }

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
}

func GetRelayMeta(c *gin.Context) *RelayMeta {
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
		APIKey:         strings.TrimPrefix(c.Request.Header.Get("Authorization"), "Bearer "),
		RequestURLPath: c.Request.URL.String(),
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
