package controller

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
)

func GetAllChannels(c *gin.Context) {
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 0 {
		page = 0
	}
	pagesize, _ := strconv.Atoi(c.Query("pagesize"))
	currentPage := page
	channels, total, err := model.GetChannelsAndCount(page, pagesize)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"list":        channels,
			"currentPage": currentPage,
			"pageSize":    pagesize,
			"total":       total,
		},
	})
	return
}

func SearchChannels(c *gin.Context) {
	keyword := c.Query("keyword")
	pageStr := c.Query("page")
	pageSizeStr := c.Query("pagesize")
	statusStr := c.Query("status") // 获取status参数

	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}

	pagesize, err := strconv.Atoi(pageSizeStr)
	if err != nil || pagesize <= 0 {
		pagesize = 10
	}

	var status *int
	if statusStr != "" {
		statusInt, err := strconv.Atoi(statusStr)
		if err == nil && (statusInt == 1 || statusInt == 2) {
			status = &statusInt
		}
	}

	currentPage := page
	channels, total, err := model.SearchChannelsAndCount(keyword, status, page, pagesize) // 将status作为参数传递
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"list":        channels,
			"currentPage": currentPage,
			"pageSize":    pagesize,
			"total":       total,
		},
	})
	return
}

func GetChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	channel, err := model.GetChannelById(id, false)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channel,
	})
	return
}

func AddChannel(c *gin.Context) {
	// 创建临时结构来接收前端数据，包括多密钥配置
	var requestData struct {
		model.Channel
		KeySelectionMode int `json:"key_selection_mode"`
		BatchImportMode  int `json:"batch_import_mode"`
	}

	err := c.ShouldBindJSON(&requestData)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	channel := requestData.Channel

	channel.CreatedTime = helper.GetTimestamp()

	// 检查是否为多Key聚合模式
	keys := strings.Split(channel.Key, "\n")
	validKeys := []string{}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key != "" {
			validKeys = append(validKeys, key)
		}
	}

	if len(validKeys) > 1 {
		// 多Key聚合模式：创建一个聚合渠道
		channel.Key = strings.Join(validKeys, "\n")
		channel.MultiKeyInfo.IsMultiKey = true
		channel.MultiKeyInfo.KeyCount = len(validKeys)
		// 使用前端配置，有默认值兜底
		keySelectionMode := model.KeySelectionMode(requestData.KeySelectionMode)
		if keySelectionMode != 0 && keySelectionMode != 1 {
			keySelectionMode = 1 // 默认随机模式
		}
		batchImportMode := model.BatchImportMode(requestData.BatchImportMode)
		if batchImportMode != 0 && batchImportMode != 1 {
			batchImportMode = 1 // 默认追加模式
		}

		channel.MultiKeyInfo.KeySelectionMode = keySelectionMode
		channel.MultiKeyInfo.PollingIndex = 0
		channel.MultiKeyInfo.KeyStatusList = make(map[int]int)
		channel.MultiKeyInfo.KeyMetadata = make(map[int]model.KeyMetadata)
		channel.MultiKeyInfo.LastBatchImportTime = helper.GetTimestamp()
		channel.MultiKeyInfo.BatchImportMode = batchImportMode

		// 初始化每个Key的元数据和状态
		batchId := fmt.Sprintf("batch_%d", time.Now().Unix())
		for i := range validKeys {
			// 设置Key状态为启用
			channel.MultiKeyInfo.KeyStatusList[i] = common.ChannelStatusEnabled
			// 设置Key元数据
			channel.MultiKeyInfo.KeyMetadata[i] = model.KeyMetadata{
				Balance:     0,
				Usage:       0,
				LastUsed:    0,
				ImportBatch: batchId,
				Note:        "",
			}
		}

		err = channel.Insert()
	} else {
		// 单Key模式：使用原有逻辑
		if len(validKeys) > 0 {
			channel.Key = validKeys[0]
		}
		err = channel.Insert()
	}
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func DeleteChannel(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	channel := model.Channel{Id: id}
	err := channel.Delete()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func BatchDelteChannel(c *gin.Context) {
	var request struct {
		Ids []int `json:"ids"`
	}

	if err := c.BindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request body",
		})
		return
	}
	if len(request.Ids) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "No IDs provided for deletion",
		})
		return
	}
	err := model.BatchDeleteChannel(request.Ids)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	// 返回成功响应
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Channels deleted successfully",
	})
}

