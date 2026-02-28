package flux

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

type Adaptor struct {
	ImageRecord *model.Image // 保存创建的图像记录
}

// Init 初始化适配器
func (a *Adaptor) Init(meta *util.RelayMeta) {
	// Flux 适配器不需要特殊初始化
}

// GetModelList 返回支持的模型列表
func (a *Adaptor) GetModelList() []string {
	return ModelList
}

// GetModelDetails 返回模型详情列表
func (a *Adaptor) GetModelDetails() []relaymodel.APIModel {
	models := make([]relaymodel.APIModel, 0, len(ModelList))
	for _, modelName := range ModelList {
		models = append(models, relaymodel.APIModel{
			Provider:    "flux",
			Name:        modelName,
			Tags:        []string{"image-generation"},
			Description: "Flux image generation model",
			PriceType:   "按量计费",
		})
	}
	return models
}

// GetChannelName 返回渠道名称
func (a *Adaptor) GetChannelName() string {
	return "flux"
}

// GetRequestURL 构建请求URL
func (a *Adaptor) GetRequestURL(meta *util.RelayMeta) (string, error) {
	// 移除路径中的 /flux 前缀
	path := strings.Replace(meta.RequestURLPath, "/flux", "", 1)

	// 如果路径中只有查询参数，需要提取干净的路径
	if idx := strings.Index(path, "?"); idx != -1 {
		path = path[:idx]
	}

	fullURL := meta.BaseURL + path
	return fullURL, nil
}

// SetupRequestHeader 设置请求头
// 接收客户端请求时兼容两种认证方式：
// 1. Authorization: Bearer <token> 或 Authorization: <token>
// 2. x-key: <api-key>
// 但向 Flux API 发送时统一使用官方的 x-key header
func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Request, meta *util.RelayMeta) error {
	req.Header.Set("Content-Type", "application/json")
	
	// 使用渠道配置的 APIKey（meta.APIKey 已经在中间件中提取并验证）
	// 统一使用官方的 x-key header 格式
	req.Header.Set("x-key", meta.APIKey)
	
	return nil
}

// ConvertRequest 实现标准接口（Flux 不使用此方法，使用自定义的 ConvertFluxRequest）
func (a *Adaptor) ConvertRequest(c *gin.Context, relayMode int, request *relaymodel.GeneralOpenAIRequest) (any, error) {
	return nil, fmt.Errorf("Flux 使用自定义请求处理流程")
}

// ConvertImageRequest 实现标准接口（Flux 不使用此方法）
func (a *Adaptor) ConvertImageRequest(request *relaymodel.ImageRequest) (any, error) {
	return nil, fmt.Errorf("Flux 使用自定义请求处理流程")
}

// ConvertFluxRequest Flux 专用的请求转换（移除不需要的字段，添加 webhook_url）
func (a *Adaptor) ConvertFluxRequest(c *gin.Context, meta *util.RelayMeta) ([]byte, error) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, fmt.Errorf("读取请求体失败: %w", err)
	}

	// 恢复请求体供后续使用
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	// 解析请求
	var requestMap map[string]any
	if err := json.Unmarshal(bodyBytes, &requestMap); err != nil {
		return nil, fmt.Errorf("解析请求体失败: %w", err)
	}

	// Flux API 不需要 model 参数（模型名已在 URL 中）
	delete(requestMap, "model")

	// 添加 webhook_url 用于回调
	if config.ServerAddress != "" {
		webhookURL := fmt.Sprintf("%s/flux/internal/callback", config.ServerAddress)
		requestMap["webhook_url"] = webhookURL
		logger.Debugf(c, "添加 Flux webhook_url: %s", webhookURL)
	}

	// 重新序列化
	modifiedBody, err := json.Marshal(requestMap)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	return modifiedBody, nil
}

