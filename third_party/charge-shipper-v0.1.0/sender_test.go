package billship

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	smithy "github.com/aws/smithy-go"
)

// fakeAPI 是可编排的 batchAPI：按调用序返回预设结果。
type fakeAPI struct {
	calls   int
	results []func(in *sqs.SendMessageBatchInput) (*sqs.SendMessageBatchOutput, error)
}

func (f *fakeAPI) SendMessageBatch(_ context.Context, in *sqs.SendMessageBatchInput, _ ...func(*sqs.Options)) (*sqs.SendMessageBatchOutput, error) {
	i := f.calls
	f.calls++
	if i >= len(f.results) {
		i = len(f.results) - 1
	}
	return f.results[i](in)
}

// allOK 返回「全部成功」输出。
func allOK(in *sqs.SendMessageBatchInput) (*sqs.SendMessageBatchOutput, error) {
	out := &sqs.SendMessageBatchOutput{}
	for _, e := range in.Entries {
		out.Successful = append(out.Successful, types.SendMessageBatchResultEntry{Id: e.Id})
	}
	return out, nil
}

func newTestSender(api batchAPI) (*sender, *capture) {
	cap := &capture{}
	st := &stats{}
	fl := newFailLogger(cap.emit, false)
	s := newSender(api, Config{QueueURL: "q", MaxRetries: 3}, st, fl, context.Background())
	s.sleep = func(time.Duration) {} // 测试不真睡
	return s, cap
}

func recs(n int) []Record {
	out := make([]Record, n)
	for i := range out {
		out[i] = Record{SiteID: "s", Model: "m", SourceType: "new-api", LogID: int64(i), Body: []byte("{}")}
	}
	return out
}

func TestSendAllSuccess(t *testing.T) {
	api := &fakeAPI{results: []func(*sqs.SendMessageBatchInput) (*sqs.SendMessageBatchOutput, error){allOK}}
	s, _ := newTestSender(api)
	s.send(recs(3))
	if got := s.st.sent.Load(); got != 3 {
		t.Errorf("Sent = %d, want 3", got)
	}
	if got := s.st.batchesSent.Load(); got != 1 {
		t.Errorf("BatchesSent = %d, want 1", got)
	}
	if api.calls != 1 {
		t.Errorf("api calls = %d, want 1", api.calls)
	}
}

// alwaysPartialFail 让每次调用都把第一个 entry 报为可重试失败（server fault），其余成功。
// 用于编排「部分成功 → 重试同一失败条」持续到重试耗尽。
func alwaysPartialFail(in *sqs.SendMessageBatchInput) (*sqs.SendMessageBatchOutput, error) {
	out := &sqs.SendMessageBatchOutput{}
	for i, e := range in.Entries {
		if i == 0 {
			out.Failed = append(out.Failed, types.BatchResultErrorEntry{
				Id: e.Id, Code: aws.String("InternalError"), Message: aws.String("boom"), SenderFault: false,
			})
		} else {
			out.Successful = append(out.Successful, types.SendMessageBatchResultEntry{Id: e.Id})
		}
	}
	return out, nil
}

// TestSendPartialFailureRetriesExhausted 部分成功后同一条持续失败，重试耗尽（attempt 达 MaxRetries）
// 必须转终态失败，绝不无限重试。对照乐观路径 TestSendPartialFailureRetriesOnlyFailed（第二次成功）。
func TestSendPartialFailureRetriesExhausted(t *testing.T) {
	// 每次调用都固定失败一条 → 初次 + MaxRetries 次重试后计入终态失败。
	api := &fakeAPI{results: []func(*sqs.SendMessageBatchInput) (*sqs.SendMessageBatchOutput, error){alwaysPartialFail}}
	s, cap := newTestSender(api) // MaxRetries=3
	s.send(recs(2))

	// 1 初次 + 3 重试（每次只补发那一条失败）= 4 次 API 调用。
	if api.calls != 4 {
		t.Errorf("api calls = %d, want 4 (initial + 3 retries)", api.calls)
	}
	// 一条始终失败 → 终态计 1 次失败；另一条初次即成功。
	if got := s.st.sendFailures.Load(); got != 1 {
		t.Errorf("SendFailures = %d, want 1", got)
	}
	if got := s.st.sent.Load(); got != 1 {
		t.Errorf("Sent = %d, want 1 (the non-failing entry, counted once)", got)
	}
	if len(cap.snapshot()) == 0 {
		t.Error("expected a terminal failure detail log after retries exhausted")
	}
}

