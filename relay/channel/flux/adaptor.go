package flux

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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

// isReplicate 根据 baseURL 判断是否为 Replicate 渠道
func isReplicate(baseURL string) bool {
	return strings.Contains(baseURL, "replicate.com")
}

// ValidateRequest 在创建 pending 记录前做前置校验（不产生 DB 副作用）
// P2-2: 不支持的 Replicate 模型应在任何 DB 操作前返回 400
func (a *Adaptor) ValidateRequest(meta *util.RelayMeta) error {
	if isReplicate(meta.BaseURL) {
		if _, ok := ReplicateModelMap[meta.OriginModelName]; !ok {
			return fmt.Errorf("模型 %s 在 Replicate 渠道暂不支持", meta.OriginModelName)
		}
	}
	return nil
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
// BFL 使用 x-key，Replicate 使用 Authorization: Bearer
func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Request, meta *util.RelayMeta) error {
	req.Header.Set("Content-Type", "application/json")

	if isReplicate(meta.BaseURL) {
		req.Header.Set("Authorization", "Bearer "+meta.APIKey)
	} else {
		req.Header.Set("x-key", meta.APIKey)
	}

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

// ConvertFluxRequest Flux 专用的请求转换
// BFL: 移除 model 字段，添加 webhook_url
// Replicate: 检查不支持模型，将参数包入 input，添加 webhook + webhook_events_filter
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

	// Replicate 渠道的特殊处理
	if isReplicate(meta.BaseURL) {
		// 将原始参数（除 model）包入 input 字段
		delete(requestMap, "model")
		input := make(map[string]any, len(requestMap))
		for k, v := range requestMap {
			input[k] = v
		}

		// output_format 归一化：Replicate 仅接受 webp/jpg/png；
		// 未传则不下发，传了但不在白名单则回落到 png。
		if raw, ok := input["output_format"]; ok {
			allowed := map[string]bool{"webp": true, "jpg": true, "png": true}
			format, isStr := raw.(string)
			if !isStr || !allowed[format] {
				logger.Infof(c, "Replicate output_format %v 不在白名单，回落为 png", raw)
				input["output_format"] = "png"
			}
		}

		replicateReq := map[string]any{
			"input": input,
		}

		if config.ServerAddress != "" {
			webhookURL := fmt.Sprintf("%s/flux/internal/replicate/callback", config.ServerAddress)
			replicateReq["webhook"] = webhookURL
			replicateReq["webhook_events_filter"] = []string{"completed"}
			logger.Debugf(c, "添加 Replicate webhook: %s", webhookURL)
		}

		return json.Marshal(replicateReq)
	}

	// BFL 渠道：移除 model 参数（模型名已在 URL 中），添加 webhook_url
	delete(requestMap, "model")

	if config.ServerAddress != "" {
		webhookURL := fmt.Sprintf("%s/flux/internal/callback", config.ServerAddress)
		requestMap["webhook_url"] = webhookURL
		logger.Debugf(c, "添加 Flux webhook_url: %s", webhookURL)
	}

	modifiedBody, err := json.Marshal(requestMap)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	return modifiedBody, nil
}

// CreatePendingRecord 在客户端请求一进来就创建 pending 记录（含 request_id）。
// 重试链路每次进入 relayFluxHelper 都会创建一条新记录，便于按 request_id 聚合一次客户端调用的所有尝试。
// task_id 留空，待上游成功响应后通过 Update 回填。
func (a *Adaptor) CreatePendingRecord(c *gin.Context, meta *util.RelayMeta) error {
	now := time.Now().Unix()

	imageRecord := &model.Image{
		TaskId:    "",
		RequestId: c.GetString("X-Request-ID"),
		Username:  meta.TokenName,
		ChannelId: meta.ChannelId,
		UserId:    meta.UserId,
		Model:     meta.OriginModelName,
		Status:    TaskStatusPending,
		Provider:  "flux",
		CreatedAt: now,
		UpdatedAt: now,
		Quota:     0,
	}

	if err := imageRecord.Insert(); err != nil {
		logger.Errorf(c, "创建 Flux pending 记录失败: %v", err)
		return fmt.Errorf("创建数据库记录失败: %w", err)
	}

	a.ImageRecord = imageRecord

	logger.Infof(c, "创建 Flux pending 记录成功: id=%d, user_id=%d, request_id=%s",
		imageRecord.Id, meta.UserId, imageRecord.RequestId)

	return nil
}

