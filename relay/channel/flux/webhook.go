package flux

import (
	"net/url"
	"os"
	"strings"

	"github.com/songquanpeng/one-api/common/config"
)

// defaultWebhookURL 图片和视频共用本站回调地址及查询参数鉴权。
func defaultWebhookURL() string {
	if config.ServerAddress == "" {
		return ""
	}
	webhookURL := strings.TrimRight(config.ServerAddress, "/") + "/flux/internal/callback"
	if secret := os.Getenv("FLUX_WEBHOOK_SECRET"); secret != "" {
		webhookURL += "?key=" + url.QueryEscape(secret)
	}
	return webhookURL
}

// defaultReplicateWebhookURL 复用 Replicate 独立回调入口及账户级签名校验。
func defaultReplicateWebhookURL() string {
	if config.ServerAddress == "" {
		return ""
	}
	return strings.TrimRight(config.ServerAddress, "/") + "/flux/internal/replicate/callback"
}
