package common

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

type pricingMapTestCase struct {
	name      string
	update    func(string) error
	serialize func() string
	get       func(string) float64
	set       func(string, float64)
}

func pricingMapTestCases() []pricingMapTestCase {
	return []pricingMapTestCase{
		{"model", UpdateModelRatioByJSONString, ModelRatio2JSONString, GetModelRatio, SetModelRatio},
		{"completion", UpdateCompletionRatioByJSONString, CompletionRatio2JSONString, GetCompletionRatio, SetCompletionRatio},
		{"audio_input", UpdateAudioInputRatioByJSONString, AudioInputRatio2JSONString, GetAudioInputRatio, SetAudioInputRatio},
		{"audio_output", UpdateAudioOutputRatioByJSONString, AudioOutputRatio2JSONString, GetAudioOutputRatio, SetAudioOutputRatio},
		{"image_input", UpdateImageInputRatioByJSONString, ImageInputRatio2JSONString, GetImageInputRatio, SetImageInputRatio},
		{"image_output", UpdateImageOutputRatioByJSONString, ImageOutputRatio2JSONString, GetImageOutputRatio, SetImageOutputRatio},
		{"cache", UpdateCacheRatioByJSONString, CacheRatio2JSONString, GetCacheRatio, SetCacheRatio},
		{"claude_cache_5m", UpdateClaudeCacheCreation5mRatioByJSONString, ClaudeCacheCreation5mRatio2JSONString, GetClaudeCacheCreation5mRatio, SetClaudeCacheCreation5mRatio},
		{"claude_cache_1h", UpdateClaudeCacheCreation1hRatioByJSONString, ClaudeCacheCreation1hRatio2JSONString, GetClaudeCacheCreation1hRatio, SetClaudeCacheCreation1hRatio},
		{"per_call", UpdateModelPriceByJSONString, ModelPrice2JSONString, func(name string) float64 { return GetModelPrice(name, false) }, SetModelPrice},
		{"group", UpdateGroupRatioByJSONString, GroupRatio2JSONString, GetGroupRatio, SetGroupRatio},
	}
}

func TestPricingInvalidJSONPreservesConfiguration(t *testing.T) {
	for _, tc := range pricingMapTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			original := tc.serialize()
			t.Cleanup(func() {
				if err := tc.update(original); err != nil {
					t.Fatal(err)
				}
			})
			if err := tc.update(`{"pricing-test":7}`); err != nil {
				t.Fatal(err)
			}
			// 类型错误可能发生在部分键已经解析之后，旧配置必须完整保留。
			if err := tc.update(`{"pricing-test":9,"broken":"invalid"}`); err == nil {
				t.Fatal("无效配置应返回解析错误")
			}
			if got := tc.get("pricing-test"); got != 7 {
				t.Fatalf("解析失败后旧配置被修改：得到 %v，预期 7", got)
			}
			var values map[string]float64
			if err := json.Unmarshal([]byte(tc.serialize()), &values); err != nil || len(values) != 1 {
				t.Fatalf("解析失败后配置不完整：%v，%v", values, err)
			}
		})
	}
}

func TestPricingConcurrentRefresh(t *testing.T) {
	for _, tc := range pricingMapTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			original := tc.serialize()
			t.Cleanup(func() { _ = tc.update(original) })
			values := make(map[string]float64)
			for i := 0; i < 64; i++ {
				values[fmt.Sprintf("pricing-test-%d", i)] = 7
			}
			data, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.update(string(data)); err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			var wg sync.WaitGroup
			for worker := 0; worker < 5; worker++ {
				wg.Add(1)
				go func(worker int) {
					defer wg.Done()
					<-start
					for i := 0; i < 150; i++ {
						if worker == 1 {
							tc.set("pricing-test-32", 7)
							continue
						}
						if worker == 0 {
							if err := tc.update(string(data)); err != nil {
								t.Error(err)
								return
							}
							continue
						}
						if got := tc.get("pricing-test-32"); got != 7 {
							t.Errorf("并发刷新暴露了不完整配置：%v", got)
							return
						}
						var snapshot map[string]float64
						if err := json.Unmarshal([]byte(tc.serialize()), &snapshot); err != nil || len(snapshot) != len(values) {
							t.Errorf("并发序列化返回了不完整配置：长度 %d，错误 %v", len(snapshot), err)
							return
						}
					}
				}(worker)
			}
			close(start)
			wg.Wait()
		})
	}
}

