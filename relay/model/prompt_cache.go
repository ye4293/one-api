package model

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type CacheWriteTokensDetails struct {
	TTL30mTokens  *int64 `json:"ttl_30m_tokens,omitempty"`
	TTL12hTokens  *int64 `json:"ttl_12h_tokens,omitempty"`
	InvalidReason string `json:"-"`
}

// 可空指针区分“上游明确报告 0”和“未报告”，不从请求 TTL 反推数量。
func (d *CacheWriteTokensDetails) UnmarshalJSON(data []byte) error {
	*d = CacheWriteTokensDetails{}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		d.InvalidReason = "invalid_cache_write_tokens_details: " + err.Error()
		return nil
	}
	var err error
	d.TTL30mTokens, err = parseOptionalCacheTokenCount(fields["ttl_30m_tokens"])
	if err != nil {
		d.InvalidReason = "invalid_ttl_30m_tokens: " + err.Error()
	}
	d.TTL12hTokens, err = parseOptionalCacheTokenCount(fields["ttl_12h_tokens"])
	if err != nil {
		d.InvalidReason += "; invalid_ttl_12h_tokens: " + err.Error()
	}
	return nil
}

// 新增的按 TTL 分桶使用固定 64 位计数；既有 Usage 主字段保留 int 兼容接口。
func parseOptionalCacheTokenCount(data json.RawMessage) (*int64, error) {
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, nil
	}
	var value int64
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("cache token count must be an integer within int64 range: %w", err)
	}
	return &value, nil
}

type PromptTokensDetails struct {
	CachedTokens            int                      `json:"cached_tokens,omitempty"`
	CacheWriteTokens        int                      `json:"cache_write_tokens,omitempty"`
	CacheWriteTokensDetails *CacheWriteTokensDetails `json:"cache_write_tokens_details,omitempty"`
	AudioTokens             int                      `json:"audio_tokens,omitempty"`
	TextTokens              int                      `json:"text_tokens,omitempty"`
	ImageTokens             int                      `json:"image_tokens,omitempty"`
	CacheWriteTokensPresent bool                     `json:"-"`
}

// 只额外记录总创建字段是否存在，保持旧 token 字段原有的 JSON 校验规则。
func (d *PromptTokensDetails) UnmarshalJSON(data []byte) error {
	*d = PromptTokensDetails{}
	type plain PromptTokensDetails
	wire := struct {
		*plain
		Written *int `json:"cache_write_tokens"`
	}{plain: (*plain)(d)}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Written != nil {
		d.CacheWriteTokens = *wire.Written
		d.CacheWriteTokensPresent = true
	}
	return nil
}
