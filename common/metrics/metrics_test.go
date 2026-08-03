package metrics

import (
	"net/http"
	"strconv"
	"sync"
	"testing"

	"github.com/songquanpeng/one-api/common/config"
)

func TestClassifyReason(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		message    string
		want       string
	}{
		{"状态码缺失", 0, "", ReasonUnknown},
		{"无可用渠道", http.StatusServiceUnavailable, "no channels available", ReasonNoChannel},
		{"被限流", http.StatusTooManyRequests, "rate limit exceeded", ReasonRateLimited},
		{"上游 500", http.StatusInternalServerError, "internal error", ReasonUpstream5xx},
		{"上游 502", http.StatusBadGateway, "bad gateway", ReasonUpstream5xx},
		// 超时没有独立分类：RelayErrorHandler 会把它包成 504，到这里已无法区分
		{"超时被包成 504", http.StatusGatewayTimeout, "timeout", ReasonUpstream5xx},
		// content_filtered 必须优先于 auth_failed —— 403 同时承载两种语义
		{"403 内容违规", http.StatusForbidden, "Content violates usage guidelines", ReasonContentFilter},
		{"403 内容违规-另一种措辞", http.StatusForbidden, `{"safety_check_type":"x"}`, ReasonContentFilter},
		{"403 无权限", http.StatusForbidden, "permission denied", ReasonAuthFailed},
		{"401 key 失效", http.StatusUnauthorized, "invalid api key", ReasonAuthFailed},
		{"422 参数非法", http.StatusUnprocessableEntity, "unprocessable", ReasonParamInvalid},
		{"400 错误请求", http.StatusBadRequest, "bad request", ReasonBadRequest},
		{"其余 4xx", http.StatusNotFound, "not found", ReasonOther4xx},
		{"2xx 不该进来但要有确定行为", http.StatusOK, "", ReasonUnknown},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyReason(tc.statusCode, tc.message); got != tc.want {
				t.Errorf("ClassifyReason(%d, %q) = %q, want %q", tc.statusCode, tc.message, got, tc.want)
			}
		})
	}
}

// TestClassifyReasonIsClosedSet 守住"枚举是封闭集合"这个前提。
// 一旦有人让 ClassifyReason 直接返回上游字符串，基数就会失控并打爆 Prometheus，
// 这个测试是那种改动的第一道拦截。
func TestClassifyReasonIsClosedSet(t *testing.T) {
	allowed := map[string]bool{
		ReasonNoChannel: true, ReasonRateLimited: true, ReasonUpstream5xx: true,
		ReasonContentFilter: true, ReasonAuthFailed: true, ReasonParamInvalid: true,
		ReasonBadRequest: true, ReasonOther4xx: true, ReasonUnknown: true,
	}
	// 遍历全部可能的状态码，配上会干扰分类的消息
	for code := 0; code < 600; code++ {
		for _, msg := range []string{"", "violates usage guidelines", "随机消息 " + strconv.Itoa(code)} {
			if got := ClassifyReason(code, msg); !allowed[got] {
				t.Fatalf("ClassifyReason(%d, %q) 返回了枚举外的值 %q", code, msg, got)
			}
		}
	}
}

func TestCodeClass(t *testing.T) {
	cases := map[int]string{100: "other", 200: "2xx", 204: "2xx", 301: "3xx", 404: "4xx", 429: "4xx", 500: "5xx", 503: "5xx"}
	for code, want := range cases {
		if got := CodeClass(code); got != want {
			t.Errorf("CodeClass(%d) = %q, want %q", code, got, want)
		}
	}
}

// resetCardinality 清空守卫状态，让每个用例独立。
func resetCardinality() {
	modelSeen = sync.Map{}
	modelSeenCount.Store(0)
}

func TestSanitizeModelPassesThroughUnderLimit(t *testing.T) {
	resetCardinality()
	defer resetCardinality()
	config.MetricsMaxModelLabels = 500

	for _, name := range []string{"gpt-4o", "claude-sonnet-4-5", "gemini-2.5-pro"} {
		if got := SanitizeModel(name); got != name {
			t.Errorf("SanitizeModel(%q) = %q, 期望原样返回", name, got)
		}
	}
	if n := modelSeenCount.Load(); n != 3 {
		t.Errorf("已记录模型数 = %d, want 3", n)
	}
	// 重复调用不应重复计数
	SanitizeModel("gpt-4o")
	if n := modelSeenCount.Load(); n != 3 {
		t.Errorf("重复调用后模型数 = %d, want 3（不应重复累加）", n)
	}
}

