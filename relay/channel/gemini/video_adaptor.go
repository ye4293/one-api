package gemini

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
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

// --- Interactions API 请求/响应结构体 ---

type interactionRequest struct {
	Model            string                 `json:"model"`
	Input            any                    `json:"input"`
	Background       bool                   `json:"background"`
	Store            bool                   `json:"store"`
	ResponseFormat   *interactionRespFormat  `json:"response_format,omitempty"`
	GenerationConfig *interactionGenConfig  `json:"generation_config,omitempty"`
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

	quota := common.CalculateVideoQuota("gemini-omni-flash-preview", "", "", "8", "", "")

	return &relaychannel.VideoTaskResult{
		TaskId:      interResp.ID,
		TaskStatus:  "succeed",
		Credentials: meta.ActualAPIKey,
		Quota:       quota,
	}, nil
}

// --- HandleVideoResult: 轮询视频生成结果 ---

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

	fullURL := fmt.Sprintf("%s/v1beta/interactions/%s?key=%s", baseURL, taskId, apiKey)

	resp, respBody, err := relaychannel.SendVideoResultQuery(fullURL, nil)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, openaiAdaptor.ErrorWrapper(
			fmt.Errorf("Interactions API returned HTTP %d: %s", resp.StatusCode, string(respBody)),
			"api_error", resp.StatusCode)
	}

	var interResp interactionResponse
	if err := json.Unmarshal(respBody, &interResp); err != nil {
		log.Printf("[GeminiOmni] Failed to parse result response: %s", string(respBody))
		return nil, openaiAdaptor.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}

	result := &model.GeneralFinalVideoResponse{
		TaskId:   taskId,
		VideoId:  taskId,
		Duration: videoTask.Duration,
	}

	switch interResp.Status {
	case "completed":
		videoURL := extractVideoFromInteraction(&interResp)
		if videoURL == "" {
			result.TaskStatus = "failed"
			result.Message = "Interaction completed but no video output found"
		} else {
			finalURL := videoURL
			if strings.HasPrefix(videoURL, "data:video/") {
				base64Data := extractBase64FromDataURI(videoURL)
				if uploaded, uploadErr := relaychannel.UploadVideoBase64ToR2(base64Data, videoTask.UserId, "mp4"); uploadErr == nil {
					finalURL = uploaded
				}
			}
			result.TaskStatus = "succeed"
			result.Message = "Video generated successfully"
			result.VideoResult = finalURL
			result.VideoResults = []model.VideoResultItem{{Url: finalURL}}
		}
	case "failed":
		result.TaskStatus = "failed"
		result.Message = "Video generation failed"
		if interResp.Error != nil {
			result.Message = interResp.Error.Message
		}
	case "cancelled":
		result.TaskStatus = "failed"
		result.Message = "Video generation was cancelled"
	default:
		result.TaskStatus = "processing"
		result.Message = "Video is being generated"
	}

	return result, nil
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
	return &model.GeneralFinalVideoResponse{
		TaskId:       taskId,
		VideoResult:  videoUrls[0],
		VideoId:      taskId,
		TaskStatus:   "succeed",
		Message:      "Video retrieved from cache",
		VideoResults: videoResults,
		Duration:     videoTask.Duration,
	}
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
