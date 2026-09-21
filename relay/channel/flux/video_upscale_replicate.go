package flux

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// buildReplicateVideoUpscaleRequest 模型参数放入 input，回调参数转换为预测请求的外层字段。
func buildReplicateVideoUpscaleRequest(req FluxVideoUpscaleRequest) (map[string]any, error) {
	if req.WebhookSecret != nil && *req.WebhookSecret != "" {
		return nil, fmt.Errorf("Replicate 不支持按请求设置 webhook_secret；本站回调使用 REPLICATE_WEBHOOK_SIGNING_KEY 验证签名")
	}
	video, err := replicateVideoInputURI(req.InputVideo)
	if err != nil {
		return nil, err
	}
	input := map[string]any{"input_video": video}
	if req.UpscaleFactor != nil {
		input["upscale_factor"] = *req.UpscaleFactor
	}
	if req.Creativity != nil {
		input["creativity"] = *req.Creativity
	}
	if req.Prompt != "" {
		input["prompt"] = req.Prompt
	}
	if req.SafetyTolerance != nil {
		input["safety_tolerance"] = *req.SafetyTolerance
	}
	body := map[string]any{"input": input}
	if req.WebhookURL != nil {
		u, err := url.Parse(*req.WebhookURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" {
			return nil, fmt.Errorf("Replicate 的 webhook_url 必须是 HTTPS URL")
		}
		body["webhook"] = *req.WebhookURL
		body["webhook_events_filter"] = []string{"completed"}
	}
	return body, nil
}

// replicateVideoInputURI Replicate 文件参数接受 HTTP(S) 或 data URL，裸 base64 补充 MP4 前缀。
func replicateVideoInputURI(input string) (string, error) {
	if u, err := url.Parse(input); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" {
		return input, nil
	}
	const prefix = "data:video/mp4;base64,"
	encoded := strings.TrimPrefix(input, prefix)
	// 流式校验避免额外分配整段视频内存；媒体时长和分辨率由上游检查。
	const maxBytes = 50 * 1024 * 1024
	size, err := io.Copy(io.Discard, io.LimitReader(base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded)), maxBytes+1))
	if err != nil || size == 0 {
		return "", fmt.Errorf("input_video 必须为 HTTP(S) URL 或有效的 base64 MP4")
	}
	if size > maxBytes {
		return "", fmt.Errorf("input_video 不能超过 50MB，请缩小视频后重试")
	}
	return prefix + encoded, nil
}