// DoRequest 执行请求（BFL 透传，Replicate 使用独立方法）
func (a *Adaptor) DoRequest(c *gin.Context, meta *util.RelayMeta, requestBody io.Reader) (*http.Response, error) {
	if isReplicate(meta.BaseURL) {
		return a.doReplicateRequest(c, meta, requestBody)
	}

	// BFL 路径：移除 /flux 前缀，直接拼接 baseURL + path
	path := strings.Replace(meta.RequestURLPath, "/flux", "", 1)
	if idx := strings.Index(path, "?"); idx != -1 {
		path = path[:idx]
	}

	fullRequestURL := meta.BaseURL + path

	bodyBytes, err := io.ReadAll(requestBody)
	if err != nil {
		return nil, fmt.Errorf("read request body failed: %w", err)
	}
	logger.Infof(c, "BFL DoRequest: method=%s, url=%s, body=%s", c.Request.Method, fullRequestURL, string(bodyBytes))

	req, err := http.NewRequest(c.Request.Method, fullRequestURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	if err := a.SetupRequestHeader(c, req, meta); err != nil {
		return nil, err
	}

	return util.HTTPClient.Do(req)
}

// doReplicateRequest 向 Replicate 发起预测请求，使用 Prefer: wait=60 同步等待
func (a *Adaptor) doReplicateRequest(c *gin.Context, meta *util.RelayMeta, requestBody io.Reader) (*http.Response, error) {
	replicateID, ok := ReplicateModelMap[meta.OriginModelName]
	if !ok {
		return nil, fmt.Errorf("Replicate 渠道不支持模型: %s", meta.OriginModelName)
	}

	requestURL := fmt.Sprintf("%s/v1/models/%s/predictions", meta.BaseURL, replicateID)

	bodyBytes, err := io.ReadAll(requestBody)
	if err != nil {
		return nil, fmt.Errorf("read request body failed: %w", err)
	}
	logger.Infof(c, "Replicate doRequest: url=%s, body=%s", requestURL, string(bodyBytes))

	req, err := http.NewRequest("POST", requestURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	if err := a.SetupRequestHeader(c, req, meta); err != nil {
		return nil, err
	}

	// Prefer: wait=60 使 Replicate 同步等待最多 60 秒，超时返回 201（任务仍在处理）
	req.Header.Set("Prefer", "wait=60")

	return util.HTTPClient.Do(req)
}

// DoResponse 处理响应（BFL 和 Replicate 走不同分支）
func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, meta *util.RelayMeta) (*relaymodel.Usage, *relaymodel.ErrorWithStatusCode) {
	defer resp.Body.Close()

	if isReplicate(meta.BaseURL) {
		return a.doReplicateResponse(c, resp, meta)
	}

	// ── BFL 原有处理逻辑 ──────────────────────────────────────────────────

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		a.updateRecordToFailed(c, fmt.Sprintf("读取响应失败: %v", err))
		return nil, &relaymodel.ErrorWithStatusCode{
			StatusCode: http.StatusInternalServerError,
			Error:      relaymodel.Error{Message: fmt.Sprintf("读取响应失败: %v", err)},
		}
	}

	logger.Infof(c, "BFL DoResponse raw: status=%d, body=%s", resp.StatusCode, string(body))

	if resp.StatusCode != http.StatusOK {
		logger.Errorf(c, "Flux API error: status %d, body: %s", resp.StatusCode, string(body))
		errorMessage := extractFluxErrorMessage(body, resp.StatusCode)
		a.updateRecordToFailed(c, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, errorMessage))
		return nil, &relaymodel.ErrorWithStatusCode{
			StatusCode: resp.StatusCode,
			Error:      relaymodel.Error{Message: errorMessage},
		}
	}

	var fluxResp FluxResponse
	if err := json.Unmarshal(body, &fluxResp); err != nil {
		logger.Errorf(c, "解析 Flux 响应失败: %v, body: %s", err, string(body))
		a.updateRecordToFailed(c, fmt.Sprintf("解析响应失败: %v", err))
		return nil, &relaymodel.ErrorWithStatusCode{
			StatusCode: http.StatusInternalServerError,
			Error:      relaymodel.Error{Message: fmt.Sprintf("解析响应失败: %v", err)},
		}
	}

	if fluxResp.Error != "" {
		logger.Errorf(c, "Flux API 返回错误: %s", fluxResp.Error)
		a.updateRecordToFailed(c, fluxResp.Error)
		return nil, &relaymodel.ErrorWithStatusCode{
			StatusCode: http.StatusBadRequest,
			Error:      relaymodel.Error{Message: fluxResp.Error},
		}
	}

	groupRatio := 1.0
	quota := CalculateQuota(fluxResp.Cost, groupRatio)

	if a.ImageRecord != nil {
		now := time.Now().Unix()
		duration := int(now - a.ImageRecord.CreatedAt)

		a.ImageRecord.TaskId = fluxResp.ID
		a.ImageRecord.Status = TaskStatusSubmitted
		a.ImageRecord.Quota = quota
		a.ImageRecord.TotalDuration = duration
		a.ImageRecord.Detail = string(body)
		a.ImageRecord.Result = string(body)

		if err := a.ImageRecord.Update(); err != nil {
			logger.Errorf(c, "更新 Flux 记录失败: %v", err)
		} else {
			logger.Infof(c, "Flux 请求成功: task_id=%s, cost=%.4f cents, quota=%d, duration=%ds",
				fluxResp.ID, fluxResp.Cost, quota, duration)
		}
	}

	var respMap map[string]any
	if err := json.Unmarshal(body, &respMap); err != nil {
		logger.Errorf(c, "解析响应为 map 失败: %v", err)
		c.Data(resp.StatusCode, "application/json", body)
		return nil, nil
	}

	delete(respMap, "webhook_url")

	if taskID, ok := respMap["id"].(string); ok && taskID != "" {
		pollingURL := fmt.Sprintf("https://api.bfl.ai/v1/get_result?id=%s", taskID)
		respMap["polling_url"] = pollingURL
		logger.Debugf(c, "添加 polling_url: %s", pollingURL)
	}

	modifiedBody, err := json.Marshal(respMap)
	if err != nil {
		logger.Errorf(c, "序列化修改后的响应失败: %v", err)
		c.Data(resp.StatusCode, "application/json", body)
		return nil, nil
	}

	c.Data(resp.StatusCode, "application/json", modifiedBody)
	return nil, nil
}

