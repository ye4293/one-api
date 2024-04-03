package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/midjourney"
	relayconstant "github.com/songquanpeng/one-api/relay/constant"

	"github.com/gin-gonic/gin"
)

type ModelRequest struct {
	Model string `json:"model"`
}

func Distribute() func(c *gin.Context) {
	return func(c *gin.Context) {
		userId := c.GetInt("id")
		userGroup, _ := model.CacheGetUserGroup(userId)
		c.Set("group", userGroup)
		var requestModel string
		var channel *model.Channel
		channelId, ok := c.Get("specific_channel_id")
		if ok {
			id, err := strconv.Atoi(channelId.(string))
			if err != nil {
				abortWithMessage(c, http.StatusBadRequest, "Invalid channel Id")
				return
			}
			channel, err = model.GetChannelById(id, true)
			if err != nil {
				abortWithMessage(c, http.StatusBadRequest, "Invalid channel Id")
				return
			}
			if channel.Status != common.ChannelStatusEnabled {
				abortWithMessage(c, http.StatusForbidden, "The channel has been disabled")
				return
			}
		} else {
			shouldSelectChannel := true
			// Select a channel for the user
			var modelRequest ModelRequest
			var err error
			if strings.HasPrefix(c.Request.URL.Path, "/mj") {
				relayMode := relayconstant.Path2RelayModeMidjourney((c.Request.URL.Path))
				if relayMode == relayconstant.RelayModeMidjourneyTaskFetch ||
					relayMode == relayconstant.RelayModeMidjourneyTaskFetchByCondition ||
					relayMode == relayconstant.RelayModeMidjourneyNotify ||
					relayMode == relayconstant.RelayModeMidjourneyTaskImageSeed {
					shouldSelectChannel = false
				} else {
					midjourneyRequest := midjourney.MidjourneyRequest{}
					err = common.UnmarshalBodyReusable(c, &midjourneyRequest)
					if err != nil {
						abortWithMidjourneyMessage(c, http.StatusBadRequest, common.MjErrorUnknown, "无效的请求, "+err.Error())
						return
					}
					midjourneyModel, mjErr, success := midjourney.GetMjRequestModel(relayMode, &midjourneyRequest)
					if mjErr != nil {
						abortWithMidjourneyMessage(c, http.StatusBadRequest, mjErr.Code, mjErr.Description)
						return
					}
					if midjourneyModel == "" {
						if !success {
							abortWithMidjourneyMessage(c, http.StatusBadRequest, common.MjErrorUnknown, "无效的请求, 无法解析模型")
							return
						} else {
							// task fetch, task fetch by condition, notify
							shouldSelectChannel = false
						}
					}
					modelRequest.Model = midjourneyModel
				}
				c.Set("relay_mode", relayMode)
			} else {
				err = common.UnmarshalBodyReusable(c, &modelRequest)
				if err != nil {
					abortWithMessage(c, http.StatusBadRequest, "Invalidxxxx request")
					return
				}
			}
			if strings.HasPrefix(c.Request.URL.Path, "/v1/moderations") {
				if modelRequest.Model == "" {
					modelRequest.Model = "text-moderation-stable"
				}
			}
			if strings.HasSuffix(c.Request.URL.Path, "embeddings") {
				if modelRequest.Model == "" {
					modelRequest.Model = c.Param("model")
				}
			}
			if strings.HasPrefix(c.Request.URL.Path, "/v1/images/generations") {
				if modelRequest.Model == "" {
					modelRequest.Model = "dall-e-2"
				}
			}
			if strings.HasPrefix(c.Request.URL.Path, "/v1/audio/transcriptions") || strings.HasPrefix(c.Request.URL.Path, "/v1/audio/translations") {
				if modelRequest.Model == "" {
					modelRequest.Model = "whisper-1"
				}
			}
			requestModel = modelRequest.Model
			if shouldSelectChannel {
				channel, err = model.CacheGetRandomSatisfiedChannel(userGroup, modelRequest.Model, false)
				logger.SysLog(fmt.Sprintf("requestModel:%s\n", requestModel))
				if err != nil {
					message := fmt.Sprintf("There are no channels available for model %s under the current group %s", userGroup, modelRequest.Model)
					if channel != nil {
						logger.SysError(fmt.Sprintf("渠道不存在：%d", channel.Id))
						message = "Database consistency has been violated, please contact the administrator"
					}
					abortWithMessage(c, http.StatusServiceUnavailable, message)
					return
				}
				SetupContextForSelectedChannel(c, channel, requestModel)
			}
		}
		c.Next()
	}
}

func SetupContextForSelectedChannel(c *gin.Context, channel *model.Channel, modelName string) {
	c.Set("channel", channel.Type)
	c.Set("channel_id", channel.Id)
	c.Set("channel_name", channel.Name)
	c.Set("model_mapping", channel.GetModelMapping())
	c.Set("original_model", modelName) // for retry
	c.Request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", channel.Key))
	c.Set("base_url", channel.GetBaseURL())
	// this is for backward compatibility
	switch channel.Type {
	case common.ChannelTypeAzure:
		c.Set(common.ConfigKeyAPIVersion, channel.Other)
	case common.ChannelTypeXunfei:
		c.Set(common.ConfigKeyAPIVersion, channel.Other)
	case common.ChannelTypeGemini:
		c.Set(common.ConfigKeyAPIVersion, channel.Other)
	case common.ChannelTypeAIProxyLibrary:
		c.Set(common.ConfigKeyLibraryID, channel.Other)
	case common.ChannelTypeAli:
		c.Set(common.ConfigKeyPlugin, channel.Other)
	}
	cfg, _ := channel.LoadConfig()
	for k, v := range cfg {
		c.Set(common.ConfigKeyPrefix+k, v)
	}
}
