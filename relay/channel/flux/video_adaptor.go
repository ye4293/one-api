package flux

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
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
	// pollingURL：BFL 原生返回上游 polling_url（多集群路由地址，必须原样使用）；
	// Replicate 无此语义，返回空串。
	var taskId, pollingURL string
	var submitErr *model.ErrorWithStatusCode
	if isReplicate(meta.BaseURL) {
		taskId, pollingURL, submitErr = a.submitReplicateVideo(fluxReq, meta, ch)
	} else {
		taskId, pollingURL, submitErr = a.submitBFLVideo(fluxReq, meta, ch)
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

	// 客户端轮询端点：one-api 自有代理地址（客户端持 one-api token 查，命中 GetFlux → GetVideoResult）。
	// 与承载 BFL 上游 polling_url 的 Credentials 严格区分——上游 polling_url 含 BFL key 语义，不可返给客户端。
	clientPollingURL := ""
	if config.ServerAddress != "" {
		clientPollingURL = fmt.Sprintf("%s/flux/v1/get_result?id=%s", strings.TrimRight(config.ServerAddress, "/"), taskId)
	}

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
		// 复用 Credentials 承载 BFL 上游 polling_url，供轮询命中正确集群（避免 Task not found）。
		// Replicate 分支 pollingURL 为空，轮询走回退自拼 URL。
		Credentials: pollingURL,
		PollingUrl:  clientPollingURL,
	}, nil
}

// submitBFLVideo 走 BFL 原生：x-key + POST /v1/flux-3-video，解析 {id, polling_url}。
// 返回 (taskId, pollingURL)：pollingURL 为上游多集群路由地址，轮询必须原样使用。
func (a *VideoAdaptor) submitBFLVideo(fluxReq FluxVideoRequest, meta *util.RelayMeta, ch *dbmodel.Channel) (string, string, *model.ErrorWithStatusCode) {
	requestURL := meta.BaseURL + "/v1/flux-3-video"

	httpResp, body, httpErr := relaychannel.SendJSONVideoRequest(requestURL, fluxReq, relaychannel.XKeyAuthHeaders(ch.Key))
	if httpErr != nil {
		return "", "", openaiAdaptor.ErrorWrapper(httpErr, "request_error", http.StatusInternalServerError)
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return "", "", openaiAdaptor.ErrorWrapper(
			fmt.Errorf("flux video API error: status=%d body=%s", httpResp.StatusCode, string(body)),
			"api_error", httpResp.StatusCode)
	}

	var submitResp FluxVideoSubmitResponse
	if parseErr := json.Unmarshal(body, &submitResp); parseErr != nil {
		return "", "", openaiAdaptor.ErrorWrapper(parseErr, "response_parse_error", http.StatusInternalServerError)
	}
	if submitResp.ID == "" {
		return "", "", openaiAdaptor.ErrorWrapper(
			fmt.Errorf("flux video API returned empty task id: body=%s", string(body)),
			"api_error", http.StatusInternalServerError)
	}
	return submitResp.ID, submitResp.PollingURL, nil
}