// doReplicateResponse 处理 Replicate 响应（同步成功 or 排队中）
func (a *Adaptor) doReplicateResponse(c *gin.Context, resp *http.Response, meta *util.RelayMeta) (*relaymodel.Usage, *relaymodel.ErrorWithStatusCode) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		a.updateRecordToFailed(c, fmt.Sprintf("读取响应失败: %v", err))
		return nil, &relaymodel.ErrorWithStatusCode{
			StatusCode: http.StatusInternalServerError,
			Error:      relaymodel.Error{Message: fmt.Sprintf("读取响应失败: %v", err)},
		}
	}

	logger.Infof(c, "Replicate DoResponse raw: status=%d, body=%s", resp.StatusCode, string(body))

	// 200 = 同步完成，201 = 超时仍在处理，其他为错误
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		logger.Errorf(c, "Replicate API error: status %d, body: %s", resp.StatusCode, string(body))
		errMsg := extractFluxErrorMessage(body, resp.StatusCode)
		a.updateRecordToFailed(c, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, errMsg))
		return nil, &relaymodel.ErrorWithStatusCode{
			StatusCode: resp.StatusCode,
			Error:      relaymodel.Error{Message: errMsg},
		}
	}

	var replicateResp ReplicateResponse
	if err := json.Unmarshal(body, &replicateResp); err != nil {
		logger.Errorf(c, "解析 Replicate 响应失败: %v, body: %s", err, string(body))
		a.updateRecordToFailed(c, fmt.Sprintf("解析响应失败: %v", err))
		return nil, &relaymodel.ErrorWithStatusCode{
			StatusCode: http.StatusInternalServerError,
			Error:      relaymodel.Error{Message: fmt.Sprintf("解析响应失败: %v", err)},
		}
	}

	if replicateResp.Error != nil {
		errStr := fmt.Sprintf("%v", replicateResp.Error)
		logger.Errorf(c, "Replicate API 返回错误: %s", errStr)
		a.updateRecordToFailed(c, errStr)
		return nil, &relaymodel.ErrorWithStatusCode{
			StatusCode: http.StatusBadRequest,
			Error:      relaymodel.Error{Message: errStr},
		}
	}

	if replicateResp.Status == "succeeded" {
		return a.handleReplicateSuccess(c, replicateResp, meta, body)
	}
	return a.handleReplicatePending(c, replicateResp, meta, body)
}

