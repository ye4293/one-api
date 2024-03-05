package common

import (
	"fmt"
	"github.com/songquanpeng/one-api/common/config"
)

func LogQuota(quota int) string {
	if config.DisplayInCurrencyEnabled {
		return fmt.Sprintf("＄%.6f quote", float64(quota)/config.QuotaPerUnit)
	} else {
		return fmt.Sprintf("%d quote", quota)
	}
}
