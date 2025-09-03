package keling

import (
	"fmt"
	"strings"

	"github.com/songquanpeng/one-api/model"
)

// KelingCredentials 可灵凭证结构
type KelingCredentials struct {
	AK string `json:"ak"`
	SK string `json:"sk"`
}

// ParseKelingCredentials 从Key字段解析可灵AK|SK格式的凭证
func ParseKelingCredentials(channel *model.Channel, keyIndex int) (*KelingCredentials, error) {
	if channel == nil {
		return nil, fmt.Errorf("channel is nil")
	}

	// 方案1：如果是多密钥模式，从Keys列表中获取
	if channel.MultiKeyInfo.IsMultiKey {
		keys := channel.ParseKeys()
		if keyIndex < 0 || keyIndex >= len(keys) {
			return nil, fmt.Errorf("invalid key index %d, total keys: %d", keyIndex, len(keys))
		}

		keyData := strings.TrimSpace(keys[keyIndex])
		if keyData == "" {
			return nil, fmt.Errorf("empty key data at index %d", keyIndex)
		}

		return parseAKSKFormat(keyData)
	}

	// 方案2：单密钥模式，直接使用Key字段
	if channel.Key != "" {
		return parseAKSKFormat(channel.Key)
	}

	return nil, fmt.Errorf("no valid Keling credentials found in Key field")
}

// parseAKSKFormat 解析AK|SK格式的字符串
func parseAKSKFormat(keyData string) (*KelingCredentials, error) {
	keyData = strings.TrimSpace(keyData)
	if keyData == "" {
		return nil, fmt.Errorf("empty key data")
	}

	// 支持AK|SK格式
	parts := strings.Split(keyData, "|")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid AK|SK format, expected 'ak|sk', got: %s", keyData)
	}

	ak := strings.TrimSpace(parts[0])
	sk := strings.TrimSpace(parts[1])

	if ak == "" || sk == "" {
		return nil, fmt.Errorf("AK or SK is empty in: %s", keyData)
	}

	fmt.Printf("[Keling] 成功解析凭证 - AK: %s***\n", ak[:min(4, len(ak))])

	return &KelingCredentials{
		AK: ak,
		SK: sk,
	}, nil
}

// GetKelingCredentialsFromConfig 从ChannelConfig或Key字段获取可灵凭证（向后兼容）
func GetKelingCredentialsFromConfig(cfg model.ChannelConfig, channel *model.Channel, keyIndex int) (*KelingCredentials, error) {
	// 方案1：优先尝试从Key字段解析（新方案）
	if channel != nil {
		credentials, err := ParseKelingCredentials(channel, keyIndex)
		if err == nil {
			fmt.Printf("[Keling] 从Key字段获取凭证 - 多密钥模式: %v\n", channel.MultiKeyInfo.IsMultiKey)
			return credentials, nil
		}
		fmt.Printf("[Keling] Key字段解析失败: %v，尝试Config回退\n", err)
	}

	// 方案2：回退到Config.AK/SK（向后兼容）
	if cfg.AK != "" && cfg.SK != "" {
		fmt.Printf("[Keling] 从Config获取凭证 - AK: %s***（建议迁移到Key字段）\n", cfg.AK[:min(4, len(cfg.AK))])
		return &KelingCredentials{
			AK: cfg.AK,
			SK: cfg.SK,
		}, nil
	}

	return nil, fmt.Errorf("无法从Key字段或Config获取有效的可灵凭证")
}

// ValidateKelingCredentials 验证可灵凭证格式
func ValidateKelingCredentials(credentials *KelingCredentials) error {
	if credentials == nil {
		return fmt.Errorf("凭证为空")
	}

	if credentials.AK == "" {
		return fmt.Errorf("AccessKey不能为空")
	}

	if credentials.SK == "" {
		return fmt.Errorf("SecretKey不能为空")
	}

	// 基本格式检查
	if len(credentials.AK) < 10 {
		return fmt.Errorf("AccessKey长度过短")
	}

	if len(credentials.SK) < 10 {
		return fmt.Errorf("SecretKey长度过短")
	}

	return nil
}

// min 辅助函数
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
