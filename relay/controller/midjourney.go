package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/midjourney"
	relayconstant "github.com/songquanpeng/one-api/relay/constant"
)

func RelayMidjourneyNotify(c *gin.Context) *midjourney.MidjourneyResponse {
	var midjRequest midjourney.MidjourneyDto
	err := common.UnmarshalBodyReusable(c, &midjRequest)
	if err != nil {
		return &midjourney.MidjourneyResponse{
			Code:        4,
			Description: "bind_request_body_failed",
			Properties:  nil,
			Result:      "",
		}
	}
	midjourneyTask := model.GetByOnlyMJId(midjRequest.MjId)
	if midjourneyTask == nil {
		return &midjourney.MidjourneyResponse{
			Code:        4,
			Description: "midjourney_task_not_found",
			Properties:  nil,
			Result:      "",
		}
	}
	midjourneyTask.Progress = midjRequest.Progress
	midjourneyTask.PromptEn = midjRequest.PromptEn
	midjourneyTask.State = midjRequest.State
	midjourneyTask.SubmitTime = midjRequest.SubmitTime
	midjourneyTask.StartTime = midjRequest.StartTime
	midjourneyTask.FinishTime = midjRequest.FinishTime
	midjourneyTask.ImageUrl = midjRequest.ImageUrl
	midjourneyTask.Status = midjRequest.Status
	midjourneyTask.FailReason = midjRequest.FailReason
	err = midjourneyTask.Update()
	if err != nil {
		return &midjourney.MidjourneyResponse{
			Code:        4,
			Description: "update_midjourney_task_failed",
		}
	}

	return nil
}

func coverMidjourneyTaskDto(c *gin.Context, originTask *model.Midjourney) (midjourneyTask midjourney.MidjourneyDto) {
	midjourneyTask.MjId = originTask.MjId
	midjourneyTask.Progress = originTask.Progress
	midjourneyTask.PromptEn = originTask.PromptEn
	midjourneyTask.State = originTask.State
	midjourneyTask.SubmitTime = originTask.SubmitTime
	midjourneyTask.StartTime = originTask.StartTime
	midjourneyTask.FinishTime = originTask.FinishTime
	midjourneyTask.ImageUrl = ""
	if originTask.ImageUrl != "" {
		midjourneyTask.ImageUrl = config.ServerAddress + "/mj/image/" + originTask.MjId
		if originTask.Status != "SUCCESS" {
			midjourneyTask.ImageUrl += "?rand=" + strconv.FormatInt(time.Now().UnixNano(), 10)
		}
	}
	midjourneyTask.Status = originTask.Status
	midjourneyTask.FailReason = originTask.FailReason
	midjourneyTask.Action = originTask.Action
	midjourneyTask.Description = originTask.Description
	midjourneyTask.Prompt = originTask.Prompt
	if originTask.Buttons != "" {
		var buttons []midjourney.ActionButton
		err := json.Unmarshal([]byte(originTask.Buttons), &buttons)
		if err == nil {
			midjourneyTask.Buttons = buttons
		}
	}
	if originTask.Properties != "" {
		var properties midjourney.Properties
		err := json.Unmarshal([]byte(originTask.Properties), &properties)
		if err == nil {
			midjourneyTask.Properties = &properties
		}
	}
	return
}

