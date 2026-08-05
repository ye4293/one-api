package controller

import (
	"fmt"
	"strings"

	relaymodel "github.com/songquanpeng/one-api/relay/model"
)

// ──────────────────────────────────────────
// 探针结论
// ──────────────────────────────────────────

// probeVerdict 是单次模型探测的结论。
//
// 设计原则：只有「上游明确说这个模型不存在」才是 not_found，其余一切失败
// （限流、鉴权、欠费、5xx、超时、内容审核）都是 inconclusive。把 inconclusive
// 误判成 not_found 的后果是批量误删模型，比"漏删"严重得多。
type probeVerdict string

const (
	// verdictAlive 上游确认该模型可用
	verdictAlive probeVerdict = "alive"
	// verdictNotFound 上游明确表示该模型不存在
	verdictNotFound probeVerdict = "not_found"
	// verdictInconclusive 本次探测无结论（默认归类）
	verdictInconclusive probeVerdict = "inconclusive"
	// verdictSkipped 该模型无法用 chat/completions 探测，探针无能力判断
	verdictSkipped probeVerdict = "skipped"
)

// 探测场景：决定同一个结论对应的处置动作
const (
	probeScenePendingAdd    = "pending_add"
	probeScenePendingRemove = "pending_remove"
)

// modelNotFoundCodes 是 error.code / error.type 中表示「模型不存在」的封闭枚举。
// 全部小写，匹配前先经 normalizeErrCode 归一化。
var modelNotFoundCodes = map[string]bool{
	"model_not_found":   true,
	"model_not_exist":   true,
	"model_not_support": true,
	"invalid_model":     true,
	"modelnotfound":     true,
}

// modelNotFoundKeywords 是错误消息中表示「模型不存在」的关键词白名单（小写匹配）。
//
// 这里刻意用白名单而非「400 就算不存在」，因为大量非模型问题也返回 400（内容审核、
// 参数错误、上下文超长）。两条容易误命中的边界已在单测中固化：
//   - "You do not have access to this model"     → 无权限，不是不存在
//   - "This model's maximum context length is N" → 上下文超长，模型存在
//
// 注意 OpenAI 的 "The model 'x' does not exist or you do not have access to it"
// 会命中 "does not exist" 判为 not_found —— 对单 key 渠道这是正确的（这个 key
// 服务不了该模型）；多 key 渠道由 classifyProbeError 降级为 inconclusive。
var modelNotFoundKeywords = []string{
	"model not found",
	"model_not_found",
	"does not exist",
	"no such model",
	"unknown model",
	"invalid model",
	"unsupported model",
	"model is not supported",
	"模型不存在",
	"不支持该模型",
	"未找到模型",
	"无此模型",
}

// normalizeErrCode 把 error.code（JSON 里可能是 string / number / null）归一化为
// 小写字符串。数字类 code（如 404）归一化后不会命中 modelNotFoundCodes，
// 等于自动被排除 —— 这是有意的，数字 code 不构成「模型不存在」的明确信号。
func normalizeErrCode(code any) string {
	if code == nil {
		return ""
	}
	if s, ok := code.(string); ok {
		return strings.ToLower(strings.TrimSpace(s))
	}
	return strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", code)))
}

// isModelNotFoundMessage 判断错误消息是否命中「模型不存在」关键词白名单
func isModelNotFoundMessage(msg string) bool {
	lower := strings.ToLower(strings.TrimSpace(msg))
	if lower == "" {
		return false
	}
	for _, kw := range modelNotFoundKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// classifyProbeError 对探测的失败路径分类，只返回 not_found 或 inconclusive。
// alive 的判定需要 usage 信息，由调用方负责，不在本函数职责内。
//
// apiErr 为 nil 表示没有解析出可用的上游错误体（空 body、HTML 错误页、非 JSON）。
// 这种情况一律 inconclusive：base_url 配错或上游反代挂掉时所有模型都会返回 404，
// 若把裸 404 判为 not_found 会一轮删光整个渠道。
//
// isMultiKey 为 true 时把 not_found 降级为 inconclusive：探针只用了一个 key，
// 而 OpenAI 把「模型不存在」和「本 key 无权限」合并进同一句错误消息，
// 直接删除会误伤其他 key 能服务的模型。
func classifyProbeError(statusCode int, apiErr *relaymodel.Error, netErr error, isMultiKey bool) probeVerdict {
	// 网络错误、超时：与模型是否存在无关
	if netErr != nil {
		return verdictInconclusive
	}
	// 没有可解析的上游错误体
	if apiErr == nil {
		return verdictInconclusive
	}

	notFound := false
	switch {
	// 信号 1：404 且带有非空错误消息（裸 404 不算，见上方注释）
	case statusCode == 404 && strings.TrimSpace(apiErr.Message) != "":
		notFound = true
	// 信号 2：error.code 或 error.type 命中封闭枚举
	case modelNotFoundCodes[normalizeErrCode(apiErr.Code)],
		modelNotFoundCodes[normalizeErrCode(apiErr.Type)]:
		notFound = true
	// 信号 3：错误消息命中关键词白名单
	case isModelNotFoundMessage(apiErr.Message):
		notFound = true
	}

	if !notFound {
		return verdictInconclusive
	}
	if isMultiKey {
		return verdictInconclusive
	}
	return verdictNotFound
}

// filterByProbeVerdicts 按探测结论把候选模型分成「批准执行」与「暂缓」两组。
//
// 处置规则（scene 决定同一结论的动作）：
//
//	              alive    not_found    inconclusive    skipped
//	pending_add    批准       暂缓           暂缓         批准
//	pending_remove 暂缓       批准           暂缓         批准
//
// 两条原则：
//   - inconclusive 一律暂缓（保持现状最安全；下轮 diff 从零重算会自然重试）
//   - skipped 一律批准（探针对这类模型无能力，维持「信任上游」的现有行为，
//     上线不引入回归）
//
// verdicts 里缺失的模型按 inconclusive 处理 —— 探测被预算或 429 中止时会出现，
// 必须保守。
func filterByProbeVerdicts(models []string, verdicts map[string]probeVerdict, scene string) (approved, held []string) {
	approved = make([]string, 0, len(models))
	held = make([]string, 0)
	for _, m := range models {
		v, ok := verdicts[m]
		if !ok {
			v = verdictInconclusive
		}
		if probeVerdictApproves(v, scene) {
			approved = append(approved, m)
		} else {
			held = append(held, m)
		}
	}
	return approved, held
}

// probeVerdictApproves 是上表的单格判定
func probeVerdictApproves(v probeVerdict, scene string) bool {
	switch v {
	case verdictSkipped:
		return true // 探针无能力 → 信任上游
	case verdictAlive:
		return scene == probeScenePendingAdd
	case verdictNotFound:
		return scene == probeScenePendingRemove
	default: // verdictInconclusive 及任何未知值
		return false
	}
}
