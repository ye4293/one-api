package gemini

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/config"
	dbmodel "github.com/songquanpeng/one-api/model"
	relaychannel "github.com/songquanpeng/one-api/relay/channel"
	openaiAdaptor "github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

type VideoAdaptor struct {
	relaychannel.BaseVideoAdaptor
}

func (a *VideoAdaptor) GetProviderName() string      { return "gemini-omni" }
func (a *VideoAdaptor) GetChannelName() string       { return "Gemini Omni Flash" }
func (a *VideoAdaptor) GetSupportedModels() []string { return []string{"gemini-omni-flash-preview"} }

// GetPrePaymentQuota 创建时不实际预扣费（Gemini Omni 改为成功完成后按真实 token 用量计费），
// 但保留 $0.2 的最低余额门槛用于创建时的余额校验，防止余额为 0 时无限提交任务透支。
func (a *VideoAdaptor) GetPrePaymentQuota() int64 {
	return int64(0.2 * config.QuotaPerUnit)
}

// --- Interactions API 请求/响应结构体 ---

type interactionRequest struct {
	Model            string                `json:"model"`
	Input            any                   `json:"input"`
	Background       bool                  `json:"background"`
	Store            bool                  `json:"store"`
	ResponseFormat   *interactionRespFormat `json:"response_format,omitempty"`
	GenerationConfig *interactionGenConfig `json:"generation_config,omitempty"`
}

type interactionRespFormat struct {
	Type        string `json:"type,omitempty"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
	Delivery    string `json:"delivery,omitempty"`
}

type interactionGenConfig struct {
	VideoConfig *videoConfig `json:"video_config,omitempty"`
}

type videoConfig struct {
	Task string `json:"task,omitempty"`
}

type interactionResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Model  string `json:"model"`
	Object string `json:"object"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Steps   []interactionStep   `json:"steps,omitempty"`
	Outputs []interactionOutput `json:"outputs,omitempty"`
	Usage   *interactionUsage   `json:"usage,omitempty"`
}

// interactionUsage 对应 Gemini Interactions API 返回的 usage 字段
type interactionUsage struct {
	TotalTokens        int64                      `json:"total_tokens"`
	TotalInputTokens   int64                      `json:"total_input_tokens"`
	TotalOutputTokens  int64                      `json:"total_output_tokens"`
	TotalCachedTokens  int64                      `json:"total_cached_tokens"`
	TotalThoughtTokens int64                      `json:"total_thought_tokens"`
	TotalToolUseTokens int64                      `json:"total_tool_use_tokens"`
	InputByModality    []interactionUsageModality `json:"input_tokens_by_modality,omitempty"`
	OutputByModality   []interactionUsageModality `json:"output_tokens_by_modality,omitempty"`
}

type interactionUsageModality struct {
	Modality string `json:"modality"`
	Tokens   int64  `json:"tokens"`
}

// GeminiOmniUsage 计费用 token 计数
type GeminiOmniUsage struct {
	InputTokens       int64
	OutputTextTokens  int64 // 含思考 token（= total_output - video）
	OutputVideoTokens int64
}

// ParseGeminiOmniUsage 从上游完整响应 JSON 解析计费用的 token 计数。
// 输出文本 = total_output_tokens - video_tokens（思考 token 计入文本，符合官方"输出文本包括思考 token"）。
func ParseGeminiOmniUsage(rawJSON string) (GeminiOmniUsage, error) {
	var resp interactionResponse
	if err := json.Unmarshal([]byte(rawJSON), &resp); err != nil {
		return GeminiOmniUsage{}, err
	}
	var u GeminiOmniUsage
	if resp.Usage == nil {
		return u, nil
	}
	u.InputTokens = resp.Usage.TotalInputTokens
	for _, m := range resp.Usage.OutputByModality {
		if m.Modality == "video" {
			u.OutputVideoTokens = m.Tokens
		}
	}
	u.OutputTextTokens = resp.Usage.TotalOutputTokens - u.OutputVideoTokens
	if u.OutputTextTokens < 0 {
		u.OutputTextTokens = 0
	}
	return u, nil
}

