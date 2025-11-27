package util

import (
	"net/http"
	"time"

	"github.com/songquanpeng/one-api/common/config"
)

// var HTTPClient *http.Client

var httpClient *http.Client
var HTTPClient *http.Client
var ImpatientHTTPClient *http.Client

func init() {
	// Configure HTTP transport with optimized settings
	transport := &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second, // 连接空闲超时：90秒
		DisableKeepAlives:     false,
		MaxConnsPerHost:       0, // 无限制，根据需要创建连接
		WriteBufferSize:       32 * 1024,
		ReadBufferSize:        32 * 1024,
		DialContext:           nil,
		TLSHandshakeTimeout:   10 * time.Second, // TLS握手超时：10秒（快速失败）
		ResponseHeaderTimeout: 15 * time.Minute, // 响应头超时：15分钟（适配10分钟左右的长响应）
		ExpectContinueTimeout: 1 * time.Second,  // Expect-Continue超时：1秒
	}

	// 默认超时时间：15分钟（适配10分钟左右的长响应），可通过RelayTimeout环境变量配置
	defaultTimeout := 15 * time.Minute
	if config.RelayTimeout > 0 {
		defaultTimeout = time.Duration(config.RelayTimeout) * time.Second
	}

	httpClient = &http.Client{
		Timeout:   defaultTimeout,
		Transport: transport,
	}
	HTTPClient = &http.Client{
		Timeout:   defaultTimeout,
		Transport: transport,
	}

	// Separate transport for impatient client (用于快速查询等场景)
	impatientTransport := &http.Transport{
		MaxIdleConns:          10,
		MaxIdleConnsPerHost:   5,
		IdleConnTimeout:       30 * time.Second, // 连接空闲超时：30秒
		DisableKeepAlives:     false,
		DialContext:           nil,
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
