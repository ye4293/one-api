package metrics

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/http/pprof"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
)

// StartServer 在独立端口上启动 metrics / pprof 服务。未开启时是空操作。
//
// 为什么用独立端口而不是挂在主 gin 引擎上：
//
//  1. 不依赖 DB。若复用 middleware.RootAuth()，每次 scrape 都会走
//     model.ValidateAccessToken 打一次库；更糟的是 DB 挂掉时 /metrics 会一起挂 ——
//     而那正是最需要看 go_sql_wait_count_total 的时刻。
//     监控端点绝不能依赖被监控对象的依赖。
//  2. 不被限流。主端口的 GlobalAPIRateLimit 走 Redis，scrape 不该消耗限流配额。
//  3. 不污染 access log，也不会被 dashboard 路由组的 gzip 中间件处理。
//  4. 网络层是第一道防线：容器/Pod 不把该端口对外暴露，即使 token 泄露也打不到。
func StartServer() {
	if !Enabled() {
		return
	}

	mux := http.NewServeMux() // 故意不用 http.DefaultServeMux，避免被其它包污染
	mux.Handle("/metrics", auth(promhttp.HandlerFor(Registry(), promhttp.HandlerOpts{
		// 采集单个 collector 出错时不要整体 500，尽量把其余指标吐出来
		ErrorHandling: promhttp.ContinueOnError,
	})))

	if config.PprofEnabled {
		// 显式注册，不用 import _ "net/http/pprof" —— 那会把 handler 装到
		// http.DefaultServeMux 上，一旦项目里有任何代码使用 DefaultServeMux 就会意外暴露。
		mux.Handle("/debug/pprof/", auth(http.HandlerFunc(pprof.Index)))
		mux.Handle("/debug/pprof/cmdline", auth(http.HandlerFunc(pprof.Cmdline)))
		mux.Handle("/debug/pprof/profile", auth(http.HandlerFunc(pprof.Profile)))
		mux.Handle("/debug/pprof/symbol", auth(http.HandlerFunc(pprof.Symbol)))
		mux.Handle("/debug/pprof/trace", auth(http.HandlerFunc(pprof.Trace)))
		logger.SysLog("pprof enabled at /debug/pprof/ on the metrics port")
	}

	addr := ":" + strconv.Itoa(config.MetricsPort)
	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
		// pprof 的 profile/trace 采集默认 30s，这里给足余量，所以不设 WriteTimeout。
		// 但必须限制读 header 的时间，否则会被 slowloris 拖住。
		ReadHeaderTimeout: 10 * time.Second,
	}

	if config.MetricsToken == "" {
		logger.SysError("METRICS_TOKEN is empty, metrics endpoint only accepts loopback requests")
	}

	go func() {
		logger.SysLog("metrics server listening on " + addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			// 指标服务挂了不应该影响主服务，只告警
			logger.SysError("metrics server stopped: " + err.Error())
		}
	}()
}

// auth 校验 Bearer token。
//
// 未配置 METRICS_TOKEN 时退化为「只允许 loopback」，方便本地开发，同时保证
// 生产上忘配 token 不会变成匿名公开端点。
func auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if config.MetricsToken == "" {
			if !isLoopback(r.RemoteAddr) {
				http.Error(w, "metrics token not configured", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		// 用常量时间比较，避免通过响应时间差爆破 token
		if subtle.ConstantTimeCompare([]byte(token), []byte(config.MetricsToken)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