// CreatePendingRecord 在发起请求前创建 pending 状态的记录
func (a *Adaptor) CreatePendingRecord(c *gin.Context, meta *util.RelayMeta) error {
	now := time.Now().Unix()

	imageRecord := &model.Image{
		TaskId:    "",
		Username:  meta.TokenName,
		ChannelId: meta.ChannelId,
		UserId:    meta.UserId,
		Model:     meta.OriginModelName,
		Status:    TaskStatusPending,
		Provider:  "flux",
		CreatedAt: now,
		UpdatedAt: now, // 创建时也设置 UpdatedAt
		Quota:     0,   // 初始配额为0，请求成功后更新
	}

	if err := imageRecord.Insert(); err != nil {
		logger.Errorf(c, "创建 Flux pending 记录失败: %v", err)
		return fmt.Errorf("创建数据库记录失败: %w", err)
	}

	// 保存记录引用，后续更新用
	a.ImageRecord = imageRecord

	logger.Infof(c, "创建 Flux pending 记录成功: id=%d, user_id=%d",
		imageRecord.Id, meta.UserId)

	return nil
}

// DoRequest 执行请求（透传）
func (a *Adaptor) DoRequest(c *gin.Context, meta *util.RelayMeta, requestBody io.Reader) (*http.Response, error) {
	// 移除路径中的 /flux 前缀
	path := strings.Replace(meta.RequestURLPath, "/flux", "", 1)

	// 如果路径中只有查询参数，需要提取干净的路径
	if idx := strings.Index(path, "?"); idx != -1 {
		path = path[:idx]
	}

	fullRequestURL := meta.BaseURL + path
	logger.Debugf(c, "Flux API request URL: %s", fullRequestURL)

	req, err := http.NewRequest(c.Request.Method, fullRequestURL, requestBody)
	if err != nil {
		return nil, err
	}

	if err := a.SetupRequestHeader(c, req, meta); err != nil {
		return nil, err
	}

	return util.HTTPClient.Do(req)
}