func RelaySwapFace(c *gin.Context) *midjourney.MidjourneyResponse {
	ctx := c.Request.Context()
	startTime := time.Now().UnixNano() / int64(time.Millisecond)
	tokenId := c.GetInt("token_id")
	userId := c.GetInt("id")
	group := c.GetString("group")
	channelId := c.GetInt("channel_id")
	var swapFaceRequest midjourney.SwapFaceRequest
	err := common.UnmarshalBodyReusable(c, &swapFaceRequest)
	if err != nil {
		return midjourney.MidjourneyErrorWrapper(common.MjRequestError, "bind_request_body_failed")
	}
	if swapFaceRequest.SourceBase64 == "" || swapFaceRequest.TargetBase64 == "" {
		return midjourney.MidjourneyErrorWrapper(common.MjRequestError, "sour_base64_and_target_base64_is_required")
	}
	modelName := midjourney.CoverActionToModelName(common.MjActionSwapFace)
	modelPrice := common.GetModelPrice(modelName, true)
	// 如果没有配置价格，则使用默认价格
	if modelPrice == -1 {
		defaultPrice, ok := common.DefaultModelPrice[modelName]
		if !ok {
			modelPrice = 0.1
		} else {
			modelPrice = defaultPrice
		}
	}
	groupRatio := common.GetGroupRatio(group)
	ratio := modelPrice * groupRatio
	userQuota, err := model.CacheGetUserQuota(ctx, userId)
	if err != nil {
		return &midjourney.MidjourneyResponse{
			Code:        4,
			Description: err.Error(),
		}
	}
	quota := int64(ratio * config.QuotaPerUnit)

	if userQuota-quota < 0 {
		return &midjourney.MidjourneyResponse{
			Code:        4,
			Description: "quota_not_enough",
		}
	}
	requestURL := c.Request.URL.String()
	baseURL := c.GetString("base_url")
	fullRequestURL := fmt.Sprintf("%s%s", baseURL, requestURL)
	mjResp, _, err := midjourney.DoMidjourneyHttpRequest(c, time.Second*60, fullRequestURL)
	if err != nil {
		return &mjResp.Response
	}
	defer func(ctx context.Context) {
		if mjResp.StatusCode == 200 && mjResp.Response.Code == 1 {
			err := model.PostConsumeTokenQuota(tokenId, quota)
			if err != nil {
				logger.SysError("error consuming token remain quota: " + err.Error())
			}
			err = model.CacheUpdateUserQuota(ctx, userId)
			if err != nil {
				logger.SysError("error update user quota cache: " + err.Error())
			}
			if quota != 0 {
				tokenName := c.GetString("token_name")
				logContent := fmt.Sprintf("模型固定价格 %.2f，分组倍率 %.2f，操作 %s", modelPrice, groupRatio, common.MjActionSwapFace)
				model.RecordConsumeLog(ctx, userId, channelId, 0, 0, modelName, tokenName, quota, logContent, 0)
				model.UpdateUserUsedQuotaAndRequestCount(userId, quota)
				channelId := c.GetInt("channel_id")
				model.UpdateChannelUsedQuota(channelId, quota)
			}
		}
	}(c.Request.Context())
	midjResponse := &mjResp.Response
	midjourneyTask := &model.Midjourney{
		UserId:      userId,
		Code:        midjResponse.Code,
		Action:      common.MjActionSwapFace,
		MjId:        midjResponse.Result,
		Prompt:      "InsightFace",
		PromptEn:    "",
		Description: midjResponse.Description,
		State:       "",
		SubmitTime:  startTime,
		StartTime:   time.Now().UnixNano() / int64(time.Millisecond),
		FinishTime:  0,
		ImageUrl:    "",
		Status:      "",
		Progress:    "0%",
		FailReason:  "",
		ChannelId:   c.GetInt("channel_id"),
		Quota:       quota,
	}
	err = midjourneyTask.Insert()
	if err != nil {
		return midjourney.MidjourneyErrorWrapper(common.MjRequestError, "insert_midjourney_task_failed")
	}
	c.Writer.WriteHeader(mjResp.StatusCode)
	respBody, err := json.Marshal(midjResponse)
	if err != nil {
		return midjourney.MidjourneyErrorWrapper(common.MjRequestError, "unmarshal_response_body_failed")
	}
	_, err = io.Copy(c.Writer, bytes.NewBuffer(respBody))
	if err != nil {
		return midjourney.MidjourneyErrorWrapper(common.MjRequestError, "copy_response_body_failed")
	}
	return nil
}

