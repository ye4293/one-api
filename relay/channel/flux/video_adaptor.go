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
		return strconv.FormatFloat(v, 'f', -1, 64)
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
	if err := normalizeVideoRequest(&fluxReq, isReplicate(meta.BaseURL)); err != nil {
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
	// 参数已在提交前归一化，计费和落库直接使用实际提交的值。
	resolution := fluxReq.Resolution
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
	if err := normalizeVideoRequest(&fluxReq, false); err != nil {
		return "", "", openaiAdaptor.ErrorWrapper(err, "invalid_video_generation_request", http.StatusBadRequest)
	}
	requestURL := meta.BaseURL + "/v1/flux-3-video"

	httpResp, body, httpErr := relaychannel.SendJSONVideoRequest(requestURL, buildBFLVideoInput(fluxReq), relaychannel.XKeyAuthHeaders(ch.Key))
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
	input, err := buildReplicateVideoInput(fluxReq)
	if err != nil {
		return "", "", openaiAdaptor.ErrorWrapper(err, "invalid_video_generation_request", http.StatusBadRequest)
	}
	reqBody := map[string]any{"input": input}

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
		generalResponse.RawResult = string(body) // 留存上游原始 body 供审计/排障
		generalResponse.FluxStatus = UpstreamStatusTaskNotFound
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
	// 留存上游 get_result 完整原始 JSON（含 status/result.sample，若上游返回则含 cost）
	generalResponse.RawResult = string(body)
	// 透传 BFL 原生字段（status/result/progress/details/preview）供 flux get_result 对齐上游结构
	generalResponse.FluxStatus = pollResp.Status
	fillFluxNativeFields(generalResponse, body)

	switch pollResp.Status {
	case UpstreamStatusReady:
		generalResponse.TaskStatus = "succeed"
		if pollResp.Result != nil && pollResp.Result.Sample != "" {
			generalResponse.VideoResult = pollResp.Result.Sample
			generalResponse.VideoResults = []model.VideoResultItem{{Url: pollResp.Result.Sample}}
		}
		generalResponse.UpstreamCost = pollResp.Cost // 上游权威费用(美分),>0 时触发完成结算
	case UpstreamStatusError, UpstreamStatusRequestModerated, UpstreamStatusContentModerated, UpstreamStatusTaskNotFound:
		generalResponse.TaskStatus = "failed"
		// 上游实际字段是 details(复数),优先取非空的 Details,回退 Detail,避免审核原因丢失显示 <nil>
		detail := pollResp.Details
		if detail == nil {
			detail = pollResp.Detail
		}
		generalResponse.Message = fmt.Sprintf("flux video %s: %v", pollResp.Status, detail)
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
		generalResponse.RawResult = string(body) // 留存上游原始 body 供审计/排障
		generalResponse.FluxStatus = UpstreamStatusTaskNotFound
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
	// 留存上游 prediction 完整原始 JSON（含 status/output/metrics）
	generalResponse.RawResult = string(body)
	// Replicate 无 BFL 原生结构，主动映射为 BFL 字面 status 供 flux get_result 对齐
	generalResponse.FluxStatus = replicateStatusToBFL(predResp.Status)

	switch predResp.Status {
	case "succeeded":
		generalResponse.TaskStatus = "succeed"
		if sample := string(predResp.Output); sample != "" {
			generalResponse.VideoResult = sample
			generalResponse.VideoResults = []model.VideoResultItem{{Url: sample}}
			// 构造 BFL 风格 result 对象 {"sample": "<mp4 url>"}
			if raw, err := json.Marshal(map[string]string{"sample": sample}); err == nil {
				generalResponse.FluxResult = raw
			}
		}
		fillReplicateVideoBilling(generalResponse, videoTask, predResp)
	case "failed", "canceled":
		generalResponse.TaskStatus = "failed"
		generalResponse.Message = fmt.Sprintf("replicate video %s: %v", predResp.Status, predResp.Error)
		// details 透传上游 error 供 flux get_result 展示失败原因
		if predResp.Error != nil {
			if raw, err := json.Marshal(map[string]interface{}{"error": predResp.Error}); err == nil {
				generalResponse.FluxDetails = raw
			}
		}
	default: // starting / processing
		generalResponse.TaskStatus = "processing"
	}

	return generalResponse, nil
}

// replicateStatusToBFL 把 Replicate prediction 状态映射为 BFL 原生 status 字面值，
// 使 flux get_result 对 Replicate 路径也返回与 BFL 一致的 status。
func replicateStatusToBFL(status string) string {
	switch status {
	case "succeeded":
		return UpstreamStatusReady
	case "failed", "canceled":
		return UpstreamStatusError
	case "starting":
		return "Pending"
	case "processing":
		return "Processing"
	default:
		return status
	}
}

// fillFluxNativeFields 从 BFL get_result 原始 body 提取 result/progress/details/preview
// 原样填充到 generalResponse（供 flux get_result 对齐上游结构）。上游为 null/缺失则保持零值。
func fillFluxNativeFields(r *model.GeneralFinalVideoResponse, body []byte) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return
	}
	notNull := func(v json.RawMessage) bool { return len(v) > 0 && string(v) != "null" }
	if v, ok := raw["result"]; ok && notNull(v) {
		r.FluxResult = v
	}
	if v, ok := raw["details"]; ok && notNull(v) {
		r.FluxDetails = v
	}
	if v, ok := raw["preview"]; ok && notNull(v) {
		r.FluxPreview = v
	}
	if v, ok := raw["progress"]; ok && notNull(v) {
		var p float64
		if json.Unmarshal(v, &p) == nil {
			r.FluxProgress = p
		}
	}
}

