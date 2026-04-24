package keling

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
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

func (a *VideoAdaptor) GetProviderName() string { return "kling" }
func (a *VideoAdaptor) GetChannelName() string  { return "可灵 Kling" }
func (a *VideoAdaptor) GetSupportedModels() []string {
	return []string{
		"kling-v1", "kling-v1-5", "kling-v1-6",
		"kling-v2", "kling-v2-master",
		"kling-v3", "kling-v3-omni", "kling-video-o1",
		"kling-lip",
	}
}

// routeMap maps request type → channel type → path suffix
var routeMap = map[string]map[int]string{
	"kling-lip": {
		41: "/v1/videos/lip-sync",
		0:  "/kling/v1/videos/lip2video",
	},
	"text-to-video": {
		41: "/v1/videos/text2video",
		0:  "/kling/v1/videos/text2video",
	},
	"image-to-video": {
		41: "/v1/videos/image2video",
		0:  "/kling/v1/videos/image2video",
	},
	"multi-image-to-video": {
		41: "/v1/videos/multi-image2video",
		0:  "/kling/v1/videos/multi-image2video",
	},
	"omni-video": {
		41: "/v1/videos/omni-video",
		0:  "/kling/v1/videos/omni-video",
	},
}

func (a *VideoAdaptor) HandleVideoRequest(c *gin.Context, req *model.VideoRequest, meta *util.RelayMeta) (*relaychannel.VideoTaskResult, *model.ErrorWithStatusCode) {
	baseUrl := meta.BaseURL

	var requestType string
	var requestBody interface{}
	var videoType string
	var videoId string
	var mode string
	var duration string
	var sound string

	modelName := meta.OriginModelName

	if modelName == "kling-lip" {
		requestType = "kling-lip"
		videoType = "kling-lip"
		var lipRequest KlingLipRequest
		if err := json.NewDecoder(c.Request.Body).Decode(&lipRequest); err != nil {
			return nil, openaiAdaptor.ErrorWrapper(err, "invalid_request", http.StatusBadRequest)
		}
		requestBody = lipRequest
		videoId = lipRequest.Input.VideoId
	} else if modelName == "kling-video-o1" || modelName == "kling-v3-omni" {
		requestType = "omni-video"
		videoType = "omni-video"
		var requestMap map[string]interface{}
		if err := json.NewDecoder(c.Request.Body).Decode(&requestMap); err != nil {
			return nil, openaiAdaptor.ErrorWrapper(err, "invalid_request", http.StatusBadRequest)
		}
		if modeVal, ok := requestMap["mode"].(string); ok {
			mode = modeVal
		}
		if durVal := requestMap["duration"]; durVal != nil {
			switch v := durVal.(type) {
			case float64:
				duration = strconv.Itoa(int(v))
			case string:
				duration = v
			}
		}
		if soundVal, ok := requestMap["sound"].(string); ok {
			sound = soundVal
		}
		if modelVal, hasModel := requestMap["model"]; hasModel {
			requestMap["model_name"] = modelVal
			delete(requestMap, "model")
		} else if _, hasModelName := requestMap["model_name"]; !hasModelName {
			requestMap["model_name"] = modelName
		}
		requestBody = requestMap
	} else {
		var requestMap map[string]interface{}
		if err := json.NewDecoder(c.Request.Body).Decode(&requestMap); err != nil {
			return nil, openaiAdaptor.ErrorWrapper(err, "invalid_request_body", http.StatusBadRequest)
		}
		if modeVal, ok := requestMap["mode"].(string); ok {
			mode = modeVal
		}
		if durVal := requestMap["duration"]; durVal != nil {
			switch v := durVal.(type) {
			case float64:
				duration = strconv.Itoa(int(v))
			case string:
				duration = v
			}
		}
		if soundVal, ok := requestMap["sound"].(string); ok {
			sound = soundVal
		}
		if modelVal, hasModel := requestMap["model"]; hasModel {
			requestMap["model_name"] = modelVal
			delete(requestMap, "model")
		} else if _, hasModelName := requestMap["model_name"]; !hasModelName {
			requestMap["model_name"] = modelName
		}

		hasImageList := false
		if listVal, ok := requestMap["image_list"].([]interface{}); ok && len(listVal) > 0 {
			hasImageList = true
		}
		hasImage := false
		if imgVal, ok := requestMap["image"].(string); ok && imgVal != "" {
			hasImage = true
		}
		hasImageTail := false
		if tailVal, ok := requestMap["image_tail"].(string); ok && tailVal != "" {
			hasImageTail = true
		}

		if hasImageList {
			requestType = "multi-image-to-video"
			videoType = "multi-image-to-video"
		} else if hasImage || hasImageTail {
			requestType = "image-to-video"
			videoType = "image-to-video"
		} else {
			requestType = "text-to-video"
			videoType = "text-to-video"
		}
		requestBody = requestMap
	}

	channelType := meta.ChannelType
	if channelType != 41 {
		channelType = 0
	}
	fullRequestUrl := baseUrl + routeMap[requestType][channelType]

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "json_marshal_error", http.StatusInternalServerError)
	}

	httpReq, err := http.NewRequest(http.MethodPost, fullRequestUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}

	// Resolve auth token
	var token string
	if modelName == "kling-lip" && videoId != "" {
		// For lip-sync: find original video's channel
		video, dbErr := dbmodel.GetVideoTaskByVideoId(videoId)
		if dbErr != nil {
			return nil, openaiAdaptor.ErrorWrapper(dbErr, "get_video_task_error", http.StatusInternalServerError)
		}
		ch, dbErr := dbmodel.GetChannelById(video.ChannelId, true)
		if dbErr != nil {
			return nil, openaiAdaptor.ErrorWrapper(dbErr, "get_channel_error", http.StatusInternalServerError)
		}
		if ch.Type == 41 {
			creds, credErr := GetKelingCredentialsFromConfig(meta.Config, ch, 0)
			if credErr != nil {
				return nil, openaiAdaptor.ErrorWrapper(credErr, "get_keling_credentials_error", http.StatusInternalServerError)
			}
			t, jwtErr := GenerateJWTToken(creds.AK, creds.SK)
			if jwtErr != nil {
				return nil, openaiAdaptor.ErrorWrapper(jwtErr, "jwt_error", http.StatusInternalServerError)
			}
			token = t
		} else {
			token = meta.APIKey
		}
	} else if channelType == 41 {
		ch, dbErr := dbmodel.GetChannelById(meta.ChannelId, true)
		if dbErr != nil {
			return nil, openaiAdaptor.ErrorWrapper(dbErr, "get_channel_error", http.StatusInternalServerError)
		}
		creds, credErr := GetKelingCredentialsFromConfig(meta.Config, ch, 0)
		if credErr != nil {
			return nil, openaiAdaptor.ErrorWrapper(credErr, "get_keling_credentials_error", http.StatusInternalServerError)
		}
		t, jwtErr := GenerateJWTToken(creds.AK, creds.SK)
		if jwtErr != nil {
			return nil, openaiAdaptor.ErrorWrapper(jwtErr, "jwt_error", http.StatusInternalServerError)
		}
		token = t
	} else {
		token = meta.APIKey
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	log.Printf("[Kling Video] response: %s", string(body))

	var kelingResp KelingVideoResponse
	if err := json.Unmarshal(body, &kelingResp); err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}
	kelingResp.StatusCode = resp.StatusCode

	if kelingResp.StatusCode != 200 {
		return nil, openaiAdaptor.ErrorWrapper(
			fmt.Errorf("API error (%d): %s, full response: %s", kelingResp.StatusCode, kelingResp.Message, string(body)),
			"api_error", kelingResp.StatusCode)
	}

	quota := common.CalculateVideoQuota(modelName, videoType, mode, duration, "*", sound)

	return &relaychannel.VideoTaskResult{
		TaskId:     kelingResp.Data.TaskID,
		TaskStatus: "succeed",
		Mode:       mode,
		Duration:   duration,
		VideoType:  videoType,
		Sound:      sound,
		Quota:      quota,
	}, nil
}

