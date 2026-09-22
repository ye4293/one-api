package model

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/songquanpeng/one-api/common"
	relayconstant "github.com/songquanpeng/one-api/relay/constant"
)

type responsesConstraintKey struct{}
type failedResponseConsumptionKey struct{}

// WithFailedResponseConsumption 失败请求仍可结算用量，但不能同时生成成功评分样本。
func WithFailedResponseConsumption(ctx context.Context) context.Context {
	return context.WithValue(ctx, failedResponseConsumptionKey{}, true)
}

func failedResponseConsumption(ctx context.Context) bool {
	failed, _ := ctx.Value(failedResponseConsumptionKey{}).(bool)
	return failed
}

// ResponseSource 保存实际发送时的来源；指纹不包含明文凭证。
type ResponseSource struct {
	Provider     string `json:"provider"`
	ChannelID    int    `json:"channel_id,omitempty"`
	KeyIndex     int    `json:"key_index,omitempty"`
	KeyHash      string `json:"key_hash,omitempty"`
	ResourceHash string `json:"resource_hash,omitempty"`
}

type ResponsesConstraint struct {
	Provider string
	Resource *ResponseSource
}

type ResponsesStateError struct {
	Code    string
	Message string
	Status  int
}

func (e *ResponsesStateError) Error() string { return e.Message }
func NewResponsesStateError(code, message string, status int) error {
	return &ResponsesStateError{Code: code, Message: message, Status: status}
}

var ErrNoCompatibleResponseChannel = NewResponsesStateError("responses_no_compatible_channel", "没有符合状态来源约束的可用渠道", 503)
var ErrResponsesResourceChanged = NewResponsesStateError("responses_resource_changed", "原渠道的 Provider、凭证或资源配置已改变", 409)

func WithResponsesConstraint(ctx context.Context, constraint ResponsesConstraint) context.Context {
	if constraint.Resource != nil {
		copy := *constraint.Resource
		constraint.Resource = &copy
	}
	return context.WithValue(ctx, responsesConstraintKey{}, constraint)
}

func GetResponsesConstraint(ctx context.Context) (ResponsesConstraint, bool) {
	constraint, ok := ctx.Value(responsesConstraintKey{}).(ResponsesConstraint)
	return constraint, ok
}

func ResponseFingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (channel *Channel) ResponseSource(key string, index int) (ResponseSource, error) {
	provider, err := channel.EffectiveProvider()
	if err != nil {
		return ResponseSource{}, err
	}
	cfg := map[string]json.RawMessage{}
	if channel.Config != "" {
		_ = json.Unmarshal([]byte(channel.Config), &cfg)
	}
	// 只绑定影响上游资源命名空间的配置，表单新增无关默认开关不会改变资源身份。
	resourceConfig := map[string]json.RawMessage{}
	for _, name := range []string{"region", "ak", "sk", "user_id", "api_version", "library_id", "vertex_ai_project_id", "vertex_ai_adc", "vertex_model_region"} {
		if value, ok := cfg[name]; ok {
			var compact bytes.Buffer
			if err := json.Compact(&compact, value); err != nil {
				return ResponseSource{}, err
			}
			value = compact.Bytes()
			if string(value) != `""` && string(value) != "null" && string(value) != "{}" {
				resourceConfig[name] = value
			}
		}
	}
	endpoint := strings.TrimRight(channel.GetBaseURL(), "/")
	if endpoint == "" && channel.Type >= 0 && channel.Type < len(common.ChannelBaseURLs) {
		endpoint = strings.TrimRight(common.ChannelBaseURLs[channel.Type], "/")
	}
	if u, err := url.Parse(endpoint); err == nil {
		u.Scheme = strings.ToLower(u.Scheme)
		u.Host = strings.ToLower(u.Host)
		endpoint = u.String()
	}
	headers := map[string]string{}
	for name, value := range channel.GetHeaderOverride() {
		headers[http.CanonicalHeaderKey(name)] = value
	}
	mapping := channel.GetModelMapping()
	if mapping == nil {
		mapping = map[string]string{}
	}
	identity, err := json.Marshal(struct {
		Type     int
		Endpoint string
		Config   map[string]json.RawMessage
		Other    string
		Headers  map[string]string
		Mapping  map[string]string
	}{channel.Type, endpoint, resourceConfig, channel.Other, headers, mapping})
	if err != nil {
		return ResponseSource{}, err
	}
	return ResponseSource{provider, channel.Id, index, ResponseFingerprint(key), ResponseFingerprint(string(identity))}, nil
}

