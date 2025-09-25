package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/util"
)

// ========================================
// Runway API Direct Relay Controller
// ========================================
//
// 此文件包含 Runway API 的直接代理功能，主要包括：
// 1. DirectRelayRunway: 处理 Runway API 的创建请求
// 2. GetRunwayResult: 处理 Runway API 的查询请求
// 3. 成功响应处理和计费逻辑
// 4. 数据库状态同步功能

// ========================================
// 主要 API 处理函数
// ========================================

// DirectRelayRunway 处理 Runway API 的创建请求
// 功能：
// 1. 透传请求到 Runway API
// 2. 根据响应类型给任务 ID 添加前缀
// 3. 创建数据库记录并执行计费
func DirectRelayRunway(c *gin.Context, meta *util.RelayMeta) {
	// 获取channel信息，使用channel.Key而不是meta.APIKey
	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取渠道信息失败: " + err.Error()})
		return
	}

	if channel.Key == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "渠道密钥为空"})
		return
	}

	// 构建完整的请求URL，去掉路径中的"runway"部分
	requestUrl := c.Request.URL.Path
	// 移除路径中的"/runway"部分
	requestUrl = strings.Replace(requestUrl, "/runway", "", 1)
	fullRequestUrl := fmt.Sprintf("%s%s", meta.BaseURL, requestUrl)

	// 读取请求体以便后续判断mode类型
	requestBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取请求体失败: " + err.Error()})
		return
	}

	// 创建新的HTTP请求，透传原始请求体
	req, err := http.NewRequest(c.Request.Method, fullRequestUrl, strings.NewReader(string(requestBody)))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建请求失败: " + err.Error()})
		return
	}

	// 复制重要的请求头
	req.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))

	// 使用channel.Key设置Authorization header
	req.Header.Set("Authorization", "Bearer "+channel.Key)

	req.Header.Set("Accept", c.Request.Header.Get("Accept"))
	req.Header.Set("X-Runway-Version", "2024-11-06")

	// 如果有Content-Length，也要设置
	if contentLength := c.Request.Header.Get("Content-Length"); contentLength != "" {
		req.Header.Set("Content-Length", contentLength)
	}

	// 发送请求
	resp, err := util.HTTPClient.Do(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "请求失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	// 读取响应体
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取响应失败: " + err.Error()})
		return
	}

	// 如果响应状态码是200，执行日志记录并修改响应体
	if resp.StatusCode == 200 {
		// fmt.Printf("DEBUG: 响应状态码200，开始处理\n")
		// 解析响应体获取ID
		var responseData map[string]interface{}
		if err := json.Unmarshal(responseBody, &responseData); err == nil {
			// fmt.Printf("DEBUG: 响应体解析成功\n")
			if originalTaskId, ok := responseData["id"].(string); ok {
				// 判断mode类型
				mode := determineVideoMode(string(requestBody))
				// fmt.Printf("DEBUG: 响应解析成功，originalTaskId=%s, mode=%s\n", originalTaskId, mode)

				// 根据mode在id前面加上前缀
				var modifiedTaskId string
				if mode == "texttoimage" {
					modifiedTaskId = "image-" + originalTaskId
				} else {
					modifiedTaskId = "video-" + originalTaskId
				}

				// 修改响应体中的id
				responseData["id"] = modifiedTaskId

				// 重新序列化响应体
				modifiedResponseBody, err := json.Marshal(responseData)
				if err == nil {
					responseBody = modifiedResponseBody
				}

				// 根据模式类型选择不同的日志记录方式，使用修改后的id
				if mode == "texttoimage" {
					// fmt.Printf("DEBUG: 进入图像扣费分支\n")
					// 如果是图像类型，调用图像日志记录
					quota := calculateRunwayQuota(string(requestBody))
					err := handleRunwayImageBilling(c, meta, meta.OriginModelName, mode, 1, quota)
					if err != nil {
						fmt.Printf("处理Runway图像任务扣费失败: %v\n", err)
					} else {
						// fmt.Printf("DEBUG: 图像扣费成功，quota=%d\n", quota)
					}

					err = CreateImageLog("runway", modifiedTaskId, meta, "success", "", mode, 1, quota)
					if err != nil {
						fmt.Printf("创建图像日志失败: %v\n", err)
					} else {
						// fmt.Printf("DEBUG: 图像日志创建成功\n")
					}
				} else {
					// fmt.Printf("DEBUG: 进入视频扣费分支，mode=%s\n", mode)
					// 如果是视频类型，调用视频日志记录
					// 解析请求体获取duration
					duration := extractDurationFromRequest(string(requestBody))

					// 计算quota
					quota := calculateRunwayQuota(string(requestBody))
					// fmt.Printf("DEBUG: 视频参数 - duration=%s, quota=%d, modifiedTaskId=%s\n", duration, quota, modifiedTaskId)
					err := handleRunwayVideoBilling(c, meta, meta.OriginModelName, mode, duration, quota, modifiedTaskId)
					if err != nil {
						fmt.Printf("处理Runway视频任务扣费失败: %v\n", err)
					} else {
						// fmt.Printf("DEBUG: 视频扣费成功，quota=%d\n", quota)
					}
					// 创建视频日志
					err = CreateVideoLog("runway", modifiedTaskId, meta, mode, duration, mode, modifiedTaskId, quota)
					if err != nil {
						fmt.Printf("创建视频日志失败: %v\n", err)
					} else {
						// fmt.Printf("DEBUG: 视频日志创建成功\n")
					}
				}
			} else {
				// fmt.Printf("DEBUG: 未找到响应中的id字段\n")
			}
		} else {
			// fmt.Printf("DEBUG: 响应体解析失败: %v\n", err)
		}
	} else {
		// fmt.Printf("DEBUG: 响应状态码不是200，是%d\n", resp.StatusCode)
	}

	// 复制响应头（跳过Content-Length，让Gin自动计算）
	for key, values := range resp.Header {
		// 跳过Content-Length，因为响应体可能被修改了
		if strings.ToLower(key) == "content-length" {
			continue
		}
		for _, value := range values {
			c.Writer.Header().Add(key, value)
		}
	}

	// 注意：不手动设置Content-Length，让Gin的c.Data()自动处理
	// 记录响应体大小用于调试
	ctx := c.Request.Context()
	logger.Debugf(ctx, "DirectVideo response body size: %d bytes", len(responseBody))

	// 使用c.Data()让Gin自动处理Content-Length和状态码
	c.Data(resp.StatusCode, c.Writer.Header().Get("Content-Type"), responseBody)
}

