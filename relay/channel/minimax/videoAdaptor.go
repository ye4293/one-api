package minimax

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"

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

func (a *VideoAdaptor) GetProviderName() string { return "minimax" }
func (a *VideoAdaptor) GetChannelName() string  { return "Minimax" }

func (a *VideoAdaptor) GetPrePaymentQuota() int64 {
	return int64(0.2 * config.QuotaPerUnit)
}

func (a *VideoAdaptor) GetSupportedModels() []string {
	videoModels := []string{
		"video-01", "S2V-01", "T2V-01", "I2V-01",
		"T2V-01-Director", "I2V-01-Director", "I2V-01-live", "video-01-live2d",
		"MiniMax-Hailuo-02", "MiniMax-Hailuo-2.3", "MiniMax-Hailuo-2.3-Fast",
	}
	return videoModels
}

func (a *VideoAdaptor) HandleVideoRequest(c *gin.Context, req *model.VideoRequest, meta *util.RelayMeta) (*relaychannel.VideoTaskResult, *model.ErrorWithStatusCode) {
	fullRequestUrl := meta.BaseURL + "/v1/video_generation"

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "read_request_body_failed", http.StatusBadRequest)
	}

	// 先解析为 map 以便处理 duration 的多种类型（int/float64/string）
	var requestMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &requestMap); err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "invalid_request_error", http.StatusBadRequest)
	}

	var durationInt int
	if durationValue, exists := requestMap["duration"]; exists && durationValue != nil {
		switch v := durationValue.(type) {
		case float64:
			durationInt = int(v)
		case string:
			if parsed, parseErr := strconv.Atoi(v); parseErr == nil {
				durationInt = parsed
			} else {
				durationInt = 6
			}
		case int:
			durationInt = v
		default:
			durationInt = 6
		}
	}
	if durationInt == 0 {
		durationInt = 6
		requestMap["duration"] = 6
	} else {
		requestMap["duration"] = durationInt
	}

	modifiedBodyBytes, err := json.Marshal(requestMap)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "json_marshal_error", http.StatusInternalServerError)
	}

	var videoRequestMinimax model.VideoRequestMinimax
	if err := json.Unmarshal(modifiedBodyBytes, &videoRequestMinimax); err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "invalid_request_error", http.StatusBadRequest)
	}
	if videoRequestMinimax.Resolution == "" {
		videoRequestMinimax.Resolution = "768P"
	}

	jsonData, err := json.Marshal(videoRequestMinimax)
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

	switch videoResponse.BaseResp.StatusCode {
	case 0:
		quota := a.computeQuota(req.Model, durationInt, videoRequestMinimax.Resolution)
		return &relaychannel.VideoTaskResult{
			TaskId:     videoResponse.TaskID,
			TaskStatus: "succeed",
			Message:    videoResponse.BaseResp.StatusMsg,
			Mode:       videoRequestMinimax.Resolution, // 历史行为：mode 字段存储分辨率
			Duration:   strconv.Itoa(durationInt),
			Resolution: videoRequestMinimax.Resolution,
			Quota:      quota,
		}, nil
	case 1002, 1008:
		return nil, openaiAdaptor.ErrorWrapper(
			fmt.Errorf("API error: %s", videoResponse.BaseResp.StatusMsg), "api_error", http.StatusTooManyRequests)
	case 1004:
		return nil, openaiAdaptor.ErrorWrapper(
			fmt.Errorf("API error: %s", videoResponse.BaseResp.StatusMsg), "api_error", http.StatusForbidden)
	case 1013, 1026:
		return nil, openaiAdaptor.ErrorWrapper(
			fmt.Errorf("API error: %s", videoResponse.BaseResp.StatusMsg), "api_error", http.StatusBadRequest)
	default:
		return nil, openaiAdaptor.ErrorWrapper(
			fmt.Errorf("Unknown API error: %s", videoResponse.BaseResp.StatusMsg), "api_error", http.StatusInternalServerError)
	}
}