// buildVideoUsage 将上游 interactionUsage 转为响应中透传的 VideoUsage。
func buildVideoUsage(u *interactionUsage) *model.VideoUsage {
	if u == nil {
		return nil
	}
	return &model.VideoUsage{
		TotalTokens:            u.TotalTokens,
		TotalInputTokens:       u.TotalInputTokens,
		TotalOutputTokens:      u.TotalOutputTokens,
		TotalCachedTokens:      u.TotalCachedTokens,
		TotalThoughtTokens:     u.TotalThoughtTokens,
		TotalToolUseTokens:     u.TotalToolUseTokens,
		InputTokensByModality:  convertUsageModalities(u.InputByModality),
		OutputTokensByModality: convertUsageModalities(u.OutputByModality),
	}
}

func convertUsageModalities(in []interactionUsageModality) []model.VideoUsageModality {
	if len(in) == 0 {
		return nil
	}
	out := make([]model.VideoUsageModality, len(in))
	for i, m := range in {
		out[i] = model.VideoUsageModality{Modality: m.Modality, Tokens: m.Tokens}
	}
	return out
}

type interactionStep struct {
	Type    string              `json:"type"`
	Content []interactionOutput `json:"content,omitempty"`
}

type interactionOutput struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Data     string `json:"data,omitempty"`
	URI      string `json:"uri,omitempty"`
}

// --- HandleVideoRequest: 提交视频生成任务 ---

func (a *VideoAdaptor) HandleVideoRequest(c *gin.Context, req *model.VideoRequest, meta *util.RelayMeta) (*relaychannel.VideoTaskResult, *model.ErrorWithStatusCode) {
	baseURL := meta.BaseURL
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}
	baseURL = strings.TrimRight(baseURL, "/")
	fullURL := baseURL + "/v1beta/interactions?key=" + meta.ActualAPIKey

	var input any
	var task string

	if req.ImageURL != "" {
		task = "image_to_video"
		input = buildImageInput(req.ImageURL, req.Prompt)
	} else {
		task = "text_to_video"
		input = req.Prompt
	}

	interReq := interactionRequest{
		Model:      "gemini-omni-flash-preview",
		Input:      input,
		Background: true,
		Store:      true,
		ResponseFormat: &interactionRespFormat{
			Type:     "video",
			Delivery: "uri",
		},
		GenerationConfig: &interactionGenConfig{
			VideoConfig: &videoConfig{Task: task},
		},
	}

	resp, respBody, err := relaychannel.SendJSONVideoRequest(fullURL, interReq, nil)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}

	var interResp interactionResponse
	if err := json.Unmarshal(respBody, &interResp); err != nil {
		log.Printf("[GeminiOmni] Failed to parse response: %s", string(respBody))
		return nil, openaiAdaptor.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}

	if resp.StatusCode != http.StatusOK || interResp.Error != nil {
		errMsg := fmt.Sprintf("Interactions API error (HTTP %d)", resp.StatusCode)
		if interResp.Error != nil {
			errMsg = interResp.Error.Message
		}
		return nil, openaiAdaptor.ErrorWrapper(fmt.Errorf("%s", errMsg), "api_error", resp.StatusCode)
	}

	if interResp.ID == "" {
		return nil, openaiAdaptor.ErrorWrapper(
			fmt.Errorf("no interaction ID in response"), "invalid_response", http.StatusInternalServerError)
	}

	// 创建时不扣费：改为任务成功完成后按真实 token 用量计费（见 applyGeminiOmniSuccess）。
	return &relaychannel.VideoTaskResult{
		TaskId:      interResp.ID,
		TaskStatus:  "succeed", // 仅表示任务已受理
		Credentials: meta.ActualAPIKey,
		Quota:       0,
		Prompt:      req.Prompt,
	}, nil
}

// --- HandleVideoResult: 用户主动查询视频结果 ---

