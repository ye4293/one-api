package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
)

const ResponsesProviderHeader = "X-Provider"
const responseAttemptSourceKey = "responses_attempt_source"

type responseReference struct {
	kind  string
	value string
}

func IsResponsesPath(path string) bool {
	path = strings.TrimRight(strings.Split(path, "?")[0], "/")
	return path == "/v1/responses" || path == "/v1/responses/compact"
}

func stateReferenceKey(userID int, ref responseReference) string {
	return fmt.Sprintf("responses-state:v1:%d:%s:%s", userID, ref.kind, model.ResponseFingerprint(ref.value))
}

func responseObject(raw []byte) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, fmt.Errorf("Responses 数据必须为 JSON 对象")
	}
	return obj, nil
}

func responseString(obj map[string]json.RawMessage, field string) (string, error) {
	raw, exists := obj[field]
	if !exists || string(raw) == "null" {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("%s 必须为字符串", field)
	}
	return value, nil
}

func conversationReference(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var id string
	if json.Unmarshal(raw, &id) == nil {
		return id, nil
	}
	obj, err := responseObject(raw)
	if err != nil {
		return "", err
	}
	id, err = responseString(obj, "id")
	if err == nil && id == "" {
		err = fmt.Errorf("conversation 对象缺少 id")
	}
	return id, err
}

// requestResponseReferences 只读取协议中的状态位置，不扫描工具参数中的 id/call_id。
func requestResponseReferences(body []byte) ([]responseReference, error) {
	obj, err := responseObject(body)
	if err != nil {
		return nil, err
	}
	var refs []responseReference
	id, err := responseString(obj, "previous_response_id")
	if err != nil {
		return nil, err
	}
	if id != "" {
		refs = append(refs, responseReference{"response", id})
	}
	id, err = conversationReference(obj["conversation"])
	if err != nil {
		return nil, err
	}
	if id != "" {
		refs = append(refs, responseReference{"conversation", id})
	}
	raw := obj["input"]
	if len(raw) == 0 || string(raw) == "null" {
		return refs, nil
	}
	var textInput string
	if json.Unmarshal(raw, &textInput) == nil {
		return refs, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("input 必须是字符串或数组")
	}
	for _, rawItem := range items {
		item, err := responseObject(rawItem)
		if err != nil {
			return nil, err
		}
		kind, err := responseString(item, "type")
		if err != nil {
			return nil, err
		}
		switch kind {
		case "reasoning", "compaction":
			cipher, err := responseString(item, "encrypted_content")
			if err != nil {
				return nil, err
			}
			if cipher != "" {
				refs = append(refs, responseReference{"encrypted", cipher})
				continue
			}
			id, err := responseString(item, "id")
			if err != nil {
				return nil, err
			}
			if id != "" {
				refs = append(refs, responseReference{"item", id})
			}
		case "item_reference":
			id, err := responseString(item, "id")
			if err != nil {
				return nil, err
			}
			if id == "" {
				return nil, fmt.Errorf("item_reference 缺少 id")
			}
			refs = append(refs, responseReference{"item", id})
		}
	}
	return refs, nil
}

