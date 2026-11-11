package model

import (
	"context"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/songquanpeng/one-api/common"
	"testing"
)

func setupResponseOriginRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	r := miniredis.RunT(t)
	oldClient, oldEnabled := common.RDB, common.RedisEnabled
	common.RDB = redis.NewClient(&redis.Options{Addr: r.Addr()})
	common.RedisEnabled = true
	t.Cleanup(func() { common.RDB.Close(); common.RDB = oldClient; common.RedisEnabled = oldEnabled })
	return r
}
func TestResponseOriginRejectsMissingType(t *testing.T) {
	setupResponseOriginRedis(t)
	if err := CacheResponseOrigin("resp_invalid", ResponseOrigin{ChannelID: 7, KeyIndex: 0, Region: "eastus"}); err == nil {
		t.Fatal("must not register incomplete provenance")
	}
}
func TestResponseOriginRetainsSourceAfterChannelDeleted(t *testing.T) {
	setupAbilityTestDB(t)
	setupResponseOriginRedis(t)
	ch := Channel{Id: 7, Type: common.ChannelTypeAzure, Config: `{"region":"westus"}`}
	if err := DB.Create(&ch).Error; err != nil {
		t.Fatal(err)
	}
	if err := CacheResponseOrigin("rs_A", ResponseOrigin{ResponseID: "resp_A", ChannelID: 7, ChannelType: common.ChannelTypeAzure, KeyIndex: 2, Region: "eastus"}); err != nil {
		t.Fatal(err)
	}
	DB.Delete(&Channel{}, 7)
	got, err := LookupResponseOrigin("rs_A")
	if err != nil || got.ChannelID != 7 || got.ChannelType != common.ChannelTypeAzure || got.KeyIndex != 2 || got.Region != "eastus" || got.ResponseID != "resp_A" {
		t.Fatalf("lost provenance: %+v %v", got, err)
	}
}
func TestResponseOriginPreservesMappingOnConflictingRegistration(t *testing.T) {
	setupResponseOriginRedis(t)
	a := ResponseOrigin{ResponseID: "resp_A", ChannelID: 7, ChannelType: common.ChannelTypeAzure, KeyIndex: 0, Region: "eastus"}
	if err := CacheResponseOrigin("resp_A", a); err != nil {
		t.Fatal(err)
	}
	b := a
	b.ChannelID = 8
	b.Region = "westus"
	if err := CacheResponseOrigin("resp_A", b); err == nil {
		t.Fatal("must reject conflicting registration")
	}
	got, err := LookupResponseOrigin("resp_A")
	if err != nil || got.ChannelID != 7 || got.Region != "eastus" {
		t.Fatalf("source mutated: %+v %v", got, err)
	}
}
func TestResponseOriginLegacyAndMissingKeyIndex(t *testing.T) {
	setupAbilityTestDB(t)
	setupResponseOriginRedis(t)
	DB.Create(&Channel{Id: 7, Type: common.ChannelTypeAzure, Config: `{"region":"eastus"}`})
	for _, raw := range []string{"7", "7:0", "7:2:WESTUS"} {
		common.RDB.Set(context.Background(), fmtResponseCacheKey("old"), raw, 0)
		got, err := LookupResponseOrigin("old")
		if err != nil || got.ChannelType != common.ChannelTypeAzure {
			t.Fatalf("legacy read: %+v %v", got, err)
		}
	}
	if err := SetResponseIdRegionToRedis("no_index", 7, -1, "eastus", 1440); err != nil {
		t.Fatal(err)
	}
	got, err := LookupResponseOrigin("no_index")
	if err != nil || got.KeyIndex != -1 || got.Region != "eastus" {
		t.Fatalf("region lost: %+v %v", got, err)
	}
}
