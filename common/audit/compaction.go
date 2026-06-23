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
	tableRef := fmt.Sprintf(`"%s"."%s"`, pkgConfig.AthenaDatabase, pkgConfig.AthenaTable)
	sql := fmt.Sprintf("OPTIMIZE %s REWRITE DATA USING BIN_PACK", tableRef)

	compactCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	_, err := awsClient.executeQuery(compactCtx, sql)
	if err != nil {
		logger.SysError("audit: compaction failed: " + err.Error())
		return
	}
	logger.SysLog("audit: compaction completed")
}
