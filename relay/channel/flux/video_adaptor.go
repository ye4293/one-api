package flux

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	dbmodel "github.com/songquanpeng/one-api/model"
	relaychannel "github.com/songquanpeng/one-api/relay/channel"
	openaiAdaptor "github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

// VideoAdaptor 对接 BFL FLUX 3 Video（异步：提交 → 轮询）
type VideoAdaptor struct {
	relaychannel.BaseVideoAdaptor
}

func (a *VideoAdaptor) GetProviderName() string      { return "flux" }
func (a *VideoAdaptor) GetChannelName() string       { return "Flux (BFL)" }
func (a *VideoAdaptor) GetSupportedModels() []string { return []string{"flux-3-video"} }

// durationToString 将 BFL duration（int / float64 / string，可能为 "auto"）转为字符串
func durationToString(d interface{}) string {
	switch v := d.(type) {
	case string:
		return v
	case float64:
		return strconv.Itoa(int(v))
	case int:
		return strconv.Itoa(v)
	case json.Number:
		return v.String()
	default:
		return ""
	}
}

// videoTypeFromMode 由 BFL mode 派生落库用的视频类型
func videoTypeFromMode(mode string) string {
	switch mode {
	case "t2v":
		return "text-to-video"
	case "i2v", "v2v", "draft_enhance":
		return "image-to-video"
	default:
		return ""
	}
}

func (a *VideoAdaptor) HandleVideoRequest(c *gin.Context, req *model.VideoRequest, meta *util.RelayMeta) (*relaychannel.VideoTaskResult, *model.ErrorWithStatusCode) {
	var fluxReq FluxVideoRequest
	if err := common.UnmarshalBodyReusable(c, &fluxReq); err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "invalid_video_generation_request", http.StatusBadRequest)
	}

	ch, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	// 提交：baseURL 含 replicate.com 走 Replicate，否则走 BFL 原生。
	var taskId string
	var submitErr *model.ErrorWithStatusCode
	if isReplicate(meta.BaseURL) {
		taskId, submitErr = a.submitReplicateVideo(fluxReq, meta, ch)
	} else {
		taskId, submitErr = a.submitBFLVideo(fluxReq, meta, ch)
	}
	if submitErr != nil {
		return nil, submitErr
	}

	// 计费参数归一化（方案 A：BFL/Replicate 同名共用一套按秒计费规则）
	durationStr := durationToString(fluxReq.Duration)
	// 计费口径归一：Replicate 原生 720p/1080p 需映射回 hd/fhd 才能命中计费规则
	resolution := normalizeBillingResolution(fluxReq.Resolution)
	sound := "on" // generate_audio 默认 true
	if fluxReq.GenerateAudio != nil && !*fluxReq.GenerateAudio {
		sound = "off"
	}
	videoType := videoTypeFromMode(fluxReq.Mode)

	quota := common.CalculateVideoQuota(meta.ActualModelName, videoType, fluxReq.Mode, durationStr, resolution, sound)

	return &relaychannel.VideoTaskResult{
		TaskId:     taskId,
		TaskStatus: "succeed", // 提交成功；实际生成结果由轮询决定
		Mode:       fluxReq.Mode,
		Duration:   durationStr,
		VideoType:  videoType,
		Resolution: resolution,
		Sound:      sound,
		Quota:      quota,
		Prompt:     fluxReq.Prompt,
	}, nil
}

// submitBFLVideo 走 BFL 原生：x-key + POST /v1/flux-3-video，解析 {id, polling_url}。
func (a *VideoAdaptor) submitBFLVideo(fluxReq FluxVideoRequest, meta *util.RelayMeta, ch *dbmodel.Channel) (string, *model.ErrorWithStatusCode) {
	requestURL := meta.BaseURL + "/v1/flux-3-video"

	httpResp, body, httpErr := relaychannel.SendJSONVideoRequest(requestURL, fluxReq, relaychannel.XKeyAuthHeaders(ch.Key))
	if httpErr != nil {
		return "", openaiAdaptor.ErrorWrapper(httpErr, "request_error", http.StatusInternalServerError)
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return "", openaiAdaptor.ErrorWrapper(
			fmt.Errorf("flux video API error: status=%d body=%s", httpResp.StatusCode, string(body)),
			"api_error", httpResp.StatusCode)
	}

	var submitResp FluxVideoSubmitResponse
	if parseErr := json.Unmarshal(body, &submitResp); parseErr != nil {
		return "", openaiAdaptor.ErrorWrapper(parseErr, "response_parse_error", http.StatusInternalServerError)
	}
	if submitResp.ID == "" {
		return "", openaiAdaptor.ErrorWrapper(
			fmt.Errorf("flux video API returned empty task id: body=%s", string(body)),
			"api_error", http.StatusInternalServerError)
	}
	return submitResp.ID, nil
}

