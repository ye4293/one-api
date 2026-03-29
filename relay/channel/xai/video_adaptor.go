package xai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/config"
	dbmodel "github.com/songquanpeng/one-api/model"
	relaychannel "github.com/songquanpeng/one-api/relay/channel"
	openaiAdaptor "github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

// checkBalance verifies that the user has enough quota before sending the request
func checkBalance(c *gin.Context, meta *util.RelayMeta, quota int64) *model.ErrorWithStatusCode {
	userQuota, err := dbmodel.CacheGetUserQuota(c.Request.Context(), meta.UserId)
	if err != nil {
		return openaiAdaptor.ErrorWrapper(err, "get_user_quota_error", http.StatusInternalServerError)
	}
	if userQuota-quota < 0 {
		return openaiAdaptor.ErrorWrapper(fmt.Errorf("用户余额不足"), "User balance is not enough", http.StatusBadRequest)
	}
	return nil
}

type VideoAdaptor struct {
	relaychannel.BaseVideoAdaptor
}

func (a *VideoAdaptor) GetProviderName() string { return "grok" }
func (a *VideoAdaptor) GetChannelName() string  { return "xAI Grok" }
func (a *VideoAdaptor) GetSupportedModels() []string {
	return []string{"grok-imagine-video"}
}

func (a *VideoAdaptor) HandleVideoRequest(c *gin.Context, req *model.VideoRequest, meta *util.RelayMeta) (*relaychannel.VideoTaskResult, *model.ErrorWithStatusCode) {
	baseUrl := meta.BaseURL
	if baseUrl == "" {
		baseUrl = "https://api.x.ai"
	}

	var grokParams struct {
		Duration   int    `json:"duration"`
		Resolution string `json:"resolution"`
		Video      *struct {
			URL string `json:"url"`
		} `json:"video,omitempty"`
		Image *struct {
			URL string `json:"url"`
		} `json:"image,omitempty"`
	}

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "read_request_body_failed", http.StatusBadRequest)
	}
	if err := json.Unmarshal(bodyBytes, &grokParams); err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "invalid_video_request", http.StatusBadRequest)
	}

	duration := grokParams.Duration
	if duration <= 0 {
		duration = 8
	}
	if duration > 15 {
		duration = 15
	}

	resolution := grokParams.Resolution
	if resolution == "" {
		resolution = "480p"
	}

	hasVideoInput := grokParams.Video != nil && grokParams.Video.URL != ""
	hasImageInput := grokParams.Image != nil && grokParams.Image.URL != ""

	var fullRequestUrl string
	if hasVideoInput {
		fullRequestUrl = baseUrl + "/v1/videos/edits"
		log.Printf("[Grok Video] 视频编辑请求 - video_url=%s, duration=%d, resolution=%s", grokParams.Video.URL, duration, resolution)
	} else {
		fullRequestUrl = baseUrl + "/v1/videos/generations"
		log.Printf("[Grok Video] 视频生成请求 - duration=%d, resolution=%s", duration, resolution)
	}

	quota := computeGrokQuota(duration, resolution, hasVideoInput, hasImageInput)
	log.Printf("[Grok Video] 预扣费用 - duration=%d, resolution=%s, quota=%d", duration, resolution, quota)

	// 基于实际请求参数做精确余额校验，与重构前行为一致
	if balErr := checkBalance(c, meta, quota); balErr != nil {
		return nil, balErr
	}

	httpReq, err := http.NewRequest(http.MethodPost, fullRequestUrl, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+meta.APIKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	log.Printf("[Grok Video] 响应状态码: %d, 响应体: %s", resp.StatusCode, string(body))

	var grokResponse GrokVideoResponse
	if err := json.Unmarshal(body, &grokResponse); err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}
	grokResponse.StatusCode = resp.StatusCode

	if grokResponse.StatusCode != 200 && grokResponse.StatusCode != 202 {
		errorMsg := grokResponse.Error
		if errorMsg == "" {
			errorMsg = string(body)
		}
		return nil, openaiAdaptor.ErrorWrapper(
			fmt.Errorf("%s: %s", grokResponse.Code, errorMsg),
			"api_error", grokResponse.StatusCode)
	}

	// Credentials 字段由 invokeVideoAdaptorRequest 在 CreateVideoLog 之后写入 DB
	// 此处不直接调用 UpdateVideoCredentials，避免记录未创建导致写入失败
	return &relaychannel.VideoTaskResult{
		TaskId:      grokResponse.RequestId,
		TaskStatus:  "succeed",
		Duration:    strconv.Itoa(duration),
		Resolution:  resolution,
		Quota:       quota,
		Credentials: meta.APIKey,
	}, nil
}