// ========================================
// 辅助工具函数
// ========================================

// determineVideoMode 根据请求体判断视频模式
// 支持的模式：texttoimage, imagetovideo, texttovideo, upscalevideo
func determineVideoMode(requestBody string) string {
	var requestData map[string]interface{}
	if err := json.Unmarshal([]byte(requestBody), &requestData); err != nil {
		// 如果解析失败，使用字符串匹配
		if strings.Contains(requestBody, `"promptImage"`) {
			return "imagetovideo"
		}
		if strings.Contains(requestBody, `"videoUri"`) {
			return "videotovideo"
		}
		return "upscalevideo"
	}

	// 检查model字段
	if model, ok := requestData["model"].(string); ok {
		if model == "gen4_image" {
			return "texttoimage"
		}
		if model == "gen4_aleph" {
			return "videotovideo"
		}
		if model == "act_two" {
			return "act_two"
		}
	}

	// 检查是否包含 promptImage
	if _, hasPromptImage := requestData["promptImage"]; hasPromptImage {
		return "imagetovideo"
	}

	// 如果都没有，默认为upscalevideo
	return "upscalevideo"
}

// extractDurationFromRequest 从请求体中提取duration字段
func extractDurationFromRequest(requestBody string) string {
	var requestData map[string]interface{}
	if err := json.Unmarshal([]byte(requestBody), &requestData); err != nil {
		return ""
	}

	if duration, ok := requestData["duration"]; ok {
		return fmt.Sprintf("%v", duration)
	}

	return "10"
}

