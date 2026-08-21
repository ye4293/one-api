package billship

import "time"

// batcher 从 in 攒批：满 batchSize / 超 wait / 聚合将超 256KB 即切批，送 out。
// 收到 stop 后 drain in 中剩余记录、flush、close(out)。channel 由 Shipper 注入。
type batcher struct {
	in        <-chan Record
	out       chan<- []Record
	batchSize int
	wait      time.Duration
	stop      <-chan struct{}
	abort     <-chan struct{} // 硬停机：Shutdown 超时后触发，解除 flush 对满 out 的阻塞
	onDiscard func([]Record)  // 硬停机丢弃在手批时回调（记日志/计数，绝不静默丢）；可为 nil
}

func (b *batcher) run() {
	defer close(b.out)

	var batch []Record
	var size int

	// flush 送批到 out：正常时阻塞即背压；硬停机(abort)时把在手批交 onDiscard 记录后返回 false。
	// abort 为 nil（如单测未注入）时该分支永不命中，行为与纯阻塞发送一致。
	flush := func() bool {
		if len(batch) == 0 {
			return true
		}
		select {
		case b.out <- batch:
			batch = nil
			size = 0
			return true
		case <-b.abort:
			if b.onDiscard != nil {
				b.onDiscard(batch)
			}
			return false
		}
	}

	// add 把一条记录并入当前批，必要时先切批；返回 false 表示已被硬停机中断。
	add := func(r Record) bool {
		rs := recordSize(r)
		if len(batch) > 0 && (len(batch) >= b.batchSize || size+rs > maxMessageBytes) {
			if !flush() {
				return false
			}
		}
		batch = append(batch, r)
		size += rs
		if len(batch) >= b.batchSize {
			return flush()
		}
		return true
	}

	timer := time.NewTimer(b.wait)
	defer timer.Stop()

	for {
		select {
		case r := <-b.in:
			if !add(r) {
				return
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(b.wait)
		case <-timer.C:
			if !flush() {
				return
			}
			timer.Reset(b.wait)
		case <-b.stop:
			// drain 缓冲区剩余，再 flush，退出。
			for {
				select {
				case r := <-b.in:
					if !add(r) {
						return
					}
				default:
					flush()
					return
				}
			}
		}
	}
}