// TestSendPartialFailureAbortedByShutdownDuringBackoff 部分成功后、退避等待期间遭遇硬停机：
// 注入的 sleep 在等待中 cancel stopCtx → waitBackoff 返回 false → 走 failAll(errShutdownAborted) 终态。
func TestSendPartialFailureAbortedByShutdownDuringBackoff(t *testing.T) {
	api := &fakeAPI{results: []func(*sqs.SendMessageBatchInput) (*sqs.SendMessageBatchOutput, error){alwaysPartialFail}}
	ctx, cancel := context.WithCancel(context.Background())
	cap := &capture{}
	st := &stats{}
	s := newSender(api, Config{QueueURL: "q", MaxRetries: 3}, st, newFailLogger(cap.emit, false), ctx)
	// 模拟退避等待期间收到硬停机信号：sleep 内 cancel，使 waitBackoff 返回 false。
	s.sleep = func(time.Duration) { cancel() }

	s.send(recs(2))

	// 初次调用后进入退避 → sleep 中断退避 → 不再发起第二次调用。
	if api.calls != 1 {
		t.Errorf("api calls = %d, want 1 (backoff aborted before retry)", api.calls)
	}
	// 被中断的在途失败条转终态失败（errShutdownAborted）。
	if got := st.sendFailures.Load(); got != 1 {
		t.Errorf("SendFailures = %d, want 1", got)
	}
	if got := st.sent.Load(); got != 1 {
		t.Errorf("Sent = %d, want 1", got)
	}
	if len(cap.snapshot()) == 0 {
		t.Error("expected a failure log for shutdown-aborted in-flight entry")
	}
}

func TestSendPartialFailureRetriesOnlyFailed(t *testing.T) {
	// 第 1 次：entry "1" 服务端故障失败（可重试）；第 2 次：全成功。
	first := func(in *sqs.SendMessageBatchInput) (*sqs.SendMessageBatchOutput, error) {
		out := &sqs.SendMessageBatchOutput{}
		for _, e := range in.Entries {
			if aws.ToString(e.Id) == "1" {
				out.Failed = append(out.Failed, types.BatchResultErrorEntry{
					Id: e.Id, Code: aws.String("InternalError"), Message: aws.String("boom"), SenderFault: false,
				})
			} else {
				out.Successful = append(out.Successful, types.SendMessageBatchResultEntry{Id: e.Id})
			}
		}
		return out, nil
	}
	var secondEntries int
	second := func(in *sqs.SendMessageBatchInput) (*sqs.SendMessageBatchOutput, error) {
		secondEntries = len(in.Entries)
		return allOK(in)
	}
	api := &fakeAPI{results: []func(*sqs.SendMessageBatchInput) (*sqs.SendMessageBatchOutput, error){first, second}}
	s, _ := newTestSender(api)
	s.send(recs(3))

	if secondEntries != 1 {
		t.Errorf("retry batch size = %d, want 1 (only failed entry)", secondEntries)
	}
	if got := s.st.sent.Load(); got != 3 {
		t.Errorf("Sent = %d, want 3", got)
	}
	if got := s.st.retries.Load(); got != 1 {
		t.Errorf("Retries = %d, want 1", got)
	}
}

func TestSendPartialFailureSenderFaultNoRetry(t *testing.T) {
	// SenderFault=true → 客户端错误 → 不重试，直接终态失败。
	first := func(in *sqs.SendMessageBatchInput) (*sqs.SendMessageBatchOutput, error) {
		out := &sqs.SendMessageBatchOutput{}
		out.Failed = append(out.Failed, types.BatchResultErrorEntry{
			Id: in.Entries[0].Id, Code: aws.String("InvalidParameterValue"), Message: aws.String("bad"), SenderFault: true,
		})
		return out, nil
	}
	api := &fakeAPI{results: []func(*sqs.SendMessageBatchInput) (*sqs.SendMessageBatchOutput, error){first}}
	s, cap := newTestSender(api)
	s.send(recs(1))

	if api.calls != 1 {
		t.Errorf("api calls = %d, want 1 (no retry on sender fault)", api.calls)
	}
	if got := s.st.sendFailures.Load(); got != 1 {
		t.Errorf("SendFailures = %d, want 1", got)
	}
	if len(cap.snapshot()) == 0 {
		t.Error("expected a failure detail log")
	}
}

func TestSendWholeBatchRetryableThenExhausted(t *testing.T) {
	throttled := func(*sqs.SendMessageBatchInput) (*sqs.SendMessageBatchOutput, error) {
		return nil, &smithy.GenericAPIError{Code: "RequestThrottled", Message: "slow down", Fault: smithy.FaultServer}
	}
	api := &fakeAPI{results: []func(*sqs.SendMessageBatchInput) (*sqs.SendMessageBatchOutput, error){throttled}}
	s, _ := newTestSender(api) // MaxRetries=3
	s.send(recs(2))

	if api.calls != 4 { // 1 初次 + 3 重试
		t.Errorf("api calls = %d, want 4", api.calls)
	}
	if got := s.st.sendFailures.Load(); got != 2 {
		t.Errorf("SendFailures = %d, want 2", got)
	}
}