// handleReplicateSuccess 同步成功：更新 DB、扣费、返回 BFL 格式响应给客户端
func (a *Adaptor) handleReplicateSuccess(c *gin.Context, replicateResp ReplicateResponse, meta *util.RelayMeta, rawBody []byte) (*relaymodel.Usage, *relaymodel.ErrorWithStatusCode) {
	imageURL := replicateResp.Output

	// P2-3: 空 URL 不应计为成功
	if imageURL == "" {
		errMsg := "Replicate 返回空图片 URL"
		logger.Errorf(c, "%s: task_id=%s", errMsg, replicateResp.ID)
		if a.ImageRecord != nil {
			a.updateRecordToFailed(c, errMsg)
		}
		return nil, &relaymodel.ErrorWithStatusCode{
			StatusCode: http.StatusInternalServerError,
			Error:      relaymodel.Error{Message: errMsg},
		}
	}

	if a.ImageRecord == nil {
		return nil, nil
	}

	now := time.Now().Unix()
	duration := int(now - a.ImageRecord.CreatedAt)

	group, err := model.CacheGetUserGroup(a.ImageRecord.UserId)
	if err != nil {
		group = "Lv1"
	}
	groupRatio := util.GetAsyncBillingGroupRatio(group, a.ImageRecord.UserId, a.ImageRecord.ChannelId, common.ChannelTypeFlux)
	quota := CalculateReplicateQuota(meta.OriginModelName, 1, groupRatio)

	// P2-1: 存储 BFL query 格式（{id,status:"Ready",result:{sample}}），GetFlux 可直接返回给客户端
	queryResult := map[string]any{
		"id":     replicateResp.ID,
		"status": "Ready",
		"result": map[string]any{"sample": imageURL},
	}
	resultBytes, _ := json.Marshal(queryResult)

	a.ImageRecord.TaskId = replicateResp.ID
	a.ImageRecord.Status = TaskStatusSucceed
	a.ImageRecord.Quota = quota
	a.ImageRecord.TotalDuration = duration
	a.ImageRecord.StoreUrl = imageURL
	a.ImageRecord.Result = string(resultBytes)
	a.ImageRecord.Detail = string(rawBody)

	// P1: 原子性 CAS 更新——只有赢得竞争的路径才扣费，防止 webhook 并发导致双重扣费
	applied, dbErr := a.ImageRecord.UpdateIfNotTerminal()
	if dbErr != nil {
		logger.Errorf(c, "Replicate 更新记录失败: %v", dbErr)
	} else if applied {
		if err := model.DecreaseUserQuota(a.ImageRecord.UserId, quota); err != nil {
			logger.Errorf(c, "Replicate 扣费失败: user_id=%d, quota=%d, error=%v", a.ImageRecord.UserId, quota, err)
		} else {
			logger.Infof(c, "Replicate 扣费成功: user_id=%d, quota=%d, task_id=%s, duration=%ds",
				a.ImageRecord.UserId, quota, replicateResp.ID, duration)
		}
	} else {
		// webhook 路径已先行处理，此路径跳过扣费
		logger.Warnf(c, "Replicate 同步路径竞争落败，跳过扣费: task_id=%s", replicateResp.ID)
	}

	bflResp := map[string]any{
		"id":          replicateResp.ID,
		"polling_url": fmt.Sprintf("%s/flux/v1/get_result/%s", config.ServerAddress, replicateResp.ID),
	}
	bflBytes, _ := json.Marshal(bflResp)
	c.Data(http.StatusOK, "application/json", bflBytes)

	return nil, nil
}

