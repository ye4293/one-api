package model

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/songquanpeng/one-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 只串行化新增的缓存价格保存和刷新，不改变其他 Option 的更新流程。
var cacheWriteOptionMu sync.Mutex

func isCacheWritePriceOption(key string) bool {
	return key == "CacheWriteRatio" || key == "CacheWrite12hRatio"
}

// UpdateModelCacheWriteRatio 合并数据库最新价格，避免从运行时快照覆盖其他模型。
func UpdateModelCacheWriteRatio(key, modelName string, ratio float64) error {
	if !isCacheWritePriceOption(key) {
		return fmt.Errorf("unsupported cache price option %q", key)
	}
	entry, err := json.Marshal(map[string]float64{modelName: ratio})
	if err != nil {
		return fmt.Errorf("encode cache price: %w", err)
	}
	if err := common.ValidateCacheWriteRatioJSON(string(entry)); err != nil {
		return err
	}
	return persistCacheWriteOption(key, func(current string) (string, error) {
		if err := common.ValidateCacheWriteRatioJSON(current); err != nil {
			return "", fmt.Errorf("invalid stored %s: %w", key, err)
		}
		var values map[string]float64
		if err := json.Unmarshal([]byte(current), &values); err != nil {
			return "", err
		}
		values[modelName] = ratio
		data, err := json.Marshal(values)
		return string(data), err
	})
}

func persistCacheWriteOption(key string, next func(string) (string, error)) error {
	cacheWriteOptionMu.Lock()
	defer cacheWriteOptionMu.Unlock()
	var value string
	err := DB.Transaction(func(tx *gorm.DB) error {
		option := Option{Key: key, Value: "{}"}
		// 行不存在时先创建；数据库行锁保护多个实例的单模型合并。
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&option).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(&Option{Key: key}).First(&option).Error; err != nil {
			return err
		}
		var err error
		value, err = next(option.Value)
		if err != nil {
			return err
		}
		if err := common.ValidateCacheWriteRatioJSON(value); err != nil {
			return err
		}
		return tx.Model(&option).Update("value", value).Error
	})
	if err != nil {
		return err
	}
	// 提交成功才发布；与刷新共享锁，避免本实例发生持久化和发布顺序反转。
	return updateOptionMap(key, value)
}

func refreshCacheWriteOption(key string) error {
	cacheWriteOptionMu.Lock()
	defer cacheWriteOptionMu.Unlock()
	// AllOption 的快照可能早于一次保存，因此必须在锁内重新读取。
	var option Option
	if err := DB.Where(&Option{Key: key}).First(&option).Error; err != nil {
		return err
	}
	return updateOptionMap(key, option.Value)
}
