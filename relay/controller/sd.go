package controller

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/channel/stability"
	"github.com/songquanpeng/one-api/relay/model"
)

func RelaySdGenerate(c *gin.Context, relayMode int) *model.ErrorWithStatusCode {
	createTime := time.Now()
	tokenId := c.GetInt("token_id")
	//channelType := c.GetInt("channel")
	userId := c.GetInt("id")
	group := c.GetString("group")
	channelId := c.GetInt("channel_id")
	consumeQuota := true
	var sdRequest stability.SdGenerationRequest
	err := c.Bind(&sdRequest)
	if err != nil {
		logger.SysLog(fmt.Sprintf("err:%s", err))
		return openai.ErrorWrapper(err, "Failed to bind request", http.StatusBadRequest)
	}

	requestURL := c.Request.URL.String()

	baseURL := c.GetString("base_url")

	fullRequestURL := fmt.Sprintf("%s%s", baseURL, requestURL)

	var modelPrice float64
	var modelName string
	var quota int64
	ctx := c.Request.Context()

	groupRatio := common.GetGroupRatio(group)
	modelName, err = stability.GetSdRequestModel(relayMode)
	if err != nil {
		return openai.ErrorWrapper(err, "Failed to get modelName", http.StatusBadRequest)
	}

	if relayMode == 24 {
		if sdRequest.Model == "" {
			modelName = "sd3-large"
		} else {
			modelName = sdRequest.Model
		}
	}
	modelPrice = common.GetModelPrice(modelName, true)
	ratio := modelPrice * groupRatio
	userQuota, err := dbmodel.CacheGetUserQuota(ctx, userId)
	if err != nil {
		return openai.ErrorWrapper(err, "Failed to get userQuota", http.StatusBadRequest)
	}

	quota = int64(ratio * config.QuotaPerUnit)

	if consumeQuota && userQuota-quota < 0 {
		return openai.ErrorWrapper(err, "User balance is not enough", http.StatusBadRequest)
	}

	sdResponse, responseBody, sdResponseStruct, responseModelName, err := stability.DoSdHttpRequest(c, time.Second*60, fullRequestURL)
	// logger.SysLog(fmt.Sprintf("sdResponse:%+v\n", sdResponse))
	// logger.SysLog(fmt.Sprintf("responseBody:%+v\n", responseBody))
	// logger.SysLog(fmt.Sprintf("sdResponseStruct:%+v\n", sdResponseStruct))
	// logger.SysLog(fmt.Sprintf("fullRequestURL:%+v\n", fullRequestURL))

	if err != nil {
		return openai.ErrorWrapper(err, "failed to get response", http.StatusBadRequest)
	}
	if responseModelName == "sd3-turbo" {
		modelName = responseModelName
		quota = 20000

	}
	finishTime := time.Now()

	defer func(ctx context.Context) {
		if consumeQuota && sdResponse.StatusCode == 200 {
			referer := c.Request.Header.Get("HTTP-Referer")
			title := c.Request.Header.Get("X-Title")
			err := dbmodel.PostConsumeTokenQuota(tokenId, quota)
			if err != nil {
				logger.SysError("error consuming token remain quota: " + err.Error())
			}
			err = dbmodel.CacheUpdateUserQuota(ctx, userId)
			if err != nil {
				logger.SysError("error update user quota cache: " + err.Error())
			}
			if quota != 0 {
				duration := math.Round(finishTime.Sub(createTime).Seconds()*1000) / 1000
				tokenName := c.GetString("token_name")
				logContent := fmt.Sprintf("模型固定价格 %.2f，分组倍率 %.2f，操作 %s", modelPrice, groupRatio, modelName)
				dbmodel.RecordConsumeLog(ctx, userId, channelId, 0, 0, modelName, tokenName, quota, logContent, duration, title, referer)
				dbmodel.UpdateUserUsedQuotaAndRequestCount(userId, quota)
				channelId := c.GetInt("channel_id")
				dbmodel.UpdateChannelUsedQuota(channelId, quota)
			}
		}
	}(c.Request.Context())

	var GenerationId string
	if sdResponseStruct.Id != "" {
		GenerationId = sdResponseStruct.Id
	} else {
		GenerationId = ""
	}

	username := dbmodel.GetUsernameById(userId)

	sdTask := &dbmodel.Sd{
		UserId:       userId,
		Username:     username,
		CreatedAt:    createTime.UnixNano() / int64(time.Millisecond),
		FinishAt:     finishTime.UnixNano() / int64(time.Millisecond),
		Quota:        quota,
		Model:        modelName,
		ChannelId:    c.GetInt("channel_id"),
		GenerationId: GenerationId,
	}
	err = sdTask.Insert()
	if err != nil {
		return openai.ErrorWrapper(err, "failed to Insert sdTask", http.StatusBadRequest)
	}
	bodyReader := io.NopCloser(bytes.NewBuffer(responseBody))
	c.Writer.WriteHeader(sdResponse.StatusCode)
	_, err = io.Copy(c.Writer, bodyReader)
	if err != nil {
		return openai.ErrorWrapper(err, "Failed to show response", http.StatusBadRequest)
	}
	return nil

}

func GetUpscaleResults(c *gin.Context) *model.ErrorWithStatusCode {

	generationId := c.Param("generation_id")
	logger.SysLog(fmt.Sprintf("generationId:%+v\n", generationId))
	channelId, err := dbmodel.GetChannelIdByGenerationId(generationId)
	if err != nil {
		logger.SysLog(fmt.Sprintf("err:%+v\n", err))
		return openai.ErrorWrapper(err, "get_channel_id_failed", http.StatusInternalServerError)
	}

	channel, err := dbmodel.GetChannelById(channelId, true)
	if err != nil {
		return openai.ErrorWrapper(err, "get_channel_failed", http.StatusInternalServerError)
	}

	baseURL := *channel.BaseURL
	fullRequestURL := baseURL + c.Request.RequestURI
	logger.SysLog(fmt.Sprintf("fullRequestURL:%+v\n", fullRequestURL))
	sdResponse, responseBody, err := stability.DoSdUpscaleResults(c, time.Second*60, *channel, fullRequestURL)
	if err != nil {
		return openai.ErrorWrapper(err, "failed to get response", http.StatusBadRequest)
	}
	bodyReader := io.NopCloser(bytes.NewBuffer(responseBody))
	c.Writer.WriteHeader(sdResponse.StatusCode)
	_, err = io.Copy(c.Writer, bodyReader)
	if err != nil {
		return openai.ErrorWrapper(err, "failed to show response", http.StatusBadRequest)
	}
	return nil
}
