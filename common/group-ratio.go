package common

import (
	"encoding/json"

	"github.com/songquanpeng/one-api/common/logger"
)

var GroupRatio = map[string]float64{
	"Lv1": 1,
	"Lv2": 1,
	"Lv3": 1,
	"Lv4": 1,
	"Lv5": 1,
	"Lv6": 1,
}

func GroupRatio2JSONString() string {
	jsonBytes, err := json.Marshal(GroupRatio)
	if err != nil {
		logger.SysError("error marshalling model ratio: " + err.Error())
	}
	return string(jsonBytes)
}

func UpdateGroupRatioByJSONString(jsonStr string) error {
	GroupRatio = make(map[string]float64)
	return json.Unmarshal([]byte(jsonStr), &GroupRatio)
}

func GetGroupRatio(name string) float64 {
	ratio, ok := GroupRatio[name]
	if !ok {
		logger.SysError("group ratio not found: " + name)
		return 1
	}
	return ratio
}
