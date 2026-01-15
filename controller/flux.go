package controller

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/relay/channel/flux"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/util"
)

// RelayFlux 处理 Flux API 的异步请求
func RelayFlux(c *gin.Context) {
	// 1. 获取 meta 信息
	meta := util.GetRelayMeta(c)

	// 记录请求开始
	logger.Infof(c, "Flux 请求开始: user_id=%d, channel_id=%d, model=%s, path=%s",
		meta.UserId, meta.ChannelId, meta.OriginModelName, meta.RequestURLPath)

	// 2. 创建 Flux 适配器
	adaptor := &flux.Adaptor{}
	adaptor.Init(meta)

	// 3. 在发起请求前创建 pending 状态的数据库记录
	if err := adaptor.CreatePendingRecord(c, meta); err != nil {
		logger.Errorf(c, "Flux 创建 pending 记录失败: user_id=%d, error=%v", meta.UserId, err)
		errResp := openai.ErrorWrapper(err, "create_pending_record_failed", http.StatusInternalServerError)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	// 4. 转换请求（移除不需要的字段）
	convertedBody, err := adaptor.ConvertFluxRequest(c, meta)
	if err != nil {
		logger.Errorf(c, "Flux 请求转换失败: user_id=%d, error=%v", meta.UserId, err)
		errResp := openai.ErrorWrapper(err, "convert_request_failed", http.StatusBadRequest)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	// 5. 执行请求
	resp, err := adaptor.DoRequest(c, meta, bytes.NewReader(convertedBody))
	if err != nil {
		logger.Errorf(c, "Flux 请求执行失败: user_id=%d, channel_id=%d, error=%v",
			meta.UserId, meta.ChannelId, err)
		errResp := openai.ErrorWrapper(err, "request_failed", http.StatusInternalServerError)
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	// 6. 处理响应（包括计费、更新记录、透传响应）
	_, errResp := adaptor.DoResponse(c, resp, meta)
	if errResp != nil {
		logger.Errorf(c, "Flux 响应处理失败: user_id=%d, error=%v", meta.UserId, errResp.Error.Message)
		// DoResponse 内部已经处理了响应的写入和记录更新
		return
	}

	logger.Infof(c, "Flux 请求完成: user_id=%d, channel_id=%d", meta.UserId, meta.ChannelId)
}

// HandleFluxCallback 处理 Flux API 回调通知
func HandleFluxCallback(c *gin.Context) {
	// 读取原始 body
	bodyBytes, err := c.GetRawData()
	if err != nil {
		logger.Errorf(c, "Flux callback read body error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// 【调试日志】打印原始 JSON 数据
	logger.Infof(c, "Flux callback raw JSON: %s", string(bodyBytes))

	// 解析回调通知
	var notification flux.FluxCallbackNotification
	if err := json.Unmarshal(bodyBytes, &notification); err != nil {
		logger.Errorf(c, "Flux callback parse error: %v, raw body: %s", err, string(bodyBytes))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	// 【调试日志】打印解析后的结构体（包含所有字段的值）
	logger.Debugf(c, "Flux callback parsed notification: ID=%s, Status=%s, Cost=%.4f, InputMP=%.2f, OutputMP=%.2f, Error=%s, PollingURL=%s, Result=%+v",
		notification.ID, notification.Status, notification.Cost, notification.InputMP, notification.OutputMP, notification.Error, notification.PollingURL, notification.Result)

	// 调用业务逻辑处理回调
	success, statusCode, message := flux.HandleCallback(c, notification)

	// 返回响应
	if success {
		c.JSON(statusCode, gin.H{"message": message})
	} else {
		c.JSON(statusCode, gin.H{"error": message})
	}
}
