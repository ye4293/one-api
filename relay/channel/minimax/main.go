package minimax

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/common/render"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/model"
)

func StreamHandler(c *gin.Context, resp *http.Response) (*model.ErrorWithStatusCode, *model.Usage) {
	var usage *model.Usage
	var lastResponse MinimaxResponse

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.SysError("error reading response body: " + err.Error())
		return openai.ErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError), nil
	}

	// 记录原始响应
	logger.SysLog("Raw response: " + string(bodyBytes))

	// 首先尝试解析第一个响应，检查是否为错误响应
	chunks := strings.Split(string(bodyBytes), "\n\n")
	if len(chunks) > 0 {
		firstChunk := strings.TrimSpace(chunks[0])
		// 如果有 "data: " 前缀，去掉它
		firstChunk = strings.TrimPrefix(firstChunk, "data: ")

		var firstResponse MinimaxResponse
		err = json.Unmarshal([]byte(firstChunk), &firstResponse)
		if err == nil && firstResponse.BaseResp.StatusCode != 0 {
			logger.SysError(fmt.Sprintf("MiniMax error: code=%d, message=%s",
				firstResponse.BaseResp.StatusCode,
				firstResponse.BaseResp.StatusMsg))
			return &model.ErrorWithStatusCode{
				Error: model.Error{
					Message: firstResponse.BaseResp.StatusMsg,
					Type:    fmt.Sprintf("minimax_error_%d", firstResponse.BaseResp.StatusCode),
					Code:    firstResponse.BaseResp.StatusCode,
				},
				StatusCode: getHTTPStatusFromMinimaxError(firstResponse.BaseResp.StatusCode),
			}, nil
		}
	}

	// 设置 SSE 头
	common.SetEventStreamHeaders(c)

	// 处理正常的流式响应
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}

		// 去掉 "data: " 前缀
		chunk = strings.TrimPrefix(chunk, "data: ")

		// 如果是 [DONE] 标记，跳过
		if chunk == "[DONE]" {
			continue
		}

		logger.SysLog("Processing chunk: " + chunk)

		var streamResponse MinimaxResponse
		err := json.Unmarshal([]byte(chunk), &streamResponse)
		if err != nil {
			logger.SysError("Error unmarshalling chunk: " + err.Error() + ", chunk: " + chunk)
			continue
		}

		// 处理流式数据
		if streamResponse.Object == "chat.completion.chunk" {
			// 直接转发原始的 chunk 数据
			render.StringData(c, fmt.Sprintf("data: %s\n\n", chunk))
		} else if streamResponse.Object == "chat.completion" {
			// 保存最后一条完整响应
			lastResponse = streamResponse
			usage = &model.Usage{
				PromptTokens:     lastResponse.Usage.PromptTokens,
				CompletionTokens: lastResponse.Usage.CompletionTokens,
				TotalTokens:      lastResponse.Usage.TotalTokens,
			}
		}
	}

	// 发送结束标记
	render.Done(c)

	// 如果没有获取到 usage 信息，但有最后一条响应
	if usage == nil && lastResponse.Usage.TotalTokens > 0 {
		usage = &model.Usage{
			PromptTokens:     lastResponse.Usage.PromptTokens,
			CompletionTokens: lastResponse.Usage.CompletionTokens,
			TotalTokens:      lastResponse.Usage.TotalTokens,
		}
	}

	return nil, usage
}

// 为流式响应定义的 Delta 结构
type Delta struct {
	Content      string `json:"content"`
	Role         string `json:"role"`
	Name         string `json:"name"`
	AudioContent string `json:"audio_content"`
}

// 修改 Choice 结构以支持流式响应

func Handler(c *gin.Context, resp *http.Response, promptTokens int, modelName string) (*model.ErrorWithStatusCode, *model.Usage) {
	var minimaxResponse MinimaxResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError), nil
	}
	err = resp.Body.Close()
	if err != nil {
		return openai.ErrorWrapper(err, "close_response_body_failed", http.StatusInternalServerError), nil
	}

	err = json.Unmarshal(responseBody, &minimaxResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "unmarshal_response_body_failed", http.StatusInternalServerError), nil
	}

	// 检查错误状态码
	if minimaxResponse.BaseResp.StatusCode != 0 {
		// 根据 MiniMax 的错误码映射到合适的 HTTP 状态码
		httpStatus := getHTTPStatusFromMinimaxError(minimaxResponse.BaseResp.StatusCode)
		return &model.ErrorWithStatusCode{
			Error: model.Error{
				Message: minimaxResponse.BaseResp.StatusMsg,
				Type:    fmt.Sprintf("minimax_error_%d", minimaxResponse.BaseResp.StatusCode),
				Code:    minimaxResponse.BaseResp.StatusCode,
			},
			StatusCode: httpStatus,
		}, nil
	}

	// 只有在没有错误的情况下才设置响应头和写入响应体
	for k, v := range resp.Header {
		c.Writer.Header().Set(k, v[0])
	}
	c.Writer.WriteHeader(resp.StatusCode)

	// 写入响应体
	_, err = c.Writer.Write(responseBody)
	if err != nil {
		return openai.ErrorWrapper(err, "write_response_body_failed", http.StatusInternalServerError), nil
	}

	// 转换 Usage 格式
	usage := &model.Usage{
		PromptTokens:     minimaxResponse.Usage.PromptTokens,
		CompletionTokens: minimaxResponse.Usage.CompletionTokens,
		TotalTokens:      minimaxResponse.Usage.TotalTokens,
	}

	return nil, usage
}

// 辅助函数：将 MiniMax 错误码映射到 HTTP 状态码
func getHTTPStatusFromMinimaxError(minimaxCode int) int {
	var httpStatus int
	switch minimaxCode {
	case 1000: // 未知错误
		httpStatus = http.StatusInternalServerError // 500
	case 1001: // 超时
		httpStatus = http.StatusGatewayTimeout // 504
	case 1002: // 触发RPM限流
		httpStatus = http.StatusTooManyRequests // 429
	case 1004: // 鉴权失败
		httpStatus = http.StatusUnauthorized // 401
	case 1008: // 余额不足
		httpStatus = http.StatusPaymentRequired // 402
	case 1013: // 服务内部错误
		httpStatus = http.StatusInternalServerError // 500
	case 1027: // 输出内容错误
		httpStatus = http.StatusForbidden // 403
	case 1039: // 触发TPM限流
		httpStatus = http.StatusTooManyRequests // 429
	case 2013: // 输入格式信息不正常
		httpStatus = http.StatusBadRequest // 400
	default:
		httpStatus = http.StatusInternalServerError // 500
	}

	logger.SysLog(fmt.Sprintf("Mapped MiniMax error code %d to HTTP status %d", minimaxCode, httpStatus))
	return httpStatus
}