// BuildTerminalResultFromDB 在任务已终态（succeed/failed）时，用库里数据组装
// GeneralFinalVideoResponse，供 flux get_result 的 DB 优先路径直接返回，不回源。
//
// 语义要点：
//   - TaskStatus 以 DB videoTask.Status 为权威，绝不由 raw 反推——失败任务的 raw 可能是
//     上游 404/error body（非 BFL 7 字段结构），若反推会被误判为 processing。
//   - FluxStatus/result/details/preview 尽力从 videoTask.Result 原始 JSON 提取；缺失则兜底：
//     succeed 无 sample 时回退 store_url，failed 无 details 时回退 fail_reason。
//   - 不设置 UpstreamCost：DB 优先不触发结算（cost 由调用方按已结算 quota 换算）。
//
// 返回 (gr, true) 命中 DB 优先；(nil, false) 表示非终态（processing），调用方需回源。
func BuildTerminalResultFromDB(videoTask *dbmodel.Video, baseURL string) (*model.GeneralFinalVideoResponse, bool) {
	status := videoTask.Status
	if status != "succeed" && status != "failed" {
		return nil, false
	}

	gr := &model.GeneralFinalVideoResponse{
		TaskId:     videoTask.TaskId,
		Duration:   videoTask.Duration,
		TaskStatus: status, // 以 DB 为权威
	}

	raw := []byte(videoTask.Result)
	if len(raw) > 0 {
		if isReplicate(baseURL) {
			fillFromReplicateRaw(gr, raw)
		} else {
			fillFromBFLRaw(gr, raw)
		}
	}

	// 兜底 FluxStatus：raw 未提供有效 status 时按 DB 状态回填 BFL 字面值
	if gr.FluxStatus == "" {
		if status == "succeed" {
			gr.FluxStatus = UpstreamStatusReady
		} else {
			gr.FluxStatus = UpstreamStatusError
		}
	}
	// succeed 但 raw 未取到视频 URL → 用 store_url 兜底（覆盖 result 列为空的存量成功任务）
	if status == "succeed" && gr.VideoResult == "" && videoTask.StoreUrl != "" {
		gr.VideoResult = videoTask.StoreUrl
		gr.VideoResults = []model.VideoResultItem{{Url: videoTask.StoreUrl}}
		if b, err := json.Marshal(map[string]string{"sample": videoTask.StoreUrl}); err == nil {
			gr.FluxResult = b
		}
	}
	// failed 且 raw 未取到 details → 用 fail_reason 兜底，避免失败原因丢失
	if status == "failed" && len(gr.FluxDetails) == 0 && videoTask.FailReason != "" {
		if b, err := json.Marshal(map[string]string{"detail": videoTask.FailReason}); err == nil {
			gr.FluxDetails = b
		}
	}

	return gr, true
}

// fillFromBFLRaw 从 BFL get_result 原始 JSON 提取 status/result.sample 及原生展示字段到 gr。
func fillFromBFLRaw(gr *model.GeneralFinalVideoResponse, body []byte) {
	var pollResp FluxVideoPollingResponse
	if err := json.Unmarshal(body, &pollResp); err == nil {
		gr.FluxStatus = pollResp.Status
		if pollResp.Result != nil && pollResp.Result.Sample != "" {
			gr.VideoResult = pollResp.Result.Sample
			gr.VideoResults = []model.VideoResultItem{{Url: pollResp.Result.Sample}}
		}
	}
	fillFluxNativeFields(gr, body) // result/details/preview/progress 原样透传
}

// fillFromReplicateRaw 从 Replicate prediction 原始 JSON 提取 output/error 并映射为 BFL 字面结构。
func fillFromReplicateRaw(gr *model.GeneralFinalVideoResponse, body []byte) {
	var predResp ReplicateResponse
	if err := json.Unmarshal(body, &predResp); err != nil {
		return
	}
	gr.FluxStatus = replicateStatusToBFL(predResp.Status)
	if sample := string(predResp.Output); sample != "" {
		gr.VideoResult = sample
		gr.VideoResults = []model.VideoResultItem{{Url: sample}}
		if b, err := json.Marshal(map[string]string{"sample": sample}); err == nil {
			gr.FluxResult = b
		}
	}
	if predResp.Error != nil {
		if b, err := json.Marshal(map[string]interface{}{"error": predResp.Error}); err == nil {
			gr.FluxDetails = b
		}
	}
}
