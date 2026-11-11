package middleware

import (
	"fmt"
	"github.com/songquanpeng/one-api/model"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"

	"github.com/songquanpeng/one-api/common"
)

// extractResponsesCorrelationID 提取 responses 请求的关联键（区域亲和 / 渠道钉扎用）。
//
// 优先级（高 → 低）：
//  1. body 里的 previous_response_id —— store=true 链式调用，服务端存有上一轮完整状态
//  2. body input[] 数组中最后一个带 encrypted_content 的 item 的 id —— store=false 无状态
//     回放（Codex 式用法）。encrypted_content 由产生它的 Azure 资源加密，跨区域必然 400；
//     reasoning / compaction 项的 id（rs_xxx）同样指向原资源。取"最后一个"因为它是最新
//     推理状态，多轮累积回放时数组尾部最贴近本渠道。
//
// 返回空串表示无关联键（首轮请求），调用方走正常随机选渠。
// 任何解析失败都静默返回空串（fail-open，绝不因亲和逻辑拒绝请求）。
func extractResponsesCorrelationID(c *gin.Context) string {
	body, err := common.GetRequestBody(c)
	if err != nil || len(body) == 0 {
		return ""
	}

	// 优先级 1：previous_response_id
	if pr := gjson.GetBytes(body, "previous_response_id"); pr.Exists() {
		if id := strings.TrimSpace(pr.String()); id != "" {
			return id
		}
	}

	// 优先级 2：input[] 尾部 encrypted_content item
	inputArr := gjson.GetBytes(body, "input")
	if !inputArr.Exists() || !inputArr.IsArray() {
		return ""
	}
	items := inputArr.Array()
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		if !item.IsObject() {
			continue
		}
		if item.Get("encrypted_content").Exists() {
			if id := strings.TrimSpace(item.Get("id").String()); id != "" {
				return id
			}
		}
	}
	return ""
}

// extractChatCorrelationID 提取 chat completions 请求的关联键。
//
// chat 协议无 previous_response_id 字段，多轮上下文走 messages[] 回放。
// assistant 消息里的 reasoning.encrypted_content 是上轮响应的唯一产物、且本轮必须
// 原样回传——取数组中最后一条含 encrypted_content 的消息，其 id（chatcmpl-xxx）
// 即为上轮响应 id，可反查 Redis 钉回原渠道。
//
// 返回空串表示无关联键，调用方走正常随机选渠。fail-open。
func extractChatCorrelationID(c *gin.Context) string {
	body, err := common.GetRequestBody(c)
	if err != nil || len(body) == 0 {
		return ""
	}
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return ""
	}
	msgs := messages.Array()
	for i := len(msgs) - 1; i >= 0; i-- {
		msg := msgs[i]
		if !msg.IsObject() {
			continue
		}
		if msg.Get("role").String() != "assistant" {
			continue
		}
		// 两种形态：reasoning_content 加密块（OpenAI 兼容扩展）或 reasoning 对象带 encrypted_content
		hasEncrypted := false
		if rc := msg.Get("reasoning_content"); rc.Exists() && strings.Contains(rc.String(), "encrypted") {
			hasEncrypted = true
		}
		if ec := msg.Get("reasoning.encrypted_content"); ec.Exists() && ec.String() != "" {
			hasEncrypted = true
		}
		if ec := msg.Get("encrypted_content"); ec.Exists() && ec.String() != "" {
			hasEncrypted = true
		}
		if !hasEncrypted {
			continue
		}
		if id := strings.TrimSpace(msg.Get("id").String()); id != "" {
			return id
		}
	}
	return ""
}

// captureResponseCorrelation keeps the parent separate from the ID returned by this request.
func captureResponseCorrelation(c *gin.Context) (string, error) {
	header := strings.TrimSpace(c.GetHeader("X-Response-ID"))
	path := strings.TrimRight(c.Request.URL.Path, "/")
	id := header
	if path == "/v1/responses" || path == "/v1/responses/compact" {
		body, err := common.GetRequestBody(c)
		if err != nil {
			return "", err
		}
		parent := strings.TrimSpace(gjson.GetBytes(body, "previous_response_id").String())
		if parent != "" {
			c.Request = c.Request.WithContext(model.WithPreviousResponseID(c.Request.Context(), parent))
			if header != "" && header != parent {
				return "", fmt.Errorf("X-Response-ID conflicts with previous_response_id")
			}
			id = parent
		} else if id == "" {
			id = extractResponsesCorrelationID(c)
		}
	} else if path == "/v1/chat/completions" && id == "" {
		id = extractChatCorrelationID(c)
	}
	if id != "" {
		c.Request = c.Request.WithContext(model.WithPreviousResponseID(c.Request.Context(), id))
	}
	return id, nil
}
