package metrics

import (
	"database/sql"

	"github.com/go-redis/redis/v8"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// RegisterDB 注册一个数据库连接池的指标采集器。
//
// 直接复用官方 collectors.NewDBStatsCollector，而不是自己写：它在 Collect() 时才调用
// db.Stats()，平时零开销，也不会在注册时把数值定格成一次性快照（这是自写 collector
// 最容易踩的错）。
//
// 导出指标（均带 db_name label）：
//
//	go_sql_max_open_connections        连接池上限（对应 SQL_MAX_OPEN_CONNS）
//	go_sql_open_connections            当前已建立的连接数
//	go_sql_in_use_connections          正在被使用的连接数
//	go_sql_idle_connections            空闲连接数
//	go_sql_wait_count_total            ← 核心指标：因连接池耗尽而等待的累计次数
//	go_sql_wait_duration_seconds_total 累计等待时长
//	go_sql_max_idle_closed_total       因超过 MaxIdleConns 被关闭的连接数
//	go_sql_max_idle_time_closed_total  因空闲超时被关闭的连接数
//	go_sql_max_lifetime_closed_total   因超过 ConnMaxLifetime 被关闭的连接数
//
// 生产环境 SQL_MAX_OPEN_CONNS=300 且跨公网连 RDS，wait_count 一旦开始增长就说明
// 所有请求都在排队等连接 —— 这个信号此前完全观测不到。
func RegisterDB(name string, db *sql.DB) error {
	if !Enabled() || db == nil {
		return nil
	}
	return Registry().Register(collectors.NewDBStatsCollector(db, name))
}

// RegisterRedis 注册 Redis 连接池的指标采集器。
//
// go-redis v8 没有官方 Prometheus collector，所以这里自己实现一个。
// 传入的是取快照的闭包而非 *redis.Client，让本包不必关心客户端的生命周期。
func RegisterRedis(stats func() *redis.PoolStats) error {
	if !Enabled() || stats == nil {
		return nil
	}
	return Registry().Register(newRedisCollector(stats))
}

// redisCollector 把 go-redis 的连接池快照转换成 Prometheus 指标。
// 与 DBStatsCollector 一致：在 Collect() 时才取快照。
type redisCollector struct {
	stats func() *redis.PoolStats

	conns    *prometheus.Desc
	hits     *prometheus.Desc
	misses   *prometheus.Desc
	timeouts *prometheus.Desc
	stale    *prometheus.Desc
}

func newRedisCollector(stats func() *redis.PoolStats) *redisCollector {
	return &redisCollector{
		stats: stats,
		conns: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "redis", "pool_connections"),
			"Number of connections in the Redis client pool by state.",
			[]string{"state"}, nil,
		),
		hits: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "redis", "pool_hits_total"),
			"Total number of times a free connection was found in the Redis pool.",
			nil, nil,
		),
		misses: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "redis", "pool_misses_total"),
			"Total number of times a free connection was NOT found in the Redis pool.",
			nil, nil,
		),
		timeouts: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "redis", "pool_timeouts_total"),
			"Total number of times a wait timeout occurred while getting a Redis connection.",
			nil, nil,
		),
		stale: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "redis", "pool_stale_conns_total"),
			"Total number of stale connections removed from the Redis pool.",
			nil, nil,
		),
	}
}

func (c *redisCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.conns
	ch <- c.hits
	ch <- c.misses
	ch <- c.timeouts
	ch <- c.stale
}

func (c *redisCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.stats()
	if s == nil {
		return
	}
	// TotalConns / IdleConns 是瞬时值 → Gauge
	ch <- prometheus.MustNewConstMetric(c.conns, prometheus.GaugeValue, float64(s.TotalConns), "total")
	ch <- prometheus.MustNewConstMetric(c.conns, prometheus.GaugeValue, float64(s.IdleConns), "idle")
	// Hits / Misses / Timeouts / StaleConns 是累计值 → Counter
	// StaleConns 单独作为 counter 而不是 conns 的一个 state：它是"累计被移除的连接数"，
	// 语义上不是池中当前的连接，混进 Gauge 会让 rate() 失去意义。
	ch <- prometheus.MustNewConstMetric(c.hits, prometheus.CounterValue, float64(s.Hits))
	ch <- prometheus.MustNewConstMetric(c.misses, prometheus.CounterValue, float64(s.Misses))
	ch <- prometheus.MustNewConstMetric(c.timeouts, prometheus.CounterValue, float64(s.Timeouts))
	ch <- prometheus.MustNewConstMetric(c.stale, prometheus.CounterValue, float64(s.StaleConns))
}