// calculateRunwayQuota 计算Runway API的quota
func calculateRunwayQuota(requestBody string) int64 {
	var requestData map[string]interface{}
	if err := json.Unmarshal([]byte(requestBody), &requestData); err != nil {
		return 0
	}

	// 获取model和duration
	model, modelOk := requestData["model"].(string)
	duration, durationOk := requestData["duration"]

	if !modelOk {
		return 0
	}

	// 根据模型类型计算credits
	switch model {
	case "gen4_image":
		// 图像生成根据分辨率计费
		return calculateImageCredits(requestData) * 500000 / 100
	case "gen4_turbo", "gen3a_turbo", "act_two":
		// 视频生成模型：5 credits per second
		durationSeconds := getDurationSeconds(duration, durationOk)
		return int64(0.05 * float64(durationSeconds) * 500000)
	case "gen4_aleph":
		// 视频生成模型：15 credits per second
		durationSeconds := getDurationSeconds(duration, durationOk)
		return int64(0.15 * float64(durationSeconds) * 500000)
	case "upscale_v1":
		// 视频upscale：2 credits per second
		durationSeconds := getDurationSeconds(duration, durationOk)
		return int64(0.02 * float64(durationSeconds) * 500000)
	default:
		// 默认视频模型：5 credits per second
		durationSeconds := getDurationSeconds(duration, durationOk)
		return int64(0.05 * float64(durationSeconds) * 500000)
	}
}

// calculateImageCredits 计算图像生成的credits
func calculateImageCredits(requestData map[string]interface{}) int64 {
	// 获取ratio字段
	ratio, ok := requestData["ratio"].(string)
	if !ok {
		// 默认720p，5 credits
		return 5
	}

	// 解析分辨率字符串，如 "1920:1080"
	var width, height int
	if _, err := fmt.Sscanf(ratio, "%d:%d", &width, &height); err != nil {
		// 解析失败，默认720p
		return 5
	}

	// 计算像素总数
	totalPixels := width * height

	// 根据像素数判断分辨率级别
	// 1080p (1920x1080 = 2,073,600) → 8 credits
	// 720p (1280x720 = 921,600) → 5 credits
	if totalPixels >= 1500000 { // 超过150万像素认为是1080p级别
		return 8
	}
	return 5
}

// getDurationSeconds 获取duration秒数
func getDurationSeconds(duration interface{}, durationOk bool) float64 {
	if !durationOk {
		return 10.0 // 默认10秒
	}

	switch d := duration.(type) {
	case float64:
		return d
	case int:
		return float64(d)
	default:
		return 10.0
	}
}

