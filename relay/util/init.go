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
		IdleConnTimeout:       90 * time.Second,
		DisableKeepAlives:     false,
		MaxConnsPerHost:       0, // No limit
		WriteBufferSize:       32 * 1024,
		ReadBufferSize:        32 * 1024,
		DialContext:           nil,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	defaultTimeout := 60 * time.Second
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
		IdleConnTimeout:       30 * time.Second,
		DisableKeepAlives:     false,
		DialContext:           nil,
		TLSHandshakeTimeout:   3 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
	}

	ImpatientHTTPClient = &http.Client{
		Timeout:   5 * time.Second,
		Transport: impatientTransport,
	}
}

func GetHttpClient() *http.Client {
	return httpClient
}
