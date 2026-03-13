package ali

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/songquanpeng/one-api/relay/util"
)

const defaultDashScopeBaseURL = "https://dashscope.aliyuncs.com"

// VideoAdaptor 封装 DashScope 视频生成 API 的 HTTP 调用
type VideoAdaptor struct{}

// GetBaseURL 返回 DashScope 基础 URL，优先使用渠道自定义地址
func (a *VideoAdaptor) GetBaseURL(meta *util.RelayMeta) string {
	if meta.BaseURL != "" {
		return strings.TrimRight(meta.BaseURL, "/")
	}
	return defaultDashScopeBaseURL
}

// GetCreateURL 返回视频创建任务的端点 URL
func (a *VideoAdaptor) GetCreateURL(meta *util.RelayMeta) string {
	return a.GetBaseURL(meta) + "/api/v1/services/aigc/video-generation/video-synthesis"
}

// GetQueryURL 返回查询任务状态的端点 URL
func (a *VideoAdaptor) GetQueryURL(meta *util.RelayMeta, taskID string) string {
	return fmt.Sprintf("%s/api/v1/tasks/%s", a.GetBaseURL(meta), taskID)
}

// DoCreate 提交视频生成任务（DashScope 异步模式）
func (a *VideoAdaptor) DoCreate(ctx context.Context, meta *util.RelayMeta, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.GetCreateURL(meta), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+meta.APIKey)
	req.Header.Set("X-DashScope-Async", "enable")
	return http.DefaultClient.Do(req)
}

// DoQuery 查询任务状态
func (a *VideoAdaptor) DoQuery(ctx context.Context, meta *util.RelayMeta, taskID string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.GetQueryURL(meta, taskID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+meta.APIKey)
	return http.DefaultClient.Do(req)
}

// ParseCreateResponse 解析视频创建任务的响应体
func (a *VideoAdaptor) ParseCreateResponse(body []byte) (*AliVideoResponse, error) {
	var resp AliVideoResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ParseQueryResponse 解析任务状态查询的响应体
func (a *VideoAdaptor) ParseQueryResponse(body []byte) (*AliVideoQueryResponse, error) {
	var resp AliVideoQueryResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
