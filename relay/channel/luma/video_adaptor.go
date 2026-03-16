package luma

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
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

func (a *VideoAdaptor) GetProviderName() string { return "luma" }
func (a *VideoAdaptor) GetChannelName() string  { return "Luma Dream Machine" }
func (a *VideoAdaptor) GetSupportedModels() []string {
	return []string{"luma-dream-machine", "luma-photon", "luma-photon-flash", "luma-ray2-flash", "luma-ray2", "luma-ray2-720"}
}

func (a *VideoAdaptor) HandleVideoRequest(c *gin.Context, req *model.VideoRequest, meta *util.RelayMeta) (*relaychannel.VideoTaskResult, *model.ErrorWithStatusCode) {
	var requestURL string
	if meta.ChannelType == 44 {
		requestURL = meta.BaseURL + "/dream-machine/v1/generations"
	} else {
		requestURL = meta.BaseURL + "/luma/dream-machine/v1/generations"
	}

	var lumaReq LumaGenerationRequest
	if err := common.UnmarshalBodyReusable(c, &lumaReq); err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "invalid_video_generation_request", http.StatusBadRequest)
	}

	ch, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	httpResp, body, httpErr := relaychannel.SendJSONVideoRequest(requestURL, lumaReq, relaychannel.BearerAuthHeaders(ch.Key))
	if httpErr != nil {
		return nil, openaiAdaptor.ErrorWrapper(httpErr, "request_error", http.StatusInternalServerError)
	}

	var lumaResp LumaGenerationResponse
	if parseErr := json.Unmarshal(body, &lumaResp); parseErr != nil {
		return nil, openaiAdaptor.ErrorWrapper(parseErr, "response_parse_error", http.StatusInternalServerError)
	}
	lumaResp.StatusCode = httpResp.StatusCode

	if lumaResp.StatusCode != 201 {
		return nil, openaiAdaptor.ErrorWrapper(
			fmt.Errorf("luma API error: status=%d body=%s", lumaResp.StatusCode, string(body)),
			"api_error", lumaResp.StatusCode)
	}

	defaultPrice, ok := common.DefaultModelPrice["luma"]
	if !ok {
		defaultPrice = 0.1
	}
	quota := int64(defaultPrice * config.QuotaPerUnit)

	taskStatus := "succeed"
	if lumaResp.State == "failed" {
		taskStatus = "failed"
	}

	return &relaychannel.VideoTaskResult{
		TaskId:     lumaResp.ID,
		TaskStatus: taskStatus,
		Quota:      quota,
	}, nil
}

func (a *VideoAdaptor) HandleVideoResult(c *gin.Context, videoTask *dbmodel.Video, ch *dbmodel.Channel, cfg *dbmodel.ChannelConfig) (*model.GeneralFinalVideoResponse, *model.ErrorWithStatusCode) {
	taskId := videoTask.TaskId

	var queryURL string
	if ch.Type != 44 {
		queryURL = fmt.Sprintf("%s/dream-machine/v1/generations/%s", *ch.BaseURL, taskId)
	} else {
		queryURL = fmt.Sprintf("%s/luma/dream-machine/v1/generations/%s", *ch.BaseURL, taskId)
	}

	_, body, err := relaychannel.SendVideoResultQuery(queryURL, relaychannel.BearerAuthHeaders(ch.Key))
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}

	var lumaResp LumaGenerationResponse
	if parseErr := json.Unmarshal(body, &lumaResp); parseErr != nil {
		log.Printf("Failed to parse luma response: %v, body: %s", parseErr, string(body))
		return nil, openaiAdaptor.ErrorWrapper(parseErr, "json_parse_error", http.StatusInternalServerError)
	}

	generalResponse := &model.GeneralFinalVideoResponse{
		TaskId:   taskId,
		Duration: videoTask.Duration,
	}

	switch lumaResp.State {
	case "completed":
		generalResponse.TaskStatus = "succeed"
		if lumaResp.Assets != nil {
			if assets, ok := lumaResp.Assets.(map[string]interface{}); ok {
				if videoURL, ok := assets["video"].(string); ok {
					generalResponse.VideoResult = videoURL
					generalResponse.VideoResults = []model.VideoResultItem{{Url: videoURL}}
				}
			}
		}
	case "failed":
		generalResponse.TaskStatus = "failed"
		if lumaResp.FailureReason != nil {
			generalResponse.Message = *lumaResp.FailureReason
		}
	default:
		generalResponse.TaskStatus = "processing"
	}

	return generalResponse, nil
}
