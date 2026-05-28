package controller

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/flux"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

// relayFluxHelper 执行单次 Flux 请求（成功时写入客户端响应，失败时仅返回错误）。
// 每次进入本函数都会创建一条 pending image 记录，重试链路下"切换 N 次渠道 = N 条记录"，
// 通过 X-Request-ID 把同一客户端请求的多次尝试关联起来。
func relayFluxHelper(c *gin.Context) *relaymodel.ErrorWithStatusCode {
	requestBody, err := common.GetRequestBody(c)
	if err != nil {
		logger.Errorf(c, "Flux: failed to get request body: %v", err)
		return &relaymodel.ErrorWithStatusCode{
			StatusCode: http.StatusBadRequest,
			Error:      relaymodel.Error{Message: "failed to read request body: " + err.Error()},
		}
	}

	meta := util.GetRelayMeta(c)

	adaptor := &flux.Adaptor{}
	adaptor.Init(meta)

	// 入口处即落库：失败重试场景下也能在 image 表里看到每一次尝试。
	if err := adaptor.CreatePendingRecord(c, meta); err != nil {
		logger.Errorf(c, "Flux 创建 pending 记录失败: %v", err)
		return &relaymodel.ErrorWithStatusCode{
			StatusCode: http.StatusInternalServerError,
			Error:      relaymodel.Error{Message: "create pending record failed: " + err.Error()},
		}
	}

	if err := adaptor.ValidateRequest(meta); err != nil {
		logger.Errorf(c, "Flux 请求前置校验失败: %v", err)
		adaptor.MarkFailed(c, err.Error())
		return &relaymodel.ErrorWithStatusCode{
			StatusCode: http.StatusBadRequest,
			Error:      relaymodel.Error{Message: err.Error()},
		}
	}

	c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
	convertedBody, err := adaptor.ConvertFluxRequest(c, meta)
	if err != nil {
		logger.Errorf(c, "Flux 请求转换失败: %v", err)
		adaptor.MarkFailed(c, "convert request failed: "+err.Error())
		return &relaymodel.ErrorWithStatusCode{
			StatusCode: http.StatusBadRequest,
			Error:      relaymodel.Error{Message: "convert request failed: " + err.Error()},
		}
	}

	// 余额预检：低于 $0.10（50000 quota）直接拒绝，避免请求发出后上游已执行但扣费失败
	const minBalanceQuota = int64(50000) // $0.10 × 500000 quota/USD
	balance, balErr := model.CacheGetUserQuota(c.Request.Context(), meta.UserId)
	if balErr != nil {
		logger.Errorf(c, "Flux 余额查询失败: %v", balErr)
		adaptor.MarkFailed(c, "余额查询失败")
		return &relaymodel.ErrorWithStatusCode{
			StatusCode: http.StatusInternalServerError,
			Error:      relaymodel.Error{Message: "余额查询失败: " + balErr.Error()},
		}
	}
	if balance < minBalanceQuota {
		msg := fmt.Sprintf("余额不足，当前余额 $%.4f，请充值后重试（最低需 $0.10）", float64(balance)/500000)
		logger.Warnf(c, "Flux 余额预检拒绝: user_id=%d, balance=%d", meta.UserId, balance)
		adaptor.MarkFailed(c, msg)
		return &relaymodel.ErrorWithStatusCode{
			StatusCode: http.StatusPaymentRequired,
			Error:      relaymodel.Error{Message: msg},
		}
	}

	resp, err := adaptor.DoRequest(c, meta, bytes.NewReader(convertedBody))
	if err != nil {
		logger.Errorf(c, "Flux 请求执行失败: channel_id=%d, error=%v", meta.ChannelId, err)
		adaptor.MarkFailed(c, "request failed: "+err.Error())
		return &relaymodel.ErrorWithStatusCode{
			StatusCode: http.StatusInternalServerError,
			Error:      relaymodel.Error{Message: "request failed: " + err.Error()},
		}
	}

	// 读取响应 body，用于错误处理和 4xx 场景下的脱敏日志记录
	bodyBytes, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		logger.Errorf(c, "Flux 读取响应 body 失败: %v", readErr)
		adaptor.MarkFailed(c, "read response body failed: "+readErr.Error())
		return &relaymodel.ErrorWithStatusCode{
			StatusCode: http.StatusInternalServerError,
			Error:      relaymodel.Error{Message: "read response failed: " + readErr.Error()},
		}
	}
	resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	_, errResp := adaptor.DoResponse(c, resp, meta)
	if errResp != nil {
		// 非 429 的 4xx 错误是客户端问题，换渠道不会变好，直接返回错误内容给客户端
		if errResp.StatusCode >= 400 && errResp.StatusCode != http.StatusTooManyRequests && errResp.StatusCode < 500 {
			sanitizedDetails := extractFluxValidationDetails(bodyBytes)
			logger.Errorf(c, "Flux 客户端错误(4xx)，不重试: status=%d, message=%s", errResp.StatusCode, errResp.Error.Message)
			c.JSON(errResp.StatusCode, buildFluxUnifiedErrorResponse(errResp.StatusCode, sanitizedDetails, errResp.Error.Message))
			return nil
		}
		return errResp
	}

	logger.Infof(c, "Flux 请求成功: channel_id=%d", meta.ChannelId)
	return nil
}