// handleReplicatePending 60s 超时仍未完成：更新 DB 为 submitted，等待 webhook 或客户端轮询
func (a *Adaptor) handleReplicatePending(c *gin.Context, replicateResp ReplicateResponse, meta *util.RelayMeta, rawBody []byte) (*relaymodel.Usage, *relaymodel.ErrorWithStatusCode) {
	if a.ImageRecord != nil {
		now := time.Now().Unix()
		a.ImageRecord.TaskId = replicateResp.ID
		a.ImageRecord.Status = TaskStatusSubmitted
		a.ImageRecord.TotalDuration = int(now - a.ImageRecord.CreatedAt)
		a.ImageRecord.Detail = string(rawBody)

		if err := a.ImageRecord.Update(); err != nil {
			logger.Errorf(c, "Replicate 更新 pending 记录失败: %v", err)
		} else {
			logger.Infof(c, "Replicate 任务排队中: task_id=%s, status=%s", replicateResp.ID, replicateResp.Status)
		}
	}

	bflResp := map[string]any{
		"id":          replicateResp.ID,
		"polling_url": fmt.Sprintf("%s/flux/v1/get_result/%s", config.ServerAddress, replicateResp.ID),
	}
	bflBytes, _ := json.Marshal(bflResp)
	c.Data(http.StatusOK, "application/json", bflBytes)

	return nil, nil
}

// extractFluxErrorMessage 从 Flux/Replicate 错误响应中提取错误消息
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
	return fmt.Sprintf("API 返回错误状态: %d", statusCode)
}

// MarkFailed 将当前 ImageRecord 更新为失败状态（含 fail_reason 与 total_duration）。
// 供 controller 在前置校验 / 请求转换 / 发送阶段失败时调用。
func (a *Adaptor) MarkFailed(c *gin.Context, reason string) {
	a.updateRecordToFailed(c, reason)
}

// updateRecordToFailed 更新记录为失败状态
func (a *Adaptor) updateRecordToFailed(c *gin.Context, reason string) {
	if a.ImageRecord != nil {
		now := time.Now().Unix()
		duration := int(now - a.ImageRecord.CreatedAt)

		a.ImageRecord.Status = TaskStatusFailed
		a.ImageRecord.FailReason = reason
		a.ImageRecord.TotalDuration = duration

		if err := a.ImageRecord.Update(); err != nil {
			logger.Errorf(c, "更新 Flux 失败记录失败: %v", err)
		}
	}
}

// HandleCallback 处理 BFL 回调通知
func HandleCallback(c *gin.Context, notification FluxCallbackNotification, rawBody []byte) (bool, int, string) {
	taskID := notification.TaskId
	logger.Infof(c, "Flux callback received: task_id=%s, status=%s, progress=%d", taskID, notification.Status, notification.Progress)
	logger.Debugf(c, "Flux callback notification: %+v", notification)

	image, err := model.GetImageByTaskId(taskID)
	if err != nil || image == nil {
		logger.Errorf(c, "Flux callback task not found: task_id=%s, error=%v", taskID, err)
		return false, http.StatusNotFound, "task not found"
	}

	currentStatus := image.Status
	if currentStatus == TaskStatusSucceed {
		logger.Infof(c, "Flux callback already processed: task_id=%s, status=%s", taskID, currentStatus)
		return true, http.StatusOK, "already processed"
	}

	callbackBytes, err := json.Marshal(notification)
	if err != nil {
		logger.Errorf(c, "Flux callback marshal error: %v", err)
		return false, http.StatusInternalServerError, "internal error"
	}
	image.Result = string(callbackBytes)
	image.Detail = string(rawBody)

	now := time.Now().Unix()
	image.TotalDuration = int(now - image.CreatedAt)

	normalizedStatus := strings.ToLower(notification.Status)
	if normalizedStatus == TaskStatusSucceed {
		return handleSuccessCallback(c, image, notification, taskID)
	} else if normalizedStatus == TaskStatusFailed {
		return handleFailedCallback(c, image, notification, taskID)
	} else {
		return handleProcessingCallback(c, image, notification, taskID)
	}
}