func TestPricingSingleValueAfterNull(t *testing.T) {
	for _, tc := range pricingMapTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			original := tc.serialize()
			t.Cleanup(func() { _ = tc.update(original) })
			if err := tc.update("null"); err != nil {
				t.Fatal(err)
			}
			// null 配置之后仍能安全设置；显式零值不能被当成缺失项。
			tc.set("pricing-test-zero", 0)
			if got := tc.get("pricing-test-zero"); got != 0 {
				t.Fatalf("显式零值被回退为 %v", got)
			}
			var values map[string]float64
			if err := json.Unmarshal([]byte(tc.serialize()), &values); err != nil {
				t.Fatal(err)
			}
			if value, ok := values["pricing-test-zero"]; !ok || value != 0 {
				t.Fatalf("零值配置未保留：%v", values)
			}
		})
	}
}

func TestPricingSnapshotsAreIndependent(t *testing.T) {
	cases := []struct {
		name      string
		serialize func() string
		update    func(string) error
		set       func(string, float64)
		snapshot  func() map[string]float64
	}{
		{"model", ModelRatio2JSONString, UpdateModelRatioByJSONString, SetModelRatio, ModelRatioSnapshot},
		{"per_call", ModelPrice2JSONString, UpdateModelPriceByJSONString, SetModelPrice, ModelPriceSnapshot},
		{"group", GroupRatio2JSONString, UpdateGroupRatioByJSONString, SetGroupRatio, GroupRatioSnapshot},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			original := tc.serialize()
			t.Cleanup(func() { _ = tc.update(original) })
			tc.set("snapshot-model", 7)
			snapshot := tc.snapshot()
			snapshot["snapshot-model"] = 99
			delete(snapshot, "snapshot-model")
			if got := tc.snapshot()["snapshot-model"]; got != 7 {
				t.Fatalf("调用方修改副本影响了全局配置：%v", got)
			}
			previous := tc.snapshot()
			tc.set("snapshot-model", 8)
			if previous["snapshot-model"] != 7 {
				t.Fatal("全局修改影响了已有副本")
			}
		})
	}
}

func TestPricingGroupSnapshotConcurrentDeletion(t *testing.T) {
	original := GroupRatio2JSONString()
	t.Cleanup(func() { _ = UpdateGroupRatioByJSONString(original) })
	SetGroupRatio("stable-group", 0)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for worker := 0; worker < 3; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			for i := 0; i < 500; i++ {
				if worker == 0 {
					SetGroupRatio("temporary-group", 1)
					DeleteGroupRatio("temporary-group")
					continue
				}
				snapshot := GroupRatioSnapshot()
				if value, ok := snapshot["stable-group"]; !ok || value != 0 {
					t.Error("并发增删破坏了已有零值分组")
					return
				}
				// 实际遍历副本，与持锁修改互不影响。
				for key := range snapshot {
					delete(snapshot, key)
				}
			}
		}(worker)
	}
	close(start)
	wg.Wait()
	if _, ok := GroupRatioSnapshot()["temporary-group"]; ok {
		t.Fatal("分组未删除")
	}
}

func TestPricingFallbacksAfterRefresh(t *testing.T) {
	for _, tc := range pricingMapTestCases() {
		original := tc.serialize()
		t.Cleanup(func() { _ = tc.update(original) })
		if err := tc.update("{}"); err != nil {
			t.Fatal(err)
		}
	}
	SetCompletionRatio("pricing-fallback", 4)
	for name, get := range map[string]func(string) float64{
		"cache": GetCacheRatio, "audio_output": GetAudioOutputRatio, "image_output": GetImageOutputRatio,
	} {
		if got := get("pricing-fallback"); got != 4 {
			t.Errorf("%s 的补全倍率回退变更为 %v", name, got)
		}
	}
	if got := GetClaudeCacheReadRatio("pricing-fallback"); got != 0.1 {
		t.Errorf("Claude 缓存读必须使用独立默认值，得到 %v", got)
	}
	if GetModelRatio("pricing-missing") != 30 || GetModelPrice("pricing-missing", false) != -1 ||
		GetAudioInputRatio("pricing-missing") != 1 || GetImageInputRatio("pricing-missing") != 1 ||
		GetClaudeCacheCreation5mRatio("pricing-missing") != 1.25 || GetClaudeCacheCreation1hRatio("pricing-missing") != 2 {
		t.Fatal("缺失项的默认价格发生变化")
	}
	for name, lookup := range map[string]func(string) (float64, bool){
		"model": LookupModelRatio, "completion": LookupCompletionRatio, "cache": LookupCacheRatio,
		"cache_write": LookupCacheWriteRatio, "per_call": LookupModelPrice,
	} {
		if _, ok := lookup("pricing-missing"); ok {
			t.Errorf("%s 精确查找把缺失项当成已配置", name)
		}
	}
}
