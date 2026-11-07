package util

import (
	"fmt"
	"math"

	"github.com/songquanpeng/one-api/relay/model"
)

// OpenAICacheWriteUsage 只拆分缓存创建量。Short/Long 只保存完整且校验通过的上游分项。
// 回退旧计费时仅保留 Total，不把未分类总量记为 30m；有效的明确零值保留到消费日志。
type OpenAICacheWriteUsage struct {
	Total int64
	Short int64
	Long  *int64
}

func (u OpenAICacheWriteUsage) HasBreakdown() bool {
	return u.Long != nil
}

func (u OpenAICacheWriteUsage) LongTokens() int64 {
	if u.Long != nil {
		return *u.Long
	}
	return 0
}

// SplitOpenAICacheWriteTokens 只使用响应 usage，不从请求 TTL 推断创建时长。
// 缺失明细沿用旧计费；无效的新明细返回错误和原始总创建量，调用方记录完整上下文。
func SplitOpenAICacheWriteTokens(input int, details model.PromptTokensDetails) (OpenAICacheWriteUsage, error) {
	legacy := OpenAICacheWriteUsage{Total: int64(details.CacheWriteTokens)}
	d := details.CacheWriteTokensDetails
	if d == nil {
		return legacy, nil
	}
	if d.InvalidReason != "" {
		return legacy, fmt.Errorf("invalid cache creation breakdown: %s", d.InvalidReason)
	}
	if d.TTL30mTokens == nil || d.TTL12hTokens == nil {
		return legacy, fmt.Errorf("incomplete cache creation breakdown: both ttl_30m_tokens and ttl_12h_tokens are required")
	}
	short, long := *d.TTL30mTokens, *d.TTL12hTokens
	if short < 0 || long < 0 || short > math.MaxInt64-long {
		return legacy, fmt.Errorf("invalid cache creation counts: ttl_30m=%d ttl_12h=%d", short, long)
	}
	total := legacy.Total
	if !details.CacheWriteTokensPresent && total == 0 {
		// 两档均明确报告时可补全总量；不会用总量反推任何分项。
		total = short + long
	}
	if total < 0 || short+long != total || details.CachedTokens < 0 || details.CachedTokens > input || total > int64(input-details.CachedTokens) {
		return legacy, fmt.Errorf("inconsistent cache creation usage: input=%d read=%d total=%d ttl_30m=%d ttl_12h=%d", input, details.CachedTokens, total, short, long)
	}
	// 拷贝值，异步日志不持有响应对象中的可变指针。
	return OpenAICacheWriteUsage{Total: total, Short: short, Long: &long}, nil
}
