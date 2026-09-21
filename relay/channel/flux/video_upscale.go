package flux

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	dbmodel "github.com/songquanpeng/one-api/model"
	relaychannel "github.com/songquanpeng/one-api/relay/channel"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

const (
	VideoUpscaleModel = "video-upscale-v1"
	// 模型名用于选渠和计费，上游接口保留 flux-tools 路径。
	videoUpscaleEndpoint = "/v1/flux-tools/video-upscale-v1"
)

// FluxVideoUpscaleRequest 支持视频 URL 和 base64 MP4；可选参数缺省时采用上游默认值。
type FluxVideoUpscaleRequest struct {
	InputVideo      string   `json:"input_video"`
	UpscaleFactor   *float64 `json:"upscale_factor,omitempty"`
	Creativity      *int     `json:"creativity,omitempty"`
	Prompt          string   `json:"prompt,omitempty"`
	SafetyTolerance *int     `json:"safety_tolerance,omitempty"`
	WebhookURL      *string  `json:"webhook_url,omitempty"`
	WebhookSecret   *string  `json:"webhook_secret,omitempty"`
}

func (req *FluxVideoUpscaleRequest) validate() error {
	if strings.TrimSpace(req.InputVideo) == "" {
		return fmt.Errorf("input_video 不能为空")
	}
	if req.UpscaleFactor != nil && (*req.UpscaleFactor < 1.5 || *req.UpscaleFactor > 3) {
		return fmt.Errorf("upscale_factor 必须在 1.5–3 之间")
	}
	if req.Creativity != nil && *req.Creativity != 0 && *req.Creativity != 1 {
		return fmt.Errorf("creativity 只能为 0 或 1")
	}
	if req.SafetyTolerance != nil && (*req.SafetyTolerance < 0 || *req.SafetyTolerance > 4) {
		return fmt.Errorf("safety_tolerance 必须在 0–4 之间")
	}
	if req.WebhookURL != nil {
		u, err := url.Parse(*req.WebhookURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || len(*req.WebhookURL) > 2083 {
			return fmt.Errorf("webhook_url 必须为有效的 HTTP(S) URL，长度不超过 2083")
		}
	}
	// 视频格式、50MB 大小、20 秒时长和源分辨率限制由 BFL 校验。
	return nil
}

func (a *VideoAdaptor) handleVideoUpscaleRequest(c *gin.Context, meta *util.RelayMeta) (*relaychannel.VideoTaskResult, *model.ErrorWithStatusCode) {
	replicate := isReplicate(meta.BaseURL)
	// 此端点只接收 JSON；避免缺少 Content-Type 时表单回退掩盖整数参数的类型错误。
	requestBody, err := common.GetRequestBody(c)
	if err != nil {
		return nil, openai.ErrorWrapper(err, "invalid_video_upscale_request", http.StatusBadRequest)
	}
	var req FluxVideoUpscaleRequest
	if err := json.Unmarshal(requestBody, &req); err != nil {
		return nil, openai.ErrorWrapper(err, "invalid_video_upscale_request", http.StatusBadRequest)
	}
	if req.WebhookURL == nil {
		webhookURL := defaultWebhookURL()
		if replicate {
			webhookURL = defaultReplicateWebhookURL()
		}
		if webhookURL != "" {
			req.WebhookURL = &webhookURL
		}
	}
	if err := req.validate(); err != nil {
		return nil, openai.ErrorWrapper(err, "invalid_video_upscale_request", http.StatusBadRequest)
	}
	var replicateBody map[string]any
	if replicate {
		replicateBody, err = buildReplicateVideoUpscaleRequest(req)
		if err != nil {
			return nil, openai.ErrorWrapper(err, "invalid_video_upscale_request", http.StatusBadRequest)
		}
	}
	ch, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return nil, openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}
	var taskID, pollingURL string
	var apiErr *model.ErrorWithStatusCode
	if replicate {
		requestURL := strings.TrimRight(meta.BaseURL, "/") + "/v1/models/" + ReplicateVideoModelMap[VideoUpscaleModel] + "/predictions"
		taskID, pollingURL, apiErr = submitReplicateVideoTask(requestURL, replicateBody, ch.Key)
	} else {
		requestURL := strings.TrimRight(meta.BaseURL, "/") + videoUpscaleEndpoint
		taskID, pollingURL, apiErr = submitBFLVideoTask(requestURL, req, ch.Key)
	}
	if apiErr != nil {
		return nil, apiErr
	}
	return &relaychannel.VideoTaskResult{
		TaskId: taskID, TaskStatus: "succeed", Mode: "upscale", VideoType: "video-to-video",
		Prompt: req.Prompt,
		// 输入视频时长未知，沿用可配置的视频预扣规则；完成后由上游 cost 结算。
		Quota:       common.CalculateVideoQuota(meta.ActualModelName, "video-to-video", "upscale", "", "", ""),
		Credentials: pollingURL,
		PollingUrl:  videoClientPollingURL(taskID),
	}, nil
}
