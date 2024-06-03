package midjourney

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	relayconstant "github.com/songquanpeng/one-api/relay/constant"
	"github.com/songquanpeng/one-api/relay/util"
)

func CoverActionToModelName(mjAction string) string {
	modelName := "mj_" + strings.ToLower(mjAction)
	if mjAction == common.MjActionSwapFace {
		modelName = "swap_face"
	}
	return modelName
}

func GetMjRequestModel(relayMode int, midjRequest *MidjourneyRequest) (string, *MidjourneyResponseWithStatusCode, bool) {
	action := ""
	if relayMode == relayconstant.RelayModeMidjourneyAction {
		// plus request
		err := CoverPlusActionToNormalAction(midjRequest)
		if err != nil {
			return "", err, false
		}
		action = midjRequest.Action
	} else {
		switch relayMode {
		case relayconstant.RelayModeMidjourneyImagine:
			action = common.MjActionImagine
		case relayconstant.RelayModeMidjourneyDescribe:
			action = common.MjActionDescribe
		case relayconstant.RelayModeMidjourneyBlend:
			action = common.MjActionBlend
		case relayconstant.RelayModeMidjourneyShorten:
			action = common.MjActionShorten
		case relayconstant.RelayModeMidjourneyChange:
			action = midjRequest.Action
		case relayconstant.RelayModeMidjourneyModal:
			action = common.MjActionModal
		case relayconstant.RelayModeSwapFace:
			action = common.MjActionSwapFace
		case relayconstant.RelayModeMidjourneySimpleChange:
			params := ConvertSimpleChangeParams(midjRequest.Content)
			if params == nil {
				return "", MidjourneyErrorWithStatusCodeWrapper(common.MjRequestError, "invalid_request", http.StatusInternalServerError), false
			}
			action = params.Action
		case relayconstant.RelayModeMidjourneyTaskFetch, relayconstant.RelayModeMidjourneyTaskFetchByCondition, relayconstant.RelayModeMidjourneyNotify:
			return "", nil, true
		default:
			return "", MidjourneyErrorWithStatusCodeWrapper(common.MjRequestError, "unknown_relay_action", http.StatusInternalServerError), false
		}
	}
	modelName := CoverActionToModelName(action)
	return modelName, nil, true
}

func CoverPlusActionToNormalAction(midjRequest *MidjourneyRequest) *MidjourneyResponseWithStatusCode {
	// "customId": "MJ::JOB::upsample::2::3dbbd469-36af-4a0f-8f02-df6c579e7011"
	customId := midjRequest.CustomId
	if customId == "" {
		return MidjourneyErrorWithStatusCodeWrapper(common.MjRequestError, "custom_id_is_required", http.StatusInternalServerError)
	}
	splits := strings.Split(customId, "::")
	var action string
	if splits[1] == "JOB" {
		action = splits[2]
	} else {
		action = splits[1]
	}

	if action == "" {
		return MidjourneyErrorWithStatusCodeWrapper(common.MjRequestError, "unknown_action", http.StatusInternalServerError)
	}
	if strings.Contains(action, "upsample") {
		index, err := strconv.Atoi(splits[3])
		if err != nil {
			return MidjourneyErrorWithStatusCodeWrapper(common.MjRequestError, "index_parse_failed", http.StatusInternalServerError)
		}
		midjRequest.Index = index
		midjRequest.Action = common.MjActionUpscale
	} else if strings.Contains(action, "variation") {
		midjRequest.Index = 1
		if action == "variation" {
			index, err := strconv.Atoi(splits[3])
			if err != nil {
				return MidjourneyErrorWithStatusCodeWrapper(common.MjRequestError, "index_parse_failed", http.StatusInternalServerError)
			}
			midjRequest.Index = index
			midjRequest.Action = common.MjActionVariation
		} else if action == "low_variation" {
			midjRequest.Action = common.MjActionLowVariation
		} else if action == "high_variation" {
			midjRequest.Action = common.MjActionHighVariation
		}
	} else if strings.Contains(action, "pan") {
		midjRequest.Action = common.MjActionPan
		midjRequest.Index = 1
	} else if strings.Contains(action, "reroll") {
		midjRequest.Action = common.MjActionReRoll
		midjRequest.Index = 1
	} else if action == "Outpaint" {
		midjRequest.Action = common.MjActionZoom
		midjRequest.Index = 1
	} else if action == "CustomZoom" {
		midjRequest.Action = common.MjActionCustomZoom
		midjRequest.Index = 1
	} else if action == "Inpaint" {
		midjRequest.Action = common.MjActionInPaint
		midjRequest.Index = 1
	} else {
		return MidjourneyErrorWithStatusCodeWrapper(common.MjRequestError, "unknown_action:"+customId, http.StatusInternalServerError)
	}
	return nil
}