func DeleteDisabledChannel(c *gin.Context) {
	rows, err := model.DeleteDisabledChannel()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    rows,
	})
	return
}

func UpdateChannel(c *gin.Context) {
	// 创建临时结构来接收前端数据，包括多密钥配置
	var requestData struct {
		model.Channel
		KeySelectionMode int `json:"key_selection_mode"`
		BatchImportMode  int `json:"batch_import_mode"`
	}

	err := c.ShouldBindJSON(&requestData)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	channel := requestData.Channel

	logger.SysLog(fmt.Sprintf("UpdateChannel: channel.Id=%d, IsMultiKey=%v", channel.Id, channel.MultiKeyInfo.IsMultiKey))

	// 如果是多密钥渠道，处理密钥更新和配置
	if channel.MultiKeyInfo.IsMultiKey {
		// 先获取现有渠道数据
		existingChannel, err := model.GetChannelById(channel.Id, true)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "获取现有渠道信息失败: " + err.Error(),
			})
			return
		}

		// 先更新配置（确保使用正确的模式）
		keySelectionMode := model.KeySelectionMode(requestData.KeySelectionMode)
		if keySelectionMode != 0 && keySelectionMode != 1 {
			keySelectionMode = existingChannel.MultiKeyInfo.KeySelectionMode // 保持原值
		}
		batchImportMode := model.BatchImportMode(requestData.BatchImportMode)
		if batchImportMode != 0 && batchImportMode != 1 {
			batchImportMode = existingChannel.MultiKeyInfo.BatchImportMode // 保持原值
		}

		// 处理密钥更新
		if strings.TrimSpace(channel.Key) != "" {
			logger.SysLog(fmt.Sprintf("Updating keys for multi-key channel %d with mode %d", channel.Id, batchImportMode))

			// 解析新的密钥
			newKeys := strings.Split(channel.Key, "\n")
			validNewKeys := []string{}
			for _, key := range newKeys {
				key = strings.TrimSpace(key)
				if key != "" {
					validNewKeys = append(validNewKeys, key)
				}
			}

			if len(validNewKeys) > 0 {
				// 根据编辑模式处理密钥（现在使用正确的模式）

				var finalKeys []string
				if batchImportMode == 0 { // 覆盖模式
					finalKeys = validNewKeys
					logger.SysLog(fmt.Sprintf("Channel %d: Overwriting with %d new keys", channel.Id, len(validNewKeys)))
				} else { // 追加模式
					existingKeys := existingChannel.ParseKeys()
					finalKeys = append(existingKeys, validNewKeys...)
					logger.SysLog(fmt.Sprintf("Channel %d: Appending %d keys to existing %d keys", channel.Id, len(validNewKeys), len(existingKeys)))
				}

				// 更新密钥字符串
				channel.Key = strings.Join(finalKeys, "\n")

				// 更新多密钥信息（保留现有配置）
				channel.MultiKeyInfo = existingChannel.MultiKeyInfo
				channel.MultiKeyInfo.KeyCount = len(finalKeys)
				channel.MultiKeyInfo.KeySelectionMode = keySelectionMode
				channel.MultiKeyInfo.BatchImportMode = batchImportMode

				// 初始化新密钥的状态和元数据
				if channel.MultiKeyInfo.KeyStatusList == nil {
					channel.MultiKeyInfo.KeyStatusList = make(map[int]int)
				}
				if channel.MultiKeyInfo.KeyMetadata == nil {
					channel.MultiKeyInfo.KeyMetadata = make(map[int]model.KeyMetadata)
				}

				batchId := fmt.Sprintf("batch_%d", time.Now().Unix())

				if batchImportMode == 0 { // 覆盖模式，重置所有状态
					channel.MultiKeyInfo.KeyStatusList = make(map[int]int)
					channel.MultiKeyInfo.KeyMetadata = make(map[int]model.KeyMetadata)
					for i := range finalKeys {
						channel.MultiKeyInfo.KeyStatusList[i] = common.ChannelStatusEnabled
						channel.MultiKeyInfo.KeyMetadata[i] = model.KeyMetadata{
							Balance: 0, Usage: 0, LastUsed: 0, ImportBatch: batchId, Note: "",
						}
					}
				} else { // 追加模式，只为新密钥设置状态
					startIndex := len(existingChannel.ParseKeys())
					for i, _ := range validNewKeys {
						keyIndex := startIndex + i
						channel.MultiKeyInfo.KeyStatusList[keyIndex] = common.ChannelStatusEnabled
						channel.MultiKeyInfo.KeyMetadata[keyIndex] = model.KeyMetadata{
							Balance: 0, Usage: 0, LastUsed: 0, ImportBatch: batchId, Note: "",
						}
					}
				}

				channel.MultiKeyInfo.LastBatchImportTime = helper.GetTimestamp()
			}
		} else {
			// 即使没有新密钥，也要更新配置
			channel.MultiKeyInfo = existingChannel.MultiKeyInfo
			channel.MultiKeyInfo.KeySelectionMode = keySelectionMode
			channel.MultiKeyInfo.BatchImportMode = batchImportMode
		}
	}

	err = channel.Update()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channel,
	})
	return
}

