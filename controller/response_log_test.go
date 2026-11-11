package controller

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
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
