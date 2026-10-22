package model

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
)

var DefaultRequestStatistics *RequestStatistics

type RequestStatistics struct {
	enabled, workerEnabled    bool
	success, models, failures *statsResource
	worker                    *sql.DB
	lockName                  string
	stop                      context.CancelFunc
	wg                        sync.WaitGroup
	workerGate                chan struct{}
	initial                   *statsBootstrap // 仅在 Worker 锁内访问；首批汇总前的内存边界。
}

type statsCacheEntry struct {
	body    []byte
	expires time.Time
}
type statsFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	body    []byte
	err     error
	epoch   uint64
}
type statsResource struct {
	db      *sql.DB
	gate    chan struct{}
	mu      sync.Mutex
	cache   map[string]statsCacheEntry
	bytes   int
	epoch   uint64
	flights map[string]*statsFlight
	active  map[*statsFlight]struct{}
	wg      sync.WaitGroup
	actors  map[int]int
	closed  bool
}

func newStatsResource(db *sql.DB, concurrent int) *statsResource {
	return &statsResource{db: db, gate: make(chan struct{}, concurrent), cache: map[string]statsCacheEntry{}, flights: map[string]*statsFlight{}, active: map[*statsFlight]struct{}{}, actors: map[int]int{}}
}

// 独立底层池，仅 Ping，不调用 GORM/InitDB 或执行迁移。
func openStatsPool(ctx context.Context, dsn string, size int, read bool) (*sql.DB, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || cfg.DBName == "" {
		return nil, fmt.Errorf("报表需要有效的 MySQL LOG_SQL_DSN/SQL_DSN")
	}
	cfg.ParseTime = true
	cfg.Timeout = 2 * time.Second
	cfg.ReadTimeout = 6 * time.Second
	cfg.WriteTimeout = 6 * time.Second
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	if read {
		cfg.Params["max_execution_time"] = "1800"
	} else {
		// Worker 允许较慢的历史分钟汇总；驱动超时覆盖单分钟事务的完整预算。
		cfg.ReadTimeout = 2 * time.Minute
		cfg.WriteTimeout = 2 * time.Minute
		cfg.Params["max_execution_time"] = strconv.FormatInt(statsSourceReadTimeout.Milliseconds(), 10)
	}
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(size)
	db.SetMaxIdleConns(size)
	db.SetConnMaxLifetime(5 * time.Minute)
	ping, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err = db.PingContext(ping); err != nil {
		db.Close()
		return nil, fmt.Errorf("报表连接检查失败")
	}
	return db, nil
}

// 所有节点初始化报表查询；只有显式启用 Worker 的主节点启动聚合。
// 初始化失败只影响报表；四类池互不借用连接。
func InitRequestStatistics(ctx context.Context) *RequestStatistics {
	s := &RequestStatistics{
		enabled:       true,
		workerEnabled: os.Getenv("REQUEST_STATS_WORKER_ENABLED") == "true",
		workerGate:    make(chan struct{}, 1),
	}
	DefaultRequestStatistics = s
	dsn := os.Getenv("LOG_SQL_DSN")
	if dsn == "" {
		dsn = os.Getenv("SQL_DSN")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || cfg.DBName == "" {
		logger.SysError("request statistics disabled: MySQL DSN required")
		s.enabled = false
		return s
	}
	// 同一张表必须共用锁；不使用实例 ID 或调用方提供的站点名分裂互斥域。
	hash := sha256.Sum256([]byte(cfg.DBName + "/request_stats_minute"))
	s.lockName = "request_stats:" + hex.EncodeToString(hash[:16])
	for _, item := range []struct {
		name   string
		target **statsResource
		size   int
	}{{"success", &s.success, 2}, {"models", &s.models, 2}, {"failures", &s.failures, 1}} {
		db, err := openStatsPool(ctx, dsn, item.size, true)
		if err != nil {
			logger.SysError("request statistics " + item.name + " pool unavailable")
			continue
		}
		*item.target = newStatsResource(db, item.size)
	}
	if s.workerEnabled && config.IsMasterNode {
		s.worker, err = openStatsPool(ctx, dsn, 1, false)
		if err != nil {
			logger.SysError("request statistics worker pool unavailable")
		} else {
			workerCtx, cancel := context.WithCancel(ctx)
			s.stop = cancel
			s.wg.Add(1)
			go func() { defer s.wg.Done(); s.runWorker(workerCtx) }()
			logger.SysLog("request statistics worker started")
		}
	} else if !config.IsMasterNode {
		logger.SysLog("request statistics worker skipped: NODE_TYPE is not master")
	} else {
		logger.SysLog("request statistics worker skipped: REQUEST_STATS_WORKER_ENABLED is not true")
	}
	return s
}

func (s *RequestStatistics) Close() {
	if s == nil {
		return
	}
	if s.stop != nil {
		s.stop()
	}
	s.wg.Wait()
	for _, r := range []*statsResource{s.success, s.models, s.failures} {
		if r == nil {
			continue
		}
		r.mu.Lock()
		r.closed = true
		for f := range r.active {
			f.cancel()
		}
		r.mu.Unlock()
		r.wg.Wait()
		if r.db != nil {
			r.db.Close()
		}
	}
	if s.worker != nil {
		s.worker.Close()
	}
}
func statsRetainedFrom(coverageStart int64, now time.Time) int64 {
	return max(coverageStart, now.Unix()/60*60-30*86400)
}
func (s *RequestStatistics) clearCaches() {
	for _, r := range []*statsResource{s.success, s.models} {
		if r != nil {
			r.mu.Lock()
			r.epoch++
			clear(r.cache)
			r.bytes = 0
			r.mu.Unlock()
		}
	}
}

func (s *RequestStatistics) Query(ctx context.Context, q RequestStatsQuery) ([]byte, error) {
	if s == nil || !s.enabled {
		return nil, statsError(503, "分钟报表数据库不可用")
	}
	var r *statsResource
	var query func(context.Context, RequestStatsQuery) (any, error)
	switch q.Kind {
	case "summary":
		r, query = s.success, s.querySuccess
	case "models":
		r, query = s.models, s.queryModels
	case "failures":
		r, query = s.failures, s.queryFailures
	default:
		return nil, statsError(400, "无效查询类型")
	}
	if r == nil || r.db == nil {
		return nil, statsError(503, "该统计查询池不可用")
	}
	keyBytes, _ := json.Marshal(struct {
		Query    RequestStatsQuery
		Version  int
		Retained int64
	}{q, RequestStatsMetricVersion, statsRetainedFrom(0, time.Now())})
	return r.load(ctx, string(keyBytes), q.ActorID, q.Kind != "failures", func(ctx context.Context) ([]byte, error) {
		data, err := query(ctx, q)
		if err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
			Data    any    `json:"data"`
		}{true, "", data})
	})
}

