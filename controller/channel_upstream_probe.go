package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/middleware"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/constant"
	"github.com/songquanpeng/one-api/relay/helper"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
	"golang.org/x/sync/errgroup"
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
	// verdictRateLimited 上游对该模型限流（429）。
	//
	// 语义上这是「模型可用」的证据：上游能对它做限流，说明它认识这个模型
	// 并愿意服务，只是速率超了。因此在 pendingRemove 场景下绝不能删。
	//
	// 但 pendingAdd 场景仍然不加 —— 很多网关的限流检查发生在模型路由之前，
	// 对不存在的模型也会返回 429，直接当 alive 会把不存在的模型加进列表。
	// 不加的代价只是延迟一轮（下轮 diff 会重试）。
	verdictRateLimited probeVerdict = "rate_limited"
	// verdictUnavailable 该模型当前无可用后端（503）。
	//
	// 是模型级信号而非渠道级：渠道本身正常，只是这个模型此刻服务不了，
	// 因此不触发渠道中止，继续探其他模型。
	//
	// pendingRemove 场景准予删除 —— 能进 pendingRemove 说明上游 /v1/models
	// 已不返回它，503 构成第二个独立证据。留着只会让请求路由到它然后失败。
	verdictUnavailable probeVerdict = "unavailable"
	// verdictInconclusive 本次探测无结论（默认归类）
	verdictInconclusive probeVerdict = "inconclusive"
	// verdictSkipped 该模型无法用 chat/completions 探测，探针无能力判断
	verdictSkipped probeVerdict = "skipped"
)

