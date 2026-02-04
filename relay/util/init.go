package util

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/songquanpeng/one-api/common/config"
)

// var HTTPClient *http.Client

var httpClient *http.Client
var HTTPClient *http.Client
var ImpatientHTTPClient *http.Client

// LongRunningHTTPClient 专用于长时间运行的请求（如 Gemini 图像生成）
// 使用独立的超时设置，不受外部上下文限制
var LongRunningHTTPClient *http.Client

func init() {
	// 获取超时配置（优先使用 RELAY_TIMEOUT 环境变量）
	var relayTimeout time.Duration
	if config.RelayTimeout > 0 {
		relayTimeout = time.Duration(config.RelayTimeout) * time.Second
	} else {
		// 默认 5 分钟（合理的请求超时时间）
		relayTimeout = 5 * time.Minute
	}

	// Configure HTTP transport with optimized settings
	// 超时由 RELAY_TIMEOUT 环境变量统一控制
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second, // 连接空闲超时：90秒
		DisableKeepAlives:   false,
		MaxConnsPerHost:     0, // 无限制，根据需要创建连接
		WriteBufferSize:     32 * 1024,
		ReadBufferSize:      32 * 1024,
		// 设置连接拨号器，解决 HTTP/2 连接超时问题
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second, // 连接超时：30秒
			KeepAlive: 30 * time.Second, // TCP KeepAlive：30秒
		}).DialContext,
		ForceAttemptHTTP2:     true,             // 强制尝试 HTTP/2，确保正确处理 HTTP/2 协议
		TLSHandshakeTimeout:   15 * time.Second, // TLS握手超时：15秒（增加以适应网络延迟）
		ResponseHeaderTimeout: relayTimeout,     // 响应头超时：使用 RELAY_TIMEOUT 配置
		ExpectContinueTimeout: 1 * time.Second,  // Expect-Continue超时：1秒
	}

	httpClient = &http.Client{
		Timeout:   relayTimeout,
		Transport: transport,
	}
	HTTPClient = &http.Client{
		Timeout:   relayTimeout,
		Transport: transport,
	}

	// LongRunningHTTPClient 复用主配置，超时由 RELAY_TIMEOUT 统一控制
	LongRunningHTTPClient = &http.Client{
		Timeout:   relayTimeout,
		Transport: transport,
	}

	// Separate transport for impatient client (用于快速查询等场景)
	impatientTransport := &http.Transport{
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 5,
		IdleConnTimeout:     30 * time.Second, // 连接空闲超时：30秒
		DisableKeepAlives:   false,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second, // 连接超时：10秒
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   5 * time.Second,  // TLS握手超时：5秒
		ResponseHeaderTimeout: 60 * time.Second, // 响应头超时：60秒（用于快速接口）
	}

	ImpatientHTTPClient = &http.Client{
		Timeout:   2 * time.Minute, // ImpatientClient总超时：2分钟
		Transport: impatientTransport,
	}
}

func GetHttpClient() *http.Client {
	return httpClient
}

// GetRelayTimeout 获取 RelayTimeout 配置的超时时间
// 如果 RelayTimeout > 0，返回配置的秒数；否则返回默认 5 分钟
func GetRelayTimeout() time.Duration {
	if config.RelayTimeout > 0 {
		return time.Duration(config.RelayTimeout) * time.Second
	}
	// 默认 5 分钟（合理的请求超时时间）
	return 5 * time.Minute
}

// DoLongRunningRequest 执行请求，使用 RelayTimeout 控制超时
// 保留原始上下文的取消能力，同时添加超时控制
func DoLongRunningRequest(req *http.Request) (*http.Response, error) {
	// 获取基于 RelayTimeout 的超时时间
	timeout := GetRelayTimeout()

	// 创建带超时的上下文，但保留原始上下文的取消能力
	// 这样当客户端断开连接时，请求可以被正确取消，避免内存泄漏
	ctx, cancel := context.WithTimeout(req.Context(), timeout)
	defer cancel()

	reqWithCtx := req.WithContext(ctx)
	return HTTPClient.Do(reqWithCtx)
}
