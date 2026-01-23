package kling

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
)

// 预解析的私有 IP 网段，避免每次调用都重新解析
var privateIPNets []*net.IPNet

func init() {
	privateIPBlocks := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16",
		"127.0.0.0/8",
		"fc00::/7",
		"fe80::/10",
		"::1/128",
	}

	for _, block := range privateIPBlocks {
		_, ipNet, err := net.ParseCIDR(block)
		if err != nil {
			logger.SysError(fmt.Sprintf("Failed to parse CIDR: %s, error=%v", block, err))
			continue
		}
		privateIPNets = append(privateIPNets, ipNet)
	}
}

// IsValidCallbackURL 验证用户回调 URL（排除内网地址）
func IsValidCallbackURL(callbackURL string) error {
	if callbackURL == "" {
		return nil // 空 URL 不是错误，只是不需要回调
	}

	// 解析 URL
	parsedURL, err := url.Parse(callbackURL)
	if err != nil {
		return fmt.Errorf("invalid URL format: %w", err)
	}

	// 检查协议
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("only http/https protocols are allowed")
	}

	// 提取主机名
	host := parsedURL.Hostname()
	if host == "" {
		return fmt.Errorf("missing host in URL")
	}

	// 检查是否为内网地址
	if isPrivateHost(host) {
		return fmt.Errorf("callback to private network addresses is not allowed")
	}

	return nil
}

// isPrivateHost 检查主机名是否为内网地址
func isPrivateHost(host string) bool {
	// 检查常见的本地主机名
	lowerHost := strings.ToLower(host)
	if lowerHost == "localhost" || lowerHost == "127.0.0.1" || lowerHost == "::1" {
		return true
	}

	// 解析为 IP 地址
	ip := net.ParseIP(host)
	if ip == nil {
		// 域名需要解析后检查（防止恶意域名解析到内网 IP）
		ips, err := net.LookupIP(host)
		if err != nil {
			// 解析失败，为安全起见拒绝
			logger.SysError(fmt.Sprintf("Failed to resolve host: %s, error=%v", host, err))
			return true
		}
		// 检查所有解析的 IP 是否都是公网地址
		for _, resolvedIP := range ips {
			if isPrivateIP(resolvedIP) {
				return true // 任何一个 IP 是内网地址就拒绝
			}
		}
		return false
	}

	// 检查 IP 本身
	return isPrivateIP(ip)
}

// isPrivateIP 检查 IP 是否为私有地址
func isPrivateIP(ip net.IP) bool {
	for _, ipNet := range privateIPNets {
		if ipNet.Contains(ip) {
			return true
		}
	}
	return false
}

// NotifyUserCallback 向用户的回调地址发送通知
// 此函数设计为在 goroutine 中运行，因此包含 panic 恢复机制
func NotifyUserCallback(ctx context.Context, video *dbmodel.Video, callbackData []byte) {
	// 添加 panic 恢复机制，防止 goroutine 崩溃影响主程序
	defer func() {
		if r := recover(); r != nil {
			logger.Error(ctx, fmt.Sprintf("Panic in NotifyUserCallback: task_id=%s, error=%v",
				video.TaskId, r))
		}
	}()

	if video.CallbackUrl == "" {
		return
	}

	// 验证 URL
	if err := IsValidCallbackURL(video.CallbackUrl); err != nil {
		logger.Error(ctx, fmt.Sprintf("Invalid callback URL: task_id=%s, url=%s, error=%v",
			video.TaskId, video.CallbackUrl, err))
		video.CallbackStatus = "failed"
		video.CallbackError = fmt.Sprintf("invalid URL: %v", err)
		video.CallbackTime = time.Now().Unix()
		if err := video.Update(); err != nil {
			logger.Error(ctx, fmt.Sprintf("Failed to update video callback status: task_id=%s, error=%v",
				video.TaskId, err))
		}
		return
	}

	// 创建 HTTP 客户端（配置完整的超时参数）
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   2 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   2 * time.Second,
			ResponseHeaderTimeout: 3 * time.Second,
			MaxIdleConns:          10,
			IdleConnTimeout:       30 * time.Second,
		},
	}

	// 发送 POST 请求
	req, err := http.NewRequest("POST", video.CallbackUrl, bytes.NewReader(callbackData))
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("Create callback request failed: task_id=%s, url=%s, error=%v",
			video.TaskId, video.CallbackUrl, err))
		video.CallbackStatus = "failed"
		video.CallbackError = fmt.Sprintf("create request failed: %v", err)
		video.CallbackTime = time.Now().Unix()
		if err := video.Update(); err != nil {
			logger.Error(ctx, fmt.Sprintf("Failed to update video callback status: task_id=%s, error=%v",
				video.TaskId, err))
		}
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "OneAPI-Kling-Callback/1.0")

	// 发送请求
	resp, err := client.Do(req)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("User callback request failed: task_id=%s, url=%s, error=%v",
			video.TaskId, video.CallbackUrl, err))
		video.CallbackStatus = "failed"
		video.CallbackError = fmt.Sprintf("request failed: %v", err)
		video.CallbackTime = time.Now().Unix()
		if err := video.Update(); err != nil {
			logger.Error(ctx, fmt.Sprintf("Failed to update video callback status: task_id=%s, error=%v",
				video.TaskId, err))
		}
		return
	}
	defer resp.Body.Close()

	// 读取响应（限制大小为 64KB，防止攻击）
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("Read callback response failed: task_id=%s, url=%s, error=%v",
			video.TaskId, video.CallbackUrl, err))
	}

	// 检查响应状态码
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		video.CallbackStatus = "success"
		video.CallbackTime = time.Now().Unix()
		logger.Info(ctx, fmt.Sprintf("User callback success: task_id=%s, url=%s, status_code=%d",
			video.TaskId, video.CallbackUrl, resp.StatusCode))
	} else {
		video.CallbackStatus = "failed"
		video.CallbackError = fmt.Sprintf("status_code=%d, response=%s", resp.StatusCode, string(respBody))
		video.CallbackTime = time.Now().Unix()
		logger.Error(ctx, fmt.Sprintf("User callback failed: task_id=%s, url=%s, status_code=%d, response=%s",
			video.TaskId, video.CallbackUrl, resp.StatusCode, string(respBody)))
	}

	// 更新数据库
	if err := video.Update(); err != nil {
		logger.Error(ctx, fmt.Sprintf("Failed to update video callback status: task_id=%s, error=%v",
			video.TaskId, err))
	}
}