func (a *VideoAdaptor) HandleVideoResult(c *gin.Context, videoTask *dbmodel.Video, ch *dbmodel.Channel, cfg *dbmodel.ChannelConfig) (*model.GeneralFinalVideoResponse, *model.ErrorWithStatusCode) {
	taskId := videoTask.TaskId

	url := fmt.Sprintf("%s/v1/query/video_generation?task_id=%s", *ch.BaseURL, taskId)
	httpReq, err := http.NewRequest(http.MethodGet, url, nil)
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

	log.Printf("[MiniMax原始响应] TaskId:%s, StatusCode:%d, Body:%s", taskId, resp.StatusCode, string(body))

	var minimaxResp model.FinalVideoResponse
	if err := json.Unmarshal(body, &minimaxResp); err != nil {
		return nil, openaiAdaptor.ErrorWrapper(fmt.Errorf("failed to parse response JSON: %v", err), "json_parse_error", http.StatusInternalServerError)
	}

	generalResponse := &model.GeneralFinalVideoResponse{
		TaskId:      taskId,
		TaskStatus:  mapTaskStatusMinimax(minimaxResp.Status),
		Message:     minimaxResp.BaseResp.StatusMsg,
		VideoResult: "",
		Duration:    videoTask.Duration,
	}

	if minimaxResp.FileID == "" {
		return generalResponse, nil
	}

	// 有 FileID 时额外查询文件下载地址
	fileUrl := fmt.Sprintf("%s/v1/files/retrieve?file_id=%s", *ch.BaseURL, minimaxResp.FileID)
	fileReq, err := http.NewRequest(http.MethodGet, fileUrl, nil)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(fmt.Errorf("failed to create file request: %v", err), "api_error", http.StatusInternalServerError)
	}
	fileReq.Header.Set("Content-Type", "application/json")
	fileReq.Header.Set("Authorization", "Bearer "+ch.Key)

	fileResp, err := http.DefaultClient.Do(fileReq)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(fmt.Errorf("failed to send file request: %v", err), "api_error", http.StatusInternalServerError)
	}
	defer fileResp.Body.Close()

	fileBody, err := io.ReadAll(fileResp.Body)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(fmt.Errorf("failed to read file response body: %v", err), "internal_error", http.StatusInternalServerError)
	}

	log.Printf("[MiniMax文件响应] TaskId:%s, FileID:%s, StatusCode:%d, Body:%s", taskId, minimaxResp.FileID, fileResp.StatusCode, string(fileBody))

	var fileResponse model.MinimaxFinalResponse
	if err := json.Unmarshal(fileBody, &fileResponse); err != nil {
		return nil, openaiAdaptor.ErrorWrapper(fmt.Errorf("failed to parse file response JSON: %v", err), "json_parse_error", http.StatusInternalServerError)
	}

	generalResponse.VideoResult = fileResponse.File.DownloadURL
	generalResponse.VideoResults = []model.VideoResultItem{{Url: fileResponse.File.DownloadURL}}
	generalResponse.TaskStatus = "succeed"
	return generalResponse, nil
}

// computeQuota 根据模型、时长、分辨率计算实际扣费额度
func (a *VideoAdaptor) computeQuota(modelName string, durationInt int, resolution string) int64 {
	switch modelName {
	case "MiniMax-Hailuo-2.3-Fast":
		var priceCNY float64
		switch {
		case resolution == "768P" && durationInt == 6:
			priceCNY = 1.35
		case resolution == "768P" && durationInt == 10:
			priceCNY = 2.25
		case resolution == "1080P" && durationInt == 6:
			priceCNY = 2.31
		default:
			priceCNY = 1.35
		}
		return int64(priceCNY / 7.2 * config.QuotaPerUnit)
	case "MiniMax-Hailuo-2.3":
		var priceCNY float64
		switch {
		case resolution == "768P" && durationInt == 6:
			priceCNY = 2.0
		case resolution == "768P" && durationInt == 10:
			priceCNY = 4.0
		case resolution == "1080P" && durationInt == 6:
			priceCNY = 3.5
		default:
			priceCNY = 2.0
		}
		return int64(priceCNY / 7.2 * config.QuotaPerUnit)
	case "MiniMax-Hailuo-02":
		var priceCNY float64
		switch {
		case resolution == "512P" && durationInt == 6:
			priceCNY = 1.5
		case resolution == "512P" && durationInt == 10:
			priceCNY = 3.0
		case resolution == "768P" && durationInt == 6:
			priceCNY = 2.0
		case resolution == "768P" && durationInt == 10:
			priceCNY = 4.0
		case resolution == "1080P" && durationInt == 6:
			priceCNY = 3.5
		case resolution == "1088P" && durationInt == 6:
			priceCNY = 3.5
		default:
			priceCNY = 2.0
		}
		return int64(priceCNY / 7.2 * config.QuotaPerUnit)
	default:
		defaultPrice, ok := common.DefaultModelPrice[modelName]
		if !ok {
			defaultPrice = 0.1
		}
		return int64(defaultPrice * config.QuotaPerUnit)
	}
}

func mapTaskStatusMinimax(status string) string {
	switch status {
	case "Preparing", "Processing":
		return "processing"
	case "Success":
		return "succeed"
	case "Fail":
		return "failed"
	default:
		log.Printf("[MiniMax状态映射] 未知状态: %s", status)
		return "unknown"
	}
}
