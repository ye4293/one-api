package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/keling"
	"github.com/songquanpeng/one-api/relay/channel/midjourney"
	relayconstant "github.com/songquanpeng/one-api/relay/constant"
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

		var channel *model.Channel
		var err error

		// 先统一解析模型名和设置 relay_mode（参考 new-api 的设计）
		// 这样不管是否指定特定渠道，都会正确解析请求
		modelRequest, shouldSelectChannel := getModelRequest(c)

		// 检查是否指定了特定渠道
		channelId, ok := c.Get("specific_channel_id")
		if ok {
			// 指定特定渠道：直接通过 ID 获取渠道
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
			// 正常流程：根据模型选择渠道
			if shouldSelectChannel {
				if modelRequest.Model == "" {
					abortWithMessage(c, http.StatusBadRequest, "Model name is required")
					return
				}
				// 获取客户端传递的 X-Response-ID（用于 Claude 缓存）
				responseID := c.GetHeader("X-Response-ID")
				channel, err = model.CacheGetRandomSatisfiedChannel(userGroup, modelRequest.Model, 0, responseID)
				if err != nil {
					message := fmt.Sprintf("There are no channels available for model %s under the current group %s", modelRequest.Model, userGroup)
					if channel != nil {
						logger.SysError(fmt.Sprintf("Channel does not exist：%d", channel.Id))
						message = "Database consistency has been violated, please contact the administrator"
					}
					abortWithMessage(c, http.StatusServiceUnavailable, message)
					return
				}
			}
		}

		// 统一设置上下文（不管是哪个分支都执行）
		requestModel := modelRequest.Model
		if requestModel == "" {
			requestModel = modelRequest.ModelName
		}
		c.Set("model", requestModel)

		if channel != nil {
			SetupContextForSelectedChannel(c, channel, requestModel)
		}
		c.Next()
	}
}

// getModelRequest 从请求中解析模型名称并设置 relay_mode
// 返回 modelRequest 和是否需要选择渠道
func getModelRequest(c *gin.Context) (*ModelRequest, bool) {
	var modelRequest ModelRequest
	shouldSelectChannel := true
	path := c.Request.URL.Path

	if strings.HasPrefix(path, "/mj") {
		relayMode := relayconstant.Path2RelayModeMidjourney(path)
		if relayMode == relayconstant.RelayModeMidjourneyTaskFetch ||
			relayMode == relayconstant.RelayModeMidjourneyTaskFetchByCondition ||
			relayMode == relayconstant.RelayModeMidjourneyNotify ||
			relayMode == relayconstant.RelayModeMidjourneyTaskImageSeed {
			shouldSelectChannel = false
		} else {
			midjourneyRequest := midjourney.MidjourneyRequest{}
			if err := common.UnmarshalBodyReusable(c, &midjourneyRequest); err == nil {
				midjourneyModel, mjErr, success := midjourney.GetMjRequestModel(relayMode, &midjourneyRequest)
				if mjErr == nil && midjourneyModel != "" {
					modelRequest.Model = midjourneyModel
				} else if !success {
					shouldSelectChannel = false
				}
			}
		}
		c.Set("relay_mode", relayMode)

	} else if strings.HasPrefix(path, "/v1beta/models/") || strings.HasPrefix(path, "/v1/models/") || strings.HasPrefix(path, "/v1alpha/models/") {
		// Gemini API 路径处理
		relayMode := relayconstant.Path2RelayModeGemini(path)
		if relayMode != relayconstant.RelayModeUnknown {
			modelName := extractModelNameFromGeminiPath(path)
			if modelName != "" {
				modelRequest.Model = modelName
			}
			c.Set("relay_mode", relayMode)
		}

	} else if strings.HasPrefix(path, "/kling/v1/") {
		// Kling API 路径处理
		// 判断是查询接口还是生成接口
		// 查询接口格式: /kling/v1/videos/{task_id} (GET)
		// 生成接口格式: /kling/v1/videos/text2video 等 (POST)
		if c.Request.Method == "GET" {
			// GET 请求是查询接口，从查询参数中读取 model_name 或 model
			if modelName := c.Query("model_name"); modelName != "" {
				modelRequest.Model = modelName
			} else if model := c.Query("model"); model != "" {
				modelRequest.Model = model
			}
		} else {
			// POST 请求是视频生成接口，解析请求体中的 model 字段
			_ = common.UnmarshalBodyReusable(c, &modelRequest)
			if modelRequest.ModelName != "" {
				modelRequest.Model = modelRequest.ModelName
			}
		}

	} else {
		// OpenAI 格式请求
		_ = common.UnmarshalBodyReusable(c, &modelRequest)
	}

	// 默认模型处理
	if strings.HasPrefix(path, "/v1/moderations") && modelRequest.Model == "" {
		modelRequest.Model = "text-moderation-stable"
	}
	if strings.HasSuffix(path, "embeddings") && modelRequest.Model == "" {
		modelRequest.Model = c.Param("model")
	}
	if strings.HasPrefix(path, "/v1/images/generations") && modelRequest.Model == "" {
		modelRequest.Model = "dall-e-2"
	}
	if (strings.HasPrefix(path, "/v1/audio/transcriptions") || strings.HasPrefix(path, "/v1/audio/translations")) && modelRequest.Model == "" {
		modelRequest.Model = "whisper-1"
	}

	return &modelRequest, shouldSelectChannel
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
	case common.ChannelTypeKeling:
		// 使用统一方法处理 Kling 凭证和 Token 生成
		token, err := keling.GetCredentialsAndGenerateToken(channel, keyIndex)
		if err != nil {
			logger.SysError(fmt.Sprintf("Failed to generate Kling token for channel %d: %s", channel.Id, err.Error()))
			// 使用原始 key 作为降级方案
			c.Request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", actualKey))
			c.Set("Config", cfg)
			return
		}

		// 设置 Authorization header
		c.Request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
		logger.SysLog(fmt.Sprintf("Kling JWT token generated for channel %d", channel.Id))
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