// DoResponse 处理响应并保存初始结果（不扣费，等待回调）
func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, meta *util.RelayMeta) (*relaymodel.Usage, *relaymodel.ErrorWithStatusCode) {
	defer resp.Body.Close()

	// 读取响应体
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		// 更新记录为失败状态
		a.updateRecordToFailed(c, fmt.Sprintf("读取响应失败: %v", err))
		return nil, &relaymodel.ErrorWithStatusCode{
			StatusCode: http.StatusInternalServerError,
			Error:      relaymodel.Error{Message: fmt.Sprintf("读取响应失败: %v", err)},
		}
	}

	// 如果响应不是200，处理错误
	if resp.StatusCode != http.StatusOK {
		logger.Errorf(c, "Flux API error: status %d, body: %s", resp.StatusCode, string(body))

		errorMessage := extractFluxErrorMessage(body, resp.StatusCode)

		a.updateRecordToFailed(c, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, errorMessage))
		c.Data(resp.StatusCode, "application/json", body)
		return nil, &relaymodel.ErrorWithStatusCode{
			StatusCode: resp.StatusCode,
			Error:      relaymodel.Error{Message: errorMessage},
		}
	}

	// 解析响应
	var fluxResp FluxResponse
	if err := json.Unmarshal(body, &fluxResp); err != nil {
		logger.Errorf(c, "解析 Flux 响应失败: %v, body: %s", err, string(body))
		a.updateRecordToFailed(c, fmt.Sprintf("解析响应失败: %v", err))
		// 即使解析失败，也要透传响应给客户端
		c.Data(resp.StatusCode, "application/json", body)
		return nil, &relaymodel.ErrorWithStatusCode{
			StatusCode: http.StatusInternalServerError,
			Error:      relaymodel.Error{Message: fmt.Sprintf("解析响应失败: %v", err)},
		}
	}

	// 检查是否有错误
	if fluxResp.Error != "" {
		logger.Errorf(c, "Flux API 返回错误: %s", fluxResp.Error)
		a.updateRecordToFailed(c, fluxResp.Error)
		c.Data(resp.StatusCode, "application/json", body)
		return nil, &relaymodel.ErrorWithStatusCode{
			StatusCode: http.StatusBadRequest,
			Error:      relaymodel.Error{Message: fluxResp.Error},
		}
	}

	// 计算配额
	groupRatio := 1.0
	quota := CalculateQuota(fluxResp.Cost, groupRatio)

	// 更新记录为成功状态
	if a.ImageRecord != nil {
		now := time.Now().Unix()
		duration := int(now - a.ImageRecord.CreatedAt) // 计算总时长（秒）

		a.ImageRecord.TaskId = fluxResp.ID // 用真实的 task_id 替换临时 id
		a.ImageRecord.Status = TaskStatusSubmitted
		a.ImageRecord.Quota = quota
		a.ImageRecord.TotalDuration = duration
		a.ImageRecord.Detail = fmt.Sprintf("cost=%.4f,input_mp=%.2f,output_mp=%.2f",
			fluxResp.Cost, fluxResp.InputMP, fluxResp.OutputMP)
		a.ImageRecord.Result = string(body) // 保存完整的响应 JSON

		if err := a.ImageRecord.Update(); err != nil {
			logger.Errorf(c, "更新 Flux 记录失败: %v", err)
			// 继续处理，不因数据库错误而中断
		} else {
			logger.Infof(c, "Flux 请求成功: task_id=%s, cost=%.4f cents, quota=%d, duration=%ds",
				fluxResp.ID, fluxResp.Cost, quota, duration)
		}
	}

	// 修改响应数据：删除 webhook_url，添加 polling_url
	var respMap map[string]any
	if err := json.Unmarshal(body, &respMap); err != nil {
		logger.Errorf(c, "解析响应为 map 失败: %v", err)
		// 如果解析失败，透传原始响应
		c.Data(resp.StatusCode, "application/json", body)
		return nil, nil
	}

	// 删除 webhook_url
	delete(respMap, "webhook_url")

	// 添加 polling_url
	if taskID, ok := respMap["id"].(string); ok && taskID != "" {
		pollingURL := fmt.Sprintf("https://api.bfl.ai/v1/get_result?id=%s", taskID)
		respMap["polling_url"] = pollingURL
		logger.Debugf(c, "添加 polling_url: %s", pollingURL)
	}

	// 重新序列化修改后的响应
	modifiedBody, err := json.Marshal(respMap)
	if err != nil {
		logger.Errorf(c, "序列化修改后的响应失败: %v", err)
		// 如果序列化失败，透传原始响应
		c.Data(resp.StatusCode, "application/json", body)
		return nil, nil
	}

	// 返回修改后的响应给客户端
	c.Data(resp.StatusCode, "application/json", modifiedBody)

	// Flux 不计算 token usage，返回 nil
	return nil, nil
}

// extractFluxErrorMessage 从 Flux API 错误响应中提取错误消息
// Flux API 可能返回多种格式: {"detail": "..."}, {"error": "..."}, {"message": "..."}
func extractFluxErrorMessage(body []byte, statusCode int) string {
	var errMap map[string]any
	if err := json.Unmarshal(body, &errMap); err == nil {
		if detail, ok := errMap["detail"].(string); ok && detail != "" {
			return detail
		}
		if errMsg, ok := errMap["error"].(string); ok && errMsg != "" {
			return errMsg
		}
		if msg, ok := errMap["message"].(string); ok && msg != "" {
			return msg
		}
	}
	return fmt.Sprintf("Flux API 返回错误状态: %d", statusCode)
}

