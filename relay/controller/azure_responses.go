package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/audit"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/relay/channel"
	"github.com/songquanpeng/one-api/relay/util"
)

// doOpenaiResponseRequest 仅在 Azure 明确拒绝加密历史时降级重试，避免破坏可用的推理上下文。
func doOpenaiResponseRequest(c *gin.Context, meta *util.RelayMeta, adaptor channel.Adaptor, body []byte) (*http.Response, error) {
	resp, err := adaptor.DoRequest(c, meta, bytes.NewReader(body))
	if err != nil || resp == nil || resp.Body == nil || meta.ChannelType != common.ChannelTypeAzure ||
		strings.TrimRight(c.Request.URL.Path, "/") != "/v1/responses" || resp.StatusCode != http.StatusBadRequest {
		return resp, err
	}

	// 只检查有限的错误正文；未命中时恢复完整响应，交给原有错误处理流程。
	const maxErrorBody = 64 << 10
	errorBody, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody+1))
	if err != nil {
		util.CloseResponseBodyGracefully(resp)
		return nil, fmt.Errorf("读取 Azure 错误响应失败：%w", err)
	}
	resp.Body = &combinedReadCloser{
		Reader: io.MultiReader(bytes.NewReader(errorBody), resp.Body),
		Closer: resp.Body,
	}
	if len(errorBody) > maxErrorBody || !isAzureEncryptedContentError(errorBody) {
		return resp, nil
	}

	cleanBody, reasoningItems, compactionItems, err := sanitizeAzureResponsesInput(body)
	if err != nil || reasoningItems+compactionItems == 0 || c.Request.Context().Err() != nil {
		return resp, nil
	}
	util.CloseResponseBodyGracefully(resp)
	audit.SetConvertedBody(c, string(cleanBody))
	logger.Warnf(c.Request.Context(), "Azure 加密历史验证失败，清理后向同一渠道重试一次：渠道=%d，推理项=%d，压缩项=%d", meta.ChannelId, reasoningItems, compactionItems)
	if compactionItems > 0 {
		logger.Warnf(c.Request.Context(), "Azure 降级重试移除了不可验证的压缩项，压缩项承载的历史上下文将丢失：渠道=%d", meta.ChannelId)
	}
	// 不改写缓存的原始请求，后续跨渠道重试仍可使用原来的加密历史。
	return adaptor.DoRequest(c, meta, bytes.NewReader(cleanBody))
}

func isAzureEncryptedContentError(body []byte) bool {
	var response struct {
		Error struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return false
	}
	if response.Error.Code == "invalid_encrypted_content" || response.Error.Type == "invalid_encrypted_content" {
		return true
	}
	// 兼容只给出通用错误码的 Azure 响应，但不把字段缺失等普通参数错误当成验证失败。
	message := strings.ToLower(response.Error.Message)
	if !strings.Contains(message, "encrypted content") && !strings.Contains(message, "encrypted_content") {
		return false
	}
	return strings.Contains(message, "could not be verified") ||
		strings.Contains(message, "cannot be verified") ||
		strings.Contains(message, "failed to verify") ||
		strings.Contains(message, "verification failed")
}

// sanitizeAzureResponsesInput 清理跨平台不可复用的历史字段，保留消息、摘要和工具调用关系。
// 使用 RawMessage 保留未知字段和大整数；压缩密文无法解密，只能移除整个压缩项。
func sanitizeAzureResponsesInput(body []byte) ([]byte, int, int, error) {
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		return body, 0, 0, err
	}
	var input []json.RawMessage
	if err := json.Unmarshal(request["input"], &input); err != nil || len(input) == 0 {
		return body, 0, 0, nil
	}
	cleanInput := make([]json.RawMessage, 0, len(input))
	reasoningItems, compactionItems := 0, 0
	for _, raw := range input {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(raw, &item); err != nil {
			cleanInput = append(cleanInput, raw)
			continue
		}
		var itemType string
		_ = json.Unmarshal(item["type"], &itemType)
		switch itemType {
		case "reasoning":
			_, hasID := item["id"]
			_, hasEncryptedContent := item["encrypted_content"]
			if hasID || hasEncryptedContent {
				delete(item, "id")
				delete(item, "encrypted_content")
				var err error
				raw, err = json.Marshal(item)
				if err != nil {
					return body, 0, 0, err
				}
				reasoningItems++
			}
		case "compaction":
			if _, encrypted := item["encrypted_content"]; encrypted {
				compactionItems++
				continue
			}
		}
		cleanInput = append(cleanInput, raw)
	}
	if reasoningItems+compactionItems == 0 {
		return body, 0, 0, nil
	}
	var err error
	request["input"], err = json.Marshal(cleanInput)
	if err != nil {
		return body, 0, 0, err
	}
	cleanBody, err := json.Marshal(request)
	return cleanBody, reasoningItems, compactionItems, err
}
