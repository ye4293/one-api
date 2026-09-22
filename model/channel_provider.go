package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/songquanpeng/one-api/common"
)

// NormalizeProvider 保留名称的大小写及内部空格，空值表示跟随渠道类型。
func NormalizeProvider(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 128 {
		return "", fmt.Errorf("Provider 必须为不超过 128 个字符的文字")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("Provider 不能包含控制字符")
		}
	}
	return value, nil
}

// MergeChannelConfig 按字段合并原始 JSON，保留未知字段；空字符串清空整个配置。
func MergeChannelConfig(existing, incoming string) (string, error) {
	if incoming == "" {
		return "", nil
	}
	parse := func(raw string) (map[string]json.RawMessage, error) {
		obj := map[string]json.RawMessage{}
		if raw == "" {
			return obj, nil
		}
		if err := json.Unmarshal([]byte(raw), &obj); err != nil || obj == nil {
			return nil, fmt.Errorf("渠道 config 必须是 JSON 对象")
		}
		return obj, nil
	}
	old, err := parse(existing)
	if err != nil {
		return "", err
	}
	patch, err := parse(incoming)
	if err != nil {
		return "", err
	}
	for key, value := range patch {
		old[key] = value
	}
	if raw, exists := old["provider"]; exists {
		var provider string
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &provider) != nil {
			return "", fmt.Errorf("Provider 必须是字符串")
		}
		provider, err = NormalizeProvider(provider)
		if err != nil {
			return "", err
		}
		old["provider"], _ = json.Marshal(provider)
	}
	result, err := json.Marshal(old)
	return string(result), err
}

func (channel *Channel) EffectiveProvider() (string, error) {
	// 单独解析 Provider，避免不相关配置字段影响默认值，同时拒绝损坏配置。
	normalized, err := MergeChannelConfig("", channel.Config)
	if err != nil {
		return "", err
	}
	var cfg struct {
		Provider string `json:"provider"`
	}
	if normalized != "" {
		_ = json.Unmarshal([]byte(normalized), &cfg)
	}
	if cfg.Provider != "" {
		return cfg.Provider, nil
	}
	return common.ChannelTypeDefaultProvider(channel.Type), nil
}