// GetRunwayResult 处理 Runway API 的任务状态查询请求
// 功能：
// 1. 解析带前缀的任务 ID
// 2. 查询对应的数据库记录
// 3. 向 Runway API 获取最新状态
// 4. 更新数据库状态并透传响应
func GetRunwayResult(c *gin.Context, taskId string) {
	// 检查taskId是否有前缀，如果有则移除前缀获取原始ID
	var originalTaskId string
	var isImageTask bool
	var channelId int

	if strings.HasPrefix(taskId, "image") {
		originalTaskId = strings.TrimPrefix(taskId, "image-")
		isImageTask = true
	} else if strings.HasPrefix(taskId, "video") {
		originalTaskId = strings.TrimPrefix(taskId, "video-")
		isImageTask = false
	} else {
		// 如果没有前缀，直接使用原始ID（向后兼容）
		originalTaskId = taskId
		isImageTask = false // 默认为视频任务
	}

	// 根据任务类型查询不同的数据库表获取channelId
	if isImageTask {
		// 查询图像任务
		task, err := dbmodel.GetImageByTaskId(taskId)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "图像任务不存在: " + err.Error()})
			return
		}
		channelId = task.ChannelId
	} else {
		// 查询视频任务
		task, err := dbmodel.GetVideoTaskById(taskId)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "视频任务不存在: " + err.Error()})
			return
		}
		channelId = task.ChannelId
	}

	// 获取channel信息
	channel, err := dbmodel.GetChannelById(channelId, true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取渠道信息失败: " + err.Error()})
		return
	}

	// 构建请求URL，使用原始taskId（不带前缀）
	fullRequestUrl := fmt.Sprintf("%s/v1/tasks/%s", channel.GetBaseURL(), originalTaskId)

	// 创建HTTP请求
	req, err := http.NewRequest("GET", fullRequestUrl, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建请求失败: " + err.Error()})
		return
	}

	// 设置请求头
	req.Header.Set("Authorization", "Bearer "+channel.Key)
	req.Header.Set("X-Runway-Version", "2024-11-06")
	req.Header.Set("Accept", "application/json")

	// 发送请求
	resp, err := util.HTTPClient.Do(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "请求失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	// 读取响应体
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取响应失败: " + err.Error()})
		return
	}

	// 解析响应体并更新数据库状态（不论状态码如何）
	var responseData map[string]interface{}
	if err := json.Unmarshal(responseBody, &responseData); err == nil {
		// 更新数据库状态
		updateTaskStatus(taskId, responseData, isImageTask)

		// 如果是成功响应，修改响应体中的taskId加上前缀
		if resp.StatusCode == 200 {
			if id, ok := responseData["id"].(string); ok && id == originalTaskId {
				if isImageTask {
					responseData["id"] = "image-" + originalTaskId
				} else {
					responseData["id"] = "video-" + originalTaskId
				}

				// 重新序列化响应体
				modifiedResponseBody, err := json.Marshal(responseData)
				if err == nil {
					responseBody = modifiedResponseBody
				}
			}
		}
	}

	// 复制响应头（跳过Content-Length，让Gin自动计算）
	for key, values := range resp.Header {
		// 跳过Content-Length，因为响应体可能被修改了
		if strings.ToLower(key) == "content-length" {
			continue
		}
		for _, value := range values {
			c.Writer.Header().Add(key, value)
		}
	}

	// 设置响应状态码
	c.Writer.WriteHeader(resp.StatusCode)

	// 透传响应体
	_, err = c.Writer.Write(responseBody)
	if err != nil {
		fmt.Printf("写入响应体失败: %v\n", err)
	}
}

// ========================================
// 数据库状态管理函数
// ========================================