// 合并仅限同一身份、同一规范参数；等待者与数据库执行都受有界预算约束。
func (r *statsResource) load(ctx context.Context, key string, actor int, cache bool, load func(context.Context) ([]byte, error)) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, statsError(503, "统计资源已关闭")
	}
	if entry, ok := r.cache[key]; cache && ok && time.Now().Before(entry.expires) {
		r.mu.Unlock()
		return entry.body, nil
	}
	if r.actors[actor] >= 4 {
		r.mu.Unlock()
		return nil, statsError(429, "当前用户的统计请求过多")
	}
	f := r.flights[key]
	if !cache || (f != nil && f.epoch != r.epoch) {
		f = nil
	}
	if f == nil {
		select {
		case r.gate <- struct{}{}:
		default:
			r.mu.Unlock()
			return nil, statsError(429, "统计查询繁忙，请稍后重试")
		}
		shared, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		f = &statsFlight{done: make(chan struct{}), cancel: cancel, epoch: r.epoch}
		r.active[f] = struct{}{}
		r.wg.Add(1)
		if cache {
			r.flights[key] = f
		}
		go func(f *statsFlight) {
			defer r.wg.Done()
			body, err := load(shared)
			if err == nil {
				err = shared.Err()
			}
			cancel()
			r.mu.Lock()
			f.body, f.err = body, err
			if cache && r.flights[key] == f {
				delete(r.flights, key)
			}
			if cache && err == nil && f.waiters > 0 && f.epoch == r.epoch && !r.closed {
				r.putCache(key, body)
			}
			close(f.done)
			delete(r.active, f)
			<-r.gate
			r.mu.Unlock()
		}(f)
	}
	f.waiters++
	r.actors[actor]++
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.actors[actor]--
		if r.actors[actor] == 0 {
			delete(r.actors, actor)
		}
		f.waiters--
		if f.waiters == 0 {
			f.cancel()
			if r.flights[key] == f {
				delete(r.flights, key)
			}
		}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.done:
		return f.body, f.err
	}
}

// 调用方持锁；按最早过期淘汰，最大仅 128 项，无额外维护链表的开销。
func (r *statsResource) putCache(key string, body []byte) {
	const maxBytes = 16 << 20
	if len(body) > maxBytes {
		return
	}
	if old, ok := r.cache[key]; ok {
		r.bytes -= len(old.body)
		delete(r.cache, key)
	}
	now := time.Now()
	for k, v := range r.cache {
		if !now.Before(v.expires) {
			r.bytes -= len(v.body)
			delete(r.cache, k)
		}
	}
	for len(r.cache) >= 128 || r.bytes+len(body) > maxBytes {
		var oldest string
		var deadline time.Time
		for k, v := range r.cache {
			if deadline.IsZero() || v.expires.Before(deadline) {
				oldest, deadline = k, v.expires
			}
		}
		r.bytes -= len(r.cache[oldest].body)
		delete(r.cache, oldest)
	}
	r.cache[key] = statsCacheEntry{body, now.Add(30 * time.Second)}
	r.bytes += len(body)
}