// GetChannelModelsById 根据渠道ID获取该渠道配置的所有模型
func GetChannelModelsById(c *gin.Context) {
	idStr := c.Query("id")
	if idStr == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Missing channel id parameter",
		})
		return
	}

	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Invalid channel id: " + err.Error(),
		})
		return
	}

	// 调用模型层函数获取渠道的模型列表
	supportedModels, err := model.GetChannelModelsbyId(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Failed to get channel models: " + err.Error(),
		})
		return
	}

	if len(supportedModels) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "No models configured for this channel",
		})
		return
	}

	// 构造返回的模型数据
	var modelData []gin.H
	for _, modelName := range supportedModels {
		modelData = append(modelData, gin.H{
			"id":       modelName,
			"object":   "model",
			"created":  time.Now().Unix(),
			"owned_by": fmt.Sprintf("channel-%d", id),
		})
	}

	logger.SysLog(fmt.Sprintf("Channel #%d has %d configured models: %v",
		id, len(supportedModels), supportedModels))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    modelData,
	})
}

// ==================== 多Key渠道管理API ====================

// GetChannelKeyStats 获取渠道的Key统计信息
func GetChannelKeyStats(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Invalid channel id",
		})
		return
	}

	channel, err := model.GetChannelById(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Channel not found: " + err.Error(),
		})
		return
	}

	stats := channel.GetKeyStats()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    stats,
	})
}

// GetChannelKeyDetails 获取渠道的详细Key信息
func GetChannelKeyDetails(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Invalid channel id",
		})
		return
	}

	channel, err := model.GetChannelById(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Channel not found: " + err.Error(),
		})
		return
	}

	keys := channel.ParseKeys()

	// 获取分页参数
	pageStr := c.Query("page")
	pageSizeStr := c.Query("page_size")
	statusFilter := c.Query("status")

	page, _ := strconv.Atoi(pageStr)
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(pageSizeStr)
	if pageSize < 1 || pageSize > 100 { // 限制最大页面大小
		pageSize = 20
	}

	var filteredIndices []int
	for i := range keys {
		status := channel.GetKeyStatus(i)
		// 状态过滤
		if statusFilter != "" {
			filterStatus, _ := strconv.Atoi(statusFilter)
			if status != filterStatus {
				continue
			}
		}
		filteredIndices = append(filteredIndices, i)
	}

	totalCount := len(filteredIndices)
	startIndex := (page - 1) * pageSize
	endIndex := startIndex + pageSize

	if startIndex >= totalCount {
		startIndex = totalCount
		endIndex = totalCount
	}
	if endIndex > totalCount {
		endIndex = totalCount
	}

	var keyDetails []gin.H
	for _, i := range filteredIndices[startIndex:endIndex] {
		key := keys[i]
		status := channel.GetKeyStatus(i)
		var statusText string
		switch status {
		case 1:
			statusText = "已启用"
		case 2:
			statusText = "手动禁用"
		case 3:
			statusText = "自动禁用"
		default:
			statusText = "未知状态"
		}

		// 获取Key元数据
		metadata := model.KeyMetadata{}
		if channel.MultiKeyInfo.KeyMetadata != nil {
			if meta, exists := channel.MultiKeyInfo.KeyMetadata[i]; exists {
				metadata = meta
			}
		}

		// 隐藏Key的敏感部分，只显示前4位和后4位
		maskedKey := key
		if len(key) > 8 {
			maskedKey = key[:4] + "..." + key[len(key)-4:]
		}

		keyDetails = append(keyDetails, gin.H{
			"index":        i,
			"key":          maskedKey, // 脱敏后的Key
			"status":       status,
			"status_text":  statusText,
			"balance":      metadata.Balance,
			"usage":        metadata.Usage,
			"last_used":    metadata.LastUsed,
			"import_batch": metadata.ImportBatch,
			"note":         metadata.Note,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"channel_id":     channel.Id,
			"channel_name":   channel.Name,
			"is_multi_key":   channel.MultiKeyInfo.IsMultiKey,
			"selection_mode": channel.MultiKeyInfo.KeySelectionMode,
			"total_keys":     len(keys),
			"keys":           keyDetails,
			"total_count":    totalCount,
			"page":           page,
			"page_size":      pageSize,
			"total_pages":    (totalCount + pageSize - 1) / pageSize,
		},
	})
}

