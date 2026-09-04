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
	requestURL := meta.BaseURL + "/v1/flux-3-video"

	var fluxReq FluxVideoRequest
	if err := common.UnmarshalBodyReusable(c, &fluxReq); err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "invalid_video_generation_request", http.StatusBadRequest)
	}

	ch, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	httpResp, body, httpErr := relaychannel.SendJSONVideoRequest(requestURL, fluxReq, relaychannel.XKeyAuthHeaders(ch.Key))
	if httpErr != nil {
		return nil, openaiAdaptor.ErrorWrapper(httpErr, "request_error", http.StatusInternalServerError)
	}

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, openaiAdaptor.ErrorWrapper(
			fmt.Errorf("flux video API error: status=%d body=%s", httpResp.StatusCode, string(body)),
			"api_error", httpResp.StatusCode)
	}

	var submitResp FluxVideoSubmitResponse
	if parseErr := json.Unmarshal(body, &submitResp); parseErr != nil {
		return nil, openaiAdaptor.ErrorWrapper(parseErr, "response_parse_error", http.StatusInternalServerError)
	}
	if submitResp.ID == "" {
		return nil, openaiAdaptor.ErrorWrapper(
			fmt.Errorf("flux video API returned empty task id: body=%s", string(body)),
			"api_error", http.StatusInternalServerError)
	}

	// 计费参数归一化
	durationStr := durationToString(fluxReq.Duration)
	resolution := fluxReq.Resolution
	if resolution == "" {
		resolution = "hd"
	}
	sound := "on" // generate_audio 默认 true
	if fluxReq.GenerateAudio != nil && !*fluxReq.GenerateAudio {
		sound = "off"
	}
	videoType := videoTypeFromMode(fluxReq.Mode)

	quota := common.CalculateVideoQuota(meta.ActualModelName, videoType, fluxReq.Mode, durationStr, resolution, sound)

	return &relaychannel.VideoTaskResult{
		TaskId:     submitResp.ID,
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

func (a *VideoAdaptor) HandleVideoResult(c *gin.Context, videoTask *dbmodel.Video, ch *dbmodel.Channel, cfg *dbmodel.ChannelConfig) (*model.GeneralFinalVideoResponse, *model.ErrorWithStatusCode) {
	taskId := videoTask.TaskId

	baseURL := "https://api.bfl.ai"
	if ch.BaseURL != nil && *ch.BaseURL != "" {
		baseURL = *ch.BaseURL
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
