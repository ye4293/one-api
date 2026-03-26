package runway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
	Meta *util.RelayMeta
}

func (a *VideoAdaptor) Init(meta *util.RelayMeta) { a.Meta = meta }

func (a *VideoAdaptor) GetProviderName() string { return "runway" }
func (a *VideoAdaptor) GetChannelName() string  { return "Runway" }

func (a *VideoAdaptor) GetPrePaymentQuota() int64 {
	return int64(0.2 * config.QuotaPerUnit)
}

func (a *VideoAdaptor) GetSupportedModels() []string {
	return []string{"gen3a_turbo"}
}

func (a *VideoAdaptor) HandleVideoRequest(c *gin.Context, req *model.VideoRequest, meta *util.RelayMeta) (*relaychannel.VideoTaskResult, *model.ErrorWithStatusCode) {
	var fullRequestUrl string
	if meta.ChannelType == 42 {
		fullRequestUrl = meta.BaseURL + "/v1/image_to_video"
	} else {
		fullRequestUrl = meta.BaseURL + "/runwayml/v1/image_to_video"
	}

	var runwayRequest VideoGenerationRequest
	if err := common.UnmarshalBodyReusable(c, &runwayRequest); err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "invalid_video_generation_request", http.StatusBadRequest)
	}
	if runwayRequest.Duration == 0 {
		runwayRequest.Duration = 10
	}

	jsonData, err := json.Marshal(runwayRequest)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "failed to marshal request body", http.StatusInternalServerError)
	}

	ch, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	httpReq, err := http.NewRequest(http.MethodPost, fullRequestUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Runway-Version", "2024-11-06")
	httpReq.Header.Set("authorization", "Bearer "+ch.Key)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	var videoResponse VideoResponse
	if err = json.Unmarshal(body, &videoResponse); err != nil {
		log.Printf("Unmarshal error: %v", err)
		return nil, openaiAdaptor.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}
	videoResponse.StatusCode = resp.StatusCode

	switch resp.StatusCode {
	case 200:
		defaultPrice, ok := common.DefaultModelPrice["gen3a_turbo"]
		if !ok {
			defaultPrice = 0.1
		}
		quota := int64(defaultPrice * config.QuotaPerUnit)
		if runwayRequest.Duration == 10 {
			quota = quota * 2
		}
		durationStr := fmt.Sprintf("%d", runwayRequest.Duration)
		return &relaychannel.VideoTaskResult{
			TaskId:     videoResponse.Id,
			TaskStatus: "succeed",
			Duration:   durationStr,
			Quota:      quota,
		}, nil
	case 400:
		return nil, openaiAdaptor.ErrorWrapper(
			fmt.Errorf("API error: %s", videoResponse.Error), "api_error", http.StatusBadRequest)
	case 429:
		return nil, openaiAdaptor.ErrorWrapper(
			fmt.Errorf("API error: %s", videoResponse.Error), "api_error", http.StatusTooManyRequests)
	default:
		return nil, openaiAdaptor.ErrorWrapper(
			fmt.Errorf("Unknown API error: %s", videoResponse.Error), "api_error", http.StatusInternalServerError)
	}
}

func (a *VideoAdaptor) HandleVideoResult(c *gin.Context, videoTask *dbmodel.Video, ch *dbmodel.Channel, cfg *dbmodel.ChannelConfig) (*model.GeneralFinalVideoResponse, *model.ErrorWithStatusCode) {
	taskId := videoTask.TaskId

	var fullRequestUrl string
	if ch.Type != 42 {
		fullRequestUrl = fmt.Sprintf("%s/runwayml/v1/tasks/%s", *ch.BaseURL, taskId)
	} else {
		fullRequestUrl = fmt.Sprintf("%s/v1/tasks/%s", *ch.BaseURL, taskId)
	}

	httpReq, err := http.NewRequest(http.MethodGet, fullRequestUrl, nil)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(fmt.Errorf("failed to create request: %v", err), "api_error", http.StatusInternalServerError)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Runway-Version", "2024-11-06")
	httpReq.Header.Set("Authorization", "Bearer "+ch.Key)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(fmt.Errorf("failed to send request: %v", err), "api_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(fmt.Errorf("failed to read response body: %v", err), "internal_error", http.StatusInternalServerError)
	}

	var runwayResp VideoFinalResponse
	if err := json.Unmarshal(body, &runwayResp); err != nil {
		log.Printf("Failed to parse response: %v, body: %s", err, string(body))
		return nil, openaiAdaptor.ErrorWrapper(fmt.Errorf("failed to parse response JSON: %v", err), "json_parse_error", http.StatusInternalServerError)
	}

	generalResponse := &model.GeneralFinalVideoResponse{
		TaskId:      taskId,
		TaskStatus:  mapTaskStatusRunway(runwayResp.Status),
		VideoResult: "",
		Duration:    videoTask.Duration,
	}

	if runwayResp.Status == "SUCCEEDED" && len(runwayResp.Output) > 0 {
		generalResponse.VideoResult = runwayResp.Output[0]
		generalResponse.VideoResults = []model.VideoResultItem{{Url: runwayResp.Output[0]}}
	} else {
		log.Printf("Task not succeeded or no output. Status: %s, Output length: %d",
			runwayResp.Status, len(runwayResp.Output))
	}

	failReason := ""
	if runwayResp.Status == "FAILED" {
		failReason = "Task failed"
	}
	generalResponse.Message = failReason
	return generalResponse, nil
}

func mapTaskStatusRunway(status string) string {
	switch status {
	case "PENDING":
		return "processing"
	case "SUCCEEDED":
		return "succeed"
	default:
		return "unknown"
	}
}
