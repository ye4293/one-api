package middleware

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/service"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestResponsesDistributorBinding(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.User{}))
	oldDB, redisEnabled, memory, dynamic := model.DB, common.RedisEnabled, config.MemoryCacheEnabled, config.DynamicPriorityApplyEnabled
	model.DB = db
	common.RedisEnabled = false
	config.MemoryCacheEnabled = false
	config.DynamicPriorityApplyEnabled = false
	t.Cleanup(func() {
		model.DB = oldDB
		common.RedisEnabled = redisEnabled
		config.MemoryCacheEnabled = memory
		config.DynamicPriorityApplyEnabled = dynamic
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "test", Group: "default"}).Error)
	weight := uint(1)
	for id, kind := range map[int]int{1: 3, 2: 1} {
		priority := int64(10 - id)
		ch := model.Channel{Id: id, Type: kind, Key: "key", Models: "m", Group: "default", Weight: &weight, Priority: &priority}
		require.NoError(t, ch.Insert())
	}
	ch, err := model.GetChannelById(2, true)
	require.NoError(t, err)
	source, err := ch.ResponseSource("key", 0)
	require.NoError(t, err)
	seed, _ := gin.CreateTestContext(httptest.NewRecorder())
	seed.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	seed.Set("id", 1)
	service.SetResponseAttemptSource(seed, source)
	require.NoError(t, service.RegisterResponseOutput(seed, []byte(`{"id":"resp_middleware","output":[]}`)))
	for _, tc := range []struct {
		path, body, provider string
		want                 int
		channel              int
	}{
		{"/v1/responses", `{"model":"m","input":"hi"}`, "", 200, 1},
		{"/v1/responses", `{"model":"m","input":"hi"}`, "OpenAI", 200, 2},
		{"/v1/responses/compact", `{"model":"m","previous_response_id":"resp_middleware"}`, "", 200, 2},
		{"/v1/responses", `{"model":"m","previous_response_id":"unknown"}`, "OpenAI", 409, 0},
		{"/v1/responses", `{"model":"m","previous_response_id":"resp_middleware"}`, "Azure OpenAI", 409, 0},
	} {
		t.Run(tc.path+tc.provider+tc.body, func(t *testing.T) {
			r := gin.New()
			r.POST(tc.path, func(c *gin.Context) { c.Set("id", 1) }, Distribute(), func(c *gin.Context) {
				require.Equal(t, tc.channel, c.GetInt("channel_id"))
				constraint, active := model.GetResponsesConstraint(c.Request.Context())
				require.True(t, active)
				require.NotEmpty(t, constraint.Provider)
				require.Equal(t, "key", c.GetString("actual_key"))
				c.Status(200)
			})
			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Provider", tc.provider)
			r.ServeHTTP(w, req)
			require.Equal(t, tc.want, w.Code, w.Body.String())
		})
	}
	// 已禁用 ability 不能通过资源引用捷径使用。
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", 2).Update("enabled", false).Error)
	ctx := model.WithResponsesConstraint(context.Background(), model.ResponsesConstraint{Provider: "OpenAI", Resource: &source})
	require.Error(t, model.ValidateResponsesChannel(ctx, ch, "default", "m"))
}

func TestResponsesAttemptUsesOneKeyAndClearsPreviousHeaders(t *testing.T) {
	enabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = enabled })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Request = c.Request.WithContext(model.WithResponsesConstraint(c.Request.Context(), model.ResponsesConstraint{Provider: "OpenAI"}))
	headers := `{"X-Project":"project-a"}`
	channel := model.Channel{Id: 99291, Type: 1, Key: "key-a\nkey-b", HeaderOverride: &headers, MultiKeyInfo: model.MultiKeyInfo{IsMultiKey: true, KeySelectionMode: model.KeySelectionPolling}}
	_, previous, err := channel.GetNextAvailableKey()
	require.NoError(t, err)
	SetupContextForSelectedChannel(c, &channel, "m")
	require.False(t, c.IsAborted())
	require.Equal(t, (previous+1)%2, c.GetInt("key_index"))
	require.Equal(t, "project-a", c.GetStringMapString("headers_override")["X-Project"])
	channel.Id++
	channel.Key = "key-c"
	channel.HeaderOverride = nil
	channel.MultiKeyInfo = model.MultiKeyInfo{}
	SetupContextForSelectedChannel(c, &channel, "m")
	require.False(t, c.IsAborted())
	require.Empty(t, c.GetStringMapString("headers_override"))
	require.Equal(t, "key-c", c.GetString("actual_key"))
}