func (constraint ResponsesConstraint) Matches(channel *Channel) bool {
	if channel == nil || channel.Type <= 0 || channel.Type >= common.ChannelTypeDummy {
		return false
	}
	apiType := relayconstant.ChannelType2APIType(channel.Type)
	if apiType != relayconstant.APITypeOpenAI && apiType != relayconstant.APITypeXAI {
		return false
	}
	provider, err := channel.EffectiveProvider()
	if err != nil || (constraint.Provider != "" && provider != constraint.Provider) {
		return false
	}
	if constraint.Resource != nil && constraint.Resource.ChannelID != channel.Id {
		return false
	}
	for index := range channel.ParseKeys() {
		if key, err := channel.GetKeyByIndex(index); err == nil && strings.TrimSpace(key) != "" {
			return true
		}
	}
	return false
}

// ValidateResponsesChannel 同时验证真实 ability，防止指定渠道及亲和捷径绕过权限分组。
func ValidateResponsesChannel(ctx context.Context, channel *Channel, group, modelName string) error {
	constraint, active := GetResponsesConstraint(ctx)
	if !active {
		return nil
	}
	if channel == nil || channel.Status != common.ChannelStatusEnabled {
		return ErrNoCompatibleResponseChannel
	}
	provider, err := channel.EffectiveProvider()
	if err != nil {
		return err
	}
	if constraint.Resource != nil {
		key, keyErr := channel.GetKeyByIndex(constraint.Resource.KeyIndex)
		if keyErr != nil || key == "" {
			return ErrResponsesResourceChanged
		}
		source, sourceErr := channel.ResponseSource(key, constraint.Resource.KeyIndex)
		if sourceErr != nil {
			return sourceErr
		}
		if source != *constraint.Resource {
			return ErrResponsesResourceChanged
		}
	}
	if constraint.Provider != "" && provider != constraint.Provider {
		return ErrNoCompatibleResponseChannel
	}
	if !constraint.Matches(channel) {
		return ErrNoCompatibleResponseChannel
	}
	var count int64
	err = DB.WithContext(ctx).Model(&Ability{}).Where(&Ability{Group: group, Model: modelName, ChannelId: channel.Id}).Where("enabled = ?", true).Count(&count).Error
	if err != nil {
		return fmt.Errorf("校验渠道能力失败: %w", err)
	}
	if count == 0 {
		return ErrNoCompatibleResponseChannel
	}
	return nil
}

func selectResponsesChannel(ctx context.Context, group, modelName string, skip int, excluded []int) (*Channel, int, error) {
	constraint, _ := GetResponsesConstraint(ctx)
	if constraint.Resource != nil {
		if isExcludedChannel(constraint.Resource.ChannelID, excluded) {
			return nil, -1, ErrNoCompatibleResponseChannel
		}
		var channel Channel
		err := DB.WithContext(ctx).First(&channel, constraint.Resource.ChannelID).Error
		if err != nil {
			return nil, -1, err
		}
		if err = ValidateResponsesChannel(ctx, &channel, group, modelName); err != nil {
			return nil, -1, err
		}
		return &channel, constraint.Resource.KeyIndex, nil
	}
	channel, err := CacheGetRandomSatisfiedChannelWithCapability(ctx, group, modelName, func(ch *Channel, _ ChannelConfig) bool {
		return constraint.Matches(ch)
	}, skip, "", excluded)
	return channel, -1, err
}

func IsResponsesStateError(err error) bool {
	var stateErr *ResponsesStateError
	return errors.As(err, &stateErr)
}