func ConvertSimpleChangeParams(content string) *MidjourneyRequest {
	split := strings.Split(content, " ")
	if len(split) != 2 {
		return nil
	}

	action := strings.ToLower(split[1])
	changeParams := &MidjourneyRequest{}
	changeParams.TaskId = split[0]

	if action[0] == 'u' {
		changeParams.Action = "UPSCALE"
	} else if action[0] == 'v' {
		changeParams.Action = "VARIATION"
	} else if action == "r" {
		changeParams.Action = "REROLL"
		return changeParams
	} else {
		return nil
	}

	index, err := strconv.Atoi(action[1:2])
	if err != nil || index < 1 || index > 4 {
		return nil
	}
	changeParams.Index = index
	return changeParams
}

func DoMidjourneyHttpRequest(c *gin.Context, timeout time.Duration, fullRequestURL string) (*MidjourneyResponseWithStatusCode, []byte, error) {
	var nullBytes []byte

	var mapResult map[string]interface{}
	// if get request, no need to read request body
	if c.Request.Method != "GET" {
		err := json.NewDecoder(c.Request.Body).Decode(&mapResult)
		if err != nil {
			return MidjourneyErrorWithStatusCodeWrapper(common.MjErrorUnknown, "read_request_body_failed", http.StatusInternalServerError), nullBytes, err
		}
		delete(mapResult, "accountFilter")
		if !common.MjNotifyEnabled {
			delete(mapResult, "notifyHook")
		}
		//req, err := http.NewRequest(c.Request.Method, fullRequestURL, requestBody)
		// make new request with mapResult
	}
	reqBody, err := json.Marshal(mapResult)
	if err != nil {
		return MidjourneyErrorWithStatusCodeWrapper(common.MjErrorUnknown, "marshal_request_body_failed", http.StatusInternalServerError), nullBytes, err
	}
	req, err := http.NewRequest(c.Request.Method, fullRequestURL, strings.NewReader(string(reqBody)))
	if err != nil {
		return MidjourneyErrorWithStatusCodeWrapper(common.MjErrorUnknown, "create_request_failed", http.StatusInternalServerError), nullBytes, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	// 使用带有超时的 context 创建新的请求
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))
	req.Header.Set("Accept", c.Request.Header.Get("Accept"))
	auth := c.Request.Header.Get("Authorization")
	if auth != "" {
		auth = strings.TrimPrefix(auth, "Bearer ")
		req.Header.Set("mj-api-secret", auth)
	}
	defer cancel()
	resp, err := util.GetHttpClient().Do(req)
	if err != nil {
		logger.SysError("do request failed: " + util.ProcessString(err.Error()))
		return MidjourneyErrorWithStatusCodeWrapper(common.MjErrorUnknown, "do_request_failed", http.StatusInternalServerError), nullBytes, err
	}
	statusCode := resp.StatusCode

	err = req.Body.Close()
	if err != nil {
		return MidjourneyErrorWithStatusCodeWrapper(common.MjErrorUnknown, "close_request_body_failed", statusCode), nullBytes, err
	}
	err = c.Request.Body.Close()
	if err != nil {
		return MidjourneyErrorWithStatusCodeWrapper(common.MjErrorUnknown, "close_request_body_failed", statusCode), nullBytes, err
	}
	var midjResponse MidjourneyResponse

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return MidjourneyErrorWithStatusCodeWrapper(common.MjErrorUnknown, "read_response_body_failed", statusCode), nullBytes, err
	}
	err = resp.Body.Close()
	if err != nil {
		return MidjourneyErrorWithStatusCodeWrapper(common.MjErrorUnknown, "close_response_body_failed", statusCode), responseBody, err
	}
	respStr := string(responseBody)
	log.Printf("statusCode:%d responseBody: %s", statusCode, respStr)
	if respStr == "" {
		return MidjourneyErrorWithStatusCodeWrapper(common.MjErrorUnknown, "empty_response_body", statusCode), responseBody, nil
	} else {
		err = json.Unmarshal(responseBody, &midjResponse)
		if err != nil {
			return MidjourneyErrorWithStatusCodeWrapper(common.MjErrorUnknown, "unmarshal_response_body_failed", statusCode), responseBody, err
		}
	}

	return &MidjourneyResponseWithStatusCode{
		StatusCode: statusCode,
		Response:   midjResponse,
	}, responseBody, nil
}