// 探测场景：决定同一个结论对应的处置动作
const (
	probeScenePendingAdd    = "pending_add"
	probeScenePendingRemove = "pending_remove"
	probeSceneHealth        = "health"
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
// bodyParsed 表示上游响应体是否成功解析出了**上游自己给的**错误结构。这是
// not_found 的硬前置条件，原因是 util.RelayErrorHandler 在解析失败时会
// **编造兜底文案**（relay/util/common.go:182-202），其中 404 的文案是
// "资源未找到 (404): 请求的端点或模型不存在" —— 含「模型不存在」四字，正好命中
// 下面的关键词白名单。若不以 bodyParsed 为前提，base_url 配错或上游反代挂掉时
// 所有模型都会返回 404 + 该兜底文案，被判为 not_found 后一轮删光整个渠道。
// 因此探针必须自己解析上游 body（见 parseProbeUpstreamError），并如实报告是否
// 解析成功；拿不到上游原话时一律 inconclusive。
//
// isMultiKey 为 true 时把 not_found 降级为 inconclusive：探针只用了一个 key，
// 而 OpenAI 把「模型不存在」和「本 key 无权限」合并进同一句错误消息，
// 直接删除会误伤其他 key 能服务的模型。
func classifyProbeError(statusCode int, apiErr *relaymodel.Error, bodyParsed bool, netErr error, isMultiKey bool) probeVerdict {
	// 网络错误、超时：与模型是否存在无关
	if netErr != nil {
		return verdictInconclusive
	}

	// 429：上游能对这个模型做限流，说明它认识该模型并愿意服务 —— 是「模型可用」
	// 的证据，而非渠道故障。独立成 verdict 而不并入 inconclusive，是为了让日志里
	// 能区分「被限流」与「真的没结论」，两者的运维含义完全不同。
	if statusCode == http.StatusTooManyRequests {
		return verdictRateLimited
	}
	// 503：该模型当前无可用后端。是模型级信号，渠道本身正常。
	if statusCode == http.StatusServiceUnavailable {
		return verdictUnavailable
	}

	// 没拿到上游原话（空 body、HTML 错误页、非 JSON、或只有本地编造的兜底文案）
	if apiErr == nil || !bodyParsed {
		return verdictInconclusive
	}

	notFound := false
	switch {
	// 信号 1：404 且带有非空错误消息
	case statusCode == 404 && strings.TrimSpace(apiErr.Message) != "":
		notFound = true
	// 信号 1.5：403 视为模型不可用（权限不足等同于不存在）
	case statusCode == http.StatusForbidden && strings.TrimSpace(apiErr.Message) != "":
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
//	                alive  not_found  unavailable  rate_limited  inconclusive  skipped
//	pending_add      批准     暂缓        暂缓         暂缓          暂缓        批准
//	pending_remove   暂缓     批准        批准         暂缓          暂缓        批准
//
// 三条原则：
//   - inconclusive / rate_limited 一律暂缓（保持现状最安全；下轮 diff 从零
//     重算会自然重试）
//   - skipped 一律批准（探针对这类模型无能力，维持「信任上游」的现有行为，
//     上线不引入回归）
//   - not_found 与 unavailable 处置相同（准删），但保留独立 verdict ——
//     「模型下架了」和「模型还在但后端全挂了」的运维含义不同，后者通常意味着
//     需要联系上游，日志里必须能区分
//
// verdicts 里缺失的模型按 inconclusive 处理 —— 探测被预算中止时会出现，必须保守。
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
	case verdictRateLimited:
		// 429 两个方向都不动：
		//   pendingRemove —— 上游能限流说明模型可用，删了是误伤
		//   pendingAdd    —— 限流检查可能发生在模型路由之前，对不存在的模型
		//                    也会返回 429，加了可能是假的；不加只延迟一轮
		return false
	case verdictUnavailable:
		// 503 该模型当前无可用后端：
		//   pendingRemove —— 批准删除。能进 pendingRemove 说明上游 /v1/models
		//                    也已不返回它，503 是第二个独立证据，两个信号都指向
		//                    「这个模型没了」。留着只会让请求路由到它然后失败，
		//                    删掉后路由能找到其他仍提供该模型的渠道。
		//                    （若只是临时抖动，models 接口通常还列着它，
		//                     根本不会进 pendingRemove，探针也不会被触发。）
		//   pendingAdd    —— 不加。当前就用不了，加进去只会让用户请求失败，
		//                    等恢复后下一轮 diff 会重新尝试。
		return scene == probeScenePendingRemove
	default: // verdictInconclusive 及任何未知值
		return false
	}
}

// ──────────────────────────────────────────
// 上游错误体解析
// ──────────────────────────────────────────

// probeUpstreamErrorBody 覆盖主流上游的错误响应形态。
// 刻意不复用 util.GeneralErrorResponse：那个结构没有 code/type 字段，
// 且配套的 RelayErrorHandler 会在解析失败时编造兜底文案（见 classifyProbeError
// 的注释），而探针需要的恰恰是「上游到底说了什么，以及我是否真的拿到了」。
type probeUpstreamErrorBody struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error"`
	// 部分上游把错误平铺在顶层
	Message   string `json:"message"`
	Msg       string `json:"msg"`
	ErrorMsg  string `json:"error_msg"`
	ErrorCode any    `json:"error_code"`
}

// parseProbeUpstreamError 解析上游错误响应体。
//
// 第二个返回值表示是否真的从上游拿到了错误信息：JSON 解析失败（HTML 错误页、
// 空 body、纯文本）或解析成功但没有任何可用字段，均返回 false。
// 这个布尔值是 not_found 判定的硬前置条件，绝不能凭空给 true。
func parseProbeUpstreamError(body []byte) (*relaymodel.Error, bool) {
	if len(body) == 0 {
		return nil, false
	}
	var parsed probeUpstreamErrorBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, false
	}

	apiErr := &relaymodel.Error{
		Message: parsed.Error.Message,
		Type:    parsed.Error.Type,
		Code:    parsed.Error.Code,
	}
	// 嵌套 error 对象没给消息时回退到顶层平铺字段
	if apiErr.Message == "" {
		for _, candidate := range []string{parsed.Message, parsed.Msg, parsed.ErrorMsg} {
			if candidate != "" {
				apiErr.Message = candidate
				break
			}
		}
	}
	if apiErr.Code == nil {
		apiErr.Code = parsed.ErrorCode
	}

	// 一个字段都没拿到 → 视为未解析出上游错误
	if apiErr.Message == "" && apiErr.Type == "" && normalizeErrCode(apiErr.Code) == "" {
		return nil, false
	}
	return apiErr, true
}

// ──────────────────────────────────────────
// 探测预算
// ──────────────────────────────────────────