func (a *VideoAdaptor) HandleVideoResult(c *gin.Context, videoTask *dbmodel.Video, ch *dbmodel.Channel, cfg *dbmodel.ChannelConfig) (*model.GeneralFinalVideoResponse, *model.ErrorWithStatusCode) {
	taskId := videoTask.TaskId

	if videoTask.StoreUrl != "" {
		return buildCachedVideoResponse(taskId, videoTask), nil
	}

	apiKey := videoTask.Credentials
	if apiKey == "" {
		apiKey = ch.Key
	}

	baseURL := ch.GetBaseURL()
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	status, videoURL, failReason, rawJSON, err := FetchAndStoreVideoResult(baseURL, apiKey, taskId, videoTask.UserId)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}

	// 将上游完整响应 JSON 落库到 videos.result
	if rawJSON != "" {
		if jsonErr := dbmodel.UpdateVideoResult(taskId, rawJSON); jsonErr != nil {
			log.Printf("[GeminiOmni] Failed to save result JSON for task %s: %v", taskId, jsonErr)
		}
	}

	result := &model.GeneralFinalVideoResponse{
		TaskId:   taskId,
		VideoId:  taskId,
		Duration: videoTask.Duration,
	}

	switch status {
	case "succeed":
		result.TaskStatus = "succeed"
		result.Message = "Video generated successfully"
		result.VideoResult = videoURL
		result.VideoResults = []model.VideoResultItem{{Url: videoURL}}
		// 解析真实 usage 供 controller 按 token 计费；DB 状态/扣费由 controller 的
		// applyGeminiOmniSuccess 通过 CAS 统一处理，此处不更新 DB。
		if rawJSON != "" {
			result.RawResult = rawJSON
			if usage, parseErr := ParseGeminiOmniUsage(rawJSON); parseErr == nil {
				result.InputTokens = usage.InputTokens
				result.OutputTextTokens = usage.OutputTextTokens
				result.OutputVideoTokens = usage.OutputVideoTokens
			} else {
				log.Printf("[GeminiOmni] Failed to parse usage for task %s: %v", taskId, parseErr)
			}
			// 透传上游 usage 到响应，供客户端查看真实 token 用量
			var interResp interactionResponse
			if json.Unmarshal([]byte(rawJSON), &interResp) == nil {
				result.Usage = buildVideoUsage(interResp.Usage)
			}
		}
	case "failed":
		result.TaskStatus = "failed"
		result.Message = failReason
	default:
		result.TaskStatus = "processing"
		result.Message = "Video is being generated"
	}

	return result, nil
}

// --- FetchAndStoreVideoResult: 核心逻辑，供用户轮询和后台 poller 共用 ---

