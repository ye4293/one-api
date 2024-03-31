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
	if config.RelayTimeout == 0 {
		httpClient = &http.Client{}
		HTTPClient = &http.Client{}
	} else {
		httpClient = &http.Client{
			Timeout: time.Duration(config.RelayTimeout) * time.Second,
		}
		HTTPClient = &http.Client{
			Timeout: time.Duration(config.RelayTimeout) * time.Second,
		}
	}

	ImpatientHTTPClient = &http.Client{
		Timeout: 5 * time.Second,
	}
}

func GetHttpClient() *http.Client {
	return httpClient
}
