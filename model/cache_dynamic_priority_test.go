package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/metrics"
)

// rateLimitReadHook 模拟 Redis 的窗口计数，覆盖真实选渠入口且不依赖外部服务。
// redis.Nil 使客户端跳过网络；读取函数允许该结果，After 中提供各命令的返回值。
type rateLimitReadHook struct {
	counts   map[string]int64
	readKeys map[string]bool
	failure  bool
}

func (h *rateLimitReadHook) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	return ctx, redis.Nil
}
func (h *rateLimitReadHook) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (h *rateLimitReadHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	if h.failure {
		return ctx, errors.New("模拟 Redis 不可用")
	}
	return ctx, redis.Nil
}
func (h *rateLimitReadHook) AfterProcessPipeline(_ context.Context, cmds []redis.Cmder) error {
	if h.failure {
		return nil
	}
	for _, cmd := range cmds {
		if cmd.Name() == "zcount" {
			key := fmt.Sprint(cmd.Args()[1])
			h.readKeys[key] = true
			cmd.SetErr(nil)
			cmd.(*redis.IntCmd).SetVal(h.counts[key])
		}
	}
	return nil
}

type dynamicPriorityFixture struct {
	id         int
	dp         int64
	rateLimits int64
	age        time.Duration
}

func setupDynamicPriorityRouting(t *testing.T, fixtures ...dynamicPriorityFixture) *rateLimitReadHook {
	t.Helper()
	setupAbilityTestDB(t)
	oldApply, oldThreshold := config.DynamicPriorityApplyEnabled, config.DynamicPriorityTopThreshold
	oldSlots, oldRatio, oldTTL := config.DynamicPriorityExploreSlots, config.DynamicPriorityExploreRatio, config.DynamicPriorityExplorationTTLHours
	oldEnabled, oldRDB, oldPostgres := common.RedisEnabled, common.RDB, common.UsingPostgreSQL
	config.DynamicPriorityApplyEnabled = true
	config.DynamicPriorityTopThreshold = 10
	config.DynamicPriorityExploreSlots = 0
	config.DynamicPriorityExploreRatio = 0
	config.DynamicPriorityExplorationTTLHours = 24
	common.UsingPostgreSQL = false
	hook := &rateLimitReadHook{counts: make(map[string]int64), readKeys: make(map[string]bool)}
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	client.AddHook(hook)
	common.RedisEnabled, common.RDB = true, client
	t.Cleanup(func() {
		config.DynamicPriorityApplyEnabled, config.DynamicPriorityTopThreshold = oldApply, oldThreshold
		config.DynamicPriorityExploreSlots, config.DynamicPriorityExploreRatio, config.DynamicPriorityExplorationTTLHours = oldSlots, oldRatio, oldTTL
		common.RedisEnabled, common.RDB, common.UsingPostgreSQL = oldEnabled, oldRDB, oldPostgres
		_ = client.Close()
	})
	for _, f := range fixtures {
		weight := uint(10)
		channel := Channel{Id: f.id, Status: common.ChannelStatusEnabled, Weight: &weight,
			CreatedTime: time.Now().Add(-f.age).Unix(), Group: "default", Models: "gemini-test"}
		if err := DB.Create(&channel).Error; err != nil {
			t.Fatal(err)
		}
		dp := f.dp
		if err := DB.Create(&Ability{ChannelId: f.id, Group: "default", Model: "gemini-test", Enabled: true, DynamicPriority: &dp}).Error; err != nil {
			t.Fatal(err)
		}
		hook.counts[fmt.Sprintf("%s%d:gemini-test", metrics.RateLimitKeyPrefix, f.id)] = f.rateLimits
	}
	return hook
}