// BatchImportChannelKeys 批量导入渠道Keys
func BatchImportChannelKeys(c *gin.Context) {
	type ImportRequest struct {
		ChannelId int      `json:"channel_id" binding:"required"`
		Keys      []string `json:"keys" binding:"required"`
		Mode      int      `json:"mode"` // 0=覆盖, 1=追加
	}

	var req ImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	if len(req.Keys) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "No keys provided",
		})
		return
	}

	channel, err := model.GetChannelById(req.ChannelId, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Channel not found: " + err.Error(),
		})
		return
	}

	// 验证并清理Keys
	var validKeys []string
	for _, key := range req.Keys {
		if trimmedKey := strings.TrimSpace(key); trimmedKey != "" {
			validKeys = append(validKeys, trimmedKey)
		}
	}

	if len(validKeys) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "No valid keys provided",
		})
		return
	}

	// 执行批量导入
	mode := model.BatchImportMode(req.Mode)
	err = channel.BatchImportKeys(validKeys, mode)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Failed to import keys: " + err.Error(),
		})
		return
	}

	logger.SysLog(fmt.Sprintf("User imported %d keys to channel %d with mode %d",
		len(validKeys), req.ChannelId, req.Mode))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Successfully imported %d keys", len(validKeys)),
		"data": gin.H{
			"imported_count": len(validKeys),
			"mode":           req.Mode,
		},
	})
}

// ToggleChannelKey 切换单个Key的状态
func ToggleChannelKey(c *gin.Context) {
	type ToggleRequest struct {
		ChannelId int  `json:"channel_id" binding:"required"`
		KeyIndex  *int `json:"key_index" binding:"required"`
		Enabled   bool `json:"enabled"`
	}

	var req ToggleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	channel, err := model.GetChannelById(req.ChannelId, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Channel not found: " + err.Error(),
		})
		return
	}

	if req.KeyIndex == nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Key index is required",
		})
		return
	}

	err = channel.ToggleKeyStatus(*req.KeyIndex, req.Enabled)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Failed to toggle key status: " + err.Error(),
		})
		return
	}

	action := "disabled"
	if req.Enabled {
		action = "enabled"
	}

	logger.SysLog(fmt.Sprintf("Key %d in channel %d %s",
		*req.KeyIndex, req.ChannelId, action))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Key %s successfully", action),
	})
}

// BatchToggleChannelKeys 批量切换Keys状态
func BatchToggleChannelKeys(c *gin.Context) {
	type BatchToggleRequest struct {
		ChannelId  int   `json:"channel_id" binding:"required"`
		KeyIndices []int `json:"key_indices" binding:"required"`
		Enabled    bool  `json:"enabled"`
	}

	var req BatchToggleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	if len(req.KeyIndices) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "No key indices provided",
		})
		return
	}

	channel, err := model.GetChannelById(req.ChannelId, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Channel not found: " + err.Error(),
		})
		return
	}

	err = channel.BatchToggleKeyStatus(req.KeyIndices, req.Enabled)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Failed to batch toggle keys: " + err.Error(),
		})
		return
	}

	action := "disabled"
	if req.Enabled {
		action = "enabled"
	}

	logger.SysLog(fmt.Sprintf("Batch %s %d keys in channel %d",
		action, len(req.KeyIndices), req.ChannelId))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Successfully %s %d keys", action, len(req.KeyIndices)),
	})
}

