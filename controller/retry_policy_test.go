package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	dbmodel "github.com/songquanpeng/one-api/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func setupRetryPolicyDB(t *testing.T, dynamic bool) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&dbmodel.Channel{}, &dbmodel.Ability{}); err != nil {
		t.Fatal(err)
	}
	oldDB, oldRedis, oldPostgres := dbmodel.DB, common.RedisEnabled, common.UsingPostgreSQL
	oldDynamic := config.DynamicPriorityApplyEnabled
	oldThreshold := config.DynamicPriorityTopThreshold
	oldSlots, oldRatio := config.DynamicPriorityExploreSlots, config.DynamicPriorityExploreRatio
	dbmodel.DB, common.RedisEnabled, common.UsingPostgreSQL = db, false, false
	config.DynamicPriorityApplyEnabled = dynamic
	config.DynamicPriorityTopThreshold = 10
	config.DynamicPriorityExploreSlots, config.DynamicPriorityExploreRatio = 0, 0
	t.Cleanup(func() {
		dbmodel.DB, common.RedisEnabled, common.UsingPostgreSQL = oldDB, oldRedis, oldPostgres
		config.DynamicPriorityApplyEnabled = oldDynamic
		config.DynamicPriorityTopThreshold = oldThreshold
		config.DynamicPriorityExploreSlots, config.DynamicPriorityExploreRatio = oldSlots, oldRatio
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
}

func seedRetryChannel(t *testing.T, id, channelType int, cfg string, priority int64) {
	t.Helper()
	weight := uint(1)
	channel := dbmodel.Channel{
		Id: id, Type: channelType, Config: cfg, Status: common.ChannelStatusEnabled,
		Group: "default", Models: "test-model", Weight: &weight, Priority: &priority,
	}
	if err := dbmodel.DB.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	ability := dbmodel.Ability{
		ChannelId: id, Group: "default", Model: "test-model", Enabled: true,
		Priority: &priority, DynamicPriority: &priority,
	}
	if err := dbmodel.DB.Create(&ability).Error; err != nil {
		t.Fatal(err)
	}
}

func newAzureRetryTestContext(region string) (*gin.Context, context.Context) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("channel", common.ChannelTypeAzure)
	c.Set("Config", dbmodel.ChannelConfig{Region: region})
	return c, newRetryContext(c)
}

func TestAzureRetryRegionSelection(t *testing.T) {
	for _, dynamic := range []bool{false, true} {
		name := "静态优先级"
		if dynamic {
			name = "动态优先级"
		}
		t.Run(name, func(t *testing.T) {
			setupRetryPolicyDB(t, dynamic)
			seedRetryChannel(t, 1, common.ChannelTypeAzure, `{"region":"eastus"}`, 100)
			seedRetryChannel(t, 2, common.ChannelTypeAzure, `{"region":" EASTUS "}`, 90)
			seedRetryChannel(t, 3, common.ChannelTypeAzure, `{"region":"westus"}`, 1000)
			seedRetryChannel(t, 4, common.ChannelTypeOpenAI, `{"region":"eastus"}`, 1000)
			seedRetryChannel(t, 5, common.ChannelTypeAzure, `{}`, 1000)
			seedRetryChannel(t, 6, common.ChannelTypeAzure, `{"region":`, 1000)
			seedRetryChannel(t, 7, common.ChannelTypeAzure, `{"region":"eastus"}`, 1000)
			seedRetryChannel(t, 8, common.ChannelTypeAzure, `{"region":"eastus"}`, 1000)
			seedRetryChannel(t, 9, common.ChannelTypeAzure, `{"region":"eastus"}`, 1000)
			seedRetryChannel(t, 10, common.ChannelTypeAzure, `{"region":"eastus"}`, 1000)
			seedRetryChannel(t, 11, common.ChannelTypeAzure, `{"region":"eastus"}`, 1)
			for _, change := range []struct {
				model any
				id    int
				key   string
				value any
			}{
				{&dbmodel.Channel{}, 7, "status", common.ChannelStatusManuallyDisabled},
				{&dbmodel.Ability{}, 8, "enabled", false},
				{&dbmodel.Ability{}, 9, "group", "other-group"},
				{&dbmodel.Ability{}, 10, "model", "other-model"},
			} {
				idColumn := "channel_id"
				if _, ok := change.model.(*dbmodel.Channel); ok {
					idColumn = "id"
				}
				if err := dbmodel.DB.Model(change.model).Where(idColumn+" = ?", change.id).Update(change.key, change.value).Error; err != nil {
					t.Fatal(err)
				}
			}

			c, ctx := newAzureRetryTestContext(" eastus ")
			failed := []int{1}
			for _, want := range []int{2, 11} {
				channel, err := selectRetryChannel(ctx, "default", "test-model", &failed)
				if err != nil || channel == nil || channel.Id != want {
					t.Fatalf("应选择同区域渠道 %d，实际 channel=%+v err=%v", want, channel, err)
				}
				failed = appendUniqueChannelID(failed, channel.Id)
				// 模拟重试更新 Gin 配置，初始区域限制应保持不变。
				c.Set("Config", dbmodel.ChannelConfig{Region: "westus"})
			}
			channel, err := selectRetryChannel(ctx, "default", "test-model", &failed)
			if err == nil || channel != nil {
				t.Fatalf("同区域渠道耗尽后必须停止，实际 channel=%+v err=%v", channel, err)
			}
			if !reflect.DeepEqual(failed, []int{1, 2, 11}) {
				t.Fatalf("Azure 重试不应清空失败列表：%v", failed)
			}
			if !isAzureRetry(ctx) {
				t.Fatal("耗尽后仍应保持 Azure 限制，禁止回退最后一个渠道")
			}
		})
	}
}

func TestAzureRetryWithoutRegionStops(t *testing.T) {
	for _, region := range []string{"", "   "} {
		t.Run("区域="+region, func(t *testing.T) {
			setupRetryPolicyDB(t, false)
			seedRetryChannel(t, 2, common.ChannelTypeAzure, `{}`, 1)
			_, ctx := newAzureRetryTestContext(region)
			failed := []int{1}
			channel, err := selectRetryChannel(ctx, "default", "test-model", &failed)
			if err == nil || channel != nil {
				t.Fatalf("未配置区域时不应重试，实际 channel=%+v err=%v", channel, err)
			}
		})
	}
}

func TestNonAzureRetryStillCycles(t *testing.T) {
	setupRetryPolicyDB(t, false)
	seedRetryChannel(t, 1, common.ChannelTypeOpenAI, `{}`, 1)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("channel", common.ChannelTypeOpenAI)
	ctx := newRetryContext(c)
	failed := []int{1}
	channel, err := selectRetryChannel(ctx, "default", "test-model", &failed)
	if err != nil || channel == nil || channel.Id != 1 || len(failed) != 0 {
		t.Fatalf("非 Azure 应保留耗尽后循环的行为，实际 channel=%+v failed=%v err=%v", channel, failed, err)
	}
	if isAzureRetry(ctx) {
		t.Fatal("非 Azure 请求不应限制最后渠道回退")
	}
}
