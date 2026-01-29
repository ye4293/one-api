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
	// Configure HTTP transport with optimized settings
	// 注意：Gemini 图像生成等场景需要较长的超时时间
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
		ResponseHeaderTimeout: 15 * time.Minute, // 响应头超时：15分钟（适配图像生成等长响应）
		ExpectContinueTimeout: 1 * time.Second,  // Expect-Continue超时：1秒
	}

	// 超时时间配置：
	// - 如果 RelayTimeout > 0，使用配置的秒数
	// - 如果 RelayTimeout == 0，使用默认 30 分钟（与常见 nginx 配置一致）
	var defaultTimeout time.Duration
	if config.RelayTimeout > 0 {
		defaultTimeout = time.Duration(config.RelayTimeout) * time.Second
	} else {
		// 默认 30 分钟，与常见 nginx 超时配置一致
		defaultTimeout = 30 * time.Minute
	}

	httpClient = &http.Client{
		Timeout:   defaultTimeout,
		Transport: transport,
	}
	HTTPClient = &http.Client{
		Timeout:   defaultTimeout,
		Transport: transport,
	}

	// 专用于长时间运行请求的客户端（如 Gemini 图像生成）
	// 使用独立的 Transport 实例，避免连接复用问题
	// 不设置客户端级别的 Timeout，只依赖 Transport 级别的超时控制
	longRunningTransport := &http.Transport{
		MaxIdleConns:        50,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     120 * time.Second, // 更长的空闲超时
		DisableKeepAlives:   false,
		MaxConnsPerHost:     0,
		WriteBufferSize:     64 * 1024, // 更大的写缓冲区
		ReadBufferSize:      64 * 1024, // 更大的读缓冲区
		DialContext: (&net.Dialer{
			Timeout:   60 * time.Second, // 更长的连接超时
			KeepAlive: 60 * time.Second, // 更长的 KeepAlive
		}).DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   30 * time.Second,  // 更长的 TLS 握手超时
		ResponseHeaderTimeout: 30 * time.Minute,  // 响应头超时：30分钟（足够 Gemini 图像生成）
		ExpectContinueTimeout: 2 * time.Second,
	}

	// 设置 30 分钟超时，与 nginx 配置一致
	LongRunningHTTPClient = &http.Client{
		Timeout:   30 * time.Minute,
		Transport: longRunningTransport,
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

// DoLongRunningRequest 执行长时间运行的请求，使用独立的上下文
// 这可以避免 gin 请求上下文超时导致的问题
func DoLongRunningRequest(req *http.Request) (*http.Response, error) {
	// 使用独立的 background 上下文，不继承外部上下文的超时限制
	// 这样可以避免 nginx/反向代理等外部超时导致的 context deadline exceeded 错误
	// 超时由 LongRunningHTTPClient 的 Timeout (30分钟) 控制
	reqWithCtx := req.WithContext(context.Background())
	return LongRunningHTTPClient.Do(reqWithCtx)
}
