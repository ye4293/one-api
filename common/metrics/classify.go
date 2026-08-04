package metrics

import "strings"

// 错误原因枚举。**这是一个封闭集合** —— 它是 Prometheus label 值，
// 每增加一个值就多一批时间序列，因此不允许把上游返回的字符串放进来。
//
// 为什么不直接用 relay/model.Error 的 Type 字段：
// relay/util/common.go:169 会用上游 JSON 里的 error.type 整体覆盖它，
// 取值完全由上游决定 → 基数无界，一旦当 label 用就会打爆 Prometheus。
// Error.Code 同理（它是 any 类型，序列化结果不可控）。
//
// 所以只能以 HTTP 状态码为主键自建映射。分类依据来自
// controller/relay.go 的 shouldRetry / shouldRetryBadRequest / shouldRetryForbidden。
const (
	ReasonNoChannel     = "no_channel"       // 503：无可用渠道
	ReasonRateLimited   = "rate_limited"     // 429：被上游限流
	ReasonUpstream5xx   = "upstream_5xx"     // 5xx：上游故障。**超时也落在这里**，见下方说明
	ReasonContentFilter = "content_filtered" // 403 + 违规关键词：内容审核拦截
	ReasonAuthFailed    = "auth_failed"      // 401/403：key 失效、无权限、余额不足
	ReasonParamInvalid  = "param_invalid"    // 422：参数校验失败
	ReasonBadRequest    = "bad_request"      // 400
	ReasonOther4xx      = "other_4xx"        // 其余 4xx
	ReasonUnknown       = "unknown"          // 状态码缺失或不可分类
)

// ClassifyReason 把 HTTP 状态码 + 错误消息映射成上面的封闭枚举。
//
// 注意：**没有独立的 timeout 分类**。RELAY_TIMEOUT / STREAMING_TIMEOUT 触发的超时
// 在 relay/util/common.go 的 RelayErrorHandler 里被包装成 502/504，到这里已经无法与
// 上游真实故障区分，统一归入 upstream_5xx。想识别超时只能看延迟直方图的最后一桶。
// 这是既有代码结构决定的，不为了指标去改错误映射链路。
func ClassifyReason(statusCode int, message string) string {
	switch {
	case statusCode == 0:
		return ReasonUnknown
	case statusCode == 503:
		return ReasonNoChannel
	case statusCode == 429:
		return ReasonRateLimited
	case statusCode >= 500:
		return ReasonUpstream5xx
	case statusCode == 403 && hasContentViolationKeyword(message):
		// 必须排在 auth_failed 之前：403 同时被用于"无权限"和"内容违规"两种语义
		return ReasonContentFilter
	case statusCode == 401 || statusCode == 403:
		return ReasonAuthFailed
	case statusCode == 422:
		return ReasonParamInvalid
	case statusCode == 400:
		return ReasonBadRequest
	case statusCode >= 400:
		return ReasonOther4xx
	}
	return ReasonUnknown
}

// contentViolationPatterns 与 controller/relay.go 的 isXAIContentViolation 保持一致。
//
// 这里是刻意的重复：isXAIContentViolation 参与**重试决策**（决定是否换渠道重试），
// 改动它会影响线上转发行为；本函数只用于给指标打标签，纯读。
// 把两者合并需要让 controller 依赖 common/metrics 或反之，收益不抵风险。
// **若那边的关键词有增删，这里要同步。**
var contentViolationPatterns = []string{
	"content violates usage guidelines",
	"violates usage guidelines",
	"safety_check_type",
}

func hasContentViolationKeyword(message string) bool {
	if message == "" {
		return false
	}
	lower := strings.ToLower(message)
	for _, pattern := range contentViolationPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// CodeClass 把状态码收敛成 2xx/3xx/4xx/5xx，用于低基数的 HTTP 维度统计。
func CodeClass(statusCode int) string {
	switch {
	case statusCode >= 500:
		return "5xx"
	case statusCode >= 400:
		return "4xx"
	case statusCode >= 300:
		return "3xx"
	case statusCode >= 200:
		return "2xx"
	}
	return "other"
}