func TestDynamicPriorityRouting(t *testing.T) {
	cases := []struct {
		name     string
		channels []dynamicPriorityFixture
		exclude  []int
		skip     int
		want     int
	}{
		{"限流后转向档外低分渠道", []dynamicPriorityFixture{{id: 1, dp: 32, rateLimits: 1}, {id: 2, dp: 30}}, nil, 0, 2},
		{"重试转向未失败且不拥塞的渠道", []dynamicPriorityFixture{{id: 1, dp: 40}, {id: 2, dp: 32, rateLimits: 1}, {id: 3, dp: 30}}, []int{1}, 0, 3},
		{"持续限流后使用未知渠道", []dynamicPriorityFixture{{id: 1, dp: 32, rateLimits: 10}, {id: 2, dp: 0}}, nil, 0, 2},
		{"重试允许未知渠道承接溢出", []dynamicPriorityFixture{{id: 1, dp: 40}, {id: 2, dp: 32, rateLimits: 10}, {id: 3, dp: 0}}, []int{1}, 0, 3},
		{"未知渠道自身限流时不抢占健康渠道", []dynamicPriorityFixture{{id: 1, dp: 32}, {id: 2, dp: 0, rateLimits: 20}}, nil, 0, 1},
		{"全部未评分也避开拥塞渠道", []dynamicPriorityFixture{{id: 1, dp: 0, rateLimits: 1}, {id: 2, dp: 0, age: time.Hour}}, nil, 0, 2},
		{"最低有效评分也优先于未知渠道", []dynamicPriorityFixture{{id: 1, dp: 1}, {id: 2, dp: 0}}, nil, 0, 1},
		{"没有限流时保留已评分优先", []dynamicPriorityFixture{{id: 1, dp: 32}, {id: 2, dp: 30}, {id: 3, dp: 0}}, nil, 0, 1},
		{"全部拥塞时仍保留可用候选", []dynamicPriorityFixture{{id: 1, dp: 32, rateLimits: 20}, {id: 2, dp: 30, rateLimits: 30}}, nil, 0, 1},
		{"跳过实时排序中的首个候选", []dynamicPriorityFixture{{id: 1, dp: 32, rateLimits: 1}, {id: 2, dp: 30}}, nil, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupDynamicPriorityRouting(t, tc.channels...)
			channel, _, err := CacheGetRandomSatisfiedChannel(context.Background(), "default", "gemini-test", tc.skip, "", tc.exclude)
			if err != nil {
				t.Fatal(err)
			}
			if channel.Id != tc.want {
				t.Fatalf("选到渠道 %d，期望 %d", channel.Id, tc.want)
			}
		})
	}
}

func TestDynamicPriorityExplorationAvoidsRateLimits(t *testing.T) {
	for _, ratio := range []int{0, 100} {
		t.Run(fmt.Sprintf("探索概率%d", ratio), func(t *testing.T) {
			setupDynamicPriorityRouting(t, dynamicPriorityFixture{id: 1, dp: 32}, dynamicPriorityFixture{id: 2, rateLimits: 10}, dynamicPriorityFixture{id: 3, age: time.Hour})
			config.DynamicPriorityExploreRatio = ratio
			config.DynamicPriorityExploreSlots = 2
			for i := 0; i < 50; i++ {
				channel, _, err := CacheGetRandomSatisfiedChannel(context.Background(), "default", "gemini-test", 0, "")
				if err != nil {
					t.Fatal(err)
				}
				if channel.Id == 2 {
					t.Fatal("探索绕过限流反馈，选中了持续 429 的未知渠道")
				}
				if ratio == 100 && channel.Id != 3 {
					t.Fatalf("应探索健康未知渠道，实际为 %d", channel.Id)
				}
			}
		})
	}
}

func TestDynamicPriorityRedisFallback(t *testing.T) {
	for _, unavailable := range []string{"未启用", "查询失败"} {
		t.Run(unavailable, func(t *testing.T) {
			hook := setupDynamicPriorityRouting(t, dynamicPriorityFixture{id: 1, dp: 32, rateLimits: 20}, dynamicPriorityFixture{id: 2, dp: 30}, dynamicPriorityFixture{id: 3})
			if unavailable == "未启用" {
				common.RedisEnabled = false
			} else {
				hook.failure = true
			}
			channel, _, err := CacheGetRandomSatisfiedChannel(context.Background(), "default", "gemini-test", 0, "")
			if err != nil {
				t.Fatal(err)
			}
			if channel.Id != 1 {
				t.Fatalf("反馈缺失时应按原评分选渠，实际为 %d", channel.Id)
			}
		})
	}
}