// ToggleChannelKeysByBatch 按批次切换Keys状态
func ToggleChannelKeysByBatch(c *gin.Context) {
	type BatchToggleRequest struct {
		ChannelId int    `json:"channel_id" binding:"required"`
		BatchId   string `json:"batch_id" binding:"required"`
		Enabled   bool   `json:"enabled"`
	}

	var req BatchToggleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	channel, err := model.GetChannelById(req.ChannelId, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Channel not found: " + err.Error(),
		})
		return
	}

	err = channel.ToggleKeysByBatch(req.BatchId, req.Enabled)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Failed to toggle keys by batch: " + err.Error(),
		})
		return
	}

	action := "disabled"
	if req.Enabled {
		action = "enabled"
	}

	logger.SysLog(fmt.Sprintf("Batch %s keys with batch_id %s in channel %d",
		action, req.BatchId, req.ChannelId))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Successfully %s keys in batch %s", action, req.BatchId),
	})
}

// UpdateChannelMultiKeySettings 更新渠道多Key设置
func UpdateChannelMultiKeySettings(c *gin.Context) {
	type SettingsRequest struct {
		ChannelId        int                    `json:"channel_id" binding:"required"`
		IsMultiKey       bool                   `json:"is_multi_key"`
		KeySelectionMode model.KeySelectionMode `json:"key_selection_mode"`
	}

	var req SettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	channel, err := model.GetChannelById(req.ChannelId, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Channel not found: " + err.Error(),
		})
		return
	}

	// 更新多Key设置
	channel.MultiKeyInfo.IsMultiKey = req.IsMultiKey
	channel.MultiKeyInfo.KeySelectionMode = req.KeySelectionMode

	// 如果禁用多Key模式，重置相关设置
	if !req.IsMultiKey {
		channel.MultiKeyInfo.KeyStatusList = make(map[int]int)
		channel.MultiKeyInfo.KeyMetadata = make(map[int]model.KeyMetadata)
		channel.MultiKeyInfo.PollingIndex = 0
	}

	err = channel.Update()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Failed to update channel settings: " + err.Error(),
		})
		return
	}

	logger.SysLog(fmt.Sprintf("Updated multi-key settings for channel %d: multi_key=%v, mode=%d",
		req.ChannelId, req.IsMultiKey, req.KeySelectionMode))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Channel settings updated successfully",
		"data": gin.H{
			"is_multi_key":       channel.MultiKeyInfo.IsMultiKey,
			"key_selection_mode": channel.MultiKeyInfo.KeySelectionMode,
		},
	})
}

// RetryChannelKey 手动重试特定渠道的Key
func RetryChannelKey(c *gin.Context) {
	type RetryRequest struct {
		ChannelId int  `json:"channel_id" binding:"required"`
		KeyIndex  *int `json:"key_index" binding:"required"`
	}

	var req RetryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	channel, err := model.GetChannelById(req.ChannelId, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Channel not found: " + err.Error(),
		})
		return
	}

	if !channel.MultiKeyInfo.IsMultiKey {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "This channel is not in multi-key mode",
		})
		return
	}

	if req.KeyIndex == nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Key index is required",
		})
		return
	}

	// 重新启用该Key
	err = channel.ToggleKeyStatus(*req.KeyIndex, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Failed to enable key: " + err.Error(),
		})
		return
	}

	logger.SysLog(fmt.Sprintf("Manually retried key %d in channel %d",
		*req.KeyIndex, req.ChannelId))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Key enabled and ready for retry",
	})
}