func ResolveResponsesConstraint(ctx context.Context, userID int, body []byte, explicitProvider, responseID string) (model.ResponsesConstraint, error) {
	constraint := model.ResponsesConstraint{}
	provider, err := model.NormalizeProvider(explicitProvider)
	if err != nil {
		return constraint, model.NewResponsesStateError("responses_invalid_provider", err.Error(), 400)
	}
	constraint.Provider = provider
	refs, err := requestResponseReferences(body)
	if err != nil {
		return constraint, model.NewResponsesStateError("responses_invalid_request", err.Error(), 400)
	}
	if responseID != "" {
		refs = append(refs, responseReference{"response", responseID})
	}
	keys := make([]string, 0, len(refs))
	unique := map[string]responseReference{}
	for _, ref := range refs {
		key := stateReferenceKey(userID, ref)
		if _, exists := unique[key]; !exists {
			keys = append(keys, key)
			unique[key] = ref
		}
	}
	if len(keys) == 0 {
		return constraint, nil
	}
	sources, err := currentResponseStateStore().Lookup(ctx, keys)
	if err != nil {
		logger.Warnf(ctx, "读取 Responses 状态索引失败: %v", err)
		return constraint, responseStateUnavailable()
	}
	for _, key := range keys {
		source, found := sources[key]
		if !found {
			if unique[key].kind == "encrypted" && provider != "" {
				continue
			}
			return constraint, model.NewResponsesStateError("responses_state_unknown", "历史状态未知或已过期，无法确定原始来源", 409)
		}
		if constraint.Provider != "" && constraint.Provider != source.Provider {
			return constraint, responseStateConflict()
		}
		constraint.Provider = source.Provider
		if unique[key].kind != "encrypted" {
			if source.ChannelID <= 0 || source.KeyHash == "" || source.ResourceHash == "" {
				return constraint, responseStateUnavailable()
			}
			if constraint.Resource != nil && *constraint.Resource != source {
				return constraint, responseStateConflict()
			}
			copy := source
			constraint.Resource = &copy
		}
	}
	return constraint, nil
}

func SetResponseAttemptSource(c *gin.Context, source model.ResponseSource) {
	c.Set(responseAttemptSourceKey, source)
}

// RegisterResponseOutput 仅登记上游实际输出的资源；同域密文保存 Provider，引用保存精确来源。
func RegisterResponseOutput(c *gin.Context, payload []byte) error {
	value, active := c.Get(responseAttemptSourceKey)
	if !active {
		return nil
	}
	source, ok := value.(model.ResponseSource)
	if !ok || source.Provider == "" {
		return responseStateUnavailable()
	}
	obj, err := responseObject(payload)
	if err != nil {
		return err
	}
	records := map[string]model.ResponseSource{}
	add := func(kind, value string) {
		if value == "" {
			return
		}
		stored := source
		if kind == "encrypted" {
			stored = model.ResponseSource{Provider: source.Provider}
		}
		records[stateReferenceKey(c.GetInt("id"), responseReference{kind, value})] = stored
	}
	addItem := func(raw json.RawMessage) error {
		item, err := responseObject(raw)
		if err != nil {
			return err
		}
		id, err := responseString(item, "id")
		if err != nil {
			return err
		}
		add("item", id)
		kind, err := responseString(item, "type")
		if err != nil {
			return err
		}
		if kind == "reasoning" || kind == "compaction" {
			cipher, err := responseString(item, "encrypted_content")
			if err != nil {
				return err
			}
			add("encrypted", cipher)
		}
		return nil
	}
	typeName, _ := responseString(obj, "type")
	if raw := obj["item"]; len(raw) > 0 && string(raw) != "null" {
		if err := addItem(raw); err != nil {
			return err
		}
	}
	response := obj
	if raw := obj["response"]; len(raw) > 0 && string(raw) != "null" {
		response, err = responseObject(raw)
		if err != nil {
			return err
		}
	} else if strings.HasPrefix(typeName, "response.") || typeName == "error" {
		response = nil
	}
	if response != nil {
		id, err := responseString(response, "id")
		if err != nil {
			return err
		}
		add("response", id)
		conversation, err := conversationReference(response["conversation"])
		if err != nil {
			return err
		}
		add("conversation", conversation)
		var output []json.RawMessage
		if raw := response["output"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &output); err != nil {
				return err
			}
		}
		for _, item := range output {
			if err := addItem(item); err != nil {
				return err
			}
		}
	}
	if len(records) == 0 {
		return nil
	}
	if err := currentResponseStateStore().Write(c.Request.Context(), records); err != nil {
		if model.IsResponsesStateError(err) {
			return err
		}
		logger.Warnf(c.Request.Context(), "登记 Responses 状态索引失败: %v", err)
		return responseStateUnavailable()
	}
	return nil
}
