package model

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"strings"
	"testing"
)

func setupResponseLogDB(t *testing.T) {
	t.Helper()
	setupAbilityTestDB(t)
	oldLog, oldEnabled, oldRedis := LOG_DB, config.LogConsumeEnabled, common.RedisEnabled
	LOG_DB = DB
	config.LogConsumeEnabled = true
	common.RedisEnabled = false
	if err := DB.AutoMigrate(&Log{}, &User{}); err != nil {
		t.Fatal(err)
	}
	DB.Create(&User{Id: 1, Username: "response-test"})
	t.Cleanup(func() { LOG_DB = oldLog; config.LogConsumeEnabled = oldEnabled; common.RedisEnabled = oldRedis })
}
func TestResponseLogKeepsParentAndCurrentIDs(t *testing.T) {
	setupResponseLogDB(t)
	for _, parent := range []string{"resp_A", "chatcmpl-A"} {
		ctx := WithPreviousResponseID(context.Background(), parent)
		RecordConsumeLogWithOtherAndRequestID(ctx, 1, 7, 1, 1, "gpt-test", "token", 1, "ok", 1, "", "", false, 0, "", "req_B", 0, "resp_B")
		var got Log
		if err := LOG_DB.Last(&got).Error; err != nil {
			t.Fatal(err)
		}
		var correlation map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(got.Other, "responseCorrelation:")), &correlation); err != nil {
			t.Fatalf("missing correlation Other: %q", got.Other)
		}
		if got.XResponseID != "resp_B" || correlation["previous_response_id"] != parent {
			t.Fatalf("response chain lost: current=%q Other=%q", got.XResponseID, got.Other)
		}
	}
}
func TestResponseLogOtherPreservesExistingMetadata(t *testing.T) {
	setupResponseLogDB(t)
	ctx := WithResponseOrigin(WithPreviousResponseID(context.Background(), "resp_A"), ResponseOrigin{ResponseID: "resp_A", ChannelID: 7, ChannelType: common.ChannelTypeAzure, KeyIndex: 2, Region: "eastus"})
	for _, other := range []string{`{"usageDetails":{"input_tokens":2}}`, `adminInfo:[7];usageDetails:{"input_tokens":2}`} {
		RecordConsumeLogWithOtherAndRequestID(ctx, 1, 8, 1, 1, "gpt-test", "token", 1, "ok", 1, "", "", false, 0, other, "req_B", 0, "resp_B")
		var got Log
		LOG_DB.Last(&got)
		if !strings.Contains(got.Other, `"origin_region":"eastus"`) || !strings.Contains(got.Other, `"origin_channel_type":3`) || !strings.Contains(got.Other, `"origin_channel_id":7`) || !strings.Contains(got.Other, `"origin_key_index":2`) || !strings.Contains(got.Other, `"input_tokens":2`) {
			t.Fatalf("metadata lost: %s", got.Other)
		}
	}
	if LOG_DB.Migrator().HasColumn(&Log{}, "previous_response_id") {
		t.Fatal("must not change logs schema")
	}
}

func TestResponseLogFallbackKeepsLookupFailureAndParent(t *testing.T) {
	setupResponseLogDB(t)
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"not_found", ErrResponseOriginNotFound},
		{"unavailable", fmt.Errorf("private backend detail: %w", ErrResponseOriginUnavailable)},
		{"invalid", ErrResponseOriginUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := WithResponseOriginFallback(WithPreviousResponseID(context.Background(), "rs_old"), tc.err)
			// The selected channel's retry snapshot is not the unknown parent's source.
			ctx = WithResponseOrigin(ctx, ResponseOrigin{ChannelID: 8, ChannelType: common.ChannelTypeAzure, Region: "eastus"})
			for _, other := range []string{`{"usageDetails":{"input_tokens":2}}`, `adminInfo:[8];usageDetails:{"input_tokens":2}`} {
				RecordConsumeLogWithOtherAndRequestID(ctx, 1, 8, 2, 1, "gpt-test", "token", 1, "ok", 1, "", "", false, 0, other, "req_new", 0, "resp_new")
				var got Log
				if err := LOG_DB.Last(&got).Error; err != nil {
					t.Fatal(err)
				}
				if got.XResponseID != "resp_new" || !strings.Contains(got.Other, `"previous_response_id":"rs_old"`) || !strings.Contains(got.Other, `"origin_lookup_status":"`+tc.name+`"`) || !strings.Contains(got.Other, `"input_tokens":2`) || strings.Contains(got.Other, `"origin_channel_id"`) || strings.Contains(got.Other, "private backend detail") {
					t.Fatalf("fallback metadata lost or leaked: %+v", got)
				}
			}
		})
	}
}