// GetChannelKeyHealthStatus 获取渠道Key健康状态
func GetChannelKeyHealthStatus(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Invalid channel id",
		})
		return
	}

	channel, err := model.GetChannelById(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Channel not found: " + err.Error(),
		})
		return
	}

	if !channel.MultiKeyInfo.IsMultiKey {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data": gin.H{
				"is_multi_key": false,
				"channel_health": map[string]interface{}{
					"status": channel.Status,
					"health": "single_key_mode",
				},
			},
		})
		return
	}

	keys := channel.ParseKeys()
	healthStatus := make([]gin.H, len(keys))

	enabledCount := 0
	disabledCount := 0
	autoDisabledCount := 0

	for i, key := range keys {
		status := channel.GetKeyStatus(i)
		var statusText string
		switch status {
		case 1:
			statusText = "已启用"
			enabledCount++
		case 2:
			statusText = "手动禁用"
			disabledCount++
		case 3:
			statusText = "自动禁用"
			autoDisabledCount++
		default:
			statusText = "未知状态"
		}

		// 获取Key元数据
		metadata := model.KeyMetadata{}
		if channel.MultiKeyInfo.KeyMetadata != nil {
			if meta, exists := channel.MultiKeyInfo.KeyMetadata[i]; exists {
				metadata = meta
			}
		}

		// 脱敏Key
		maskedKey := key
		if len(key) > 8 {
			maskedKey = key[:4] + "***" + key[len(key)-4:]
		}

		healthStatus[i] = gin.H{
			"index":        i,
			"key":          maskedKey,
			"status":       status,
			"status_text":  statusText,
			"usage":        metadata.Usage,
			"last_used":    metadata.LastUsed,
			"health_score": calculateKeyHealthScore(metadata, status),
		}
	}

	// 计算整体健康状态
	totalKeys := len(keys)
	healthyRatio := float64(enabledCount) / float64(totalKeys)

	var overallHealth string
	if healthyRatio >= 0.8 {
		overallHealth = "excellent"
	} else if healthyRatio >= 0.6 {
		overallHealth = "good"
	} else if healthyRatio >= 0.4 {
		overallHealth = "fair"
	} else if healthyRatio > 0 {
		overallHealth = "poor"
	} else {
		overallHealth = "critical"
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"channel_id":         channel.Id,
			"channel_name":       channel.Name,
			"is_multi_key":       true,
			"selection_mode":     channel.MultiKeyInfo.KeySelectionMode,
			"total_keys":         totalKeys,
			"enabled_keys":       enabledCount,
			"disabled_keys":      disabledCount,
			"auto_disabled_keys": autoDisabledCount,
			"healthy_ratio":      healthyRatio,
			"overall_health":     overallHealth,
			"keys_health":        healthStatus,
		},
	})
}

// calculateKeyHealthScore 计算Key的健康分数
func calculateKeyHealthScore(metadata model.KeyMetadata, status int) int {
	if status != 1 { // 如果不是启用状态
		return 0
	}

	score := 100

	// 根据使用频率调整分数（使用次数过多可能意味着负载过重）
	if metadata.Usage > 10000 {
		score -= 20
	} else if metadata.Usage > 5000 {
		score -= 10
	}

	// 根据最后使用时间调整分数（长时间未使用的Key可能有问题）
	if metadata.LastUsed > 0 {
		daysSinceLastUse := (time.Now().Unix() - metadata.LastUsed) / 86400
		if daysSinceLastUse > 7 {
			score -= 30
		} else if daysSinceLastUse > 3 {
			score -= 15
		}
	}

	if score < 0 {
		score = 0
	}

	return score
}

// FixMultiKeyChannelStatus 修复多密钥渠道的状态初始化问题
func FixMultiKeyChannelStatus(c *gin.Context) {
	var params struct {
		Id int `json:"id"`
	}
	if err := c.ShouldBindJSON(&params); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无效的参数",
		})
		return
	}

	channel, err := model.GetChannelById(params.Id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "渠道不存在",
		})
		return
	}

	if !channel.MultiKeyInfo.IsMultiKey {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "该渠道不是多密钥聚合渠道",
		})
		return
	}

	err = channel.FixMultiKeyStatus()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "多密钥状态修复成功",
	})
}

func DeleteDisabledKeys(c *gin.Context) {
	var params struct {
		Id int `json:"id"`
	}
	if err := c.ShouldBindJSON(&params); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无效的参数",
		})
		return
	}

	channel, err := model.GetChannelById(params.Id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	if !channel.MultiKeyInfo.IsMultiKey {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "该渠道不是多密钥渠道",
		})
		return
	}

	err = channel.DeleteDisabledKeys()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "成功删除所有禁用密钥",
	})
}