func TestSendWholeBatchNonRetryable(t *testing.T) {
	denied := func(*sqs.SendMessageBatchInput) (*sqs.SendMessageBatchOutput, error) {
		return nil, &smithy.GenericAPIError{Code: "AccessDenied", Message: "no", Fault: smithy.FaultClient}
	}
	api := &fakeAPI{results: []func(*sqs.SendMessageBatchInput) (*sqs.SendMessageBatchOutput, error){denied}}
	s, _ := newTestSender(api)
	s.send(recs(2))

	if api.calls != 1 {
		t.Errorf("api calls = %d, want 1 (non-retryable)", api.calls)
	}
	if got := s.st.sendFailures.Load(); got != 2 {
		t.Errorf("SendFailures = %d, want 2", got)
	}
}

func TestBackoffNoOverflowPanic(t *testing.T) {
	// 大 attempt（误配高 MaxRetries）不得让 1<<attempt 溢出成负 Duration → rand.Int63n panic。
	for _, attempt := range []int{0, 6, 33, 40, 62, 63} {
		d := backoff(attempt) // 不 panic 即通过
		if d < 0 {
			t.Errorf("backoff(%d) = %v, want >= 0", attempt, d)
		}
		if d > 5*time.Second+5*time.Second/2 { // 上限 5s + 最大抖动 base/2
			t.Errorf("backoff(%d) = %v, exceeds cap", attempt, d)
		}
	}
}

func TestSendAbortsRetryOnShutdown(t *testing.T) {
	// stopCtx 取消后：不再重试，剩余在途条目转终态失败并记日志。
	throttled := func(*sqs.SendMessageBatchInput) (*sqs.SendMessageBatchOutput, error) {
		return nil, &smithy.GenericAPIError{Code: "RequestThrottled", Message: "slow", Fault: smithy.FaultServer}
	}
	api := &fakeAPI{results: []func(*sqs.SendMessageBatchInput) (*sqs.SendMessageBatchOutput, error){throttled}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 已处于硬停机
	cap := &capture{}
	st := &stats{}
	s := newSender(api, Config{QueueURL: "q", MaxRetries: 3}, st, newFailLogger(cap.emit, false), ctx)
	s.sleep = func(time.Duration) {}

	s.send(recs(2))

	if api.calls != 1 {
		t.Errorf("api calls = %d, want 1 (no retry after shutdown)", api.calls)
	}
	if got := st.sendFailures.Load(); got != 2 {
		t.Errorf("SendFailures = %d, want 2", got)
	}
}

func TestSendUnmappableFailedIdCounted(t *testing.T) {
	// Failed.Id 无法映射回记录（不应发生）时：不静默吞，记日志 + 计失败。
	resp := func(in *sqs.SendMessageBatchInput) (*sqs.SendMessageBatchOutput, error) {
		out := &sqs.SendMessageBatchOutput{}
		out.Successful = append(out.Successful, types.SendMessageBatchResultEntry{Id: in.Entries[0].Id})
		out.Failed = append(out.Failed, types.BatchResultErrorEntry{
			Id: aws.String("bogus"), Code: aws.String("X"), Message: aws.String("y"), SenderFault: false,
		})
		return out, nil
	}
	api := &fakeAPI{results: []func(*sqs.SendMessageBatchInput) (*sqs.SendMessageBatchOutput, error){resp}}
	s, cap := newTestSender(api)
	s.send(recs(2))

	if got := api.calls; got != 1 {
		t.Errorf("api calls = %d, want 1 (unmappable id not retried)", got)
	}
	if got := s.st.sent.Load(); got != 1 {
		t.Errorf("Sent = %d, want 1", got)
	}
	if got := s.st.sendFailures.Load(); got != 1 {
		t.Errorf("SendFailures = %d, want 1 (unmappable id counted)", got)
	}
	if len(cap.snapshot()) == 0 {
		t.Error("expected a failure log for unmappable id")
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"throttle", &smithy.GenericAPIError{Code: "RequestThrottled", Fault: smithy.FaultServer}, true},
		{"server fault", &smithy.GenericAPIError{Code: "InternalError", Fault: smithy.FaultServer}, true},
		{"access denied", &smithy.GenericAPIError{Code: "AccessDenied", Fault: smithy.FaultClient}, false},
		{"invalid param", &smithy.GenericAPIError{Code: "InvalidParameterValue", Fault: smithy.FaultClient}, false},
		{"plain network err", errors.New("dial tcp: timeout"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryable(tt.err); got != tt.want {
				t.Errorf("isRetryable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
	_ = strconv.Itoa // 保留 strconv import 提示（send 内使用）
}