func TestDynamicPriorityFeedbackModelIsolation(t *testing.T) {
	hook := setupDynamicPriorityRouting(t, dynamicPriorityFixture{id: 1, dp: 32}, dynamicPriorityFixture{id: 2, dp: 30})
	hook.counts[metrics.RateLimitKeyPrefix+"1:other-model"] = 100
	channel, _, err := CacheGetRandomSatisfiedChannel(context.Background(), "default", "gemini-test", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if channel.Id != 1 {
		t.Fatal("其他模型的 429 影响了当前模型")
	}
	for key := range hook.readKeys {
		if !strings.HasSuffix(key, ":gemini-test") {
			t.Fatalf("读取了错误的模型维度：%s", key)
		}
	}
}

// 窗口中的 429 消失后，选路应自动回到原评分偏好，且不改写落库评分。
func TestDynamicPriorityRecovery(t *testing.T) {
	hook := setupDynamicPriorityRouting(t, dynamicPriorityFixture{id: 1, dp: 32, rateLimits: 10}, dynamicPriorityFixture{id: 2})
	for _, want := range []int{2, 1} {
		channel, _, err := CacheGetRandomSatisfiedChannel(context.Background(), "default", "gemini-test", 0, "")
		if err != nil {
			t.Fatal(err)
		}
		if channel.Id != want {
			t.Fatalf("选到渠道 %d，期望 %d", channel.Id, want)
		}
		clear(hook.counts)
	}
	var ability Ability
	if err := DB.Where("channel_id = ?", 1).First(&ability).Error; err != nil {
		t.Fatal(err)
	}
	if ability.DynamicPriority == nil || *ability.DynamicPriority != 32 {
		t.Fatal("实时反馈错误地改写了落库评分")
	}
}

// 按线上快照的候选数量、评分和主档比例验证分流，不依赖概率探索触发。
func TestDynamicPriorityProductionPoolOverflow(t *testing.T) {
	scores := []int64{32, 32, 30, 30, 29, 29, 28, 28, 27, 27, 27, 27, 27, 27, 27, 27, 27, 27}
	fixtures := make([]dynamicPriorityFixture, 0, 39)
	for i, dp := range scores {
		f := dynamicPriorityFixture{id: i + 1, dp: dp, age: time.Duration(i) * time.Minute}
		if i < 6 {
			f.rateLimits = 10
		}
		fixtures = append(fixtures, f)
	}
	for id := 19; id <= 39; id++ {
		fixtures = append(fixtures, dynamicPriorityFixture{id: id})
	}
	hook := setupDynamicPriorityRouting(t, fixtures...)
	config.DynamicPriorityTopThreshold = 30
	for i := 0; i < 30; i++ {
		channel, _, err := CacheGetRandomSatisfiedChannel(context.Background(), "default", "gemini-test", 0, "")
		if err != nil {
			t.Fatal(err)
		}
		if channel.Id <= 6 || channel.Id >= 19 {
			t.Fatalf("原主档拥塞时应转向其他健康已评分渠道，实际为 %d", channel.Id)
		}
	}
	for id := 1; id <= 18; id++ {
		hook.counts[fmt.Sprintf("%s%d:gemini-test", metrics.RateLimitKeyPrefix, id)] = 10
	}
	config.DynamicPriorityExploreSlots = 2
	for i := 0; i < 30; i++ {
		channel, _, err := CacheGetRandomSatisfiedChannel(context.Background(), "default", "gemini-test", 0, "")
		if err != nil {
			t.Fatal(err)
		}
		if channel.Id < 19 {
			t.Fatalf("已评分池持续拥塞时应允许未知渠道承接，实际为 %d", channel.Id)
		}
	}
}
