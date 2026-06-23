package audit

import (
	"context"
	"fmt"

	"github.com/songquanpeng/one-api/common/logger"
)

// RunCompaction 执行一次 Iceberg BIN_PACK compaction。
// 由外部定时调度调用，不再内置定时器。
func RunCompaction(ctx context.Context) {
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
	logger.SysLog("audit: compaction completed")
}
