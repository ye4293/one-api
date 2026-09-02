package billship

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	smithy "github.com/aws/smithy-go"
)

// batchAPI 抽出 SendMessageBatch 便于单测注入 fake；*sqs.Client 天然满足。
type batchAPI interface {
	SendMessageBatch(ctx context.Context, in *sqs.SendMessageBatchInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageBatchOutput, error)
}

type sender struct {
	api        batchAPI
	queueURL   string
	timeout    time.Duration
	maxRetries int
	st         *stats
	fl         *failLogger
	stopCtx    context.Context     // 硬停机信号：取消在途 HTTP + 中断退避重试
	sleep      func(time.Duration) // 可注入；默认按 stopCtx 可中断
}

// errShutdownAborted 标记因硬停机而中断的在途重试（记入失败日志供人工补数据）。
var errShutdownAborted = errors.New("billship: send aborted by shutdown")

func newSender(api batchAPI, cfg Config, st *stats, fl *failLogger, stopCtx context.Context) *sender {
	s := &sender{
		api:        api,
		queueURL:   cfg.QueueURL,
		timeout:    cfg.SendTimeout,
		maxRetries: cfg.MaxRetries,
		st:         st,
		fl:         fl,
		stopCtx:    stopCtx,
	}
	// 默认退避可被硬停机中断，避免 Shutdown 超时后 worker 仍卡在 sleep 里。
	s.sleep = func(d time.Duration) {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-t.C:
		case <-stopCtx.Done():
		}
	}
	return s
}

// send 发一批（≤10 条）：整批可重试错误退避重试；部分成功只补发 Failed 条；
// 终态失败逐条打详细日志并计数。「尽力不丢」的核心。
func (s *sender) send(batch []Record) {
	pending := batch
	for attempt := 0; ; attempt++ {
		entries := make([]types.SendMessageBatchRequestEntry, len(pending))
		for i, r := range pending {
			entries[i] = buildEntry(strconv.Itoa(i), r)
		}

		ctx, cancel := context.WithTimeout(s.stopCtx, s.timeout)
		out, err := s.api.SendMessageBatch(ctx, &sqs.SendMessageBatchInput{
			QueueUrl: aws.String(s.queueURL),
			Entries:  entries,
		})
		cancel()

		// 整批级错误。
		if err != nil {
			// 硬停机中：不再重试（HTTP ctx 已随 stopCtx 取消），逐条记失败日志交人工补。
			if s.stopCtx.Err() != nil {
				s.failAll(pending, attempt+1, err)
				return
			}
			if isRetryable(err) && attempt < s.maxRetries {
				s.st.retries.Add(int64(len(pending)))
				if !s.waitBackoff(backoff(attempt)) {
					s.failAll(pending, attempt+1, err)
					return
				}
				continue
			}
			s.failAll(pending, attempt+1, err)
			return
		}

		s.st.sent.Add(int64(len(out.Successful)))
		s.st.batchesSent.Add(1)
		if len(out.Failed) == 0 {
			return
		}

		// 部分成功：只收集 Failed 条。SenderFault=true → 客户端错误不可重试。
		var next []Record
		for _, f := range out.Failed {
			idx, convErr := strconv.Atoi(aws.ToString(f.Id))
			if convErr != nil || idx < 0 || idx >= len(pending) {
				// 防御：Id 无法映射回具体记录（不应发生）。无法定位就无法补数据，
				// 但绝不静默吞——记一条告警并计失败，保住 Sent+SendFailures 的账。
				s.fl.log(reasonSendFailed, Record{}, attempt+1,
					fmt.Errorf("unmappable failed id %q: %s: %s",
						aws.ToString(f.Id), aws.ToString(f.Code), aws.ToString(f.Message)))
				s.st.sendFailures.Add(1)
				continue
			}
			cause := fmt.Errorf("%s: %s", aws.ToString(f.Code), aws.ToString(f.Message))
			if !f.SenderFault && attempt < s.maxRetries {
				next = append(next, pending[idx])
			} else {
				s.fl.log(reasonSendFailed, pending[idx], attempt+1, cause)
				s.st.sendFailures.Add(1)
			}
		}
		if len(next) == 0 {
			return
		}
		s.st.retries.Add(int64(len(next)))
		if !s.waitBackoff(backoff(attempt)) {
			s.failAll(next, attempt+1, errShutdownAborted)
			return
		}
		pending = next
	}
}

// waitBackoff 退避等待；被硬停机中断则返回 false（调用方把在途条目转终态失败）。
func (s *sender) waitBackoff(d time.Duration) bool {
	if s.stopCtx.Err() != nil {
		return false
	}
	s.sleep(d)
	return s.stopCtx.Err() == nil
}

func (s *sender) failAll(pending []Record, attempts int, cause error) {
	for _, r := range pending {
		s.fl.log(reasonSendFailed, r, attempts, cause)
	}
	s.st.sendFailures.Add(int64(len(pending)))
}

// isRetryable：限流 / 服务端故障 / 网络与超时可重试；明确的客户端错误不可重试。
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "RequestThrottled", "ThrottlingException", "Throttling",
			"ServiceUnavailable", "InternalError", "InternalFailure":
			return true
		case "AccessDenied", "AccessDeniedException",
			"InvalidParameterValue", "MissingParameter",
			"AWS.SimpleQueueService.NonExistentQueue", "QueueDoesNotExist":
			return false
		}
		return apiErr.ErrorFault() == smithy.FaultServer
	}
	// 非 API 错误（网络 / 超时 / 连接重置）→ 可重试。
	return true
}

// backoff：指数退避 + 抖动。attempt 从 0 起。
func backoff(attempt int) time.Duration {
	// 钳制移位指数：1<<6 * 100ms = 6.4s 已越过 5s 上限，再大只会让 1<<attempt
	// 溢出成负 Duration，导致上限判断失效、rand.Int63n 收到负参 panic。
	if attempt > 6 {
		attempt = 6
	}
	base := time.Duration(1<<uint(attempt)) * 100 * time.Millisecond
	if base > 5*time.Second {
		base = 5 * time.Second
	}
	jitter := time.Duration(rand.Int63n(int64(base/2) + 1))
	return base + jitter
}
