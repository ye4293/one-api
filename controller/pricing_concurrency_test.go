package controller

import (
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
)

func TestPricingViewsConcurrentUpdates(t *testing.T) {
	modelJSON, priceJSON, groupJSON := common.ModelRatio2JSONString(), common.ModelPrice2JSONString(), common.GroupRatio2JSONString()
	t.Cleanup(func() {
		_ = common.UpdateModelRatioByJSONString(modelJSON)
		_ = common.UpdateModelPriceByJSONString(priceJSON)
		_ = common.UpdateGroupRatioByJSONString(groupJSON)
	})
	const name = "pricing-view-model"
	const data = `{"pricing-view-model":7}`
	for _, update := range []func(string) error{common.UpdateModelRatioByJSONString, common.UpdateModelPriceByJSONString, common.UpdateGroupRatioByJSONString} {
		if err := update(data); err != nil {
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
			for i := 0; i < 150; i++ {
				switch worker {
				case 0:
					if err := common.UpdateModelRatioByJSONString(data); err != nil {
						t.Error(err)
					}
					if err := common.UpdateModelPriceByJSONString(data); err != nil {
						t.Error(err)
					}
					if err := common.UpdateGroupRatioByJSONString(data); err != nil {
						t.Error(err)
					}
				case 1:
					common.SetModelRatio(name, 7)
					common.SetModelPrice(name, 7)
					common.SetGroupRatio("temporary-group", 1)
					common.DeleteGroupRatio("temporary-group")
				default:
					prices := getAllModelPrices()
					if len(prices) != 1 || prices[0].ModelName != name || prices[0].FixedPrice != 7 {
						t.Errorf("并发更新期间价格列表不完整：%v", prices)
						return
					}
					if price := buildPriceMap()[name]; price == nil || price.FixedPrice != 7 {
						t.Errorf("并发更新期间模型广场价格不完整：%v", price)
						return
					}
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					GetGroups(c)
					var response struct {
						Success bool     `json:"success"`
						Data    []string `json:"data"`
					}
					if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || !response.Success || len(response.Data) == 0 {
						t.Errorf("并发更新期间分组列表无效：%s", recorder.Body.String())
						return
					}
				}
			}
		}(worker)
	}
	close(start)
	wg.Wait()
}
