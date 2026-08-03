package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/metrics"
)

// RelayMetrics 记录**用户请求级**的 LLM 指标（每个 API 调用恰好一次，重试不计）。
//
// 为什么用中间件而不是在 relay 层埋点：
//
//  1. gin 中间件天然是"每用户请求一次"的位置。relay 层有 12 个独立重试循环，
//     在那里数请求只会数成"渠道调用次数"（那是 oneapi_channel_* 的语义，见 common/metrics/channel.go）。
//  2. 能覆盖 video / midjourney / sora / flux 等链路 —— 它们根本不经过
//     model.RecordConsumeLogWithOtherAndRequestID，在那里埋点会漏掉。
//
// 挂载点是根引擎（router/relay-router.go），但只对**真正走了 relay 的请求**计数，
// 判据见 resolveModel：非 relay 路由（web / /api/*）永远不会设置 model / original_model 键。
// 这样不必维护一份路径白名单 —— relay 路由有 19 个前缀，白名单会随新增路由而失效。
func RelayMetrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !metrics.LLMEnabled() {
			c.Next()
			return
		}

		c.Next()

		model := resolveModel(c)
		_, relayFailed := c.Get(metrics.CtxRelayFailedKey)
		if model == "" && !relayFailed {
			// 不是 relay 请求（没选到过渠道、也没被 relay 层标记失败），不计入 LLM 指标
			return
		}

		metrics.IncRequest(model, c.GetString("group"), resolveOutcome(c, relayFailed))
	}
}

// resolveModel 取用于打标签的模型名。
//
// 优先 original_model（由 SetupContextForSelectedChannel 设置，是用户请求的原始模型名），
// 回落到 model（由 Distribute 在选中渠道后设置）。两者都受 abilities 表约束 ——
// 能选到渠道就说明该模型名命中了某个启用渠道的 abilities 记录，不可能是用户随意填的字符串。
//
// 与 DB 侧口径一致：logs.model_name 列存的也是原始请求模型名
// （model/log.go 会在 other 含 origin_model_name 时回退成 origin），
// 所以两边对账时模型名能对上。
//
// 最后才回落到 metrics.CtxModelKey：那是"无可用渠道"503 分支专用的兜底，
// 值未经 abilities 校验，但下游 metrics.IncRequest 会过基数守卫。
func resolveModel(c *gin.Context) string {
	if m := c.GetString("original_model"); m != "" {
		return m
	}
	if m := c.GetString("model"); m != "" {
		return m
	}
	return c.GetString(metrics.CtxModelKey)
}

// resolveOutcome 判定用户实际感受到的结果。
//
// relay 层的标记优先于状态码：流式请求中途失败时状态码仍是 200（见 metrics.CtxRelayFailedKey
// 的注释），只看状态码会把失败记成成功。
func resolveOutcome(c *gin.Context, relayFailed bool) string {
	if relayFailed {
		return metrics.OutcomeError
	}
	if c.Writer.Status() >= 400 {
		// 兜底：非流式失败、鉴权失败、以及 Distribute 的 503 无可用渠道
		return metrics.OutcomeError
	}
	return metrics.OutcomeSuccess
}