// FetchAndStoreVideoResult 从 Gemini Interactions API 获取视频生成结果。
// 若已完成则下载视频并转存到 R2，返回安全的公开 URL。
// API Key 仅用于服务端内部请求，不会暴露给终端用户。
func FetchAndStoreVideoResult(baseURL, apiKey, taskId string, userId int) (status string, videoURL string, failReason string, rawJSON string, err error) {
	fullURL := fmt.Sprintf("%s/v1beta/interactions/%s?key=%s", baseURL, taskId, apiKey)

	resp, respBody, reqErr := relaychannel.SendVideoResultQuery(fullURL, nil)
	if reqErr != nil {
		return "", "", "", "", fmt.Errorf("request failed: %v", reqErr)
	}

	if resp.StatusCode != http.StatusOK {
		return "", "", "", "", fmt.Errorf("Interactions API returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var interResp interactionResponse
	if jsonErr := json.Unmarshal(respBody, &interResp); jsonErr != nil {
		return "", "", "", "", fmt.Errorf("failed to parse response: %v", jsonErr)
	}

	// 上游完整响应体，供调用方落库到 videos.result
	rawJSON = string(respBody)

	switch interResp.Status {
	case "completed":
		rawURL := extractVideoFromInteraction(&interResp)
		if rawURL == "" {
			return "failed", "", "Interaction completed but no video output found", rawJSON, nil
		}

		finalURL, storeErr := storeVideoToR2(rawURL, apiKey, userId)
		if storeErr != nil {
			log.Printf("[GeminiOmni] R2 upload failed for task %s: %v", taskId, storeErr)
			return "processing", "", "", rawJSON, nil
		}
		return "succeed", finalURL, "", rawJSON, nil

	case "failed":
		reason := "Video generation failed"
		if interResp.Error != nil {
			reason = interResp.Error.Message
		}
		return "failed", "", reason, rawJSON, nil

	case "cancelled":
		return "failed", "", "Video generation was cancelled", rawJSON, nil

	default:
		return "processing", "", "", rawJSON, nil
	}
}

// storeVideoToR2 将视频安全转存到 R2，不暴露 API Key
func storeVideoToR2(rawURL, apiKey string, userId int) (string, error) {
	if strings.HasPrefix(rawURL, "data:video/") {
		base64Data := extractBase64FromDataURI(rawURL)
		return relaychannel.UploadVideoBase64ToR2(base64Data, userId, "mp4")
	}

	if strings.Contains(rawURL, "generativelanguage.googleapis.com") {
		sep := "&"
		if !strings.Contains(rawURL, "?") {
			sep = "?"
		}
		authedURL := rawURL + sep + "key=" + apiKey
		return relaychannel.UploadVideoURLToR2(authedURL, userId, "mp4")
	}

	return rawURL, nil
}

// --- 辅助函数 ---

func buildImageInput(imageURL, prompt string) []map[string]string {
	mimeType := "image/jpeg"
	imageData := imageURL

	if strings.HasPrefix(imageURL, "data:") {
		parts := strings.SplitN(imageURL, ",", 2)
		if len(parts) == 2 {
			imageData = parts[1]
			if metaPart := strings.TrimPrefix(parts[0], "data:"); strings.Contains(metaPart, ";") {
				mimeType = strings.Split(metaPart, ";")[0]
			}
		}
		return []map[string]string{
			{"type": "image", "data": imageData, "mime_type": mimeType},
			{"type": "text", "text": prompt},
		}
	}

	if strings.HasPrefix(imageURL, "http") {
		return []map[string]string{
			{"type": "image", "uri": imageURL, "mime_type": mimeType},
			{"type": "text", "text": prompt},
		}
	}

	return []map[string]string{
		{"type": "image", "data": imageData, "mime_type": mimeType},
		{"type": "text", "text": prompt},
	}
}

func buildCachedVideoResponse(taskId string, videoTask *dbmodel.Video) *model.GeneralFinalVideoResponse {
	var videoUrls []string
	if err := json.Unmarshal([]byte(videoTask.StoreUrl), &videoUrls); err != nil {
		videoUrls = []string{videoTask.StoreUrl}
	}
	videoResults := make([]model.VideoResultItem, len(videoUrls))
	for i, u := range videoUrls {
		videoResults[i] = model.VideoResultItem{Url: u}
	}
	resp := &model.GeneralFinalVideoResponse{
		TaskId:       taskId,
		VideoResult:  videoUrls[0],
		VideoId:      taskId,
		TaskStatus:   "succeed",
		Message:      "Video retrieved from cache",
		VideoResults: videoResults,
		Duration:     videoTask.Duration,
	}
	// 缓存命中时从已落库的 result JSON 还原 usage，保持响应格式一致
	if videoTask.Result != "" {
		var interResp interactionResponse
		if json.Unmarshal([]byte(videoTask.Result), &interResp) == nil {
			resp.Usage = buildVideoUsage(interResp.Usage)
		}
	}
	return resp
}

func extractVideoFromInteraction(resp *interactionResponse) string {
	for _, step := range resp.Steps {
		if step.Type == "model_output" {
			for _, content := range step.Content {
				if content.Type == "video" {
					if content.URI != "" {
						return content.URI
					}
					if content.Data != "" {
						mime := content.MimeType
						if mime == "" {
							mime = "video/mp4"
						}
						return "data:" + mime + ";base64," + content.Data
					}
				}
			}
		}
	}
	for _, output := range resp.Outputs {
		if output.Type == "video" {
			if output.URI != "" {
				return output.URI
			}
			if output.Data != "" {
				mime := output.MimeType
				if mime == "" {
					mime = "video/mp4"
				}
				return "data:" + mime + ";base64," + output.Data
			}
		}
	}
	return ""
}

func extractBase64FromDataURI(dataURI string) string {
	idx := strings.Index(dataURI, ";base64,")
	if idx == -1 {
		return dataURI
	}
	return dataURI[idx+8:]
}