// upstreamProbeRoundBudget 是全局每轮探测次数余额。
// 包级变量安全的前提：upstreamUpdateTaskRunning 的 atomic.Bool CAS 已保证
// 同一时刻只有一轮巡检在跑。每轮开头由 resetProbeRoundBudget 重置。
//
// 注意：这个全局余额只属于**定时任务**。手动探针（channel_upstream_probe_manual.go）
// 用独立的 manualBudget，绝不触碰它，否则会抽干定时任务本轮的额度，
// 导致后续渠道的自动同步/删除静默失效。
var upstreamProbeRoundBudget atomic.Int64

// probeStatSink 汇总一轮探测的六态计数。定时任务用包级 taskProbeStats；
// 手动探针传 nil（record/reset 均 nil 安全），不污染定时任务的轮末统计。
type probeStatSink struct {
	alive        atomic.Int64
	notFound     atomic.Int64
	inconclusive atomic.Int64
	skipped      atomic.Int64
	rateLimited  atomic.Int64
	unavailable  atomic.Int64
}

func (s *probeStatSink) record(v probeVerdict) {
	if s == nil {
		return
	}
	switch v {
	case verdictAlive:
		s.alive.Add(1)
	case verdictNotFound:
		s.notFound.Add(1)
	case verdictSkipped:
		s.skipped.Add(1)
	case verdictRateLimited:
		s.rateLimited.Add(1)
	case verdictUnavailable:
		s.unavailable.Add(1)
	default:
		s.inconclusive.Add(1)
	}
}

func (s *probeStatSink) reset() {
	if s == nil {
		return
	}
	s.alive.Store(0)
	s.notFound.Store(0)
	s.inconclusive.Store(0)
	s.skipped.Store(0)
	s.rateLimited.Store(0)
	s.unavailable.Store(0)
}

// taskProbeStats 是定时任务专用的六态计数，每轮开头由 resetProbeRoundBudget 重置。
var taskProbeStats probeStatSink

// probeTaker 抽象「是否允许再探一次」的预算判定。
// 定时任务用 *probeBudget（含全局每轮余额），手动探针用 *manualBudget（纯本次请求计数）。
type probeTaker interface {
	take() bool
}

// probeRunOptions 承载一批探测的运行时上下文，把预算/统计/来源/取消从包级状态里解耦出来。
type probeRunOptions struct {
	scene            string
	source           string          // probeSourceTask | probeSourceManual
	budget           probeTaker      // 预算判定
	stats            *probeStatSink  // nil = 不统计（手动路径）
	ctx              context.Context // nil = 不响应取消（定时任务）；手动路径传 c.Request.Context()
	modelConcurrency int             // 渠道内模型探测并发数，<1 兜底为 1
}

const (
	probeSourceTask   = "task"
	probeSourceManual = "manual"
)

// manualBudget 是手动探针的单次请求预算：只按本批模型数计数，不触碰任何全局状态。
// mu 保护 remaining：渠道内并行探测时多个 goroutine 会并发 take()。
type manualBudget struct {
	mu        sync.Mutex
	remaining int
}

func (b *manualBudget) take() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.remaining <= 0 {
		return false
	}
	b.remaining--
	return true
}

// probeBudget 是单渠道的探测预算。
//
// 这里刻意**没有**任何「渠道级中止」机制：探针只回答「这个模型怎么样」，
// 不从单个模型的失败去推断「整个渠道都完了」。曾经尝试过对 401/403/402
// 立即中止，但那是错的 ——
//   - 403 经常是模型级权限（OpenAI 部分模型需组织验证、中转站按套餐分组），
//     中止会误伤同渠道其他完全正常的模型
//   - 402 语义各家网关不统一，可能是按模型分配的额度
//   - 401 虽是 key 级，但探针只用了一个 key（GetNextAvailableKey），
//     多 key 渠道下不能代表其他 key
//
// 资源控制全部交给预算：单渠道次数（MaxPerChannel）、单渠道时长
// （ChannelBudgetSecs）、全局每轮次数（MaxPerRound）。
// 极端情况下 key 整体失效的渠道会吃掉 30 次全局配额，但这些请求立即返回、
// 不占用时长，代价可接受。
//
// mu 保护 channelRemaining：渠道内模型探测并行后（ProbeModelConcurrency>1），
// 多个 goroutine 会并发调 take()，channelRemaining-- 是 data race。
type probeBudget struct {
	mu               sync.Mutex
	channelRemaining int
	channelDeadline  time.Time
}