// updateRecordToFailed 更新记录为失败状态
func (a *Adaptor) updateRecordToFailed(c *gin.Context, reason string) {
	if a.ImageRecord != nil {
		now := time.Now().Unix()
		duration := int(now - a.ImageRecord.CreatedAt) // 计算总时长（秒）

		a.ImageRecord.Status = TaskStatusFailed
		a.ImageRecord.FailReason = reason
		a.ImageRecord.TotalDuration = duration

		if err := a.ImageRecord.Update(); err != nil {
			logger.Errorf(c, "更新 Flux 失败记录失败: %v", err)
		}
	}
}

// HandleCallback 处理 Flux API 回调通知的业务逻辑
// 返回: (是否成功, HTTP状态码, 响应消息)
func HandleCallback(c *gin.Context, notification FluxCallbackNotification) (bool, int, string) {
	taskID := notification.TaskId
	logger.Infof(c, "Flux callback received: task_id=%s, status=%s, progress=%d", taskID, notification.Status, notification.Progress)
	logger.Debugf(c, "Flux callback notification: %+v", notification)

	// 查询任务记录
	image, err := model.GetImageByTaskId(taskID)
	if err != nil || image == nil {
		logger.Errorf(c, "Flux callback task not found: task_id=%s, error=%v", taskID, err)
		return false, http.StatusNotFound, "task not found"
	}

	// 防止重复处理
	currentStatus := image.Status
	if currentStatus == TaskStatusSucceed {
		logger.Infof(c, "Flux callback already processed: task_id=%s, status=%s", taskID, currentStatus)
		return true, http.StatusOK, "already processed"
	}

	// 更新 result 字段（保存完整的回调数据）
	callbackBytes, err := json.Marshal(notification)
	if err != nil {
		logger.Errorf(c, "Flux callback marshal error: %v", err)
		return false, http.StatusInternalServerError, "internal error"
	}
	image.Result = string(callbackBytes)

	// 计算总时长
	now := time.Now().Unix()
	image.TotalDuration = int(now - image.CreatedAt)

	// 处理回调结果（注意：Flux 使用 SUCCESS/FAILED 大写状态）
	normalizedStatus := strings.ToLower(notification.Status)
	if normalizedStatus == TaskStatusSucceed {
		return handleSuccessCallback(c, image, notification, taskID)
	} else if normalizedStatus == TaskStatusFailed {
		return handleFailedCallback(c, image, notification, taskID)
	} else {
		// 其他状态（processing等），更新状态但不扣费
		return handleProcessingCallback(c, image, notification, taskID)
	}
}

// handleSuccessCallback 处理成功回调
func handleSuccessCallback(c *gin.Context, image *model.Image, notification FluxCallbackNotification, taskID string) (bool, int, string) {
	image.Status = TaskStatusSucceed

	// 提取图片URL
	if notification.Result != nil && notification.Result.Sample != "" {
		image.StoreUrl = notification.Result.Sample
	}

	// 计算配额
	var quota int64
	if notification.Cost > 0 {
		// 如果回调返回了 cost，使用回调的 cost 重新计算
		group, err := model.CacheGetUserGroup(image.UserId)
		if err != nil {
			logger.Errorf(c, "Flux callback get user group failed: user_id=%d, error=%v", image.UserId, err)
			group = "Lv1" // 默认组
		}
		groupRatio := common.GetGroupRatio(group)
		quota = CalculateQuota(notification.Cost, groupRatio)
		image.Quota = quota

		// 更新详细信息
		image.Detail = fmt.Sprintf("cost=%.4f,input_mp=%.2f,output_mp=%.2f",
			notification.Cost, notification.InputMP, notification.OutputMP)
		logger.Infof(c, "Flux callback with cost: task_id=%s, cost=%.4f cents, quota=%d", taskID, notification.Cost, quota)
	} else {
		// 如果回调没有 cost，使用初始响应时已计算的 quota
		quota = image.Quota
		if quota == 0 {
			// 如果 quota 也为 0，使用预估价格
			logger.Warnf(c, "Flux callback has no cost and no saved quota: task_id=%s, using estimated quota", taskID)
			group, err := model.CacheGetUserGroup(image.UserId)
			if err != nil {
				logger.Errorf(c, "Flux callback get user group failed: user_id=%d, error=%v", image.UserId, err)
				group = "Lv1"
			}
			groupRatio := common.GetGroupRatio(group)
			quota = EstimateQuota(image.Model, groupRatio)
			image.Quota = quota
		}
		logger.Infof(c, "Flux callback without cost: task_id=%s, using saved quota=%d", taskID, quota)
	}

	// 【真正扣费】：回调成功时才扣费
	err := model.DecreaseUserQuota(image.UserId, quota)
	if err != nil {
		logger.Errorf(c, "Flux callback billing failed: user_id=%d, quota=%d, error=%v",
			image.UserId, quota, err)
		// 扣费失败不影响状态更新，继续处理
	} else {
		logger.Infof(c, "Flux callback billing success: user_id=%d, quota=%d, task_id=%s, duration=%ds",
			image.UserId, quota, taskID, image.TotalDuration)
	}

	if err = image.Update(); err != nil {
		logger.Errorf(c, "Flux callback update record failed: task_id=%s, error=%v", taskID, err)
		return false, http.StatusInternalServerError, "update failed"
	}

	return true, http.StatusOK, "success"
}