func RelayMidjourneyTaskImageSeed(c *gin.Context) *midjourney.MidjourneyResponse {
	taskId := c.Param("id")
	userId := c.GetInt("id")
	originTask := model.GetByMJId(userId, taskId)
	if originTask == nil {
		return midjourney.MidjourneyErrorWrapper(common.MjRequestError, "task_no_found")
	}
	channel, err := model.GetChannelById(originTask.ChannelId, true)
	if err != nil {
		return midjourney.MidjourneyErrorWrapper(common.MjRequestError, "get_channel_info_failed")
	}
	if channel.Status != common.ChannelStatusEnabled {
		return midjourney.MidjourneyErrorWrapper(common.MjRequestError, "该任务所属渠道已被禁用")
	}
	c.Set("channel_id", originTask.ChannelId)
	c.Request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", channel.Key))

	requestURL := c.Request.URL.String()
	fullRequestURL := fmt.Sprintf("%s%s", channel.GetBaseURL(), requestURL)
	midjResponseWithStatus, _, err := midjourney.DoMidjourneyHttpRequest(c, time.Second*30, fullRequestURL)
	if err != nil {
		return &midjResponseWithStatus.Response
	}
	midjResponse := &midjResponseWithStatus.Response
	c.Writer.WriteHeader(midjResponseWithStatus.StatusCode)
	respBody, err := json.Marshal(midjResponse)
	if err != nil {
		return midjourney.MidjourneyErrorWrapper(common.MjRequestError, "unmarshal_response_body_failed")
	}
	_, err = io.Copy(c.Writer, bytes.NewBuffer(respBody))
	if err != nil {
		return midjourney.MidjourneyErrorWrapper(common.MjRequestError, "copy_response_body_failed")
	}
	return nil
}

func RelayMidjourneyTask(c *gin.Context, relayMode int) *midjourney.MidjourneyResponse {
	userId := c.GetInt("id")
	var err error
	var respBody []byte
	switch relayMode {
	case relayconstant.RelayModeMidjourneyTaskFetch:
		taskId := c.Param("id")
		originTask := model.GetByMJId(userId, taskId)
		if originTask == nil {
			return &midjourney.MidjourneyResponse{
				Code:        4,
				Description: "task_no_found",
			}
		}
		midjourneyTask := coverMidjourneyTaskDto(c, originTask)
		respBody, err = json.Marshal(midjourneyTask)
		if err != nil {
			return &midjourney.MidjourneyResponse{
				Code:        4,
				Description: "unmarshal_response_body_failed",
			}
		}
	case relayconstant.RelayModeMidjourneyTaskFetchByCondition:
		var condition = struct {
			IDs []string `json:"ids"`
		}{}
		err = c.BindJSON(&condition)
		if err != nil {
			return &midjourney.MidjourneyResponse{
				Code:        4,
				Description: "do_request_failed",
			}
		}
		var tasks []midjourney.MidjourneyDto
		if len(condition.IDs) != 0 {
			originTasks := model.GetByMJIds(userId, condition.IDs)
			for _, originTask := range originTasks {
				midjourneyTask := coverMidjourneyTaskDto(c, originTask)
				tasks = append(tasks, midjourneyTask)
			}
		}
		if tasks == nil {
			tasks = make([]midjourney.MidjourneyDto, 0)
		}
		respBody, err = json.Marshal(tasks)
		if err != nil {
			return &midjourney.MidjourneyResponse{
				Code:        4,
				Description: "unmarshal_response_body_failed",
			}
		}
	}

	c.Writer.Header().Set("Content-Type", "application/json")

	_, err = io.Copy(c.Writer, bytes.NewBuffer(respBody))
	if err != nil {
		return &midjourney.MidjourneyResponse{
			Code:        4,
			Description: "copy_response_body_failed",
		}
	}
	return nil
}

