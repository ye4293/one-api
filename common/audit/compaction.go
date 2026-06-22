package audit

import (
	"context"
	"fmt"
	"time"

	"github.com/songquanpeng/one-api/common/logger"
)

func compactionLoop(ctx context.Context) {
	defer func() { recover() }()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runCompaction(ctx)
		}
	}
}

func runCompaction(ctx context.Context) {
	if awsClient == nil {
		return
	}
	tableRef := fmt.Sprintf(`"%s"."%s"`, pkgConfig.AthenaDatabase, pkgConfig.AthenaTable)
	sql := fmt.Sprintf("OPTIMIZE %s REWRITE DATA USING BIN_PACK", tableRef)

	_, err := awsClient.executeQuery(ctx, sql)
	if err != nil {
		logger.SysError("audit: compaction failed: " + err.Error())
		return
	}
	logger.SysLog("audit: daily compaction completed")
}
