package controller

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/stretchr/testify/require"
)

func TestProviderChannelCreateAndUpdateAPI(t *testing.T) {
	setupEvaluatorTestDB(t)
	r := gin.New()
	r.POST("/api/channel", AddChannel)
	r.PUT("/api/channel", UpdateChannel)
	send := func(method, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		request := httptest.NewRequest(method, "/api/channel", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, request)
		return w
	}
	created := send("POST", `{"type":1,"name":"provider-test","key":"key","models":"m","group":"default","config":"{\"provider\":\" Anthropic Claude \",\"api_version\":\"v1\",\"unknown\":9007199254740993}"}`)
	require.Equal(t, 200, created.Code)
	var result struct {
		Success   bool `json:"success"`
		ChannelID int  `json:"channel_id"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &result))
	require.True(t, result.Success, created.Body.String())
	channel, err := dbmodel.GetChannelById(result.ChannelID, true)
	require.NoError(t, err)
	provider, err := channel.EffectiveProvider()
	require.NoError(t, err)
	require.Equal(t, "Anthropic Claude", provider)
	body, _ := json.Marshal(map[string]interface{}{"id": channel.Id, "config": `{"provider":""}`})
	updated := send("PUT", string(body))
	require.Equal(t, 200, updated.Code)
	require.Contains(t, updated.Body.String(), `"success":true`)
	channel, err = dbmodel.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.JSONEq(t, `{"provider":"","api_version":"v1","unknown":9007199254740993}`, channel.Config)
	provider, err = channel.EffectiveProvider()
	require.NoError(t, err)
	require.Equal(t, "OpenAI", provider)
	for _, invalid := range []string{`{"provider":null}`, `{"provider":42}`, `{"provider":[]}`} {
		body, _ = json.Marshal(map[string]interface{}{"id": channel.Id, "config": invalid})
		require.Equal(t, 400, send("PUT", string(body)).Code)
	}
	body, _ = json.Marshal(map[string]interface{}{"id": channel.Id, "config": ""})
	require.Equal(t, 200, send("PUT", string(body)).Code)
	channel, err = dbmodel.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.Empty(t, channel.Config)
}
