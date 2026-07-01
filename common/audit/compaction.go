package audit

import (
	"context"
	"fmt"
	"time"

	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
)

// master 节点 compactionLoop 每天凌晨 2 点（UTC）执行一次昨日分区合并与数据保留。
func compactionLoop(ctx context.Context) {
	if !config.IsMasterNode {
		return
	}
	defer func() { recover() }()
	for {
		now := time.Now().UTC()
		next := time.Date(now.Year(), now.Month(), now.Day()+1, 2, 0, 0, 0, time.UTC)
		t := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
			RunCompaction(ctx)
		}
	}
}

func RunCompaction(ctx context.Context) {
	RunCompactionForDate(ctx, time.Now().UTC().AddDate(0, 0, -1))
}

func RunCompactionForDate(ctx context.Context, day time.Time) {
	if awsClient == nil {
		return
	}

	compactCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	// OPTIMIZE/DELETE/ALTER 语句不支持双引号 identifier，必须用裸名
	tableRef := fmt.Sprintf(`%s.%s`, pkgConfig.AthenaDatabase, pkgConfig.AthenaTable)
	start := day.Format("2006-01-02")
	end := day.AddDate(0, 0, 1).Format("2006-01-02")

	if pkgConfig.RetentionDays > 0 {
		runRetention(compactCtx, tableRef)
	}

	sql := fmt.Sprintf(
		`OPTIMIZE %s REWRITE DATA USING BIN_PACK`+
			` WHERE event_time >= TIMESTAMP '%s 00:00:00'`+
			` AND event_time < TIMESTAMP '%s 00:00:00'`,
		tableRef, start, end,
	)
	_, err := awsClient.executeQuery(compactCtx, sql)
	if err != nil {
		logger.SysError("audit: compaction failed: " + err.Error())
		return
	}
	logger.SysLog("audit: compaction completed for partition " + start)
}

func runRetention(ctx context.Context, tableRef string) {
	cutoff := time.Now().UTC().AddDate(0, 0, -pkgConfig.RetentionDays).Format("2006-01-02 15:04:05")

	deleteSQL := fmt.Sprintf("DELETE FROM %s WHERE event_time < TIMESTAMP '%s'", tableRef, cutoff)
	_, err := awsClient.executeQuery(ctx, deleteSQL)
	if err != nil {
		logger.SysError("audit: retention delete failed: " + err.Error())
		return
	}

	expireSQL := fmt.Sprintf("ALTER TABLE %s EXECUTE expire_snapshots(retention_threshold => '7d')", tableRef)
	_, err = awsClient.executeQuery(ctx, expireSQL)
	if err != nil {
		logger.SysError("audit: expire_snapshots failed: " + err.Error())
		return
	}
	logger.SysLog(fmt.Sprintf("audit: retention cleanup done (cutoff=%s)", cutoff))
}