func resetProbeRoundBudget() {
	upstreamProbeRoundBudget.Store(int64(config.UpstreamModelProbeMaxPerRound))
	taskProbeStats.reset()
}

func newProbeBudget() *probeBudget {
	return &probeBudget{
		channelRemaining: config.UpstreamModelProbeMaxPerChannel,
		channelDeadline:  time.Now().Add(time.Duration(config.UpstreamModelProbeChannelBudgetSecs) * time.Second),
	}
}

// take 尝试为一次探测扣减预算。返回 false 表示应停止探测（剩余按 inconclusive 处理）。
func (b *probeBudget) take() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.channelRemaining <= 0 || time.Now().After(b.channelDeadline) {
		return false
	}
	if upstreamProbeRoundBudget.Add(-1) < 0 {
		upstreamProbeRoundBudget.Add(1) // 回退，保持余额不为负
		return false
	}
	b.channelRemaining--
	return true
}

// ──────────────────────────────────────────
// 单次探测
// ──────────────────────────────────────────

// probeResult 是单次探测的完整结果，用于写日志
type probeResult struct {
	Model string
	// MappedModel 是套用 model_mapping 后实际发给上游的模型名。
	// 与 Model 不同时必须记进日志，否则排查会困惑：
	// 「我探的是 gpt-4，为什么错误信息里说 gpt-4-turbo 不存在」
	MappedModel string
	Verdict     probeVerdict
	StatusCode  int
	ErrCode     string
	ErrType     string
	Message     string
	Duration    float64
	Usage       *relaymodel.Usage
	SkipReason  string
}

const probeMessageMaxLen = 500

func truncateProbeMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	runes := []rune(msg)
	if len(runes) <= probeMessageMaxLen {
		return msg
	}
	return string(runes[:probeMessageMaxLen]) + "...(truncated)"
}

// probeChannelUnsupportedReason 只做**渠道级**判定：整个渠道类型是否无法用
// chat-completions 探测。空字符串表示渠道类型本身可探。
//
// 抽出来单独用是因为：手动探针 handler 必须在进入任何逐模型循环**之前**做这个判定。
// 否则渠道类型不支持时，循环里每个模型都在 budget.take() 之前判 skipped、
// 不消耗预算也无停止机制，会把整批模型全标成 skipped（而 skipped 在处置表里
// 视为「信任上游=批准」），UI 上呈现为「全部建议应用」，实际一次请求都没发。
func probeChannelUnsupportedReason(channel *model.Channel) string {
	if isUnsupportedTestChannel(channel.Type) {
		name, ok := common.ChannelTypeToProvider[channel.Type]
		if !ok {
			name = fmt.Sprintf("type=%d", channel.Type)
		}
		return fmt.Sprintf("渠道类型 %s 不支持 chat-completions 探测", name)
	}
	return ""
}

// probeUnsupportedReason 返回该渠道/模型无法用 chat-completions 探测的原因，
// 空字符串表示可以探测。
func probeUnsupportedReason(channel *model.Channel, modelName string) string {
	if reason := probeChannelUnsupportedReason(channel); reason != "" {
		return reason
	}
	if isUnsupportedTestModel(modelName) {
		return "非聊天类模型，不支持 chat-completions 探测"
	}
	// Codex 仅支持 /v1/responses。复用 testChannelViaResponses 会连带写入
	// LogTypeConsume + tokenName=channel-test 的日志，污染渠道测试记录，
	// 第一版不支持。
	if isCodexModel(modelName) {
		return "Codex 系列仅支持 /v1/responses，探针暂不支持"
	}
	return ""
}