func (a *VideoAdaptor) HandleVideoResult(c *gin.Context, videoTask *dbmodel.Video, ch *dbmodel.Channel, cfg *dbmodel.ChannelConfig) (*model.GeneralFinalVideoResponse, *model.ErrorWithStatusCode) {
	taskId := videoTask.TaskId
	videoTaskType := videoTask.Type

	// advanced-lip-sync and identify-face are handled separately in video.go
	// For all other types, build the query URL
	chType := ch.Type
	if chType != 41 {
		chType = 0
	}

	typeRoutes := map[string]map[int]string{
		"text-to-video": {
			41: "/v1/videos/text2video/%s",
			0:  "/kling/v1/videos/text2video/%s",
		},
		"image-to-video": {
			41: "/v1/videos/image2video/%s",
			0:  "/kling/v1/videos/image2video/%s",
		},
		"kling-lip": {
			41: "/v1/videos/lip-sync/%s",
			0:  "/kling/v1/videos/lip2video/%s",
		},
		"multi-image-to-video": {
			41: "/v1/videos/multi-image2video/%s",
			0:  "/kling/v1/videos/multi-image2video/%s",
		},
		"omni-video": {
			41: "/v1/videos/omni-video/%s",
			0:  "/kling/v1/videos/omni-video/%s",
		},
	}

	pathFmt, ok := typeRoutes[videoTaskType][chType]
	if !ok {
		// default fallback
		if chType == 41 {
			pathFmt = "/v1/videos/text2video/%s"
		} else {
			pathFmt = "/kling/v1/videos/text2video/%s"
		}
	}
	fullRequestUrl := fmt.Sprintf(*ch.BaseURL+pathFmt, taskId)

	// Build auth token
	var token string
	if ch.Type == 41 {
		creds, err := GetKelingCredentialsFromConfig(*cfg, ch, 0)
		if err != nil {
			return nil, openaiAdaptor.ErrorWrapper(err, "get_keling_credentials_error", http.StatusInternalServerError)
		}
		t, err := GenerateJWTToken(creds.AK, creds.SK)
		if err != nil {
			return nil, openaiAdaptor.ErrorWrapper(err, "jwt_error", http.StatusInternalServerError)
		}
		token = t
	} else {
		token = ch.Key
	}

	_, body, err := relaychannel.SendVideoResultQuery(fullRequestUrl, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}

	var klingResp KelingVideoResponse
	if err := json.Unmarshal(body, &klingResp); err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "json_parse_error", http.StatusInternalServerError)
	}

	generalResponse := &model.GeneralFinalVideoResponse{
		TaskId:   klingResp.Data.TaskID,
		Message:  klingResp.Data.TaskStatusMsg,
		Duration: videoTask.Duration,
	}

	if len(klingResp.Data.TaskResult.Videos) > 0 {
		generalResponse.VideoId = klingResp.Data.TaskResult.Videos[0].ID
		if klingResp.Data.TaskResult.Videos[0].Duration != "" {
			generalResponse.Duration = klingResp.Data.TaskResult.Videos[0].Duration
		}
	}

	switch klingResp.Data.TaskStatus {
	case "submitted":
		generalResponse.TaskStatus = "processing"
	default:
		generalResponse.TaskStatus = klingResp.Data.TaskStatus
	}

	if klingResp.Data.TaskStatus == "succeed" && len(klingResp.Data.TaskResult.Videos) > 0 {
		generalResponse.VideoResult = klingResp.Data.TaskResult.Videos[0].URL
		generalResponse.Duration = klingResp.Data.TaskResult.Videos[0].Duration
		generalResponse.VideoResults = []model.VideoResultItem{
			{Url: klingResp.Data.TaskResult.Videos[0].URL},
		}
		if generalResponse.VideoResult != "" {
			if updateErr := dbmodel.UpdateVideoStoreUrl(taskId, generalResponse.VideoResult); updateErr != nil {
				log.Printf("[Kling Video] Failed to update store_url for task %s: %v", taskId, updateErr)
			}
		}
	}

	return generalResponse, nil
}

// extractModeAndDuration extracts mode and duration from a request map
func extractModeAndDuration(requestMap map[string]interface{}) (mode, duration string) {
	if modeVal, ok := requestMap["mode"].(string); ok {
		mode = modeVal
	}
	if durVal := requestMap["duration"]; durVal != nil {
		switch v := durVal.(type) {
		case float64:
			duration = strconv.Itoa(int(v))
		case string:
			duration = v
		}
	}
	return
}

// hasPrefix checks if any string in the slice has the given prefix
func hasPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
