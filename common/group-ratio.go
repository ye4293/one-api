package common

import "github.com/songquanpeng/one-api/common/logger"

var GroupRatio = map[string]float64{
	"Lv1": 1,
	"Lv2": 1,
	"Lv3": 1,
	"Lv4": 1,
	"Lv5": 1,
	"Lv6": 1,
}

func GroupRatio2JSONString() string {
	jsonBytes, err := marshalPricingMap(&GroupRatio)
	if err != nil {
		logger.SysError("error marshalling model ratio: " + err.Error())
	}
	return string(jsonBytes)
}

func UpdateGroupRatioByJSONString(jsonStr string) error {
	return replacePricingMap(&GroupRatio, jsonStr)
}

func GetGroupRatio(name string) float64 {
	ratio, ok := lookupPricingValue(&GroupRatio, name)
	if !ok {
		logger.SysError("group ratio not found: " + name)
		return 1
	}
	return ratio
}

func SetGroupRatio(name string, value float64) {
	setPricingValue(&GroupRatio, name, value)
}

func DeleteGroupRatio(name string) {
	pricingMu.Lock()
	delete(GroupRatio, name)
	pricingMu.Unlock()
}

func GroupRatioSnapshot() map[string]float64 {
	return snapshotPricingMap(&GroupRatio)
}
