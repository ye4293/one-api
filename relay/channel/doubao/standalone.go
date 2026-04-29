package doubao

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/relay/util"
)

const defaultDoubaoBaseURL = "https://ark.cn-beijing.volces.com"

// GetBaseURL 返回豆包 API base URL
func (a *VideoAdaptor) GetBaseURL(meta *util.RelayMeta) string {
	if meta != nil && meta.BaseURL != "" {
		return strings.TrimRight(meta.BaseURL, "/")
	}
	return defaultDoubaoBaseURL
}

// DoCreate 向豆包提交视频生成任务（不依赖 gin.Context）
func (a *VideoAdaptor) DoCreate(ctx context.Context, meta *util.RelayMeta, body []byte) (*http.Response, error) {
	url := a.GetBaseURL(meta) + "/api/v3/contents/generations/tasks"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+meta.APIKey)
	return http.DefaultClient.Do(req)
}

// DoQuery 查询豆包视频任务状态（不依赖 gin.Context）
func (a *VideoAdaptor) DoQuery(ctx context.Context, meta *util.RelayMeta, taskID string) (*http.Response, error) {
	url := fmt.Sprintf("%s/api/v3/contents/generations/tasks/%s", a.GetBaseURL(meta), taskID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+meta.APIKey)
	return http.DefaultClient.Do(req)
}

// ParseCreateResponse 解析创建任务响应
func (a *VideoAdaptor) ParseCreateResponse(body []byte) (*DoubaoVideoResponse, error) {
	var resp DoubaoVideoResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ParseQueryResponse 解析任务查询响应
func (a *VideoAdaptor) ParseQueryResponse(body []byte) (*DoubaoVideoResult, error) {
	var resp DoubaoVideoResult
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CalcPrePayQuota 预扣费额度：固定 1.4 CNY 转换为 quota
func CalcPrePayQuota() int64 {
	usd, err := convertCNYToUSD(1.4)
	if err != nil {
		usd = 1.4 / 7.2
	}
	return int64(usd * config.QuotaPerUnit)
}

// CalcActualQuota 根据实际 token 数和模型精算额度
// 定价（CNY/百万token）：
//   - doubao-seedance-2-0-fast: 37
//   - doubao-seedance-2-0:      46
//   - doubao-seedance-1-5-pro:  16
//   - doubao-seedance-1-0-pro:  15
//   - doubao-seedance-1-0-lite: 10
//   - 默认:                     46
func CalcActualQuota(modelName string, tokens int64) int64 {
	var priceCNYPerMillion float64
	switch {
	case strings.Contains(modelName, "doubao-seedance-2-0-fast"):
		priceCNYPerMillion = 37.0
	case strings.Contains(modelName, "doubao-seedance-2-0"):
		priceCNYPerMillion = 46.0
	case strings.Contains(modelName, "doubao-seedance-1-5-pro"):
		priceCNYPerMillion = 16.0
	case strings.Contains(modelName, "doubao-seedance-1-0-pro"):
		priceCNYPerMillion = 15.0
	case strings.Contains(modelName, "doubao-seedance-1-0-lite"):
		priceCNYPerMillion = 10.0
	default:
		priceCNYPerMillion = 46.0
	}
	cnyAmount := priceCNYPerMillion / 1_000_000 * float64(tokens)
	usd, err := convertCNYToUSD(cnyAmount)
	if err != nil {
		usd = cnyAmount / 7.2
	}
	return int64(usd * config.QuotaPerUnit)
}
