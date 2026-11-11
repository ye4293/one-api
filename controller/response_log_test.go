package controller

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/middleware"
	dbmodel "github.com/songquanpeng/one-api/model"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponseLogErrorKeepsVisibleResponseID(t *testing.T) {
	setupRetryPolicyDB(t, false)
	oldLog := dbmodel.LOG_DB
	dbmodel.LOG_DB = dbmodel.DB
	t.Cleanup(func() { dbmodel.LOG_DB = oldLog })
	dbmodel.DB.AutoMigrate(&dbmodel.Log{}, &dbmodel.User{})
	dbmodel.DB.Create(&dbmodel.User{Id: 1, Username: "test"})
	ctx := dbmodel.WithPreviousResponseID(context.Background(), "resp_A")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	c.Set("id", 1)
	c.Set("original_model", "gpt-test")
	c.Set("channel_id", 7)
	c.Set("x_response_id", "resp_B")
	c.Request.Header.Set("X-Request-ID", "req_B")
	recordFinalErrorLog(ctx, c, &relaymodel.ErrorWithStatusCode{StatusCode: 400, Error: relaymodel.Error{Message: "bad upstream"}}, nil, nil, "")
	var got dbmodel.Log
	if err := dbmodel.LOG_DB.Last(&got).Error; err != nil {
		t.Fatal(err)
	}
	var correlation map[string]any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(got.Other, "responseCorrelation:")), &correlation); err != nil {
		t.Fatalf("missing correlation Other: %q", got.Other)
	}
	if got.XResponseID != "resp_B" || correlation["previous_response_id"] != "resp_A" {
		t.Fatalf("response chain lost: current=%q Other=%q", got.XResponseID, got.Other)
	}
}
func TestResponseLogExhaustedOriginSelectionIsLogged(t *testing.T) {
	setupRetryPolicyDB(t, false)
	auditRedis(t)
	oldLog := dbmodel.LOG_DB
	dbmodel.LOG_DB = dbmodel.DB
	t.Cleanup(func() { dbmodel.LOG_DB = oldLog })
	dbmodel.DB.AutoMigrate(&dbmodel.Log{}, &dbmodel.User{})
	dbmodel.DB.Create(&dbmodel.User{Id: 1, Username: "test", Group: "default"})
	seedRetryChannel(t, 1, common.ChannelTypeAzure, `{"region":"eastus"}`, 1)
	dbmodel.DB.Model(&dbmodel.Channel{}).Where("id = ?", 1).Update("status", common.ChannelStatusManuallyDisabled)
	if err := dbmodel.CacheResponseOrigin("resp_A", dbmodel.ResponseOrigin{ResponseID: "resp_A", ChannelID: 1, KeyIndex: 0, Region: "eastus", ChannelType: common.ChannelTypeAzure}); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.POST("/v1/chat/completions", func(c *gin.Context) { c.Set("id", 1); c.Next() }, middleware.Distribute(), func(c *gin.Context) { t.Error("exhausted origin must not call relay") })
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test-model","messages":[]}`))
	req.Header.Set("X-Response-ID", "resp_A")
	req.Header.Set("X-Request-ID", "req_B")
	router.ServeHTTP(w, req)
	if w.Code != 503 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got dbmodel.Log
	if err := dbmodel.LOG_DB.Last(&got).Error; err != nil {
		t.Fatalf("missing rejection log: %v", err)
	}
	if got.Type != dbmodel.LogTypeError || got.XResponseID != "" || !strings.Contains(got.Other, `"previous_response_id":"resp_A"`) {
		t.Fatalf("wrong rejection log: %+v", got)
	}
}