func RelayMidjourneySubmit(c *gin.Context, relayMode int) *midjourney.MidjourneyResponse {

	tokenId := c.GetInt("token_id")
	//channelType := c.GetInt("channel")
	userId := c.GetInt("id")
	group := c.GetString("group")
	channelId := c.GetInt("channel_id")
	consumeQuota := true
	var midjRequest midjourney.MidjourneyRequest
	err := common.UnmarshalBodyReusable(c, &midjRequest)
	if err != nil {
		return midjourney.MidjourneyErrorWrapper(common.MjRequestError, "bind_request_body_failed")
	}

	if relayMode == relayconstant.RelayModeMidjourneyAction { // midjourney plus，需要从customId中获取任务信息
		mjErr := midjourney.CoverPlusActionToNormalAction(&midjRequest)
		if mjErr != nil {
			return mjErr
		}
		relayMode = relayconstant.RelayModeMidjourneyChange
	}

	if relayMode == relayconstant.RelayModeMidjourneyImagine { //绘画任务，此类任务可重复
		if midjRequest.Prompt == "" {
			return midjourney.MidjourneyErrorWrapper(common.MjRequestError, "prompt_is_required")
		}
		midjRequest.Action = common.MjActionImagine
	} else if relayMode == relayconstant.RelayModeMidjourneyDescribe { //按图生文任务，此类任务可重复
		midjRequest.Action = common.MjActionDescribe
	} else if relayMode == relayconstant.RelayModeMidjourneyShorten { //缩短任务，此类任务可重复，plus only
		midjRequest.Action = common.MjActionShorten
	} else if relayMode == relayconstant.RelayModeMidjourneyBlend { //绘画任务，此类任务可重复
		midjRequest.Action = common.MjActionBlend
	} else if midjRequest.TaskId != "" { //放大、变换任务，此类任务，如果重复且已有结果，远端api会直接返回最终结果
		mjId := ""
		if relayMode == relayconstant.RelayModeMidjourneyChange {
			if midjRequest.TaskId == "" {
				return midjourney.MidjourneyErrorWrapper(common.MjRequestError, "task_id_is_required")
			} else if midjRequest.Action == "" {
				return midjourney.MidjourneyErrorWrapper(common.MjRequestError, "action_is_required")
			} else if midjRequest.Index == 0 {
				return midjourney.MidjourneyErrorWrapper(common.MjRequestError, "index_is_required")
			}
			//action = midjRequest.Action
			mjId = midjRequest.TaskId
		} else if relayMode == relayconstant.RelayModeMidjourneySimpleChange {
			if midjRequest.Content == "" {
				return midjourney.MidjourneyErrorWrapper(common.MjRequestError, "content_is_required")
			}
			params := midjourney.ConvertSimpleChangeParams(midjRequest.Content)
			if params == nil {
				return midjourney.MidjourneyErrorWrapper(common.MjRequestError, "content_parse_failed")
			}
			mjId = params.TaskId
			midjRequest.Action = params.Action
		} else if relayMode == relayconstant.RelayModeMidjourneyModal {
			//if midjRequest.MaskBase64 == "" {
			//	return midjourney.MidjourneyErrorWrapper(common.MjRequestError, "mask_base64_is_required")
			//}
			mjId = midjRequest.TaskId
			midjRequest.Action = common.MjActionModal
		}

		originTask := model.GetByMJId(userId, mjId)
		if originTask == nil {
			return midjourney.MidjourneyErrorWrapper(common.MjRequestError, "task_not_found")
		} else if originTask.Status != "SUCCESS" && relayMode != relayconstant.RelayModeMidjourneyModal {
			return midjourney.MidjourneyErrorWrapper(common.MjRequestError, "task_status_not_success")
		} else { //原任务的Status=SUCCESS，则可以做放大UPSCALE、变换VARIATION等动作，此时必须使用原来的请求地址才能正确处理
			channel, err := model.GetChannelById(originTask.ChannelId, true)
			if err != nil {
				return midjourney.MidjourneyErrorWrapper(common.MjRequestError, "get_channel_info_failed")
			}
			if channel.Status != common.ChannelStatusEnabled {
				return midjourney.MidjourneyErrorWrapper(common.MjRequestError, "该任务所属渠道已被禁用")
			}
			c.Set("base_url", channel.GetBaseURL())
			c.Set("channel_id", originTask.ChannelId)
			c.Request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", channel.Key))
			log.Printf("检测到此操作为放大、变换、重绘，获取原channel信息: %s,%s", strconv.Itoa(originTask.ChannelId), channel.GetBaseURL())
		}
		midjRequest.Prompt = originTask.Prompt

		//if channelType == common.ChannelTypeMidjourneyPlus {
		//	// plus
		//} else {
		//	// 普通版渠道
		//
		//}
	}

	if midjRequest.Action == common.MjActionInPaint || midjRequest.Action == common.MjActionCustomZoom {
		consumeQuota = false
	}

	//baseURL := common.ChannelBaseURLs[channelType]
	requestURL := c.Request.URL.String()

	baseURL := c.GetString("base_url")

	//midjRequest.NotifyHook = "http://127.0.0.1:3000/mj/notify"

	fullRequestURL := fmt.Sprintf("%s%s", baseURL, requestURL)

	var MidjourneyType string
	var modelPrice float64
	//对于价格的验证，后续做改进
	modelName := midjourney.CoverActionToModelName(midjRequest.Action)
	if modelName == "mj_imagine" || modelName == "mj_shorten" || modelName == "mj_modal" {
		MidjourneyType := midjourney.ParsePrompts(midjRequest.Prompt)
		// 根据 MidjourneyType 设置不同的 modelPrice
		switch MidjourneyType {
		case "fast":
			modelPrice = 0.036 // 示例价格
		case "turbo":
			modelPrice = 0.08 // 示例价格
		case "relax":
			modelPrice = 0.012 // 示例价格
		default:
			modelPrice = 0.036 // 默认价格
		}
	} else {
		modelPrice := common.GetModelPrice(modelName, true)
		// 如果没有配置价格，则使用默认价格
		if modelPrice == -1 {
			defaultPrice, ok := common.DefaultModelPrice[modelName]
			if !ok {
				modelPrice = 0.036
			} else {
				modelPrice = defaultPrice
			}
		}
	}
	ctx := c.Request.Context()
	groupRatio := common.GetGroupRatio(group)
	ratio := modelPrice * groupRatio
	userQuota, err := model.CacheGetUserQuota(ctx, userId)
	if err != nil {
		return &midjourney.MidjourneyResponse{
			Code:        4,
			Description: err.Error(),
		}
	}
	quota := int64(ratio * config.QuotaPerUnit)

	if consumeQuota && userQuota-quota < 0 {
		return &midjourney.MidjourneyResponse{
			Code:        4,
			Description: "quota_not_enough",
		}
	}

	midjResponseWithStatus, responseBody, err := midjourney.DoMidjourneyHttpRequest(c, time.Second*60, fullRequestURL)
	if err != nil {
		return &midjResponseWithStatus.Response
	}
	midjResponse := &midjResponseWithStatus.Response

	defer func(ctx context.Context) {
		if consumeQuota && midjResponseWithStatus.StatusCode == 200 {
			err := model.PostConsumeTokenQuota(tokenId, quota)
			if err != nil {
				logger.SysError("error consuming token remain quota: " + err.Error())
			}
			err = model.CacheUpdateUserQuota(ctx, userId)
			if err != nil {
				logger.SysError("error update user quota cache: " + err.Error())
			}
			if quota != 0 {
				tokenName := c.GetString("token_name")
				logContent := fmt.Sprintf("模型固定价格 %.2f，分组倍率 %.2f，操作 %s", modelPrice, groupRatio, midjRequest.Action)
				model.RecordConsumeLog(ctx, userId, channelId, 0, 0, modelName, tokenName, quota, logContent, 0)
				model.UpdateUserUsedQuotaAndRequestCount(userId, quota)
				channelId := c.GetInt("channel_id")
				model.UpdateChannelUsedQuota(channelId, quota)
			}
		}
	}(c.Request.Context())

	// 文档：https://github.com/novicezk/midjourney-proxy/blob/main/docs/api.md
	//1-提交成功
	// 21-任务已存在（处理中或者有结果了） {"code":21,"description":"任务已存在","result":"0741798445574458","properties":{"status":"SUCCESS","imageUrl":"https://xxxx"}}
	// 22-排队中 {"code":22,"description":"排队中，前面还有1个任务","result":"0741798445574458","properties":{"numberOfQueues":1,"discordInstanceId":"1118138338562560102"}}
	// 23-队列已满，请稍后再试 {"code":23,"description":"队列已满，请稍后尝试","result":"14001929738841620","properties":{"discordInstanceId":"1118138338562560102"}}
	// 24-prompt包含敏感词 {"code":24,"description":"可能包含敏感词","properties":{"promptEn":"nude body","bannedWord":"nude"}}
	// other: 提交错误，description为错误描述
	midjourneyTask := &model.Midjourney{
		UserId:      userId,
		Code:        midjResponse.Code,
		Action:      midjRequest.Action,
		MjId:        midjResponse.Result,
		Prompt:      midjRequest.Prompt,
		PromptEn:    "",
		Description: midjResponse.Description,
		State:       "",
		SubmitTime:  time.Now().UnixNano() / int64(time.Millisecond),
		StartTime:   0,
		FinishTime:  0,
		ImageUrl:    "",
		Status:      "",
		Progress:    "0%",
		FailReason:  "",
		ChannelId:   c.GetInt("channel_id"),
		Quota:       quota,
		Type:        MidjourneyType,
	}

	if midjResponse.Code != 1 && midjResponse.Code != 21 && midjResponse.Code != 22 {
		//非1-提交成功,21-任务已存在和22-排队中，则记录错误原因
		midjourneyTask.FailReason = midjResponse.Description
		consumeQuota = false
	}

	if midjResponse.Code == 21 { //21-任务已存在（处理中或者有结果了）
		// 将 properties 转换为一个 map
		properties, ok := midjResponse.Properties.(map[string]interface{})
		if ok {
			imageUrl, ok1 := properties["imageUrl"].(string)
			status, ok2 := properties["status"].(string)
			if ok1 && ok2 {
				midjourneyTask.ImageUrl = imageUrl
				midjourneyTask.Status = status
				if status == "SUCCESS" {
					midjourneyTask.Progress = "100%"
					midjourneyTask.StartTime = time.Now().UnixNano() / int64(time.Millisecond)
					midjourneyTask.FinishTime = time.Now().UnixNano() / int64(time.Millisecond)
					midjResponse.Code = 1
				}
			}
		}
		//修改返回值
		if midjRequest.Action != common.MjActionInPaint && midjRequest.Action != common.MjActionCustomZoom {
			newBody := strings.Replace(string(responseBody), `"code":21`, `"code":1`, -1)
			responseBody = []byte(newBody)
		}
	}

	err = midjourneyTask.Insert()
	if err != nil {
		return &midjourney.MidjourneyResponse{
			Code:        4,
			Description: "insert_midjourney_task_failed",
		}
	}

	if midjResponse.Code == 22 { //22-排队中，说明任务已存在
		//修改返回值
		newBody := strings.Replace(string(responseBody), `"code":22`, `"code":1`, -1)
		responseBody = []byte(newBody)
	}

	//resp.Body = io.NopCloser(bytes.NewBuffer(responseBody))
	bodyReader := io.NopCloser(bytes.NewBuffer(responseBody))

	//for k, v := range resp.Header {
	//	c.Writer.Header().Set(k, v[0])
	//}
	c.Writer.WriteHeader(midjResponseWithStatus.StatusCode)

	_, err = io.Copy(c.Writer, bodyReader)
	if err != nil {
		return &midjourney.MidjourneyResponse{
			Code:        4,
			Description: "copy_response_body_failed",
		}
	}
	err = bodyReader.Close()
	if err != nil {
		return &midjourney.MidjourneyResponse{
			Code:        4,
			Description: "close_response_body_failed",
		}
	}
	return nil
}

type taskChangeParams struct {
	ID     string
	Action string
	Index  int
}
