package controller

import (
	"sync"
	"testing"

	"github.com/songquanpeng/one-api/common"
)

func TestOpenAITierConcurrentPricingRefresh(t *testing.T) {
	cases := []struct {
		serialize func() string
		update    func(string) error
		set       func(string, float64)
	}{
		{common.ModelRatio2JSONString, common.UpdateModelRatioByJSONString, common.SetModelRatio},
		{common.CompletionRatio2JSONString, common.UpdateCompletionRatioByJSONString, common.SetCompletionRatio},
		{common.CacheRatio2JSONString, common.UpdateCacheRatioByJSONString, common.SetCacheRatio},
		{common.ModelPrice2JSONString, common.UpdateModelPriceByJSONString, common.SetModelPrice},
	}
	const key = "pricing-concurrency_priority"
	const data = `{"pricing-concurrency_priority":0}`
	for _, tc := range cases {
		original := tc.serialize()
		t.Cleanup(func() { _ = tc.update(original) })
		if err := tc.update(data); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			for i := 0; i < 200; i++ {
				if worker < 2 {
					for _, tc := range cases {
						if worker == 0 {
							if err := tc.update(data); err != nil {
								t.Error(err)
							}
						} else {
							tc.set(key, 0)
						}
					}
					continue
				}
				for _, entry := range []openAIPricingEntry{openAIChatPricing, openAIResponsePricing} {
					prices := lookupOpenAIPriceOverrides("pricing-concurrency", "priority", entry)
					if !prices.modelRatio.set || prices.modelRatio.value != 0 ||
						!prices.completionRatio.set || prices.completionRatio.value != 0 ||
						!prices.cacheRatio.set || prices.cacheRatio.value != 0 {
						t.Error("并发刷新丢失了显式零值档位价格")
						return
					}
					if entry == openAIChatPricing && (!prices.modelPrice.set || prices.modelPrice.value != 0) {
						t.Error("并发刷新丢失了按次计费的零值档位价格")
						return
					}
				}
			}
		}(worker)
	}
	close(start)
	wg.Wait()
}
