package billship

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"

	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

// Shipper 是发送运行时：Ship 非阻塞入队 → Batcher 攒批 → worker 池 sendBatch。
type Shipper struct {
	cfg     Config
	ch      chan Record
	batchCh chan []Record

	st        *stats
	fl        *failLogger
	sendBatch func([]Record) // 由 Init 设为 sender.send；测试可注入 fake

	stopBatcher chan struct{}
	closed      atomic.Bool

	hardStop   context.Context // Shutdown 超时后取消：中断在途 send 重试/HTTP 与 batcher 阻塞 flush
	hardCancel context.CancelFunc

	batcherWG sync.WaitGroup
	workerWG  sync.WaitGroup
	stopOnce  sync.Once
}

func newShipper(cfg Config) *Shipper {
	hardCtx, hardCancel := context.WithCancel(context.Background())
	return &Shipper{
		cfg:         cfg,
		ch:          make(chan Record, cfg.BufferSize),
		batchCh:     make(chan []Record, cfg.SendConcurrency),
		st:          &stats{},
		fl:          newFailLogger(cfg.Logger, cfg.LogFailedBody),
		stopBatcher: make(chan struct{}),
		hardStop:    hardCtx,
		hardCancel:  hardCancel,
	}
}

func (s *Shipper) start() {
	s.batcherWG.Add(1)
	go func() {
		defer s.batcherWG.Done()
		b := &batcher{
			in:        s.ch,
			out:       s.batchCh,
			batchSize: s.cfg.BatchSize,
			wait:      s.cfg.BatchWait,
			stop:      s.stopBatcher,
			abort:     s.hardStop.Done(),
			onDiscard: s.discard, // 硬停机丢弃在手批时不静默丢
		}
		b.run() // run 结束时 close(s.batchCh)
	}()

	for i := 0; i < s.cfg.SendConcurrency; i++ {
		s.workerWG.Add(1)
		go s.runWorker()
	}
}

func (s *Shipper) runWorker() {
	defer s.workerWG.Done()
	for batch := range s.batchCh {
		s.st.inFlight.Add(int64(len(batch)))
		s.sendBatch(batch)
		s.st.inFlight.Add(-int64(len(batch)))
	}
}

// Ship 非阻塞投递：校验失败 → Invalid + 日志；buffer 满 → Dropped + 日志。永不阻塞/panic。
func (s *Shipper) Ship(r Record) {
	if !s.cfg.Enabled || s.closed.Load() {
		return
	}
	if err := validate(r); err != nil {
		s.st.invalid.Add(1)
		reason := reasonInvalid
		s.fl.log(reason, r, 0, err)
		return
	}
	select {
	case s.ch <- r:
		s.st.enqueued.Add(1)
	default:
		s.st.dropped.Add(1)
		s.fl.log(reasonDropped, r, 0, nil)
	}
}

// Shutdown：关入口（Ship 变 no-op）→ 通知 Batcher drain 剩余并 close(batchCh)
// → 等 worker 排空。ctx 超时按时返回并记「未发完 N 条」。
// Shutdown 后单例仍保留（不清除）使 Stats() 可读取最终的发送/丢弃/失败计数；同进程内无法重新初始化。
func (s *Shipper) Shutdown(ctx context.Context) error {
	s.closed.Store(true)
	s.stopOnce.Do(func() { close(s.stopBatcher) })

	done := make(chan struct{})
	go func() {
		s.batcherWG.Wait() // batcher drain + close(batchCh)
		s.workerWG.Wait()  // worker 排空 batchCh
		close(done)
	}()

	select {
	case <-done:
		s.hardCancel()      // 全部完成，释放 hardStop（无副作用）
		s.drainStragglers() // 收尾竞态残留：Ship 过检后才入队、batcher 已退出的那几条
		s.fl.flush()        // 输出最后一段被抑制的失败聚合行
		return nil
	case <-ctx.Done():
		s.hardCancel() // 中断在途 send 的重试/HTTP 与 batcher 阻塞 flush，避免 goroutine 泄漏
		undelivered := s.st.inFlight.Load() +
			int64(len(s.ch)) +
			int64(len(s.batchCh))*int64(s.cfg.BatchSize) // over-estimate; safe (never receives from batchCh)
		s.cfg.Logger("error", "billship shutdown timeout", "undelivered", undelivered)
		s.fl.flush()
		return ctx.Err()
	}
}

// drainStragglers 清理极小概率竞态残留：Ship 通过 closed 检查后被调度走、
// 直到 batcher 已 drain 退出才把记录塞进 ch 的那几条——此时无接收者。
// 转成 Dropped + 失败日志（供人工补数据），绝不静默丢。仅在 batcher 确认退出（done）后调。
func (s *Shipper) drainStragglers() {
	for {
		select {
		case r := <-s.ch:
			s.discard([]Record{r})
		default:
			return
		}
	}
}

// discard 把未能投递的记录转 Dropped + 失败日志（供人工按 log_id 补数据），绝不静默丢。
// 用于硬停机丢弃在手批（batcher.onDiscard）与停机收尾竞态残留（drainStragglers）。
func (s *Shipper) discard(rs []Record) {
	for _, r := range rs {
		s.st.dropped.Add(1)
		s.fl.log(reasonDropped, r, 0, nil)
	}
}

func (s *Shipper) snapshot() Snapshot { return s.st.snapshot() }

// —— 包级默认单例（网关的便捷入口）——

var (
	defShip atomic.Pointer[Shipper]
	initMu  sync.Mutex
)

// Init 初始化默认单例。缺 QueueURL/Region 报错；重复 Init 报错。
// Init 仅可调用一次；Init 后（即使经历 Shutdown）再次调用 Init 会返回错误。Shutdown 后不支持重新初始化，需进程重启。
func Init(cfg Config) error {
	initMu.Lock()
	defer initMu.Unlock()
	if defShip.Load() != nil {
		return errors.New("billship: already initialized")
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return err
	}
	client, err := newClient(context.Background(), cfg)
	if err != nil {
		return err
	}
	sh := newShipper(cfg)
	snd := newSender(client, cfg, sh.st, sh.fl, sh.hardStop)
	sh.sendBatch = snd.send
	sh.start()
	defShip.Store(sh)
	return nil
}

// Ship 委托单例；未 Init 时安全 no-op。热路径，无锁。
func Ship(r Record) {
	if s := defShip.Load(); s != nil {
		s.Ship(r)
	}
}

// Shutdown 委托单例；未 Init 时返回 nil。
func Shutdown(ctx context.Context) error {
	if s := defShip.Load(); s != nil {
		return s.Shutdown(ctx)
	}
	return nil
}

// Stats 委托单例；未 Init 时返回零值。
func Stats() Snapshot {
	if s := defShip.Load(); s != nil {
		return s.snapshot()
	}
	return Snapshot{}
}

// newClient 构造带连接池调优的 *sqs.Client：把每 host 空闲/最大连接抬到
// ≥ SendConcurrency，否则 Go 默认每 host 仅 2 空闲连接，多 worker 退化为反复 TLS 握手。
func newClient(ctx context.Context, cfg Config) (*sqs.Client, error) {
	pool := cfg.SendConcurrency * 2
	httpClient := awshttp.NewBuildableClient().WithTransportOptions(func(tr *http.Transport) {
		tr.MaxIdleConns = pool
		tr.MaxIdleConnsPerHost = pool
		tr.MaxConnsPerHost = pool
	})
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithHTTPClient(httpClient),
	)
	if err != nil {
		return nil, err
	}
	return sqs.NewFromConfig(awsCfg), nil
}