// submitReplicateVideo 走 Replicate：Bearer + POST /v1/models/{id}/predictions，
// 请求体包进 {"input": ...}，不带 webhook（本项目走轮询），返回 prediction id。
func (a *VideoAdaptor) submitReplicateVideo(fluxReq FluxVideoRequest, meta *util.RelayMeta, ch *dbmodel.Channel) (string, *model.ErrorWithStatusCode) {
	replicateID, ok := ReplicateVideoModelMap[meta.ActualModelName]
	if !ok {
		return "", openaiAdaptor.ErrorWrapper(
			fmt.Errorf("模型 %s 在 Replicate 渠道暂不支持视频生成", meta.ActualModelName),
			"model_not_supported", http.StatusBadRequest)
	}
	requestURL := fmt.Sprintf("%s/v1/models/%s/predictions", meta.BaseURL, replicateID)
	reqBody := map[string]any{"input": buildReplicateVideoInput(fluxReq)}

	httpResp, body, httpErr := relaychannel.SendJSONVideoRequest(requestURL, reqBody, relaychannel.BearerAuthHeaders(ch.Key))
	if httpErr != nil {
		return "", openaiAdaptor.ErrorWrapper(httpErr, "request_error", http.StatusInternalServerError)
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return "", openaiAdaptor.ErrorWrapper(
			fmt.Errorf("replicate video API error: status=%d body=%s", httpResp.StatusCode, string(body)),
			"api_error", httpResp.StatusCode)
	}

	var predResp ReplicateResponse
	if parseErr := json.Unmarshal(body, &predResp); parseErr != nil {
		return "", openaiAdaptor.ErrorWrapper(parseErr, "response_parse_error", http.StatusInternalServerError)
	}
	if predResp.ID == "" {
		return "", openaiAdaptor.ErrorWrapper(
			fmt.Errorf("replicate video API returned empty prediction id: body=%s", string(body)),
			"api_error", http.StatusInternalServerError)
	}
	return predResp.ID, nil
}

func (a *VideoAdaptor) HandleVideoResult(c *gin.Context, videoTask *dbmodel.Video, ch *dbmodel.Channel, cfg *dbmodel.ChannelConfig) (*model.GeneralFinalVideoResponse, *model.ErrorWithStatusCode) {
	taskId := videoTask.TaskId

	baseURL := "https://api.bfl.ai"
	if ch.BaseURL != nil && *ch.BaseURL != "" {
		baseURL = *ch.BaseURL
	}

	// 轮询：baseURL 含 replicate.com 走 Replicate predictions 查询，否则走 BFL get_result。
	if isReplicate(baseURL) {
		return a.handleReplicateVideoResult(videoTask, ch, baseURL)
	}

	queryURL := fmt.Sprintf("%s/v1/get_result?id=%s", baseURL, taskId)

	_, body, err := relaychannel.SendVideoResultQuery(queryURL, relaychannel.XKeyAuthHeaders(ch.Key))
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}

	var pollResp FluxVideoPollingResponse
	if parseErr := json.Unmarshal(body, &pollResp); parseErr != nil {
		log.Printf("Failed to parse flux video response: %v, body: %s", parseErr, string(body))
		return nil, openaiAdaptor.ErrorWrapper(parseErr, "json_parse_error", http.StatusInternalServerError)
	}

	generalResponse := &model.GeneralFinalVideoResponse{
		TaskId:   taskId,
		Duration: videoTask.Duration,
	}

	switch pollResp.Status {
	case UpstreamStatusReady:
		generalResponse.TaskStatus = "succeed"
		if pollResp.Result != nil && pollResp.Result.Sample != "" {
			generalResponse.VideoResult = pollResp.Result.Sample
			generalResponse.VideoResults = []model.VideoResultItem{{Url: pollResp.Result.Sample}}
		}
	case UpstreamStatusError, UpstreamStatusRequestModerated, UpstreamStatusContentModerated:
		generalResponse.TaskStatus = "failed"
		generalResponse.Message = fmt.Sprintf("flux video %s: %v", pollResp.Status, pollResp.Detail)
	default:
		generalResponse.TaskStatus = "processing"
	}

	return generalResponse, nil
}

// handleReplicateVideoResult 轮询 Replicate prediction：Bearer + GET /v1/predictions/{id}。
// status: starting/processing→processing，succeeded→succeed（Output 为 mp4 URL），failed/canceled→failed。
func (a *VideoAdaptor) handleReplicateVideoResult(videoTask *dbmodel.Video, ch *dbmodel.Channel, baseURL string) (*model.GeneralFinalVideoResponse, *model.ErrorWithStatusCode) {
	taskId := videoTask.TaskId
	queryURL := fmt.Sprintf("%s/v1/predictions/%s", baseURL, taskId)

	_, body, err := relaychannel.SendVideoResultQuery(queryURL, relaychannel.BearerAuthHeaders(ch.Key))
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}

	var predResp ReplicateResponse
	if parseErr := json.Unmarshal(body, &predResp); parseErr != nil {
		log.Printf("Failed to parse replicate video response: %v, body: %s", parseErr, string(body))
		return nil, openaiAdaptor.ErrorWrapper(parseErr, "json_parse_error", http.StatusInternalServerError)
	}

	generalResponse := &model.GeneralFinalVideoResponse{
		TaskId:   taskId,
		Duration: videoTask.Duration,
	}

	switch predResp.Status {
	case "succeeded":
		generalResponse.TaskStatus = "succeed"
		if sample := string(predResp.Output); sample != "" {
			generalResponse.VideoResult = sample
			generalResponse.VideoResults = []model.VideoResultItem{{Url: sample}}
		}
	case "failed", "canceled":
		generalResponse.TaskStatus = "failed"
		generalResponse.Message = fmt.Sprintf("replicate video %s: %v", predResp.Status, predResp.Error)
	default: // starting / processing
		generalResponse.TaskStatus = "processing"
	}

	return generalResponse, nil
}