// updateTaskStatus 更新任务状态到数据库
// 根据 Runway API 响应更新 status, failReason, storeUrl 等字段
// 只有当状态从非失败变为失败时才进行退款（防止重复退款）
func updateTaskStatus(taskId string, responseData map[string]interface{}, isImageTask bool) {
	// 解析响应数据
	status, _ := responseData["status"].(string)
	failure, _ := responseData["failure"].(string)
	failureCode, _ := responseData["failureCode"].(string)

	// 映射 Runway API 状态到数据库状态
	dbStatus := mapRunwayStatusToDbStatus(status)

	if isImageTask {
		// 更新图像任务
		task, err := dbmodel.GetImageByTaskId(taskId)
		if err != nil {
			fmt.Printf("获取图像任务失败: %v\n", err)
			return
		}

		// 记录原始状态用于退款判断
		oldStatus := task.Status

		// 更新状态
		task.Status = dbStatus

		// 如果失败，更新失败原因
		if status == "FAILED" {
			if failure != "" {
				task.FailReason = failure
			} else if failureCode != "" {
				task.FailReason = failureCode
			}
		} else {
			// 清除失败原因（如果状态不是失败）
			task.FailReason = ""
		}

		// 如果成功，更新输出URL
		if status == "SUCCEEDED" {
			if outputArray, ok := responseData["output"].([]interface{}); ok && len(outputArray) > 0 {
				if firstOutput, ok := outputArray[0].(string); ok {
					task.StoreUrl = firstOutput
				}
			}
		}

		// 检查是否需要退款：只有当状态从非失败变为失败时才退款
		needRefund := (oldStatus != "failed" && oldStatus != "cancelled") && (dbStatus == "failed" || dbStatus == "cancelled")

		// 保存到数据库
		err = dbmodel.DB.Model(&dbmodel.Image{}).Where("task_id = ?", taskId).Updates(task).Error
		if err != nil {
			fmt.Printf("更新图像任务状态失败: %v\n", err)
		} else if needRefund {
			// 如果需要退款，执行退款（图像任务配额需要从日志中获取）
			fmt.Printf("图像任务 %s 状态从 '%s' 变为 '%s'，开始退款\n", taskId, oldStatus, dbStatus)
			compensateRunwayImageTask(taskId)
		}

	} else {
		// 更新视频任务
		task, err := dbmodel.GetVideoTaskById(taskId)
		if err != nil {
			fmt.Printf("获取视频任务失败: %v\n", err)
			return
		}

		// 记录原始状态用于退款判断
		oldVideoStatus := task.Status

		// 更新状态
		task.Status = dbStatus

		// 如果失败，更新失败原因
		if status == "FAILED" {
			if failure != "" {
				task.FailReason = failure
			} else if failureCode != "" {
				task.FailReason = failureCode
			}
		} else {
			// 清除失败原因（如果状态不是失败）
			task.FailReason = ""
		}

		// 如果成功，更新输出URL
		if status == "SUCCEEDED" {
			if outputArray, ok := responseData["output"].([]interface{}); ok && len(outputArray) > 0 {
				if firstOutput, ok := outputArray[0].(string); ok {
					task.StoreUrl = firstOutput
				}
			}
		}

		// 检查是否需要退款：只有当状态从非失败变为失败时才退款
		needRefund := (oldVideoStatus != "failed" && oldVideoStatus != "cancelled") && (dbStatus == "failed" || dbStatus == "cancelled")

		// 保存到数据库
		err = task.Update()
		if err != nil {
			fmt.Printf("更新视频任务状态失败: %v\n", err)
		} else if needRefund && task.Quota > 0 {
			// 如果需要退款且有配额，执行退款
			fmt.Printf("视频任务 %s 状态从 '%s' 变为 '%s'，开始退款 quota=%d\n", taskId, oldVideoStatus, dbStatus, task.Quota)
			compensateRunwayVideoTask(taskId)
		}
	}
}

// mapRunwayStatusToDbStatus 映射 Runway API 状态到数据库状态
func mapRunwayStatusToDbStatus(runwayStatus string) string {
	switch runwayStatus {
	case "PENDING":
		return "pending"
	case "RUNNING":
		return "running"
	case "SUCCEEDED":
		return "succeeded"
	case "FAILED":
		return "failed"
	case "CANCELLED":
		return "cancelled"
	case "THROTTLED":
		return "throttled"
	default:
		return runwayStatus // 保持原状态
	}
}

// ========================================
// 退款补偿函数
// ========================================

