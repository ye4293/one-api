package model

import (
	"context"
	"testing"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/stretchr/testify/require"
)

func TestChannelProviderConfig(t *testing.T) {
	for kind, name := range map[int]string{1: "OpenAI", 14: "Anthropic Claude", 3: "Azure OpenAI"} {
		channel := &Channel{Type: kind}
		provider, err := channel.EffectiveProvider()
		require.NoError(t, err)
		require.Equal(t, name, provider)
		channel.Config = `{"provider":" Anthropic Claude "}`
		provider, err = channel.EffectiveProvider()
		require.NoError(t, err)
		require.Equal(t, "Anthropic Claude", provider)
		channel.Config = `{"provider":"  "}`
		provider, err = channel.EffectiveProvider()
		require.NoError(t, err)
		require.Equal(t, name, provider)
	}
	previous := `{"provider":"OpenAI","api_version":"v1","unknown":{"number":9007199254740993}}`
	merged, err := MergeChannelConfig(previous, `{"provider":""}`)
	require.NoError(t, err)
	require.JSONEq(t, `{"provider":"","api_version":"v1","unknown":{"number":9007199254740993}}`, merged)
	merged, err = MergeChannelConfig(previous, `{"region":"east"}`)
	require.NoError(t, err)
	require.Contains(t, merged, `"provider":"OpenAI"`)
	merged, err = MergeChannelConfig(previous, "")
	require.NoError(t, err)
	require.Empty(t, merged)
	for _, invalid := range []string{`null`, `[]`, `{"provider":null}`, `{"provider":1}`, `{"provider":{}}`, `{"provider":["OpenAI"]}`, `{"provider":"a\nb"}`} {
		_, err := MergeChannelConfig("", invalid)
		require.Error(t, err, invalid)
	}
}

func TestResponsesChannelSelection(t *testing.T) {
	setupCircuitDB(t)
	old := config.DynamicPriorityApplyEnabled
	defer func() { config.DynamicPriorityApplyEnabled = old }()
	weight := uint(1)
	for i, kind := range []int{3, 1, 1} {
		priority := int64(100 - i)
		channel := Channel{Id: i + 1, Type: kind, Name: "测试", Key: "key", Models: "m", Group: "default", Status: common.ChannelStatusEnabled, Priority: &priority, Weight: &weight}
		require.NoError(t, channel.Insert())
		require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ?", channel.Id).Update("dynamic_priority", priority).Error)
	}
	ctx := WithResponsesConstraint(context.Background(), ResponsesConstraint{Provider: "OpenAI"})
	for _, dynamic := range []bool{false, true} {
		config.DynamicPriorityApplyEnabled = dynamic
		channel, _, err := CacheGetRandomSatisfiedChannel(ctx, "default", "m", 0, "")
		require.NoError(t, err)
		require.NotEqual(t, 1, channel.Id)
		channel, _, err = CacheGetRandomSatisfiedChannel(ctx, "default", "m", 0, "", []int{2})
		require.NoError(t, err)
		require.Equal(t, 3, channel.Id)
		_, _, err = CacheGetRandomSatisfiedChannel(ctx, "default", "m", 0, "", []int{2, 3})
		require.ErrorIs(t, err, ErrNoCompatibleResponseChannel)
	}
	channel, err := GetChannelById(2, true)
	require.NoError(t, err)
	source, err := channel.ResponseSource("key", 0)
	require.NoError(t, err)
	ctx = WithResponsesConstraint(context.Background(), ResponsesConstraint{Provider: source.Provider, Resource: &source})
	require.NoError(t, ValidateResponsesChannel(ctx, channel, "default", "m"))
	channel.Key = "changed"
	require.ErrorIs(t, ValidateResponsesChannel(ctx, channel, "default", "m"), ErrResponsesResourceChanged)
	channel.Key = "key"
	channel.Config = `{"provider":"Azure OpenAI"}`
	require.ErrorIs(t, ValidateResponsesChannel(ctx, channel, "default", "m"), ErrResponsesResourceChanged)
}

func TestChannelConfigClearPersists(t *testing.T) {
	setupCircuitDB(t)
	channel := Channel{Type: 1, Key: "key", Models: "m", Group: "default", Config: `{"provider":"Anthropic Claude"}`}
	require.NoError(t, channel.Insert())
	channel.Config = ""
	require.NoError(t, channel.Update())
	reloaded, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.Empty(t, reloaded.Config)
	provider, err := reloaded.EffectiveProvider()
	require.NoError(t, err)
	require.Equal(t, "OpenAI", provider)
}

func TestResponsesResourceFingerprintNormalizesFormDefaults(t *testing.T) {
	channel := Channel{Id: 1, Type: 1, Key: "key"}
	before, err := channel.ResponseSource("key", 0)
	require.NoError(t, err)
	empty := ""
	channel.HeaderOverride = &empty
	channel.ModelMapping = &empty
	channel.Config = `{"provider":"OpenAI","region":"","vertex_model_region":{},"support_count_tokens":false,"vertex_key_type":"json"}`
	after, err := channel.ResponseSource("key", 0)
	require.NoError(t, err)
	require.Equal(t, before, after)
	endpoint := "https://different-resource.example"
	channel.BaseURL = &endpoint
	after, err = channel.ResponseSource("key", 0)
	require.NoError(t, err)
	require.NotEqual(t, before.ResourceHash, after.ResourceHash)
}
