package common

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/songquanpeng/one-api/common/config"
)

func LogQuota(quota int64) string {
	if config.DisplayInCurrencyEnabled {
		return fmt.Sprintf("＄%.6f quote", float64(quota)/config.QuotaPerUnit)
	} else {
		return fmt.Sprintf("%d quote", quota)
	}
}

// ExtractJSONObjects 从一个字符串中提取出所有独立的JSON对象
func ExtractJSONObjects(s string) []string {
	var objects []string
	s = strings.TrimSpace(s)
	balance := 0
	start := -1

	for i, r := range s {
		if r == '{' {
			if balance == 0 {
				start = i
			}
			balance++
		} else if r == '}' {
			if balance > 0 {
				balance--
				if balance == 0 && start != -1 {
					objects = append(objects, s[start:i+1])
					start = -1
				}
			}
		} else {
			// 如果在对象外部遇到非空白字符，说明格式有问题
			if balance == 0 && start == -1 && !unicode.IsSpace(r) {
				// 记录日志或返回错误可能更合适，但为了保持函数签名，
				// 这里我们选择返回已经找到的对象，并停止解析。
				return objects
			}
		}
	}
	return objects
}