// compensateRunwayImageTask 补偿图像任务失败的配额
// 优化：使用时间窗口查询避免全文搜索，提高性能
func compensateRunwayImageTask(taskId string) {
	task, err := dbmodel.GetImageByTaskId(taskId)
	if err != nil {
		fmt.Printf("获取图像任务失败，无法退款: %v\n", err)
		return
	}

	// 优化查询：基于任务创建时间的时间窗口查询，避免LIKE全文搜索
	// 查找任务创建时间前后1小时内的消费日志（图像任务通常立即完成计费）
	timeWindow := int64(3600) // 1小时
	startTime := task.CreatedAt - timeWindow
	endTime := task.CreatedAt + timeWindow

	var logEntry dbmodel.Log
	result := dbmodel.DB.Where("type = ?", 0). // 0表示消费记录
							Where("user_id = ?", task.UserId).
							Where("channel_id = ?", task.ChannelId).
							Where("model_name = ?", task.Model). // 使用模型名精确匹配
							Where("created_at BETWEEN ? AND ?", startTime, endTime).
							Where("content LIKE ?", "%"+task.Provider+"%"). // 只搜索Provider避免全taskId搜索
							Order("created_at DESC").
							First(&logEntry)

	if result.Error != nil {
		fmt.Printf("未找到图像任务 %s 的消费日志（时间窗口: %d-%d），无法确定退款金额: %v\n",
			taskId, startTime, endTime, result.Error)

		// 如果找不到，使用默认配额（基于模式计算）
		defaultQuota := calculateDefaultImageQuota(task.Mode)
		if defaultQuota > 0 {
			fmt.Printf("使用默认图像配额 %d 进行退款\n", defaultQuota)
			compensateWithQuota(task.UserId, task.ChannelId, defaultQuota, taskId)
		}
		return
	}

	quota := logEntry.Quota
	if quota <= 0 {
		fmt.Printf("图像任务 %s 配额为0，无需退款\n", taskId)
		return
	}

	fmt.Printf("开始补偿用户 %d，失败任务 %s，配额 %d\n", task.UserId, taskId, quota)
	compensateWithQuota(task.UserId, task.ChannelId, int64(quota), taskId)
}

// calculateDefaultImageQuota 计算图像任务的默认配额
func calculateDefaultImageQuota(mode string) int64 {
	// 基于模式提供默认配额，避免用户损失
	switch mode {
	case "texttoimage":
		return 250000 // 默认0.05美元 (5 credits)
	default:
		return 250000 // 默认值
	}
}

// compensateWithQuota 执行配额补偿的通用函数
func compensateWithQuota(userId int, channelId int, quota int64, taskId string) {
	// 1. 补偿用户配额
	err := dbmodel.CompensateVideoTaskQuota(userId, quota)
	if err != nil {
		fmt.Printf("补偿用户配额失败，任务 %s: %v\n", taskId, err)
		return
	}
	fmt.Printf("成功补偿用户 %d 配额，任务 %s\n", userId, taskId)

	// 2. 补偿渠道配额
	err = dbmodel.CompensateChannelQuota(channelId, quota)
	if err != nil {
		fmt.Printf("补偿渠道配额失败，任务 %s: %v\n", taskId, err)
	} else {
		fmt.Printf("成功补偿渠道 %d 配额，任务 %s\n", channelId, taskId)
	}
}

// compensateRunwayVideoTask 补偿视频任务失败的配额
func compensateRunwayVideoTask(taskId string) {
	task, err := dbmodel.GetVideoTaskById(taskId)
	if err != nil {
		fmt.Printf("获取视频任务失败，无法退款: %v\n", err)
		return
	}

	if task.Quota <= 0 {
		fmt.Printf("视频任务 %s 配额为0，无需退款\n", taskId)
		return
	}

	fmt.Printf("开始补偿用户 %d，失败任务 %s，配额 %d\n", task.UserId, taskId, task.Quota)

	// 1. 补偿用户配额
	err = dbmodel.CompensateVideoTaskQuota(task.UserId, task.Quota)
	if err != nil {
		fmt.Printf("补偿用户配额失败，任务 %s: %v\n", taskId, err)
		return
	}
	fmt.Printf("成功补偿用户 %d 配额，任务 %s\n", task.UserId, taskId)

	// 2. 补偿渠道配额
	err = dbmodel.CompensateChannelQuota(task.ChannelId, task.Quota)
	if err != nil {
		fmt.Printf("补偿渠道配额失败，任务 %s: %v\n", taskId, err)
	} else {
		fmt.Printf("成功补偿渠道 %d 配额，任务 %s\n", task.ChannelId, taskId)
	}
}

// ========================================
// 成功响应处理和计费函数
// ========================================

