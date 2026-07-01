package audit

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/songquanpeng/one-api/common/logger"
)

var (
	pkgConfig  *auditConfig
	recordChan chan *AuditRecord
	ingestDone chan struct{}
	dropped    int64
	spill      *spillStore
	awsClient  *awsAuditClient
	cancelFunc context.CancelFunc

	startMu    sync.Mutex
	hasStarted bool
	appCtx     context.Context

	testDispatch func(batch []*AuditRecord)
)

func Enabled() bool { return pkgConfig != nil && pkgConfig.Enabled }

func Dropped() int64 { return atomic.LoadInt64(&dropped) }

func atomicAddDropped(n int64) { atomic.AddInt64(&dropped, n) }

func Submit(r *AuditRecord) {
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

func Start(ctx context.Context) {
	startMu.Lock()
	defer startMu.Unlock()
	if hasStarted {
		return
	}
	appCtx = ctx
	hasStarted = true
	doStart(ctx)
}

// Reload re-reads auditConfig from OptionMap and restarts the audit module.
// Safe to call at any time; no-op if Start has not been called yet.
func Reload() {
	startMu.Lock()
	defer startMu.Unlock()
	if !hasStarted {
		return
	}
	ctx := appCtx
	if ctx == nil {
		ctx = context.Background()
	}
	doStop()
	doStart(ctx)
}

func doStart(ctx context.Context) {
	cfg := loadConfig()
	if !cfg.Enabled {
		pkgConfig = cfg
		return
	}
	if cfg.AWSRegion == "" || cfg.AWSAccessKey == "" || cfg.AWSSecretKey == "" {
		logger.SysError("audit: 缺少 AWS 凭证配置，自动降级为关闭")
		cfg.Enabled = false
		pkgConfig = cfg
		return
	}
	if cfg.FirehoseStream == "" {
		logger.SysError("audit: 缺少 AUDIT_FIREHOSE_STREAM 配置，自动降级为关闭")
		cfg.Enabled = false
		pkgConfig = cfg
		return
	}

	client := newAWSClient(cfg)

	if err := client.ensureGlueResources(ctx); err != nil {
		logger.SysError("audit: Glue 建表失败，降级为关闭: " + err.Error())
		cfg.Enabled = false
		pkgConfig = cfg
		return
	}

	pkgConfig = cfg
	awsClient = client
	spill = &spillStore{dir: cfg.DiskBufferDir, maxBytes: int64(cfg.DiskBufferMaxGB) * 1024 * 1024 * 1024}
	recordChan = make(chan *AuditRecord, cfg.ChannelSize)
	ingestDone = make(chan struct{})

	bgCtx, cancel := context.WithCancel(context.Background())
	cancelFunc = cancel

	go func() {
		ingestLoop()
		close(ingestDone)
	}()
	go uploaderLoop(bgCtx)
	go compactionLoop(bgCtx)
	logger.SysLog("audit: 审计模块已启动 (Firehose → Iceberg)")
}

func doStop() {
	if recordChan != nil {
		close(recordChan)
	}
	if ingestDone != nil {
		<-ingestDone
	}
	if cancelFunc != nil {
		cancelFunc()
	}
	if awsClient != nil {
		_ = awsClient.Close()
	}
	pkgConfig = nil
	recordChan = nil
	ingestDone = nil
	spill = nil
	awsClient = nil
	cancelFunc = nil
	atomic.StoreInt64(&dropped, 0)
}

func Shutdown() {
	startMu.Lock()
	defer startMu.Unlock()
	doStop()
}

func resetForTest() {
	pkgConfig = nil
	recordChan = nil
	ingestDone = nil
	dropped = 0
	spill = nil
	awsClient = nil
	cancelFunc = nil
	hasStarted = false
	appCtx = nil
	testDispatch = nil
}
