package zhipu

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

func (a *VideoAdaptor) GetProviderName() string { return "zhipu" }
func (a *VideoAdaptor) GetChannelName() string  { return "Zhipu" }

func (a *VideoAdaptor) GetPrePaymentQuota() int64 {
	return int64(0.2 * config.QuotaPerUnit)
}

func (a *VideoAdaptor) GetSupportedModels() []string {
	return []string{"cogvideox"}
}

func (a *VideoAdaptor) HandleVideoRequest(c *gin.Context, req *model.VideoRequest, meta *util.RelayMeta) (*relaychannel.VideoTaskResult, *model.ErrorWithStatusCode) {
	fullRequestUrl := meta.BaseURL + "/api/paas/v4/videos/generations"

	videoRequestZhipu := model.VideoRequestZhipu{
		Model:    req.Model,
		Prompt:   req.Prompt,
		ImageURL: req.ImageURL,
	}

	jsonData, err := json.Marshal(videoRequestZhipu)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "json_marshal_error", http.StatusInternalServerError)
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

	var videoResponse model.VideoResponse
	if err = json.Unmarshal(body, &videoResponse); err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}
	videoResponse.StatusCode = resp.StatusCode

	switch resp.StatusCode {
	case 200:
		defaultPrice, ok := common.DefaultModelPrice["cogvideox"]
		if !ok {
			defaultPrice = 0.1
		}
		quota := int64(defaultPrice * config.QuotaPerUnit)

		taskStatus := "succeed"
		if videoResponse.TaskStatus == "FAIL" {
			taskStatus = "failed"
		}
		return &relaychannel.VideoTaskResult{
			TaskId:     videoResponse.ID,
			TaskStatus: taskStatus,
			Quota:      quota,
		}, nil
	case 400:
		msg := ""
		if videoResponse.ZhipuError != nil {
			msg = videoResponse.ZhipuError.Message
		}
		return nil, openaiAdaptor.ErrorWrapper(fmt.Errorf("API error: %s", msg), "api_error", http.StatusBadRequest)
	case 429:
		msg := ""
		if videoResponse.ZhipuError != nil {
			msg = videoResponse.ZhipuError.Message
		}
		return nil, openaiAdaptor.ErrorWrapper(fmt.Errorf("API error: %s", msg), "api_error", http.StatusTooManyRequests)
	default:
		msg := ""
		if videoResponse.ZhipuError != nil {
			msg = videoResponse.ZhipuError.Message
		}
		return nil, openaiAdaptor.ErrorWrapper(fmt.Errorf("Unknown API error: %s", msg), "api_error", http.StatusInternalServerError)
	}
}

func (a *VideoAdaptor) HandleVideoResult(c *gin.Context, videoTask *dbmodel.Video, ch *dbmodel.Channel, cfg *dbmodel.ChannelConfig) (*model.GeneralFinalVideoResponse, *model.ErrorWithStatusCode) {
	taskId := videoTask.TaskId
	fullRequestUrl := fmt.Sprintf("https://open.bigmodel.cn/api/paas/v4/async-result/%s", taskId)

	httpReq, err := http.NewRequest(http.MethodGet, fullRequestUrl, nil)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(fmt.Errorf("failed to create request: %v", err), "api_error", http.StatusInternalServerError)
	}
	httpReq.Header.Set("Content-Type", "application/json")
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

	var zhipuResp model.FinalVideoResponse
	if err := json.Unmarshal(body, &zhipuResp); err != nil {
		return nil, openaiAdaptor.ErrorWrapper(fmt.Errorf("failed to parse response JSON: %v", err), "json_parse_error", http.StatusInternalServerError)
	}

	generalResponse := &model.GeneralFinalVideoResponse{
		TaskId:      taskId,
		TaskStatus:  mapTaskStatusZhipu(zhipuResp.TaskStatus),
		Message:     "",
		VideoResult: "",
		Duration:    videoTask.Duration,
	}

	if zhipuResp.TaskStatus == "SUCCESS" && len(zhipuResp.VideoResults) > 0 {
		generalResponse.VideoResult = zhipuResp.VideoResults[0].URL
		generalResponse.VideoResults = []model.VideoResultItem{
			{Url: zhipuResp.VideoResults[0].URL},
		}
	}

	return generalResponse, nil
}

func mapTaskStatusZhipu(status string) string {
	switch status {
	case "PROCESSING":
		return "processing"
	case "SUCCESS":
		return "succeed"
	case "FAIL":
		return "failed"
	default:
		log.Printf("[Zhipu状态映射] 未知状态: %s", status)
		return "unknown"
	}
}