func TestSanitizeModelEmptyAndOverlong(t *testing.T) {
	resetCardinality()
	defer resetCardinality()
	config.MetricsMaxModelLabels = 500

	if got := SanitizeModel(""); got != labelUnset {
		t.Errorf("空模型名 = %q, want %q", got, labelUnset)
	}
	overlong := make([]byte, maxLabelLen+1)
	for i := range overlong {
		overlong[i] = 'a'
	}
	if got := SanitizeModel(string(overlong)); got != labelOverflow {
		t.Errorf("超长模型名 = %q, want %q", got, labelOverflow)
	}
	// 超长名不应占用配额
	if n := modelSeenCount.Load(); n != 0 {
		t.Errorf("超长名占用了配额，modelSeenCount = %d, want 0", n)
	}
}

// TestSanitizeModelEnforcesLimit 是本文件最重要的用例：它证明基数守卫真的会封顶。
// 对应的攻击场景是 middleware/distributor.go 的 503 分支 —— 那里的模型名来自
// 用户请求体且未经 abilities 校验，攻击者连发随机名即可无限造序列。
func TestSanitizeModelEnforcesLimit(t *testing.T) {
	resetCardinality()
	defer resetCardinality()
	config.MetricsMaxModelLabels = 50
	defer func() { config.MetricsMaxModelLabels = 500 }()

	distinct := map[string]bool{}
	for i := 0; i < 500; i++ {
		distinct[SanitizeModel("attacker-model-"+strconv.Itoa(i))] = true
	}

	// 50 个放行 + 1 个 __other__ 归并值
	if len(distinct) != 51 {
		t.Errorf("不同 label 值数量 = %d, want 51（50 个上限 + __other__）", len(distinct))
	}
	if !distinct[labelOverflow] {
		t.Errorf("超限后未归并到 %q", labelOverflow)
	}
	if n := modelSeenCount.Load(); n != 50 {
		t.Errorf("modelSeenCount = %d, want 50（不应超过上限）", n)
	}
}

// TestSanitizeModelConcurrent 守卫在并发下也不能超配额 ——
// 真实场景里 503 请求是并发打进来的。
func TestSanitizeModelConcurrent(t *testing.T) {
	resetCardinality()
	defer resetCardinality()
	config.MetricsMaxModelLabels = 100
	defer func() { config.MetricsMaxModelLabels = 500 }()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				SanitizeModel("m-" + strconv.Itoa(base*100+j))
			}
		}(i)
	}
	wg.Wait()

	if n := modelSeenCount.Load(); n > 100 {
		t.Errorf("并发下 modelSeenCount = %d, 超过了上限 100", n)
	}
}

// TestLatencyBucketsFallback 桶边界配置解析错误时必须回落到默认值，
// 不能让一个笔误导致直方图退化成单桶。
func TestLatencyBucketsFallback(t *testing.T) {
	original := config.MetricsLatencyBuckets
	defer func() { config.MetricsLatencyBuckets = original }()

	config.MetricsLatencyBuckets = ""
	if got := latencyBuckets(); len(got) != len(defaultLatencyBuckets) {
		t.Errorf("空配置未回落到默认值，got %v", got)
	}

	config.MetricsLatencyBuckets = "0.5,不是数字,2"
	if got := latencyBuckets(); len(got) != len(defaultLatencyBuckets) {
		t.Errorf("非法配置未回落到默认值，got %v", got)
	}

	config.MetricsLatencyBuckets = "1,2,3"
	if got := latencyBuckets(); len(got) != 3 || got[2] != 3 {
		t.Errorf("合法配置解析错误，got %v", got)
	}
}

// TestLatencyBucketsCoverStreamingTimeout 桶上界必须能覆盖长流式请求。
// 若上界退回 30s，>30s 的请求会全部落进 +Inf，P95/P99 变成最后一桶内的线性插值 = 编的。
func TestLatencyBucketsCoverStreamingTimeout(t *testing.T) {
	last := defaultLatencyBuckets[len(defaultLatencyBuckets)-1]
	if last < 300 {
		t.Errorf("延迟桶上界 %.0fs 太小：生产 STREAMING_TIMEOUT=600，长流式请求会全落进 +Inf 桶", last)
	}
	// 前 7 个必须与 model.LatencyBoundaries 对齐，否则两侧 P95 无法比较
	wantPrefix := []float64{0.5, 1, 2, 3, 5, 10, 30}
	for i, w := range wantPrefix {
		if defaultLatencyBuckets[i] != w {
			t.Errorf("第 %d 个桶边界 = %v, want %v（必须与 model.LatencyBoundaries 对齐）", i, defaultLatencyBuckets[i], w)
		}
	}
}
