package audit

import (
	"context"
	"fmt"
	"time"

	"github.com/songquanpeng/one-api/common/logger"
)

func RunCompaction(ctx context.Context) {
	if awsClient == nil {
		return
	}

	compactCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	tableRef := fmt.Sprintf(`"%s"."%s"`, pkgConfig.AthenaDatabase, pkgConfig.AthenaTable)

	if pkgConfig.RetentionDays > 0 {
		runRetention(compactCtx, tableRef)
	}

	sql := fmt.Sprintf("OPTIMIZE %s REWRITE DATA USING BIN_PACK", tableRef)
	_, err := awsClient.executeQuery(compactCtx, sql)
	if err != nil {
		logger.SysError("audit: compaction failed: " + err.Error())
		return
	}
	logger.SysLog("audit: compaction completed")
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