// submitReplicateVideo 走 Replicate：Bearer + POST /v1/models/{id}/predictions，
// 请求体包进 {"input": ...}，不带 webhook（本项目走轮询），返回 prediction id。
// 返回 (taskId, pollingURL)：Replicate 轮询用 predictions/{id}、渠道自有 baseURL，
// 无多集群路由问题，pollingURL 恒为空串（轮询走回退自拼）。
func (a *VideoAdaptor) submitReplicateVideo(fluxReq FluxVideoRequest, meta *util.RelayMeta, ch *dbmodel.Channel) (string, string, *model.ErrorWithStatusCode) {
	replicateID, ok := ReplicateVideoModelMap[meta.ActualModelName]
	if !ok {
		return "", "", openaiAdaptor.ErrorWrapper(
			fmt.Errorf("模型 %s 在 Replicate 渠道暂不支持视频生成", meta.ActualModelName),
			"model_not_supported", http.StatusBadRequest)
	}
	requestURL := fmt.Sprintf("%s/v1/models/%s/predictions", meta.BaseURL, replicateID)
	reqBody := map[string]any{"input": buildReplicateVideoInput(fluxReq)}

	httpResp, body, httpErr := relaychannel.SendJSONVideoRequest(requestURL, reqBody, relaychannel.BearerAuthHeaders(ch.Key))
	if httpErr != nil {
		return "", "", openaiAdaptor.ErrorWrapper(httpErr, "request_error", http.StatusInternalServerError)
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return "", "", openaiAdaptor.ErrorWrapper(
			fmt.Errorf("replicate video API error: status=%d body=%s", httpResp.StatusCode, string(body)),
			"api_error", httpResp.StatusCode)
	}

	var predResp ReplicateResponse
	if parseErr := json.Unmarshal(body, &predResp); parseErr != nil {
		return "", "", openaiAdaptor.ErrorWrapper(parseErr, "response_parse_error", http.StatusInternalServerError)
	}
	if predResp.ID == "" {
		return "", "", openaiAdaptor.ErrorWrapper(
			fmt.Errorf("replicate video API returned empty prediction id: body=%s", string(body)),
			"api_error", http.StatusInternalServerError)
	}
	return predResp.ID, "", nil
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

	// 轮询地址优先用提交时上游返回并落库（credentials）的 polling_url——BFL 多集群路由，
	// 任务只在分配到的集群可查，用 id 自拼全局 URL 会打错集群返回 404 "Task not found"。
	// 存量任务无 polling_url（credentials 空或非 http）时回退到自拼 URL，保证兼容。
	queryURL := fmt.Sprintf("%s/v1/get_result?id=%s", baseURL, taskId)
	if strings.HasPrefix(videoTask.Credentials, "http") {
		queryURL = videoTask.Credentials
	}

	resp, body, err := relaychannel.SendVideoResultQuery(queryURL, relaychannel.XKeyAuthHeaders(ch.Key))
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}

	generalResponse := &model.GeneralFinalVideoResponse{
		TaskId:   taskId,
		Duration: videoTask.Duration,
	}

	// 上游 HTTP 404：任务在 BFL 侧已不存在（无效 id 或结果已过期），不可恢复。
	// 直接判失败以触发退款；否则 body 里的 "Task not found" 会被 default 分支
	// 误判为 processing，任务永久卡死且用户永不退款（本次 stuck-processing 的根因）。
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		generalResponse.TaskStatus = "failed"
		generalResponse.Message = fmt.Sprintf("flux video task not found (upstream 404): %s", string(body))
		return generalResponse, nil
	}
	// 其余非 2xx（401/403/429/5xx 等）多为临时或配置问题：返回错误交上层重试，
	// 不判失败、不退款，避免把可恢复错误误结算为失败。
	if resp != nil && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		return nil, openaiAdaptor.ErrorWrapper(
			fmt.Errorf("flux get_result error: status=%d body=%s", resp.StatusCode, string(body)),
			"api_error", resp.StatusCode)
	}

	var pollResp FluxVideoPollingResponse
	if parseErr := json.Unmarshal(body, &pollResp); parseErr != nil {
		log.Printf("Failed to parse flux video response: %v, body: %s", parseErr, string(body))
		return nil, openaiAdaptor.ErrorWrapper(parseErr, "json_parse_error", http.StatusInternalServerError)
	}

	switch pollResp.Status {
	case UpstreamStatusReady:
		generalResponse.TaskStatus = "succeed"
		if pollResp.Result != nil && pollResp.Result.Sample != "" {
			generalResponse.VideoResult = pollResp.Result.Sample
			generalResponse.VideoResults = []model.VideoResultItem{{Url: pollResp.Result.Sample}}
		}
	case UpstreamStatusError, UpstreamStatusRequestModerated, UpstreamStatusContentModerated, UpstreamStatusTaskNotFound:
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

	resp, body, err := relaychannel.SendVideoResultQuery(queryURL, relaychannel.BearerAuthHeaders(ch.Key))
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}

	generalResponse := &model.GeneralFinalVideoResponse{
		TaskId:   taskId,
		Duration: videoTask.Duration,
	}

	// 上游 HTTP 404：Replicate 侧 prediction 不存在（无效 id 或已被清理），不可恢复。
	// 判失败以触发退款——Replicate 404 body 无 status 字段，若不在此拦截会解析出
	// 空 status 落 default 被误判为 processing，与 BFL 分支同样卡死。
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		generalResponse.TaskStatus = "failed"
		generalResponse.Message = fmt.Sprintf("replicate prediction not found (upstream 404): %s", string(body))
		return generalResponse, nil
	}
	if resp != nil && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		return nil, openaiAdaptor.ErrorWrapper(
			fmt.Errorf("replicate get prediction error: status=%d body=%s", resp.StatusCode, string(body)),
			"api_error", resp.StatusCode)
	}

	var predResp ReplicateResponse
	if parseErr := json.Unmarshal(body, &predResp); parseErr != nil {
		log.Printf("Failed to parse replicate video response: %v, body: %s", parseErr, string(body))
		return nil, openaiAdaptor.ErrorWrapper(parseErr, "json_parse_error", http.StatusInternalServerError)
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
