#!/bin/bash

# Goroutine 数量监控脚本
# 用于验证修复效果

echo "================================"
echo "  Goroutine 监控脚本"
echo "================================"
echo ""

# 检查是否提供了容器名称
CONTAINER_NAME=${1:-""}

if [ -z "$CONTAINER_NAME" ]; then
    echo "正在查找 one-api 容器..."
    CONTAINER_NAME=$(docker ps --filter "ancestor=one-api" --format "{{.Names}}" | head -n 1)
    
    if [ -z "$CONTAINER_NAME" ]; then
        echo "❌ 未找到运行中的 one-api 容器"
        echo "使用方法: ./check_goroutines.sh [容器名称]"
        exit 1
    fi
fi

echo "📊 监控容器: $CONTAINER_NAME"
echo ""

# 循环监控
INTERVAL=30
COUNT=0
MAX_COUNT=20

while [ $COUNT -lt $MAX_COUNT ]; do
    COUNT=$((COUNT + 1))
    TIMESTAMP=$(date '+%Y-%m-%d %H:%M:%S')
    
    echo "[$TIMESTAMP] 检查 #$COUNT"
    echo "----------------------------------------"
    
    # 获取容器内存使用
    MEMORY=$(docker stats $CONTAINER_NAME --no-stream --format "{{.MemUsage}}" 2>/dev/null)
    echo "💾 内存使用: $MEMORY"
    
    # 检查日志中是否有 panic 或 OOM
    RECENT_ERRORS=$(docker logs --tail 50 $CONTAINER_NAME 2>&1 | grep -i "panic\|out of memory\|killed" | wc -l)
    if [ $RECENT_ERRORS -gt 0 ]; then
        echo "⚠️  发现 $RECENT_ERRORS 个错误日志"
    else
        echo "✅ 无严重错误"
    fi
    
    # 检查连接数（通过 netstat）
    CONN_COUNT=$(docker exec $CONTAINER_NAME sh -c "netstat -an 2>/dev/null | grep ESTABLISHED | wc -l" 2>/dev/null || echo "N/A")
    echo "🔗 活动连接数: $CONN_COUNT"
    
    echo ""
    
    # 如果不是最后一次检查，等待下一个周期
    if [ $COUNT -lt $MAX_COUNT ]; then
        echo "⏳ 等待 ${INTERVAL} 秒后继续..."
        echo ""
        sleep $INTERVAL
    fi
done

echo "================================"
echo "监控完成！"
echo "================================"
echo ""
echo "💡 提示:"
echo "1. 如果内存持续增长 -> 可能还有泄漏"
echo "2. 如果内存稳定 -> 修复生效 ✅"
echo "3. 建议持续监控 24 小时以确认"