func (a *VideoAdaptor) HandleVideoResult(c *gin.Context, videoTask *dbmodel.Video, ch *dbmodel.Channel, cfg *dbmodel.ChannelConfig) (*model.GeneralFinalVideoResponse, *model.ErrorWithStatusCode) {
	taskId := videoTask.TaskId

	// Return cached URL if already stored
	if videoTask.StoreUrl != "" {
		log.Printf("[Grok Video] 使用缓存的视频URL - taskId=%s", taskId)
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
			VideoId:      taskId,
			TaskStatus:   "succeed",
			Message:      "Video retrieved from cache",
			VideoResult:  videoUrls[0],
			VideoResults: videoResults,
			Duration:     videoTask.Duration,
		}, nil
	}

	// Resolve API key: prefer saved credentials, fall back to channel key
	apiKey := videoTask.Credentials
	if apiKey == "" {
		keys := strings.Split(ch.Key, "\n")
		for _, k := range keys {
			k = strings.TrimSpace(k)
			if k != "" {
				apiKey = k
				break
			}
		}
	}

	baseUrl := *ch.BaseURL
	if baseUrl == "" {
		baseUrl = "https://api.x.ai"
	}
	url := fmt.Sprintf("%s/v1/videos/%s", baseUrl, taskId)

	httpReq, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	log.Printf("[Grok Video] 查询响应 - taskId=%s, statusCode=%d, body=%s", taskId, resp.StatusCode, string(body))

	var grokResult GrokVideoResult
	if err := json.Unmarshal(body, &grokResult); err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "json_parse_error", http.StatusInternalServerError)
	}

	generalResponse := &model.GeneralFinalVideoResponse{
		TaskId:     taskId,
		VideoId:    taskId,
		TaskStatus: "processing",
		Message:    "Video is still processing",
		Duration:   videoTask.Duration,
	}

	if resp.StatusCode != 200 && resp.StatusCode != 202 {
		errorMsg := grokResult.Error
		if errorMsg == "" {
			errorMsg = string(body)
		}
		failMessage := fmt.Sprintf("%s: %s", grokResult.Code, errorMsg)
		log.Printf("[Grok Video] 查询错误 - taskId=%s, code: %s, error: %s", taskId, grokResult.Code, errorMsg)
		generalResponse.TaskStatus = "failed"
		generalResponse.Message = failMessage
		return generalResponse, nil
	}

	if grokResult.Video != nil && grokResult.Video.URL != "" {
		log.Printf("[Grok Video] 视频完成 - taskId=%s, url=%s", taskId, grokResult.Video.URL)
		if updateErr := dbmodel.UpdateVideoStoreUrl(taskId, grokResult.Video.URL); updateErr != nil {
			log.Printf("[Grok Video] 保存URL失败 - taskId=%s, error=%v", taskId, updateErr)
		}
		generalResponse.TaskStatus = "succeed"
		generalResponse.Message = "Video generation completed"
		generalResponse.VideoResult = grokResult.Video.URL
		generalResponse.VideoResults = []model.VideoResultItem{{Url: grokResult.Video.URL}}
		if grokResult.Video.Duration > 0 {
			generalResponse.Duration = strconv.Itoa(grokResult.Video.Duration)
		}
	} else if grokResult.Status == "pending" {
		generalResponse.TaskStatus = "processing"
		generalResponse.Message = "Video generation in progress"
	} else if grokResult.Error != "" {
		generalResponse.TaskStatus = "failed"
		generalResponse.Message = fmt.Sprintf("Video generation failed: %s", grokResult.Error)
	} else {
		generalResponse.TaskStatus = "processing"
		generalResponse.Message = fmt.Sprintf("Video status: %s", grokResult.Status)
	}

	return generalResponse, nil
}

func computeGrokQuota(duration int, resolution string, hasVideoInput, hasImageInput bool) int64 {
	outputPrice := 0.05
	if resolution == "720p" {
		outputPrice = 0.07
	}
	total := float64(duration) * outputPrice
	if hasVideoInput {
		total += float64(duration) * 0.01
	} else if hasImageInput {
		total += 0.002
	}
	return int64(total * config.QuotaPerUnit)
}
