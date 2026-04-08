package model

import "gorm.io/gorm"

// findMaxIdByTimestampGeneric 二分查找某张表中 created_at < timestamp 的最大 id。
// 返回值：
//   - id > 0：找到的最大 id
//   - id == 0, found == true：表不为空，但所有记录都 >= timestamp（即没有满足条件的记录）
//   - id == 0, found == false：表为空或查询出错
func findMaxIdByTimestampGeneric(db *gorm.DB, tableName string, timestamp int64) (id int64, found bool) {
	var minId, maxId int64
	if err := db.Table(tableName).Select("MIN(id)").Scan(&minId).Error; err != nil || minId == 0 {
		return 0, false
	}
	if err := db.Table(tableName).Select("MAX(id)").Scan(&maxId).Error; err != nil || maxId == 0 {
		return 0, false
	}

	// 边界快速判断：最小 id 的记录是否 >= timestamp
	var createdAt int64
	if err := db.Table(tableName).Select("created_at").Where("id = ?", minId).Scan(&createdAt).Error; err != nil {
		return 0, false
	}
	if createdAt >= timestamp {
		return 0, true // 表不为空，但所有记录都 >= timestamp
	}

	// 最大 id 的记录是否 < timestamp
	if err := db.Table(tableName).Select("created_at").Where("id = ?", maxId).Scan(&createdAt).Error; err != nil {
		return 0, false
	}
	if createdAt < timestamp {
		return maxId, true // 所有记录都 < timestamp
	}

	// 二分查找
	var resultId int64
	for i := 0; minId < maxId && i < 100; i++ {
		midId := (minId + maxId + 1) / 2
		var row struct {
			Id        int64
			CreatedAt int64
		}
		err := db.Table(tableName).Select("id, created_at").Where("id >= ?", midId).Order("id asc").Limit(1).Scan(&row).Error
		if err != nil || row.Id == 0 {
			maxId = midId - 1
			continue
		}
		if row.CreatedAt < timestamp {
			resultId = row.Id
			minId = row.Id + 1
		} else {
			maxId = row.Id - 1
		}
	}
	return resultId, true
}

// applyTimestampIdRange 将时间戳范围转为主键 id 范围并应用到查询。
// startTimestamp: 查找 created_at >= startTimestamp 的记录 → id > maxId(created_at < startTimestamp)
// endTimestamp:   查找 created_at <= endTimestamp 的记录   → id <= maxId(created_at < endTimestamp+1)
// 传 0 表示不限制该方向。
func applyTimestampIdRange(tx *gorm.DB, db *gorm.DB, tableName string, startTimestamp, endTimestamp int64) *gorm.DB {
	if startTimestamp > 0 {
		startId, found := findMaxIdByTimestampGeneric(db, tableName, startTimestamp)
		if found && startId == 0 {
			// 所有记录都 >= startTimestamp，不需要加下界限制
		} else if found && startId > 0 {
			tx = tx.Where("id > ?", startId)
		}
		// !found (表为空/DB错误) → 不加限制，让查询自然返回空
	}
	if endTimestamp > 0 {
		endId, found := findMaxIdByTimestampGeneric(db, tableName, endTimestamp+1)
		if found && endId == 0 {
			// 所有记录都 >= endTimestamp+1，即所有记录的 created_at > endTimestamp
			// 应查出 0 条 → 加一个不可能满足的条件
			tx = tx.Where("1 = 0")
		} else if found && endId > 0 {
			tx = tx.Where("id <= ?", endId)
		}
		// !found (表为空/DB错误) → 不加限制
	}
	return tx
}
