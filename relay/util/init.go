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
		IdleConnTimeout:       30 * 60 * time.Second, // 30 minutes
		DisableKeepAlives:     false,
		MaxConnsPerHost:       0, // No limit
		WriteBufferSize:       32 * 1024,
		ReadBufferSize:        32 * 1024,
		DialContext:           nil,
		TLSHandshakeTimeout:   30 * 60 * time.Second, // 30 minutes
		ResponseHeaderTimeout: 30 * 60 * time.Second, // 30 minutes
		ExpectContinueTimeout: 30 * 60 * time.Second, // 30 minutes
	}

	defaultTimeout := 30 * 60 * time.Second // 30 minutes
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

	// Separate transport for impatient client
	impatientTransport := &http.Transport{
		MaxIdleConns:          10,
		MaxIdleConnsPerHost:   5,
		IdleConnTimeout:       30 * 60 * time.Second, // 30 minutes
		DisableKeepAlives:     false,
		DialContext:           nil,
		TLSHandshakeTimeout:   30 * 60 * time.Second, // 30 minutes
		ResponseHeaderTimeout: 30 * 60 * time.Second, // 30 minutes
	}

	ImpatientHTTPClient = &http.Client{
		Timeout:   30 * 60 * time.Second, // 30 minutes
		Transport: impatientTransport,
	}
}

func GetHttpClient() *http.Client {
	return httpClient
}