// doProbeChannelModel 对单个 channel×model 发一次真实的最小 chat 请求。
//
// 骨架复制自 testChannel（channel-test.go:251-352），三处关键差异：
//  1. 不做 strings.Contains(channel.Models, model) 白名单检查 —— pendingAdd 的
//     模型本就不在 channel.Models 里，该检查会让探测必然失败
//  2. 设置 MaxTokens 压到最小，且强制非流式
//  3. 自己读 resp.Body 并用 parseProbeUpstreamError 解析，不经过
//     util.RelayErrorHandler（它会编造兜底文案，详见 classifyProbeError 注释）
//
// model_mapping 与 testChannel 保持一致（都套用），理由见下方赋值处的注释。
//
// 返回值必须命名：下面的 defer 要写回 res.Duration。用无名返回值时，
// `return res` 先把值拷进返回槽、defer 之后才执行，改的是已废弃的局部副本，
// Duration 恒为 0（回归测试见 TestDoProbeChannelModelRecordsDuration）。
func doProbeChannelModel(channel *model.Channel, modelName string) (res probeResult) {
	res = probeResult{Model: modelName, Verdict: verdictInconclusive}
	start := time.Now()
	defer func() {
		res.Duration = time.Since(start).Seconds()
	}()

	if reason := probeUnsupportedReason(channel, modelName); reason != "" {
		res.Verdict = verdictSkipped
		res.SkipReason = reason
		return res
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = &http.Request{
		Method: "POST",
		URL:    &url.URL{Path: "/v1/chat/completions"},
		Body:   nil,
		Header: make(http.Header),
	}

	probeKey := channel.Key
	keyIndex := -1
	if channel.MultiKeyInfo.IsMultiKey {
		actualKey, selectedIndex, keyErr := channel.GetNextAvailableKey()
		if keyErr != nil {
			res.Verdict = verdictSkipped
			res.SkipReason = fmt.Sprintf("无可用 key: %v", keyErr)
			return res
		}
		probeKey = actualKey
		keyIndex = selectedIndex
	}

	c.Request.Header.Set("Authorization", "Bearer "+probeKey)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("channel", channel.Type)
	c.Set("base_url", channel.GetBaseURL())
	c.Set("test_key_index", keyIndex)
	if channel.MultiKeyInfo.IsMultiKey && keyIndex >= 0 {
		c.Set("cached_key_index", keyIndex)
	}
	// 传空 modelName：middleware/distributor.go 会因此跳过 metrics.IncChannelAttempt，
	// 探针流量不污染渠道成功率指标
	middleware.SetupContextForSelectedChannel(c, channel, "")

	meta := util.GetRelayMeta(c)
	apiType := constant.ChannelType2APIType(channel.Type)
	adaptor := helper.GetAdaptor(apiType)
	if adaptor == nil {
		res.Verdict = verdictSkipped
		res.SkipReason = fmt.Sprintf("无对应 adaptor: apiType=%d", apiType)
		return res
	}
	adaptor.Init(meta)

	// max_tokens 按模型能力自适应：Claude thinking 需 > 1024 的 thinking budget，
	// OpenAI 推理模型需覆盖 reasoning tokens，一刀切小值会让这两类必然失败。
	// 见 testRequestMaxTokensFor 的注释。
	request := buildTestRequest(modelName)
	request.Stream = false
	// 套用 model_mapping，与 testChannel 及真实请求路径保持一致。
	//
	// 探针要回答的不是「上游有没有这个名字」，而是「这个模型加进本地列表后，
	// 用户请求它能不能成功」—— 而用户请求走的就是映射后的名字。不套映射会让
	// 探针验证的链路与真实调用链路不同，探针说 alive 而用户实际调用失败，
	// 比不探更糟。
	//
	// 另一层理由：管理员显式配置 model_mapping，本身就表达了「这个模型要用
	// 映射后的名字调上游」，这个显式意图应当优先于 /v1/models 的自动发现结果。
	//
	// GetRelayMeta 内部（relay_meta.go:164-170）只对 meta.OriginModelName 应用
	// 映射，而上面传给 SetupContextForSelectedChannel 的 modelName 是空字符串，
	// 那里是空转，不会与此处产生双重映射。
	meta.OriginModelName = modelName
	mappedModel, _ := util.GetMappedModelName(modelName, meta.ModelMapping)
	request.Model = mappedModel
	meta.ActualModelName = mappedModel
	res.MappedModel = mappedModel

	convertedRequest, err := adaptor.ConvertRequest(c, constant.RelayModeChatCompletions, request)
	if err != nil {
		res.Message = truncateProbeMessage(fmt.Sprintf("构造请求失败: %v", err))
		return res
	}
	jsonData, err := json.Marshal(convertedRequest)
	if err != nil {
		res.Message = truncateProbeMessage(fmt.Sprintf("序列化请求失败: %v", err))
		return res
	}
	requestBody := bytes.NewBuffer(jsonData)
	c.Request.Body = io.NopCloser(bytes.NewBuffer(jsonData))

	resp, err := adaptor.DoRequest(c, meta, requestBody)
	if err != nil {
		// 网络错误、超时：与模型存在性无关
		res.Verdict = classifyProbeError(0, nil, false, err, channel.MultiKeyInfo.IsMultiKey)
		res.Message = truncateProbeMessage(err.Error())
		return res
	}

	// resp 可能为 nil（AWS SDK 等直接处理请求的渠道不返回 HTTP 响应）
	if resp != nil {
		res.StatusCode = resp.StatusCode
		if resp.StatusCode != http.StatusOK {
			bodyBytes, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr != nil {
				res.Message = truncateProbeMessage(fmt.Sprintf("读取上游响应失败: %v", readErr))
				return res
			}
			apiErr, parsed := parseProbeUpstreamError(bodyBytes)
			res.Verdict = classifyProbeError(resp.StatusCode, apiErr, parsed, nil, channel.MultiKeyInfo.IsMultiKey)
			if apiErr != nil {
				res.ErrCode = normalizeErrCode(apiErr.Code)
				res.ErrType = apiErr.Type
				res.Message = truncateProbeMessage(apiErr.Message)
			} else {
				res.Message = truncateProbeMessage(string(bodyBytes))
			}
			return res
		}
	}

	usage, respErr := adaptor.DoResponse(c, resp, meta)
	if respErr != nil {
		res.StatusCode = respErr.StatusCode
		// AWS SDK 渠道（DoRequest 返回 nil）的错误来自上游 API 真实响应，
		// 不是 relay 编造的兜底文案，可以信任。
		bodyParsed := resp == nil && respErr.Error.Type == "aws_error"
		res.Verdict = classifyProbeError(respErr.StatusCode, &respErr.Error, bodyParsed, nil, channel.MultiKeyInfo.IsMultiKey)
		res.ErrCode = normalizeErrCode(respErr.Error.Code)
		res.ErrType = respErr.Error.Type
		res.Message = truncateProbeMessage(respErr.Error.Message)
		return res
	}
	if usage == nil {
		res.Message = "上游未返回 usage"
		return res
	}

	// alive 判定：不把「choices 非空」当硬条件 —— AWS/Vertex 等走 SDK 的适配器
	// 可能不填 recorder（testChannel:343 就专门处理了 resp == nil）
	res.Usage = usage
	if usage.CompletionTokens > 0 || usage.TotalTokens > 0 || usage.PromptTokens > 0 {
		res.Verdict = verdictAlive
	} else {
		res.Message = "上游返回 usage 但 token 数全为 0"
	}
	return res
}

// probeChannelModel 给单次探测包一层 wall-clock 超时。
//
// 必需的原因：DoRequestHelper 明确不绑定 context（relay/channel/common.go:36-38），
// 超时只由全局 HTTPClient.Timeout 控制，默认 5 分钟。串行探 30 个模型最坏 2.5 小时，
// 而巡检本身是串行遍历所有渠道的。
//
// done 必须带 buffer：否则超时返回后发送方永久阻塞、goroutine 泄漏。
// 带 buffer 时泄漏的 goroutine 会在 HTTPClient 超时后自行退出，
// 同时存在的上限等于探测预算。
func probeChannelModel(channel *model.Channel, modelName string, timeout time.Duration) probeResult {
	done := make(chan probeResult, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- probeResult{
					Model:   modelName,
					Verdict: verdictInconclusive,
					Message: truncateProbeMessage(fmt.Sprintf("探测 panic: %v", r)),
				}
			}
		}()
		done <- doProbeChannelModel(channel, modelName)
	}()

	select {
	case r := <-done:
		return r
	case <-time.After(timeout):
		return probeResult{
			Model:    modelName,
			Verdict:  verdictInconclusive,
			Message:  fmt.Sprintf("探测超时（%s）", timeout),
			Duration: timeout.Seconds(),
		}
	}
}