// handleRunwayImageBilling 处理Runway图像任务扣费
// 参照 handleSuccessfulResponseImage 实现
func handleRunwayImageBilling(c *gin.Context, meta *util.RelayMeta, modelName string, mode string, n int, quota int64) error {

	// 获取请求相关信息
	referer := c.Request.Header.Get("HTTP-Referer")
	title := c.Request.Header.Get("X-Title")

	// 扣除token配额
	err := dbmodel.PostConsumeTokenQuota(meta.TokenId, quota)
	if err != nil {
		return fmt.Errorf("扣除token配额失败: %v", err)
	}

	// 更新用户配额缓存
	err = dbmodel.CacheUpdateUserQuota(context.Background(), meta.UserId)
	if err != nil {
		fmt.Printf("更新用户配额缓存失败: %v\n", err)
	}

	if quota != 0 {
		tokenName := c.GetString("token_name")
		// 记录详细的扣费日志
		logContent := fmt.Sprintf("Runway Image Generation  model: %s, mode: %s, image count: %d, total cost: $%.6f", modelName, mode, n, float64(quota)/5000000)
		dbmodel.RecordConsumeLog(context.Background(), meta.UserId, meta.ChannelId, 0, 0, modelName, tokenName, quota, logContent, 0, title, referer, false, 0.0)
		dbmodel.UpdateUserUsedQuotaAndRequestCount(meta.UserId, quota)
		channelId := c.GetInt("channel_id")
		dbmodel.UpdateChannelUsedQuota(channelId, quota)
	}

	return nil
}

// handleRunwayVideoBilling 处理Runway视频任务扣费
// 参照 handleSuccessfulResponseWithQuota 实现
func handleRunwayVideoBilling(c *gin.Context, meta *util.RelayMeta, modelName string, mode string, duration string, quota int64, taskId string) error {
	// 获取请求相关信息
	referer := c.Request.Header.Get("HTTP-Referer")
	title := c.Request.Header.Get("X-Title")

	// 扣除token配额
	err := dbmodel.PostConsumeTokenQuota(meta.TokenId, quota)
	if err != nil {
		return fmt.Errorf("扣除token配额失败: %v", err)
	}

	// 更新用户配额缓存
	err = dbmodel.CacheUpdateUserQuota(context.Background(), meta.UserId)
	if err != nil {
		fmt.Printf("更新用户配额缓存失败: %v\n", err)
	}

	if quota != 0 {
		tokenName := c.GetString("token_name")
		logContent := fmt.Sprintf("Runway Video Generation   model: %s, mode: %s, duration: %s, total cost: $%.6f", modelName, mode, duration, float64(quota)/500000)

		// 使用视频消费日志记录（包含taskId）
		dbmodel.RecordVideoConsumeLog(context.Background(), meta.UserId, meta.ChannelId, 0, 0, modelName, tokenName, quota, logContent, 0, title, referer, taskId)
		dbmodel.UpdateUserUsedQuotaAndRequestCount(meta.UserId, quota)
		channelId := c.GetInt("channel_id")
		dbmodel.UpdateChannelUsedQuota(channelId, quota)
	}

	return nil
}

// ========================================
// 文件结构说明
// ========================================
//
// 此文件的主要组织结构：
//
// 1. 主要 API 处理函数
//    - DirectRelayRunway: Runway API 创建请求处理
//    - GetRunwayResult: Runway API 查询请求处理
//
// 2. 辅助工具函数
//    - determineVideoMode: 判断请求模式
//    - extractDurationFromRequest: 提取时长参数
//    - calculateRunwayQuota: 计算配额
//    - calculateImageCredits: 计算图像积分
//    - getDurationSeconds: 获取时长秒数
//
// 3. 数据库状态管理函数
//    - updateTaskStatus: 更新任务状态（包含退款逻辑）
//    - mapRunwayStatusToDbStatus: 状态映射
//
// 4. 退款补偿函数
//    - compensateRunwayImageTask: 图像任务失败补偿
//    - compensateRunwayVideoTask: 视频任务失败补偿
//
// 5. 成功响应处理和计费函数
//    - handleRunwayImageBilling: 图像任务扣费处理
//    - handleRunwayVideoBilling: 视频任务扣费处理
