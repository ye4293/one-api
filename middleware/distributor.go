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
	"github.com/songquanpeng/one-api/relay/channel/stability"
	relayconstant "github.com/songquanpeng/one-api/relay/constant"

	"github.com/gin-gonic/gin"
)

type ModelRequest struct {
	Model     string `json:"model,omitempty" form:"model"`
	ModelName string `json:"model_name,omitempty" form:"model_name"`
}

// extractModelNameFromGeminiPath 从 Gemini API 路径中提取模型名称
// 路径格式: /v1beta/models/{model_name}:{action}
// 例如:
//   - 完整路径: /v1beta/models/gemini-2.0-flash:generateContent -> gemini-2.0-flash
//   - 通配符参数: /gemini-2.0-flash:generateContent -> gemini-2.0-flash
func extractModelNameFromGeminiPath(path string) string {
	// 处理通配符路径参数（以 / 开头）
	if strings.HasPrefix(path, "/") {
		path = path[1:] // 移除开头的 /
	}

	// 查找 /models/ 的位置
	modelsIndex := strings.Index(path, "/models/")
	if modelsIndex != -1 {
		// 获取 /models/ 之后的部分
		path = path[modelsIndex+8:] // 8 = len("/models/")
	}

	// 查找 : 的位置（action 分隔符）
	colonIndex := strings.Index(path, ":")
	if colonIndex == -1 {
		// 如果没有 :，返回整个字符串
		return path
	}

	// 返回 : 之前的模型名称
	modelName := path[:colonIndex]
	return modelName
}

func Distribute() func(c *gin.Context) {
	return func(c *gin.Context) {
		userId := c.GetInt("id")
		userGroup, _ := model.CacheGetUserGroup(userId)
		c.Set("group", userGroup)
		var requestModel string
		var channel *model.Channel
		var modelRequest ModelRequest
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
			// var modelRequest ModelRequest
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
						abortWithMidjourneyMessage(c, http.StatusBadRequest, mjErr.Response.Code, mjErr.Response.Description)
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

			} else if strings.HasPrefix(c.Request.URL.Path, "/v2beta") || strings.HasPrefix(c.Request.URL.Path, "/sd") { //sd的api开头
				relayMode := relayconstant.Path2RelayModeSd((c.Request.URL.Path))
				if relayMode == relayconstant.RelayModeUpscaleCreativeResult || relayMode == relayconstant.RelayModeVideoResult {
					shouldSelectChannel = false
				}
				sdModel, err := stability.GetSdRequestModel(relayMode)
				if err != nil {
					abortWithMessage(c, http.StatusBadRequest, "Invalid request")
					return
				}
				modelRequest.Model = sdModel
				c.Set("relay_mode", relayMode)
			} else if strings.HasPrefix(c.Request.URL.Path, "/v1beta/models/") || strings.HasPrefix(c.Request.URL.Path, "/v1/models/") {
				// Gemini API 路径处理: /v1beta/models/gemini-2.0-flash:generateContent
				//relayMode := relayconstant.Path2RelayModeGemini(c.Request.URL.Path)
				relayMode := relayconstant.Path2RelayModeGemini(c.Request.URL.Path)
				if relayMode == relayconstant.RelayModeUnknown {
					abortWithMessage(c, http.StatusBadRequest, "Invalid gemini request path: "+c.Request.URL.Path)
					return
				}
				modelName := extractModelNameFromGeminiPath(c.Request.URL.Path)
				if modelName == "" {
					abortWithMessage(c, http.StatusBadRequest, "Invalid gemini request path: "+c.Request.URL.Path)
					return
				}
				modelRequest.Model = modelName
				c.Set("relay_mode", relayMode)
			} else {

				err = common.UnmarshalBodyReusable(c, &modelRequest)

				if err != nil {

					logger.SysLog(fmt.Sprintf("err:%+v", err))
					abortWithMessage(c, http.StatusBadRequest, "Invalid request")
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
			if requestModel == "" {
				requestModel = modelRequest.ModelName
			}
			c.Set("model", requestModel)

			if shouldSelectChannel {
				channel, err = model.CacheGetRandomSatisfiedChannel(userGroup, requestModel, 0)
				if err != nil {
					message := fmt.Sprintf("There are no channels available for model %s under the current group %s", requestModel, userGroup)
					if channel != nil {
						logger.SysError(fmt.Sprintf("Channel does not exist：%d", channel.Id))
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

	// 获取实际使用的Key（支持多Key聚合）
	var actualKey string
	var keyIndex int
	var err error

	// 检查是否有排除的Key索引（用于重试时跳过失败的Key）
	excludeIndices := getExcludedKeyIndices(c)

	if channel.MultiKeyInfo.IsMultiKey && len(excludeIndices) > 0 {
		// 多Key模式且有排除列表，使用带重试的方法
		actualKey, keyIndex, err = channel.GetNextAvailableKeyWithRetry(excludeIndices)
	} else {
		// 正常获取Key
		actualKey, keyIndex, err = channel.GetNextAvailableKey()
	}

	if err != nil {
		logger.SysError(fmt.Sprintf("Failed to get available key for channel %d: %s", channel.Id, err.Error()))
		actualKey = channel.Key // 回退到原始Key
		keyIndex = 0
	}

	// 存储Key信息供后续使用
	c.Set("actual_key", actualKey)
	c.Set("key_index", keyIndex)
	c.Set("is_multi_key", channel.MultiKeyInfo.IsMultiKey)

	// 记录使用的Key（脱敏）
	maskedKey := actualKey
	if len(actualKey) > 8 {
		maskedKey = actualKey[:4] + "***" + actualKey[len(actualKey)-4:]
	}
	logger.SysLog(fmt.Sprintf("channel:%d;requestModel:%s;keyIndex:%d;maskedKey:%s",
		channel.Id, modelName, keyIndex, maskedKey))

	c.Request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", actualKey))
	c.Set("base_url", channel.GetBaseURL())
	cfg, _ := channel.LoadConfig()
	// this is for backward compatibility
	switch channel.Type {
	case common.ChannelTypeAzure:
		if cfg.APIVersion == "" {
			cfg.APIVersion = channel.Other
		}
	case common.ChannelTypeXunfei:
		c.Set(common.ConfigKeyAPIVersion, channel.Other)
	case common.ChannelTypeGemini:
		c.Set(common.ConfigKeyAPIVersion, channel.Other)
	case common.ChannelTypeAIProxyLibrary:
		c.Set(common.ConfigKeyLibraryID, channel.Other)
	case common.ChannelTypeAli:
		c.Set(common.ConfigKeyPlugin, channel.Other)
	}
	c.Set("Config", cfg)
}

// getExcludedKeyIndices 获取需要排除的Key索引列表（用于重试时跳过失败的Key）
func getExcludedKeyIndices(c *gin.Context) []int {
	if excludedKeysInterface, exists := c.Get("excluded_key_indices"); exists {
		if excludedKeys, ok := excludedKeysInterface.([]int); ok {
			return excludedKeys
		}
	}
	return []int{}
}

// addExcludedKeyIndex 添加一个需要排除的Key索引
func addExcludedKeyIndex(c *gin.Context, keyIndex int) {
	excludedKeys := getExcludedKeyIndices(c)

	// 检查是否已经存在
	for _, existingIndex := range excludedKeys {
		if existingIndex == keyIndex {
			return
		}
	}

	// 添加新的索引
	excludedKeys = append(excludedKeys, keyIndex)
	c.Set("excluded_key_indices", excludedKeys)
}