// ──────────────────────────────────────────
// 批量探测
// ──────────────────────────────────────────

// probeChannelModels 探测一批模型，返回 model → 完整结果。
//
// 渠道内并发度由 opts.modelConcurrency 控制（源自 UpstreamModelProbeModelConcurrency）。
// 早期版本刻意串行以防「同一 key 并发触发 429」，现改为可配置并发 —— 上游承受力
// 弱时把该配置设为 1 即退回串行。budget.take() 已加锁，并发安全。
//
// 预算耗尽或 opts.ctx 取消后，未探的模型不出现在返回值里 —— filterByProbeVerdicts
// 会把缺失的键按 inconclusive 处理（保守）。
func probeChannelModels(channel *model.Channel, models []string, opts probeRunOptions) map[string]probeResult {
	results := make(map[string]probeResult, len(models))
	var mu sync.Mutex
	var budgetDenied atomic.Bool
	timeout := time.Duration(config.UpstreamModelProbeTimeoutSeconds) * time.Second

	concurrency := opts.modelConcurrency
	if concurrency < 1 {
		concurrency = 1
	}
	g := new(errgroup.Group)
	g.SetLimit(concurrency)

	// store 汇总一次探测结果：results 写入加锁，stats 是 atomic、DB 写入并发安全。
	store := func(res probeResult) {
		mu.Lock()
		results[res.Model] = res
		mu.Unlock()
		opts.stats.record(res.Verdict)
		recordProbeLog(channel, res, opts.scene, opts.source)
	}

	ctxDone := func() bool {
		if opts.ctx == nil {
			return false
		}
		select {
		case <-opts.ctx.Done():
			return true
		default:
			return false
		}
	}

submitLoop:
	for _, modelName := range models {
		modelName := modelName
		if ctxDone() {
			upstreamInfo(fmt.Sprintf(
				"upstream probe: cancelled channel_id=%d scene=%s stopped_at=%s",
				channel.Id, opts.scene, modelName))
			break submitLoop
		}
		g.Go(func() error {
			// skipped 不消耗预算：它不发请求
			if reason := probeUnsupportedReason(channel, modelName); reason != "" {
				store(probeResult{Model: modelName, Verdict: verdictSkipped, SkipReason: reason})
				return nil
			}
			// 提交后到执行前可能已被取消
			if ctxDone() {
				return nil
			}
			if !opts.budget.take() {
				budgetDenied.Store(true) // 预算耗尽：不探，缺键 → inconclusive
				return nil
			}
			store(probeChannelModel(channel, modelName, timeout))
			return nil
		})
	}
	_ = g.Wait()
	if budgetDenied.Load() {
		upstreamInfo(fmt.Sprintf(
			"upstream probe: budget exhausted channel_id=%d scene=%s remaining_treated_as_inconclusive=true",
			channel.Id, opts.scene))
	}
	return results
}

