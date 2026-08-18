package billship

import "sync/atomic"

// Snapshot 是对外暴露的计数快照，供网关打点 / 告警。
type Snapshot struct {
	Enqueued     int64 // 成功入队数
	Dropped      int64 // buffer 满丢弃数
	Sent         int64 // 最终成功发送条数
	SendFailures int64 // 终态失败条数（已记失败日志）
	Retries      int64 // 累计重试条次（含部分成功补发）
	Invalid      int64 // 入队前校验失败跳过数
	InFlight     int64 // 当前在 worker 手里发送中的条数
	BatchesSent  int64 // SendMessageBatch 成功调用次数
}

type stats struct {
	enqueued     atomic.Int64
	dropped      atomic.Int64
	sent         atomic.Int64
	sendFailures atomic.Int64
	retries      atomic.Int64
	invalid      atomic.Int64
	inFlight     atomic.Int64
	batchesSent  atomic.Int64
}

func (s *stats) snapshot() Snapshot {
	return Snapshot{
		Enqueued:     s.enqueued.Load(),
		Dropped:      s.dropped.Load(),
		Sent:         s.sent.Load(),
		SendFailures: s.sendFailures.Load(),
		Retries:      s.retries.Load(),
		Invalid:      s.invalid.Load(),
		InFlight:     s.inFlight.Load(),
		BatchesSent:  s.batchesSent.Load(),
	}
}
