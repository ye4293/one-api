package controller

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/model"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
)

func TestClassifyProbeError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		apiErr     *relaymodel.Error
		bodyParsed bool
		netErr     error
		isMultiKey bool
		want       probeVerdict
	}{
		// ── 明确的「模型不存在」信号（均要求上游 body 解析成功）──
		{
			name:       "404 带可解析错误消息",
			statusCode: 404,
			apiErr:     &relaymodel.Error{Message: "The model does not exist"},
			bodyParsed: true,
			want:       verdictNotFound,
		},
		{
			name:       "code=model_not_found",
			statusCode: 400,
			apiErr:     &relaymodel.Error{Code: "model_not_found"},
			bodyParsed: true,
			want:       verdictNotFound,
		},
		{
			name:       "type=invalid_model",
			statusCode: 400,
			apiErr:     &relaymodel.Error{Type: "invalid_model"},
			bodyParsed: true,
			want:       verdictNotFound,
		},
		{
			name:       "code 大小写混合仍命中",
			statusCode: 400,
			apiErr:     &relaymodel.Error{Code: "Model_Not_Found"},
			bodyParsed: true,
			want:       verdictNotFound,
		},
		{
			name:       "中文「模型不存在」",
			statusCode: 400,
			apiErr:     &relaymodel.Error{Message: "该模型不存在，请检查模型名称"},
			bodyParsed: true,
			want:       verdictNotFound,
		},
		{
			name:       "消息大小写不敏感",
			statusCode: 400,
			apiErr:     &relaymodel.Error{Message: "UNKNOWN MODEL: foo"},
			bodyParsed: true,
			want:       verdictNotFound,
		},
		{
			name:       "OpenAI 合并消息（不存在或无权限）按不存在处理",
			statusCode: 404,
			apiErr:     &relaymodel.Error{Message: "The model 'gpt-9' does not exist or you do not have access to it"},
			bodyParsed: true,
			want:       verdictNotFound,
		},

		// ── 约束 B：裸 404 不能判 not_found（base_url 配错会全量误删）──
		//
		// 真实路径下 util.RelayErrorHandler 在 body 解析不出消息时会**编造兜底文案**
		// （relay/util/common.go:182-202），其中 404 的文案含「模型不存在」四字，
		// 正好命中关键词白名单。因此判定必须以 bodyParsed 为前置条件，
		// 不能只看 Message 是否非空。
		{
			name:       "RelayErrorHandler 的 404 兜底文案不得判为 not_found",
			statusCode: 404,
			apiErr: &relaymodel.Error{
				Message: "资源未找到 (404): 请求的端点或模型不存在",
				Type:    "upstream_error",
				Code:    "bad_response_status_code",
			},
			bodyParsed: false,
			want:       verdictInconclusive,
		},
		{
			name:       "RelayErrorHandler 的 403 兜底文案不得判为 not_found",
			statusCode: 403,
			apiErr: &relaymodel.Error{
				Message: "权限不足 (403): 无权访问此资源或模型",
				Type:    "upstream_error",
				Code:    "bad_response_status_code",
			},
			bodyParsed: false,
			want:       verdictInconclusive,
		},
		{
			name:       "404 但无可解析错误体（空 body）",
			statusCode: 404,
			apiErr:     nil,
			bodyParsed: false,
			want:       verdictInconclusive,
		},
		{
			name:       "404 但返回 HTML 错误页（解析失败）",
			statusCode: 404,
			apiErr:     nil,
			bodyParsed: false,
			want:       verdictInconclusive,
		},
		{
			name:       "404 且错误体存在但消息为空",
			statusCode: 404,
			apiErr:     &relaymodel.Error{},
			bodyParsed: true,
			want:       verdictInconclusive,
		},
		{
			name:       "404 且消息只有空白字符",
			statusCode: 404,
			apiErr:     &relaymodel.Error{Message: "   "},
			bodyParsed: true,
			want:       verdictInconclusive,
		},
		{
			name:       "body 未解析成功时即便命中关键词也不判 not_found",
			statusCode: 400,
			apiErr:     &relaymodel.Error{Message: "model does not exist"},
			bodyParsed: false,
			want:       verdictInconclusive,
		},

		// ── 关键词白名单的边界：这两条误命中会导致误删 ──
		{
			name:       "仅无权限不判 not_found",
			statusCode: 403,
			apiErr:     &relaymodel.Error{Message: "You do not have access to this model"},
			bodyParsed: true,
			want:       verdictInconclusive,
		},
		{
			name:       "上下文超长不判 not_found",
			statusCode: 400,
			apiErr:     &relaymodel.Error{Message: "This model's maximum context length is 4096 tokens"},
			bodyParsed: true,
			want:       verdictInconclusive,
		},

		// ── 数字类 code 不构成明确信号 ──
		{
			name:       "code 为 float64(404)",
			statusCode: 400,
			apiErr:     &relaymodel.Error{Code: float64(404)},
			bodyParsed: true,
			want:       verdictInconclusive,
		},
		{
			name:       "code 为 int",
			statusCode: 400,
			apiErr:     &relaymodel.Error{Code: 404},
			bodyParsed: true,
			want:       verdictInconclusive,
		},
		{
			name:       "code 为 nil",
			statusCode: 500,
			apiErr:     &relaymodel.Error{Code: nil},
			bodyParsed: true,
			want:       verdictInconclusive,
		},

		// ── 各类「与模型存在性无关」的失败 ──
		{
			name:       "401 鉴权失败",
			statusCode: 401,
			apiErr:     &relaymodel.Error{Type: "authentication_error", Message: "Incorrect API key"},
			bodyParsed: true,
			want:       verdictInconclusive,
		},
		{
			name:       "403 无权限",
			statusCode: 403,
			apiErr:     &relaymodel.Error{Message: "权限不足"},
			bodyParsed: true,
			want:       verdictInconclusive,
		},
		{
			name:       "429 限流判为 rate_limited（模型可用的证据）",
			statusCode: 429,
			apiErr:     &relaymodel.Error{Type: "rate_limit_error", Message: "Rate limit reached"},
			bodyParsed: true,
			want:       verdictRateLimited,
		},
		{
			name:       "429 即便无可解析错误体也判 rate_limited",
			statusCode: 429,
			apiErr:     nil,
			bodyParsed: false,
			want:       verdictRateLimited,
		},
		{
			name:       "503 判为 unavailable（模型级信号，渠道正常）",
			statusCode: 503,
			apiErr:     &relaymodel.Error{Message: "No available backend for this model"},
			bodyParsed: true,
			want:       verdictUnavailable,
		},
		{
			name:       "503 无错误体同样判 unavailable",
			statusCode: 503,
			apiErr:     nil,
			bodyParsed: false,
			want:       verdictUnavailable,
		},
		{
			name:       "429 优先于网络错误之外的一切判定",
			statusCode: 429,
			apiErr:     &relaymodel.Error{Message: "model does not exist"},
			bodyParsed: true,
			want:       verdictRateLimited,
		},
		{
			name:       "402 余额不足",
			statusCode: 402,
			apiErr:     &relaymodel.Error{Type: "insufficient_quota"},
			bodyParsed: true,
			want:       verdictInconclusive,
		},
		{
			name:       "500 上游内部错误",
			statusCode: 500,
			apiErr:     &relaymodel.Error{Message: "Internal server error"},
			bodyParsed: true,
			want:       verdictInconclusive,
		},
		{
			name:       "502 网关错误无错误体",
			statusCode: 502,
			apiErr:     nil,
			bodyParsed: false,
			want:       verdictInconclusive,
		},
		{
			name:       "内容审核拦截",
			statusCode: 400,
			apiErr:     &relaymodel.Error{Type: "content_filter", Message: "内容违规，已拦截"},
			bodyParsed: true,
			want:       verdictInconclusive,
		},
		{
			name:       "网络超时",
			statusCode: 0,
			apiErr:     nil,
			bodyParsed: false,
			netErr:     context.DeadlineExceeded,
			want:       verdictInconclusive,
		},
		{
			name:       "网络错误优先于错误体判定",
			statusCode: 404,
			apiErr:     &relaymodel.Error{Message: "model does not exist"},
			bodyParsed: true,
			netErr:     errors.New("connection reset by peer"),
			want:       verdictInconclusive,
		},

		// ── 决策 2：多 key 渠道降级（bodyParsed 必须为 true，否则测不出降级逻辑）──
		{
			name:       "多 key 渠道的 404 降级为无结论",
			statusCode: 404,
			apiErr:     &relaymodel.Error{Message: "The model does not exist"},
			bodyParsed: true,
			isMultiKey: true,
			want:       verdictInconclusive,
		},
		{
			name:       "多 key 渠道的 model_not_found 降级为无结论",
			statusCode: 400,
			apiErr:     &relaymodel.Error{Code: "model_not_found"},
			bodyParsed: true,
			isMultiKey: true,
			want:       verdictInconclusive,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyProbeError(tt.statusCode, tt.apiErr, tt.bodyParsed, tt.netErr, tt.isMultiKey)
			if got != tt.want {
				t.Errorf("classifyProbeError() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestRelayErrorHandlerFallbackMessagesNeverCauseNotFound 锁定一条关键不变式：
// util.RelayErrorHandler 在无法解析上游 body 时会编造兜底文案
// （relay/util/common.go:182-202），这些**本地生成**的文案绝不能被判为 not_found。
//
// 危险性已实测：8 条兜底文案里 404 那条（"资源未找到 (404): 请求的端点或模型不存在"）
// 含「模型不存在」四字，会命中关键词白名单；且它同时命中「404 + Message 非空」这条
// 信号，属于双重命中。而 404 恰恰是 base_url 配错或上游反代挂掉时的典型返回 ——
// 一旦误判，该渠道所有 pendingRemove 模型会在一轮内被删光。
//
// 若有人日后"简化"掉 classifyProbeError 的 bodyParsed 门禁，本测试会立刻失败。
func TestRelayErrorHandlerFallbackMessagesNeverCauseNotFound(t *testing.T) {
	fallbacks := map[int]string{
		504: "网关超时 (504): 上游服务器响应超时，请稍后重试或检查API服务状态",
		502: "网关错误 (502): 上游服务器返回无效响应",
		503: "服务不可用 (503): 上游服务器暂时无法处理请求",
		429: "请求过于频繁 (429): 已达到API调用限制，请稍后重试",
		401: "认证失败 (401): API密钥无效或已过期",
		403: "权限不足 (403): 无权访问此资源或模型",
		404: "资源未找到 (404): 请求的端点或模型不存在",
		500: "上游服务错误 (状态码: 500)",
	}
	for statusCode, msg := range fallbacks {
		t.Run(strconv.Itoa(statusCode), func(t *testing.T) {
			// RelayErrorHandler 未能解析上游 body 时的完整形态
			apiErr := &relaymodel.Error{
				Message: msg,
				Type:    "upstream_error",
				Code:    "bad_response_status_code",
				Param:   strconv.Itoa(statusCode),
			}
			got := classifyProbeError(statusCode, apiErr, false /* bodyParsed */, nil, false)
			// 断言的是「不得判为 not_found」而非「必须是 inconclusive」——
			// 429/503 有各自的 verdict（rate_limited / unavailable），
			// 把断言写成等于 inconclusive 会在新增 verdict 时误报。
			if got == verdictNotFound {
				t.Errorf("兜底文案被判为 not_found，会导致误删：%q", msg)
			}
		})
	}

	// 反向断言：证明 bodyParsed 门禁是必需的而非多余防御 ——
	// 404 的兜底文案确实命中关键词白名单，去掉门禁就会误判。
	t.Run("404 兜底文案确实命中关键词白名单", func(t *testing.T) {
		if !isModelNotFoundMessage(fallbacks[404]) {
			t.Fatal("前提已变化：404 兜底文案不再命中白名单。" +
				"请确认 relay/util/common.go 的文案或本处白名单是否改动，" +
				"并重新评估 bodyParsed 门禁的必要性")
		}
	})
}

// TestParseProbeUpstreamError 是约束 B 的第一道防线：
// 只有真的从上游 body 里解析出错误信息时第二个返回值才能是 true。
// 凭空给 true 会让 classifyProbeError 的 bodyParsed 门禁失效。
func TestParseProbeUpstreamError(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantParsed  bool
		wantMessage string
		wantCode    string
		wantType    string
	}{
		// ── 必须返回 false 的：拿不到上游原话 ──
		{name: "空 body", body: "", wantParsed: false},
		{name: "HTML 错误页", body: "<html><body>404 Not Found</body></html>", wantParsed: false},
		{name: "nginx 纯文本", body: "404 page not found", wantParsed: false},
		{name: "空 JSON 对象", body: `{}`, wantParsed: false},
		{name: "error 为空对象", body: `{"error":{}}`, wantParsed: false},
		{name: "截断的 JSON", body: `{"error":{"message":`, wantParsed: false},
		{name: "JSON 数组", body: `[1,2,3]`, wantParsed: false},
		{name: "全是空字符串字段", body: `{"error":{"message":"","type":"","code":null}}`, wantParsed: false},

		// ── 标准 OpenAI 形态 ──
		{
			name:        "标准 OpenAI 错误",
			body:        `{"error":{"message":"The model does not exist","type":"invalid_request_error","code":"model_not_found"}}`,
			wantParsed:  true,
			wantMessage: "The model does not exist",
			wantType:    "invalid_request_error",
			wantCode:    "model_not_found",
		},
		{
			name:       "只有 type",
			body:       `{"error":{"type":"invalid_model"}}`,
			wantParsed: true,
			wantType:   "invalid_model",
		},
		{
			name:       "只有 code",
			body:       `{"error":{"code":"model_not_found"}}`,
			wantParsed: true,
			wantCode:   "model_not_found",
		},
		{
			name:        "code 为数字",
			body:        `{"error":{"message":"boom","code":404}}`,
			wantParsed:  true,
			wantMessage: "boom",
			wantCode:    "404",
		},

		// ── 顶层平铺形态（部分国内上游）──
		{
			name:        "顶层 message",
			body:        `{"message":"模型不存在"}`,
			wantParsed:  true,
			wantMessage: "模型不存在",
		},
		{
			name:        "顶层 msg",
			body:        `{"msg":"invalid model"}`,
			wantParsed:  true,
			wantMessage: "invalid model",
		},
		{
			name:        "顶层 error_msg + error_code",
			body:        `{"error_msg":"unknown model","error_code":17}`,
			wantParsed:  true,
			wantMessage: "unknown model",
			wantCode:    "17",
		},
		{
			name:        "嵌套 error.message 优先于顶层 message",
			body:        `{"error":{"message":"inner"},"message":"outer"}`,
			wantParsed:  true,
			wantMessage: "inner",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiErr, parsed := parseProbeUpstreamError([]byte(tt.body))
			if parsed != tt.wantParsed {
				t.Fatalf("parsed = %v, want %v (apiErr=%+v)", parsed, tt.wantParsed, apiErr)
			}
			if !tt.wantParsed {
				if apiErr != nil {
					t.Errorf("parsed=false 时 apiErr 必须为 nil，实际为 %+v", apiErr)
				}
				return
			}
			if apiErr == nil {
				t.Fatal("parsed=true 时 apiErr 不能为 nil")
			}
			if apiErr.Message != tt.wantMessage {
				t.Errorf("Message = %q, want %q", apiErr.Message, tt.wantMessage)
			}
			if apiErr.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", apiErr.Type, tt.wantType)
			}
			if got := normalizeErrCode(apiErr.Code); got != tt.wantCode {
				t.Errorf("Code = %q, want %q", got, tt.wantCode)
			}
		})
	}

	// 端到端：HTML 错误页 + 404 走完整链路必须是 inconclusive
	t.Run("404+HTML 走完整链路为 inconclusive", func(t *testing.T) {
		apiErr, parsed := parseProbeUpstreamError([]byte("<html>404</html>"))
		if got := classifyProbeError(404, apiErr, parsed, nil, false); got != verdictInconclusive {
			t.Errorf("got %v, want inconclusive —— base_url 配错时会全量误删", got)
		}
	})
}

func TestTruncateProbeMessage(t *testing.T) {
	t.Run("短消息原样返回", func(t *testing.T) {
		if got := truncateProbeMessage("  hello  "); got != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
	})
	t.Run("超长 ASCII 截断", func(t *testing.T) {
		got := truncateProbeMessage(strings.Repeat("a", 600))
		if !strings.HasSuffix(got, "...(truncated)") {
			t.Errorf("缺少截断标记: %q", got[len(got)-20:])
		}
		if n := len([]rune(got)); n != probeMessageMaxLen+len("...(truncated)") {
			t.Errorf("截断后长度 %d 不符预期", n)
		}
	})
	t.Run("多字节字符不被切断", func(t *testing.T) {
		got := truncateProbeMessage(strings.Repeat("模", 600))
		if !utf8.ValidString(got) {
			t.Error("截断产生了非法 UTF-8 序列")
		}
	})
	t.Run("空消息", func(t *testing.T) {
		if got := truncateProbeMessage("   "); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func TestProbeBudget(t *testing.T) {
	origPerChannel := config.UpstreamModelProbeMaxPerChannel
	origPerRound := config.UpstreamModelProbeMaxPerRound
	origBudgetSecs := config.UpstreamModelProbeChannelBudgetSecs
	t.Cleanup(func() {
		config.UpstreamModelProbeMaxPerChannel = origPerChannel
		config.UpstreamModelProbeMaxPerRound = origPerRound
		config.UpstreamModelProbeChannelBudgetSecs = origBudgetSecs
	})

	t.Run("单渠道次数上限生效", func(t *testing.T) {
		config.UpstreamModelProbeMaxPerChannel = 3
		config.UpstreamModelProbeMaxPerRound = 100
		config.UpstreamModelProbeChannelBudgetSecs = 60
		resetProbeRoundBudget()
		b := newProbeBudget()
		for i := 0; i < 3; i++ {
			if !b.take() {
				t.Fatalf("第 %d 次 take 应成功", i+1)
			}
		}
		if b.take() {
			t.Error("超出单渠道上限后 take 应返回 false")
		}
	})

	t.Run("全局每轮上限跨渠道共享", func(t *testing.T) {
		config.UpstreamModelProbeMaxPerChannel = 10
		config.UpstreamModelProbeMaxPerRound = 2
		config.UpstreamModelProbeChannelBudgetSecs = 60
		resetProbeRoundBudget()
		b1, b2 := newProbeBudget(), newProbeBudget()
		if !b1.take() || !b2.take() {
			t.Fatal("前两次 take 应成功")
		}
		if b1.take() || b2.take() {
			t.Error("全局预算耗尽后所有渠道都应停止")
		}
		if left := upstreamProbeRoundBudget.Load(); left < 0 {
			t.Errorf("全局余额不应为负，实际 %d", left)
		}
	})

	t.Run("时间预算耗尽则停止", func(t *testing.T) {
		config.UpstreamModelProbeMaxPerChannel = 100
		config.UpstreamModelProbeMaxPerRound = 100
		resetProbeRoundBudget()
		b := newProbeBudget()
		b.channelDeadline = time.Now().Add(-time.Second) // 已过期
		if b.take() {
			t.Error("时间预算过期后 take 应返回 false")
		}
	})

	t.Run("确定性渠道级故障第一次就中止", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			code int
		}{
			{"401 key 无效", 401},
			{"403 无权限", 403},
			{"402 欠费", 402},
		} {
			t.Run(tc.name, func(t *testing.T) {
				b := newProbeBudget()
				b.noteResult(probeResult{StatusCode: tc.code})
				if !b.aborted {
					t.Errorf("状态码 %d 应第一次就中止本渠道", tc.code)
				}
				if b.abortReason == "" {
					t.Error("中止必须记录原因，否则日志无法排查")
				}
			})
		}
	})

	t.Run("429 与 503 不中止渠道", func(t *testing.T) {
		// 429 说明模型可用、渠道正常；503 是模型级信号，渠道本身正常。
		// 两者都不该拖累同渠道其他模型的探测。
		for _, code := range []int{429, 503} {
			b := newProbeBudget()
			for i := 0; i < 5; i++ {
				b.noteResult(probeResult{StatusCode: code})
			}
			if b.aborted {
				t.Errorf("状态码 %d 连续 5 次也不应中止渠道", code)
			}
		}
	})

	t.Run("5xx 与超时不中止渠道（由预算兜底）", func(t *testing.T) {
		for _, code := range []int{500, 502, 504, 0 /* 超时/网络错误 */} {
			b := newProbeBudget()
			for i := 0; i < 5; i++ {
				b.noteResult(probeResult{StatusCode: code})
			}
			if b.aborted {
				t.Errorf("状态码 %d 不应中止渠道，应由次数/时长预算兜底", code)
			}
		}
	})

	t.Run("中止后重复 noteResult 不覆盖原因", func(t *testing.T) {
		b := newProbeBudget()
		b.noteResult(probeResult{StatusCode: 401})
		first := b.abortReason
		b.noteResult(probeResult{StatusCode: 403})
		if b.abortReason != first {
			t.Errorf("中止原因被覆盖：%q → %q，应保留首个原因", first, b.abortReason)
		}
	})

	t.Run("中止后 take 一律失败", func(t *testing.T) {
		config.UpstreamModelProbeMaxPerChannel = 100
		config.UpstreamModelProbeMaxPerRound = 100
		resetProbeRoundBudget()
		b := newProbeBudget()
		b.aborted = true
		if b.take() {
			t.Error("已中止的渠道 take 应返回 false")
		}
	})
}

func TestUpstreamProbeEnabledFor(t *testing.T) {
	orig := config.UpstreamModelProbeEnabled
	t.Cleanup(func() { config.UpstreamModelProbeEnabled = orig })

	tests := []struct {
		name            string
		globalEnabled   bool
		channelDisabled bool
		want            bool
	}{
		{"全局关闭时一律不探", false, false, false},
		{"全局关闭且渠道也关闭", false, true, false},
		{"全局开启且渠道未关闭", true, false, true},
		{"全局开启但渠道单独关闭", true, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config.UpstreamModelProbeEnabled = tt.globalEnabled
			s := &config.ChannelOtherSettings{UpstreamModelProbeDisabled: tt.channelDisabled}
			if got := upstreamProbeEnabledFor(s); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProbeUnsupportedReason(t *testing.T) {
	tests := []struct {
		name        string
		channelType int
		modelName   string
		wantSkipped bool
	}{
		{"普通 chat 模型可探测", common.ChannelTypeOpenAI, "gpt-4o", false},
		{"embedding 模型跳过", common.ChannelTypeOpenAI, "text-embedding-3-small", true},
		{"tts 模型跳过", common.ChannelTypeOpenAI, "tts-1", true},
		{"视频模型跳过", common.ChannelTypeOpenAI, "sora-2", true},
		{"codex 模型跳过", common.ChannelTypeOpenAI, "gpt-5-codex", true},
		{"Kling 渠道整体跳过", common.ChannelTypeKeling, "gpt-4o", true},
		{"Flux 渠道整体跳过", common.ChannelTypeFlux, "gpt-4o", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := &model.Channel{Type: tt.channelType}
			reason := probeUnsupportedReason(ch, tt.modelName)
			if gotSkipped := reason != ""; gotSkipped != tt.wantSkipped {
				t.Errorf("reason=%q（skipped=%v），want skipped=%v", reason, gotSkipped, tt.wantSkipped)
			}
		})
	}
}

func TestIsModelNotFoundMessage(t *testing.T) {
	hits := []string{
		"model not found",
		"Model Not Found",
		"error: model_not_found",
		"The model 'x' does not exist",
		"no such model: foo",
		"Unknown model",
		"invalid model name",
		"unsupported model",
		"this model is not supported by the endpoint",
		"模型不存在",
		"不支持该模型",
		"未找到模型",
		"无此模型",
	}
	for _, msg := range hits {
		t.Run("命中/"+msg, func(t *testing.T) {
			if !isModelNotFoundMessage(msg) {
				t.Errorf("isModelNotFoundMessage(%q) = false, want true", msg)
			}
		})
	}

	misses := []string{
		"",
		"   ",
		"You do not have access to this model",
		"This model's maximum context length is 4096 tokens",
		"Rate limit reached for model gpt-4",
		"Incorrect API key provided",
		"insufficient_quota",
		"内容违规，已拦截",
		"upstream timeout",
		"the model is overloaded, please retry",
	}
	for _, msg := range misses {
		t.Run("不命中/"+msg, func(t *testing.T) {
			if isModelNotFoundMessage(msg) {
				t.Errorf("isModelNotFoundMessage(%q) = true, want false", msg)
			}
		})
	}
}

func TestNormalizeErrCode(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, ""},
		{"字符串小写化", "Model_Not_Found", "model_not_found"},
		{"字符串去空白", "  model_not_found  ", "model_not_found"},
		{"空字符串", "", ""},
		{"float64", float64(404), "404"},
		{"int", 404, "404"},
		{"bool", true, "true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeErrCode(tt.in); got != tt.want {
				t.Errorf("normalizeErrCode(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFilterByProbeVerdicts(t *testing.T) {
	// 处置表的全部 8 格
	cells := []struct {
		verdict     probeVerdict
		scene       string
		wantApprove bool
	}{
		{verdictAlive, probeScenePendingAdd, true},
		{verdictNotFound, probeScenePendingAdd, false},
		{verdictInconclusive, probeScenePendingAdd, false},
		{verdictSkipped, probeScenePendingAdd, true},

		{verdictAlive, probeScenePendingRemove, false},
		{verdictNotFound, probeScenePendingRemove, true},
		{verdictInconclusive, probeScenePendingRemove, false},
		{verdictSkipped, probeScenePendingRemove, true},
	}
	for _, c := range cells {
		t.Run(string(c.verdict)+"/"+c.scene, func(t *testing.T) {
			approved, held := filterByProbeVerdicts(
				[]string{"m"}, map[string]probeVerdict{"m": c.verdict}, c.scene,
			)
			if c.wantApprove {
				if !slices.Equal(approved, []string{"m"}) || len(held) != 0 {
					t.Errorf("approved=%v held=%v, want approved=[m] held=[]", approved, held)
				}
			} else {
				if len(approved) != 0 || !slices.Equal(held, []string{"m"}) {
					t.Errorf("approved=%v held=%v, want approved=[] held=[m]", approved, held)
				}
			}
		})
	}

	t.Run("verdicts 中缺失的模型按 inconclusive 暂缓", func(t *testing.T) {
		for _, scene := range []string{probeScenePendingAdd, probeScenePendingRemove} {
			approved, held := filterByProbeVerdicts(
				[]string{"known", "missing"},
				map[string]probeVerdict{"known": verdictSkipped},
				scene,
			)
			if !slices.Equal(approved, []string{"known"}) {
				t.Errorf("scene=%s approved=%v, want [known]", scene, approved)
			}
			if !slices.Equal(held, []string{"missing"}) {
				t.Errorf("scene=%s held=%v, want [missing]", scene, held)
			}
		}
	})

	t.Run("未知 scene 一律暂缓非 skipped 项", func(t *testing.T) {
		approved, held := filterByProbeVerdicts(
			[]string{"a", "b"},
			map[string]probeVerdict{"a": verdictAlive, "b": verdictNotFound},
			"bogus_scene",
		)
		if len(approved) != 0 {
			t.Errorf("approved=%v, want []", approved)
		}
		if !slices.Equal(held, []string{"a", "b"}) {
			t.Errorf("held=%v, want [a b]", held)
		}
	})

	t.Run("空输入返回空切片而非 nil", func(t *testing.T) {
		approved, held := filterByProbeVerdicts(nil, nil, probeScenePendingAdd)
		if approved == nil || held == nil {
			t.Errorf("approved=%v held=%v, want non-nil empty slices", approved, held)
		}
		if len(approved) != 0 || len(held) != 0 {
			t.Errorf("approved=%v held=%v, want empty", approved, held)
		}
	})

	t.Run("保留输入顺序", func(t *testing.T) {
		models := []string{"z", "a", "m"}
		verdicts := map[string]probeVerdict{"z": verdictAlive, "a": verdictAlive, "m": verdictAlive}
		approved, _ := filterByProbeVerdicts(models, verdicts, probeScenePendingAdd)
		if !slices.Equal(approved, models) {
			t.Errorf("approved=%v, want %v（须保留输入顺序）", approved, models)
		}
	})
}