// buildProbeLogOther 拼装 logs 表的 Other 字段（结构化，便于 grep）。
// 抽成纯函数以便单测；probe_source 区分定时任务(task)与手动(manual)。
func buildProbeLogOther(res probeResult, scene, source string) string {
	return fmt.Sprintf("probe_verdict:%s;probe_scene:%s;probe_status:%d;probe_source:%s",
		res.Verdict, scene, res.StatusCode, source)
}

// recordProbeLog 把一次探测写入 logs 表（Type=LogTypeSystem）与 SysLog 摘要。
//
// 复用 logs 表而非新建探针表：ChannelId / ModelName / TokenName / Type 都有索引，
// 前端现有日志页筛「类型=系统」+「令牌名称=model-probe」即可按渠道/模型检索，
// 零前端改动。
func recordProbeLog(channel *model.Channel, res probeResult, scene, source string) {
	var quota int64
	var promptTokens, completionTokens int
	ratioNote := ""
	if res.Usage != nil {
		promptTokens = res.Usage.PromptTokens
		completionTokens = res.Usage.CompletionTokens
		quota, ratioNote = calcProbeQuota(res.Model, promptTokens, completionTokens)
	}

	detail := fmt.Sprintf("探针结论=%s 场景=%s 来源=%s", res.Verdict, scene, source)
	if res.MappedModel != "" && res.MappedModel != res.Model {
		detail += fmt.Sprintf(" 映射后=%s", res.MappedModel)
	}
	if res.StatusCode > 0 {
		detail += fmt.Sprintf(" 状态码=%d", res.StatusCode)
	}
	if res.ErrCode != "" {
		detail += fmt.Sprintf(" code=%s", res.ErrCode)
	}
	if res.ErrType != "" {
		detail += fmt.Sprintf(" type=%s", res.ErrType)
	}
	if res.SkipReason != "" {
		detail += fmt.Sprintf(" 跳过原因=%s", res.SkipReason)
	}
	if res.Message != "" {
		detail += fmt.Sprintf(" 错误=%s", res.Message)
	}
	if ratioNote != "" {
		detail += " " + ratioNote
	}

	other := buildProbeLogOther(res, scene, source)

	model.RecordModelProbeLog(
		channel.Id,
		res.Model,
		fmt.Sprintf("模型探针: %s", channel.Name),
		detail,
		other,
		res.Duration,
		quota,
		promptTokens,
		completionTokens,
	)

	// 只有产生了实际判决（非 skipped）才打 SysLog，避免刷屏
	if res.Verdict != verdictSkipped {
		upstreamInfo(fmt.Sprintf(
			"upstream probe: channel_id=%d channel_name=%s model=%s scene=%s source=%s verdict=%s status=%d duration=%.2fs",
			channel.Id, channel.Name, res.Model, scene, source, res.Verdict, res.StatusCode, res.Duration))
	}
}

