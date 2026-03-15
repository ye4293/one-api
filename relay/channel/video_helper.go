package channel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// SendJSONVideoRequest 发送 JSON POST 请求并返回原始响应体
func SendJSONVideoRequest(url string, body any, headers map[string]string) (*http.Response, []byte, error) {
	jsonData, err := json.Marshal(body)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal request body: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(jsonData))
	if err != nil {
		return nil, nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, nil, fmt.Errorf("read response body: %w", err)
	}
	return resp, respBody, nil
}

// SendVideoResultQuery 发送 GET 请求用于结果轮询，返回原始响应体
func SendVideoResultQuery(url string, headers map[string]string) (*http.Response, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, nil, fmt.Errorf("read response body: %w", err)
	}
	return resp, respBody, nil
}

// BearerAuthHeaders 返回携带 Bearer Token 的请求头 map
func BearerAuthHeaders(apiKey string) map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + apiKey,
	}
}