// handleFailedCallback 处理失败回调
func handleFailedCallback(c *gin.Context, image *model.Image, notification FluxCallbackNotification, taskID string) (bool, int, string) {
	image.Status = TaskStatusFailed
	image.FailReason = notification.Error
	if image.FailReason == "" {
		image.FailReason = "Flux API 任务失败"
	}

	logger.Infof(c, "Flux callback task failed: task_id=%s, reason=%s, duration=%ds",
		taskID, image.FailReason, image.TotalDuration)

	if err := image.Update(); err != nil {
		logger.Errorf(c, "Flux callback update failed record failed: task_id=%s, error=%v", taskID, err)
		return false, http.StatusInternalServerError, "update failed"
	}

	return true, http.StatusOK, "success"
}

// handleProcessingCallback 处理处理中状态回调
func handleProcessingCallback(c *gin.Context, image *model.Image, notification FluxCallbackNotification, taskID string) (bool, int, string) {
	image.Status = notification.Status
	logger.Infof(c, "Flux callback task status updated: task_id=%s, status=%s, duration=%ds",
		taskID, notification.Status, image.TotalDuration)

	if err := image.Update(); err != nil {
		logger.Errorf(c, "Flux callback update processing record failed: task_id=%s, error=%v", taskID, err)
		return false, http.StatusInternalServerError, "update failed"
	}

	return true, http.StatusOK, "success"
}

// QueryResult 查询 Flux 任务结果
// 返回: (HTTP状态码, 响应体, 错误)
func (a *Adaptor) QueryResult(c *gin.Context, taskID string, baseURL string, apiKey string) (int, []byte, error) {
	// 1. 构建查询 URL
	queryURL := fmt.Sprintf("%s/v1/get_result?id=%s", baseURL, taskID)
	logger.Debugf(c, "Flux 查询 URL: %s", queryURL)

	// 2. 创建 HTTP 请求
	req, err := http.NewRequest("GET", queryURL, nil)
	if err != nil {
		return http.StatusInternalServerError, nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 3. 设置请求头 - 使用官方的 x-key header
	req.Header.Set("x-key", apiKey)

	// 4. 发送请求
	resp, err := util.HTTPClient.Do(req)
	if err != nil {
		return http.StatusInternalServerError, nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 5. 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return http.StatusInternalServerError, nil, fmt.Errorf("读取响应失败: %w", err)
	}

	// 6. 记录响应日志
	logger.Debugf(c, "Flux 查询响应: status=%d, body=%s", resp.StatusCode, string(body))

	// 7. 返回状态码和响应体（透传）
	return resp.StatusCode, body, nil
}
