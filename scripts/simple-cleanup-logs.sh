#!/bin/bash
# 简单的日志清理脚本 - 基于时间的安全清理

LOG_DIR="/Users/yueqingli/code/one-api/logs"
RETENTION_DAYS=7  # 保留最近 7 天的日志

echo "开始清理超过 ${RETENTION_DAYS} 天的日志..."

# 清理普通日志
DELETED_COUNT=0
for file in $(find "$LOG_DIR" -name "oneapi-*.log" -type f -mtime +$RETENTION_DAYS); do
    echo "删除: $(basename "$file")"
    rm -f "$file"
    ((DELETED_COUNT++))
done

# 清理错误日志
for file in $(find "$LOG_DIR" -name "oneapi-error-*.log" -type f -mtime +$RETENTION_DAYS); do
    echo "删除: $(basename "$file")"
    rm -f "$file"
    ((DELETED_COUNT++))
done

if [ $DELETED_COUNT -eq 0 ]; then
    echo "没有需要清理的旧日志"
else
    echo "✅ 已清理 ${DELETED_COUNT} 个日志文件"
fi
