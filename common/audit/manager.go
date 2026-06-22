package audit

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/songquanpeng/one-api/common/logger"
)

var (
	pkgConfig  *config
	recordChan chan *AuditRecord
	ingestDone chan struct{}
	dropped    int64
	spill      *spillStore
	gcp        *gcpClient
	startOnce  sync.Once

	// 测试注入点
	testDispatch func(batch []*AuditRecord)
)

func Enabled() bool { return pkgConfig != nil && pkgConfig.Enabled }

func Dropped() int64 { return atomic.LoadInt64(&dropped) }

func atomicAddDropped(n int64) { atomic.AddInt64(&dropped, n) }

func Submit(r *AuditRecord) {
	// 审计绝不能影响主请求：兜底 recover，防止关停时 close(recordChan) 与本次 send 竞态
	// 导致的 send-on-closed-channel panic 逃逸到请求 goroutine。
	defer func() { _ = recover() }()
	if !Enabled() || recordChan == nil {
		return
	}
	select {
	case recordChan <- r:
	default:
		atomic.AddInt64(&dropped, 1)
	}
}

// Start 在 main 中调用：加载配置、校验 GCP、建表、启动 worker。
// 任何初始化失败都降级为关闭，绝不阻断主服务。
func Start(ctx context.Context) {
	startOnce.Do(func() {
		cfg := loadConfig()
		if !cfg.Enabled {
			pkgConfig = cfg
			return
		}
		if cfg.GCPProject == "" {
			logger.SysError("audit: 缺少 GCP_PROJECT 配置，自动降级为关闭")
			cfg.Enabled = false
			pkgConfig = cfg
			return
		}
		client, err := newGCPClient(ctx, cfg)
		if err != nil {
			logger.SysError("audit: 初始化 GCP 客户端失败，降级为关闭: " + err.Error())
			cfg.Enabled = false
			pkgConfig = cfg
			return
		}
		if err := client.ensureTable(ctx); err != nil {
			logger.SysError("audit: 建表失败，降级为关闭: " + err.Error())
			cfg.Enabled = false
			pkgConfig = cfg
			return
		}
		pkgConfig = cfg
		gcp = client
		spill = &spillStore{dir: cfg.DiskBufferDir, maxBytes: int64(cfg.DiskBufferMaxGB) * 1024 * 1024 * 1024}
		recordChan = make(chan *AuditRecord, cfg.ChannelSize)
		ingestDone = make(chan struct{})
		go func() {
			ingestLoop()
			close(ingestDone)
		}()
		go uploaderLoop()
		logger.SysLog("audit: 审计模块已启动")
	})
}

func Shutdown() {
	if !Enabled() || recordChan == nil {
		return
	}
	close(recordChan) // ingestLoop 收到 !ok 后 flush 残余并退出
	if ingestDone != nil {
		<-ingestDone
	}
	if gcp != nil {
		_ = gcp.Close()
	}
}

func resetForTest() {
	pkgConfig = nil
	recordChan = nil
	ingestDone = nil
	dropped = 0
	spill = nil
	gcp = nil
	testDispatch = nil
}