type fluxValidationDetail struct {
	Type string   `json:"type,omitempty"`
	Loc  []string `json:"loc,omitempty"`
	Msg  string   `json:"msg,omitempty"`
}

func extractFluxValidationDetails(body []byte) []fluxValidationDetail {
	var fluxError struct {
		Detail []struct {
			Loc  []string `json:"loc"`
			Msg  string   `json:"msg"`
			Type string   `json:"type"`
		} `json:"detail"`
	}

	if err := json.Unmarshal(body, &fluxError); err != nil || len(fluxError.Detail) == 0 {
		return nil
	}

	details := make([]fluxValidationDetail, 0, len(fluxError.Detail))
	for _, item := range fluxError.Detail {
		details = append(details, fluxValidationDetail{
			Type: item.Type,
			Loc:  item.Loc,
			Msg:  item.Msg,
		})
	}
	return details
}

func buildFluxUnifiedErrorResponse(statusCode int, details []fluxValidationDetail, fallbackMessage string) gin.H {
	message := fallbackMessage
	if message == "" {
		message = fmt.Sprintf("API 返回错误状态: %d", statusCode)
	}
	if detailMessage := buildFluxValidationMessage(details); detailMessage != "" {
		message = detailMessage
	}

	return gin.H{
		"error": gin.H{
			"code":    nil,
			"message": message,
			"param":   "",
			"type":    "api_error",
		},
	}
}

func buildFluxValidationMessage(details []fluxValidationDetail) string {
	if len(details) == 0 {
		return ""
	}

	first := details[0]
	msg := strings.TrimSpace(first.Msg)
	if len(first.Loc) == 0 {
		return msg
	}

	locParts := first.Loc
	if len(locParts) > 0 && strings.EqualFold(locParts[0], "body") {
		locParts = locParts[1:]
	}
	loc := strings.TrimSpace(strings.Join(locParts, " "))
	if msg == "" {
		return loc
	}
	if loc == "" {
		return msg
	}
	return msg + " missing " + loc
}

