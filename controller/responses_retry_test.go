package controller

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/config"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/stretchr/testify/require"
)

func TestResponsesRetryCycleStaysWithinProvider(t *testing.T) {
	setupEvaluatorTestDB(t)
	previous := config.DynamicPriorityApplyEnabled
	config.DynamicPriorityApplyEnabled = false
	t.Cleanup(func() { config.DynamicPriorityApplyEnabled = previous })
	weight := uint(1)
	for id, kind := range map[int]int{1: 1, 2: 1, 3: 3} {
		priority := int64(100 - id)
		ch := dbmodel.Channel{Id: id, Type: kind, Key: "key", Models: "m", Group: "default", Weight: &weight, Priority: &priority}
		require.NoError(t, ch.Insert())
	}
	ctx := dbmodel.WithResponsesConstraint(context.Background(), dbmodel.ResponsesConstraint{Provider: "OpenAI"})
	failed := []int{1}
	channel, err := selectRetryChannel(ctx, "default", "m", &failed)
	require.NoError(t, err)
	require.Equal(t, 2, channel.Id)
	failed = append(failed, 2)
	channel, err = selectRetryChannel(ctx, "default", "m", &failed)
	require.NoError(t, err)
	require.Equal(t, 1, channel.Id)
	require.Empty(t, failed)
	// 查询故障不能伪装成候选耗尽、清空失败列表再选。
	require.NoError(t, dbmodel.DB.Migrator().DropTable(&dbmodel.Ability{}))
	failed = []int{1, 2}
	_, err = selectRetryChannel(ctx, "default", "m", &failed)
	require.Error(t, err)
	require.Equal(t, []int{1, 2}, failed)
}

func TestResponsesRetryGuardsAndStreamError(t *testing.T) {
	upstream := &model.ErrorWithStatusCode{Error: model.Error{Message: "upstream"}, StatusCode: 503}
	for _, kind := range []string{"normal", "resource", "specific", "affinity", "written", "state", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			err := upstream
			switch kind {
			case "resource":
				c.Request = c.Request.WithContext(dbmodel.WithResponsesConstraint(c.Request.Context(), dbmodel.ResponsesConstraint{Provider: "OpenAI", Resource: &dbmodel.ResponseSource{ChannelID: 1}}))
			case "specific":
				c.Set("specific_channel_id", "1")
			case "affinity":
				c.Set("affinity_skip_retry", true)
			case "written":
				c.Header("Content-Type", "text/event-stream")
				_, _ = c.Writer.WriteString("data: {}\n\n")
			case "state":
				err = responseRoutingError(dbmodel.ErrNoCompatibleResponseChannel)
			case "cancelled":
				ctx, cancel := context.WithCancel(c.Request.Context())
				cancel()
				c.Request = c.Request.WithContext(ctx)
			}
			require.Equal(t, kind == "normal", shouldRetryResponses(c, err))
			if kind == "written" {
				writeResponsesRelayError(c, upstream)
				require.Contains(t, w.Body.String(), "event: error\n")
				require.Equal(t, 200, w.Code)
			}
			if kind == "state" {
				require.False(t, responsesChannelHealthError(c, err))
				writeResponsesRelayError(c, err)
				require.Equal(t, 503, w.Code)
			}
		})
	}
}