// calcProbeQuota 换算探测消耗的 quota（仅用于日志展示，不扣配额、不累计渠道用量）。
// 第二个返回值说明倍率来源 —— pendingAdd 的模型基本都不在倍率表里，
// common.GetModelRatio 对未知模型返回 30，不注明会让看日志的人困惑。
func calcProbeQuota(modelName string, promptTokens, completionTokens int) (int64, string) {
	if promptTokens+completionTokens == 0 {
		return 0, ""
	}
	modelRatio := common.GetModelRatio(modelName)
	completionRatio := common.GetCompletionRatio(modelName)
	quota := int64(math.Ceil((float64(promptTokens) + float64(completionTokens)*completionRatio) * modelRatio))
	if quota <= 0 {
		quota = 1
	}
	note := fmt.Sprintf("模型倍率=%.2f 补全倍率=%.2f", modelRatio, completionRatio)
	if _, known := common.ModelRatio[modelName]; !known {
		note += "（倍率表无此模型，按默认值计）"
	}
	return quota, note
}

// ──────────────────────────────────────────
// 对外入口：过滤 pending 列表
// ──────────────────────────────────────────

// upstreamProbeEnabledFor 判断某渠道是否应走探针
func upstreamProbeEnabledFor(settings *config.ChannelOtherSettings) bool {
	if !config.UpstreamModelProbeEnabled {
		return false
	}
	return !settings.UpstreamModelProbeDisabled
}

// probeFilterPendingModels 用真实请求过滤 pending 列表，返回批准执行的模型。
// held 为被暂缓的模型，调用方应把它们留在 settings 里供管理员在 UI 上处置。
//
// 这是**定时任务专用**路径：用 *probeBudget（含全局每轮余额）+ taskProbeStats 统计。
func probeFilterPendingModels(channel *model.Channel, models []string, scene string, budget *probeBudget) (approved, held []string) {
	if len(models) == 0 {
		return models, nil
	}
	results := probeChannelModels(channel, models, probeRunOptions{
		scene:            scene,
		source:           probeSourceTask,
		budget:           budget,
		stats:            &taskProbeStats,
		modelConcurrency: config.UpstreamModelProbeModelConcurrency,
	})
	return filterByProbeVerdicts(models, projectVerdicts(results), scene)
}

// projectVerdicts 把探测结果投影成 model → verdict，喂给纯函数 filterByProbeVerdicts。
func projectVerdicts(results map[string]probeResult) map[string]probeVerdict {
	verdicts := make(map[string]probeVerdict, len(results))
	for m, r := range results {
		verdicts[m] = r.Verdict
	}
	return verdicts
}
