package common

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ExtractPreviousResponseID 从 /v1/responses 请求体中读取 previous_response_id
// 返回空串表示未提供或解析失败
func ExtractPreviousResponseID(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	res := gjson.GetBytes(body, "previous_response_id")
	if !res.Exists() {
		return ""
	}
	return strings.TrimSpace(res.String())
}

// extractReasoningEncryptedHashes 从给定 JSON 字段（input 或 output 数组）中提取
// 所有 type=reasoning 的 encrypted_content 并 SHA-256 后返回十六进制哈希数组
func extractReasoningEncryptedHashes(body []byte, field string) []string {
	if len(body) == 0 {
		return nil
	}
	var hashes []string
	gjson.GetBytes(body, field).ForEach(func(_, item gjson.Result) bool {
		if item.Get("type").String() != "reasoning" {
			return true
		}
		enc := item.Get("encrypted_content").String()
		if enc == "" {
			return true
		}
		sum := sha256.Sum256([]byte(enc))
		hashes = append(hashes, hex.EncodeToString(sum[:]))
		return true
	})
	return hashes
}

// ExtractEncryptedContentHashes 从请求体的 input[] 中提取所有 reasoning.encrypted_content 字段并 SHA-256
// 返回十六进制字符串数组（按出现顺序），长度 0 表示没有任何 encrypted_content
func ExtractEncryptedContentHashes(body []byte) []string {
	return extractReasoningEncryptedHashes(body, "input")
}

// ExtractOutputEncryptedContentHashes 从响应体 output[] 中提取 reasoning.encrypted_content 哈希
// 用途：响应成功后把本轮新生成的 reasoning 绑定到当前渠道，下一轮续轮时可定向回同渠道
func ExtractOutputEncryptedContentHashes(body []byte) []string {
	return extractReasoningEncryptedHashes(body, "output")
}

// StripEncryptedContentFromInput 清除请求体 input[] 中所有 reasoning.encrypted_content 字段
// 返回清理后的 body。其他字段保持不变
func StripEncryptedContentFromInput(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}
	inputArr := gjson.GetBytes(body, "input")
	if !inputArr.IsArray() {
		return body, nil
	}
	out := body
	var err error
	items := inputArr.Array()
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Get("type").String() != "reasoning" {
			continue
		}
		if !items[i].Get("encrypted_content").Exists() {
			continue
		}
		out, err = sjson.DeleteBytes(out, "input."+strconv.Itoa(i)+".encrypted_content")
		if err != nil {
			return out, fmt.Errorf("strip encrypted_content at input[%d]: %w", i, err)
		}
	}
	return out, nil
}

// IsInvalidEncryptedContentError 判断错误是否为 encrypted_content 解密失败
// 触发 strip-and-retry fallback 的关键判定
func IsInvalidEncryptedContentError(code, message string) bool {
	lowerCode := strings.ToLower(code)
	lowerMsg := strings.ToLower(message)
	if lowerCode == "invalid_encrypted_content" {
		return true
	}
	// 规划文档测试用例明确：status_401 不应匹配，只认 4xx 中的 400
	// （invalid_encrypted_content 上游只会以 400 返回，401 是认证失败场景）
	if lowerCode == "status_400" {
		if strings.Contains(lowerMsg, "invalid_encrypted_content") {
			return true
		}
		if strings.Contains(lowerMsg, "could not be decrypted or parsed") {
			return true
		}
	}
	return false
}
