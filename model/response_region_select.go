package model

import (
	"encoding/json"
	"strings"
)

// GetAzureChannelRegion 解析渠道 Config JSON 里的 region 字段。
// CRM 创建 Azure 渠道时会写入 region；解析失败返回空串。
// config.region 对 Bedrock 渠道同样适用，此处仅按字段名解析，不区分渠道类型。
func GetAzureChannelRegion(ch *Channel) string {
	if ch == nil || ch.Config == "" {
		return ""
	}
	var cfg ChannelConfig
	if err := json.Unmarshal([]byte(ch.Config), &cfg); err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.Region)
}
