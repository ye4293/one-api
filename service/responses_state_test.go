package service

import (
	"context"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/model"
	"github.com/stretchr/testify/require"
)

func setupResponseLocalStore(t *testing.T) {
	previous, enabled := localResponseStates, common.RedisEnabled
	localResponseStates = newLocalResponseStateStore(100)
	common.RedisEnabled = false
	t.Cleanup(func() { localResponseStates = previous; common.RedisEnabled = enabled })
}

func TestResponsesStateRoundTripAndIsolation(t *testing.T) {
	setupResponseLocalStore(t)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Set("id", 7)
	source := model.ResponseSource{Provider: "OpenAI", ChannelID: 1, KeyIndex: 2, KeyHash: "key-hash", ResourceHash: "resource-hash"}
	SetResponseAttemptSource(c, source)
	require.NoError(t, RegisterResponseOutput(c, []byte(`{"id":"resp_a","conversation":{"id":"conv_a"},"output":[{"type":"reasoning","id":"rs_a","encrypted_content":"cipher_a"}]}`)))
	cases := []struct {
		body, provider string
		exact          bool
		code           string
	}{
		{`{"input":[{"type":"reasoning","id":"rs_a","encrypted_content":"cipher_a"}]}`, "", false, ""},
		{`{"previous_response_id":"resp_a"}`, "", true, ""},
		{`{"conversation":"conv_a"}`, "", true, ""},
		{`{"conversation":{"id":"conv_a"}}`, "", true, ""},
		{`{"input":[{"type":"reasoning","id":"rs_a"}]}`, "", true, ""},
		{`{"input":[{"type":"reasoning","encrypted_content":"cipher_a"}]}`, "Azure OpenAI", false, "responses_state_conflict"},
		{`{"previous_response_id":"unknown"}`, "OpenAI", false, "responses_state_unknown"},
		{`{"input":[{"type":"compaction","encrypted_content":"unknown"}]}`, "OpenAI", false, ""},
		{`{"input":[{"type":"compaction","encrypted_content":"unknown"}]}`, "", false, "responses_state_unknown"},
		{`{"input":[{"type":"function_call_output","call_id":"unknown","output":{"id":"unknown"}}]}`, "", false, ""},
	}
	for _, tc := range cases {
		constraint, err := ResolveResponsesConstraint(context.Background(), 7, []byte(tc.body), tc.provider, "")
		if tc.code != "" {
			var stateErr *model.ResponsesStateError
			require.ErrorAs(t, err, &stateErr)
			require.Equal(t, tc.code, stateErr.Code)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, tc.exact, constraint.Resource != nil)
		if tc.exact {
			require.Equal(t, source, *constraint.Resource)
		}
	}
	_, err := ResolveResponsesConstraint(context.Background(), 8, []byte(`{"previous_response_id":"resp_a"}`), "", "")
	require.Error(t, err)
	// 同 Provider 的密文允许跨渠道再次输出，但同一服务端引用不能改写来源。
	source.ChannelID = 2
	SetResponseAttemptSource(c, source)
	require.NoError(t, RegisterResponseOutput(c, []byte(`{"id":"resp_b","output":[{"type":"reasoning","id":"rs_b","encrypted_content":"cipher_a"}]}`)))
	require.Error(t, RegisterResponseOutput(c, []byte(`{"id":"resp_a","output":[]}`)))
	_, err = ResolveResponsesConstraint(context.Background(), 7, []byte(`{"previous_response_id":"resp_a","input":[{"type":"item_reference","id":"rs_b"}]}`), "", "")
	require.Error(t, err)
}

func responseStoreContract(t *testing.T, store responseStateStore) {
	ctx := context.Background()
	one := model.ResponseSource{Provider: "OpenAI"}
	two := model.ResponseSource{Provider: "Azure OpenAI"}
	require.NoError(t, store.Write(ctx, map[string]model.ResponseSource{"a": one}))
	require.NoError(t, store.Write(ctx, map[string]model.ResponseSource{"a": one}))
	require.Error(t, store.Write(ctx, map[string]model.ResponseSource{"a": two, "b": two}))
	values, err := store.Lookup(ctx, []string{"a", "b"})
	require.NoError(t, err)
	require.Equal(t, one, values["a"])
	require.NotContains(t, values, "b")
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = store.Write(ctx, map[string]model.ResponseSource{"c": one}) }()
	}
	wg.Wait()
	values, err = store.Lookup(ctx, []string{"c"})
	require.NoError(t, err)
	require.Equal(t, one, values["c"])
}

func TestResponsesLocalStateStore(t *testing.T) {
	store := newLocalResponseStateStore(2)
	responseStoreContract(t, store)
	now := time.Now()
	store.now = func() time.Time { return now }
	require.NoError(t, store.Write(context.Background(), map[string]model.ResponseSource{"ttl": {Provider: "OpenAI"}}))
	now = now.Add(responseStateTTL + time.Second)
	values, err := store.Lookup(context.Background(), []string{"ttl"})
	require.NoError(t, err)
	require.Empty(t, values)
}

func TestResponsesRedisStateStore(t *testing.T) {
	binary, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("未安装 redis-server")
	}
	socket := filepath.Join(t.TempDir(), "redis.sock")
	cmd := exec.Command(binary, "--port", "0", "--unixsocket", socket, "--save", "", "--appendonly", "no")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	client := redis.NewClient(&redis.Options{Network: "unix", Addr: socket})
	t.Cleanup(func() { _ = client.Close() })
	deadline := time.Now().Add(5 * time.Second)
	for client.Ping(context.Background()).Err() != nil {
		if time.Now().After(deadline) {
			t.Fatal("测试 Redis 未启动")
		}
		time.Sleep(10 * time.Millisecond)
	}
	previous := common.RDB
	common.RDB = client
	t.Cleanup(func() { common.RDB = previous })
	responseStoreContract(t, redisResponseStateStore{})
	require.Greater(t, client.TTL(context.Background(), "a").Val(), 6*24*time.Hour)
	require.NoError(t, client.Close())
	_, err = (redisResponseStateStore{}).Lookup(context.Background(), []string{"a"})
	require.Error(t, err)
}

func TestResponsesRedisUnavailableDoesNotFallback(t *testing.T) {
	setupResponseLocalStore(t)
	common.RedisEnabled = true
	previous := common.RDB
	common.RDB = nil
	t.Cleanup(func() { common.RDB = previous })
	_, err := ResolveResponsesConstraint(context.Background(), 1, []byte(`{"previous_response_id":"a"}`), "", "")
	var stateErr *model.ResponsesStateError
	require.ErrorAs(t, err, &stateErr)
	require.Equal(t, 503, stateErr.Status)
}