// HandleFluxCallback 处理 Flux API 回调通知
func HandleFluxCallback(c *gin.Context) {
	// BFL 协议本身不签名，我们在注入的 webhook URL 上挂 ?key=<secret>，由本端校验。
	// 未配置 FLUX_WEBHOOK_SECRET 时跳过校验，保持向后兼容（与 Replicate 校验逻辑一致）。
	if expected := os.Getenv("FLUX_WEBHOOK_SECRET"); expected != "" {
		got := c.Query("key")
		if subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
			logger.Errorf(c, "Flux callback secret mismatch, ip=%s", c.ClientIP())
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
			return
		}
	}

	bodyBytes, err := c.GetRawData()
	if err != nil {
		logger.Errorf(c, "Flux callback read body error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	var notification flux.FluxCallbackNotification
	if err := json.Unmarshal(bodyBytes, &notification); err != nil {
		logger.Errorf(c, "Flux callback parse error: %v, raw body: %s", err, string(bodyBytes))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	success, statusCode, message := flux.HandleCallback(c, notification, bodyBytes)

	if success {
		c.JSON(statusCode, gin.H{"message": message})
	} else {
		c.JSON(statusCode, gin.H{"error": message})
	}
}

// HandleReplicateCallback 处理 Replicate webhook 回调通知
func HandleReplicateCallback(c *gin.Context) {
	bodyBytes, err := c.GetRawData()
	if err != nil {
		logger.Errorf(c, "Replicate callback read body error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	//logger.Infof(c, "Replicate callback raw JSON: %s", string(bodyBytes))

	// 验证签名（配置 REPLICATE_WEBHOOK_SIGNING_KEY 后启用，否则跳过）
	webhookID := c.GetHeader("webhook-id")
	webhookTimestamp := c.GetHeader("webhook-timestamp")
	webhookSignature := c.GetHeader("webhook-signature")
	if !flux.VerifyReplicateWebhook(webhookID, webhookTimestamp, webhookSignature, bodyBytes) {
		logger.Errorf(c, "Replicate callback signature verification failed")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	var replicateResp flux.ReplicateResponse
	if err := json.Unmarshal(bodyBytes, &replicateResp); err != nil {
		logger.Errorf(c, "Replicate callback parse error: %v, raw body: %s", err, string(bodyBytes))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	logger.Infof(c, "Replicate callback: id=%s, status=%s", replicateResp.ID, replicateResp.Status)

	success, statusCode, message := flux.HandleReplicateCallback(c, replicateResp, bodyBytes)

	if success {
		c.JSON(statusCode, gin.H{"message": message})
	} else {
		c.JSON(statusCode, gin.H{"error": message})
	}
}

// GetFlux 查询 Flux 任务结果
func GetFlux(c *gin.Context) {
	taskID := c.Query("id")
	if taskID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}

	fromSource := c.DefaultQuery("from_source", "false")
	isFromSource := fromSource == "true" || fromSource == "1"

	logger.Infof(c, "查询 Flux 任务: task_id=%s, from_source=%v", taskID, isFromSource)

	image, err := model.GetImageByTaskId(taskID)
	if err != nil {
		logger.Errorf(c, "Flux 任务不存在: task_id=%s, error=%v", taskID, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}

	if !isFromSource {
		logger.Infof(c, "从本地数据库返回 Flux 任务: task_id=%s, status=%s", taskID, image.Status)

		if image.Status == flux.TaskStatusSucceed && image.Result != "" {
			c.Data(http.StatusOK, "application/json", []byte(image.Result))
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"id":     image.TaskId,
			"status": image.Status,
			"error":  image.FailReason,
		})
		return
	}

	channel, err := model.GetChannelById(image.ChannelId, true)
	if err != nil {
		logger.Errorf(c, "获取 channel 失败: channel_id=%d, error=%v", image.ChannelId, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get channel"})
		return
	}

	if channel.BaseURL == nil || *channel.BaseURL == "" {
		logger.Errorf(c, "Channel base_url 为空: channel_id=%d", image.ChannelId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid channel configuration"})
		return
	}

	adaptor := &flux.Adaptor{}
	statusCode, responseBody, err := adaptor.QueryResult(c, taskID, *channel.BaseURL, channel.ResolveKeyByIndex(image.KeyIndex))
	if err != nil {
		logger.Errorf(c, "查询 Flux 结果失败: task_id=%s, error=%v", taskID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Data(statusCode, "application/json", responseBody)
	logger.Infof(c, "Flux 查询完成（源站）: task_id=%s, status=%d", taskID, statusCode)
}