// handleSuccessCallback 处理 BFL 成功回调
func handleSuccessCallback(c *gin.Context, image *model.Image, notification FluxCallbackNotification, taskID string) (bool, int, string) {
	image.Status = TaskStatusSucceed

	if notification.Result != nil && notification.Result.Sample != "" {
		image.StoreUrl = notification.Result.Sample
	}

	var quota int64
	if notification.Cost > 0 {
		group, err := model.CacheGetUserGroup(image.UserId)
		if err != nil {
			logger.Errorf(c, "Flux callback get user group failed: user_id=%d, error=%v", image.UserId, err)
			group = "Lv1"
		}
		groupRatio := util.GetAsyncBillingGroupRatio(group, image.UserId, image.ChannelId, common.ChannelTypeFlux)
		quota = CalculateQuota(notification.Cost, groupRatio)
		image.Quota = quota

		logger.Infof(c, "Flux callback with cost: task_id=%s, cost=%.4f cents, quota=%d", taskID, notification.Cost, quota)
	} else {
		quota = image.Quota
		if quota == 0 {
			logger.Warnf(c, "Flux callback has no cost and no saved quota: task_id=%s, using estimated quota", taskID)
			group, err := model.CacheGetUserGroup(image.UserId)
			if err != nil {
				logger.Errorf(c, "Flux callback get user group failed: user_id=%d, error=%v", image.UserId, err)
				group = "Lv1"
			}
			groupRatio := util.GetAsyncBillingGroupRatio(group, image.UserId, image.ChannelId, common.ChannelTypeFlux)
			quota = EstimateQuota(image.Model, groupRatio)
			image.Quota = quota
		}
		logger.Infof(c, "Flux callback without cost: task_id=%s, using saved quota=%d", taskID, quota)
	}

	err := model.DecreaseUserQuota(image.UserId, quota)
	if err != nil {
		logger.Errorf(c, "Flux callback billing failed: user_id=%d, quota=%d, error=%v",
			image.UserId, quota, err)
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

// handleFailedCallback 处理 BFL 失败回调
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

// handleProcessingCallback 处理 BFL 处理中状态回调
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

// QueryResult 查询任务结果（BFL 和 Replicate 走不同分支）
func (a *Adaptor) QueryResult(c *gin.Context, taskID string, baseURL string, apiKey string) (int, []byte, error) {
	if isReplicate(baseURL) {
		return a.queryReplicateResult(c, taskID, baseURL, apiKey)
	}

	// BFL 路径
	queryURL := fmt.Sprintf("%s/v1/get_result?id=%s", baseURL, taskID)
	logger.Debugf(c, "Flux 查询 URL: %s", queryURL)

	req, err := http.NewRequest("GET", queryURL, nil)
	if err != nil {
		return http.StatusInternalServerError, nil, fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("x-key", apiKey)

	resp, err := util.HTTPClient.Do(req)
	if err != nil {
		return http.StatusInternalServerError, nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return http.StatusInternalServerError, nil, fmt.Errorf("读取响应失败: %w", err)
	}

	logger.Debugf(c, "Flux 查询响应: status=%d, body=%s", resp.StatusCode, string(body))
	return resp.StatusCode, body, nil
}

// queryReplicateResult 查询 Replicate 预测结果，归一化为 BFL 轮询格式返回
// 如果检测到终态，顺便更新 DB（作为 webhook 的兜底）
func (a *Adaptor) queryReplicateResult(c *gin.Context, taskID string, baseURL string, apiKey string) (int, []byte, error) {
	queryURL := fmt.Sprintf("%s/v1/predictions/%s", baseURL, taskID)
	logger.Debugf(c, "Replicate 查询 URL: %s", queryURL)

	req, err := http.NewRequest("GET", queryURL, nil)
	if err != nil {
		return http.StatusInternalServerError, nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := util.HTTPClient.Do(req)
	if err != nil {
		return http.StatusInternalServerError, nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return http.StatusInternalServerError, nil, fmt.Errorf("读取响应失败: %w", err)
	}

	logger.Infof(c, "Replicate QueryResult raw: task_id=%s, status=%d, body=%s", taskID, resp.StatusCode, string(body))

	var replicateResp ReplicateResponse
	if err := json.Unmarshal(body, &replicateResp); err != nil {
		return resp.StatusCode, body, nil
	}

	// 检测到终态时主动更新 DB（作为 webhook 的兜底，防止 webhook 未到达时数据永远 pending）
	if replicateResp.Status == "succeeded" || replicateResp.Status == "failed" || replicateResp.Status == "canceled" {
		if image, dbErr := model.GetImageByTaskId(taskID); dbErr == nil && image != nil {
			if image.Status != TaskStatusSucceed && image.Status != TaskStatusFailed {
				HandleReplicateCallback(c, replicateResp, body)
			}
		}
	}

	// 将 Replicate 格式转为 BFL 轮询格式，客户端无需感知后端差异
	bflStatus := "Pending"
	switch replicateResp.Status {
	case "succeeded":
		bflStatus = "Ready"
	case "failed", "canceled":
		bflStatus = "Error"
	case "processing":
		bflStatus = "Processing"
	}

	bflPolling := map[string]any{
		"id":     replicateResp.ID,
		"status": bflStatus,
	}
	if replicateResp.Status == "succeeded" {
		bflPolling["result"] = map[string]any{"sample": replicateResp.Output}
	}
	if replicateResp.Error != nil {
		bflPolling["error"] = fmt.Sprintf("%v", replicateResp.Error)
	}

	bflBytes, err := json.Marshal(bflPolling)
	if err != nil {
		return resp.StatusCode, body, nil
	}

	logger.Debugf(c, "Replicate 查询结果（归一化）: status=%s", bflStatus)
	return http.StatusOK, bflBytes, nil
}

// HandleReplicateCallback 处理 Replicate webhook 回调，更新 DB 并在成功时扣费
func HandleReplicateCallback(c *gin.Context, replicateResp ReplicateResponse, rawBody []byte) (bool, int, string) {
	taskID := replicateResp.ID
	logger.Infof(c, "Replicate callback: task_id=%s, status=%s", taskID, replicateResp.Status)

	image, err := model.GetImageByTaskId(taskID)
	if err != nil || image == nil {
		logger.Errorf(c, "Replicate callback task not found: task_id=%s", taskID)
		return false, http.StatusNotFound, "task not found"
	}

	// 幂等：已是终态则直接返回
	if image.Status == TaskStatusSucceed || image.Status == TaskStatusFailed {
		logger.Infof(c, "Replicate callback already processed: task_id=%s, status=%s", taskID, image.Status)
		return true, http.StatusOK, "already processed"
	}

	now := time.Now().Unix()
	image.TotalDuration = int(now - image.CreatedAt)
	image.Detail = string(rawBody)

	switch replicateResp.Status {
	case "succeeded":
		imageURL := replicateResp.Output

		// P2-3: 空 URL 按失败处理，不扣费
		if imageURL == "" {
			image.Status = TaskStatusFailed
			image.FailReason = "Replicate 返回空图片 URL"
			logger.Errorf(c, "Replicate callback empty output: task_id=%s", taskID)
			if err := image.Update(); err != nil {
				logger.Errorf(c, "Replicate callback update failed: task_id=%s, error=%v", taskID, err)
				return false, http.StatusInternalServerError, "update failed"
			}
			return true, http.StatusOK, "success"
		}

		// P2-1: 存储 BFL query 格式，与 queryReplicateResult 归一化输出一致
		queryResult := map[string]any{
			"id":     taskID,
			"status": "Ready",
			"result": map[string]any{"sample": imageURL},
		}
		resultBytes, _ := json.Marshal(queryResult)

		group, _ := model.CacheGetUserGroup(image.UserId)
		if group == "" {
			group = "Lv1"
		}
		groupRatio := util.GetAsyncBillingGroupRatio(group, image.UserId, image.ChannelId, common.ChannelTypeFlux)
		quota := CalculateReplicateQuota(image.Model, 1, groupRatio)

		image.Status = TaskStatusSucceed
		image.StoreUrl = imageURL
		image.Result = string(resultBytes)
		image.Quota = quota

		// P1: 原子性 CAS 更新——只有赢得竞争的路径才扣费
		applied, dbErr := image.UpdateIfNotTerminal()
		if dbErr != nil {
			logger.Errorf(c, "Replicate callback update failed: task_id=%s, error=%v", taskID, dbErr)
			return false, http.StatusInternalServerError, "update failed"
		}
		if !applied {
			// 同步路径已先行处理
			logger.Infof(c, "Replicate callback 竞争落败，跳过扣费: task_id=%s", taskID)
			return true, http.StatusOK, "already processed"
		}
		if err := model.DecreaseUserQuota(image.UserId, quota); err != nil {
			logger.Errorf(c, "Replicate callback billing failed: user_id=%d, quota=%d, error=%v", image.UserId, quota, err)
		} else {
			logger.Infof(c, "Replicate callback billing success: user_id=%d, quota=%d, task_id=%s", image.UserId, quota, taskID)
		}
		return true, http.StatusOK, "success"

	case "failed", "canceled":
		image.Status = TaskStatusFailed
		errMsg := fmt.Sprintf("%v", replicateResp.Error)
		if errMsg == "<nil>" || errMsg == "" {
			errMsg = fmt.Sprintf("Replicate 任务 %s", replicateResp.Status)
		}
		image.FailReason = errMsg
		logger.Infof(c, "Replicate callback task %s: task_id=%s, reason=%s", replicateResp.Status, taskID, errMsg)

	default:
		image.Status = replicateResp.Status
		logger.Infof(c, "Replicate callback status update: task_id=%s, status=%s", taskID, replicateResp.Status)
	}

	// failed / canceled / processing 走常规更新（无扣费）
	if err := image.Update(); err != nil {
		return false, http.StatusInternalServerError, "update failed"
	}

	return true, http.StatusOK, "success"
}

// VerifyReplicateWebhook 验证 Replicate webhook 签名（HMAC-SHA256）
// 若未配置 REPLICATE_WEBHOOK_SIGNING_KEY 则跳过验证（返回 true）
func VerifyReplicateWebhook(webhookID, webhookTimestamp, webhookSignature string, body []byte) bool {
	signingKey := os.Getenv("REPLICATE_WEBHOOK_SIGNING_KEY")
	if signingKey == "" {
		return true
	}

	// 签名密钥格式: whsec_<base64>
	keyBase64 := strings.TrimPrefix(signingKey, "whsec_")
	keyBytes, err := base64.StdEncoding.DecodeString(keyBase64)
	if err != nil {
		return false
	}

	// 待签内容: {webhook-id}.{webhook-timestamp}.{body}
	signedContent := fmt.Sprintf("%s.%s.%s", webhookID, webhookTimestamp, string(body))

	mac := hmac.New(sha256.New, keyBytes)
	mac.Write([]byte(signedContent))
	computedSig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	// webhook-signature 可能包含多个空格分隔的签名（重试场景），格式: "v1,<sig>"
	for _, sig := range strings.Fields(webhookSignature) {
		parts := strings.SplitN(sig, ",", 2)
		if len(parts) == 2 && parts[0] == "v1" && parts[1] == computedSig {
			return true
		}
	}
	return false
}
