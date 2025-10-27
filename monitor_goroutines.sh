#!/bin/bash

# Goroutine 实时监控脚本
# 用于线上环境监控 goroutine 数量

# 配置
API_URL=${1:-"http://localhost:3000"}
INTERVAL=${2:-5}  # 默认5秒刷新一次

echo "================================"
echo "  Goroutine 实时监控"
echo "================================"
echo "API地址: $API_URL"
echo "刷新间隔: ${INTERVAL}秒"
echo "按 Ctrl+C 停止监控"
echo "================================"
echo ""

# 颜色代码
RED='\033[0;31m'
YELLOW='\033[1;33m'
GREEN='\033[0;32m'
NC='\033[0m' # No Color

# 记录起始时间
START_TIME=$(date +%s)
MAX_GOROUTINES=0
MIN_GOROUTINES=999999

while true; do
    # 获取当前时间
    CURRENT_TIME=$(date '+%H:%M:%S')
    ELAPSED=$(($(date +%s) - START_TIME))
    ELAPSED_MIN=$((ELAPSED / 60))
    ELAPSED_SEC=$((ELAPSED % 60))
    
    # 调用监控API
    RESPONSE=$(curl -s "$API_URL/api/monitor/health" 2>/dev/null)
    
    if [ $? -eq 0 ] && [ ! -z "$RESPONSE" ]; then
        # 解析JSON响应
        GOROUTINES=$(echo "$RESPONSE" | grep -o '"goroutines":[0-9]*' | grep -o '[0-9]*')
        ALLOC_MB=$(echo "$RESPONSE" | grep -o '"alloc_mb":[0-9]*' | grep -o '[0-9]*')
        SYS_MB=$(echo "$RESPONSE" | grep -o '"sys_mb":[0-9]*' | grep -o '[0-9]*')
        NUM_GC=$(echo "$RESPONSE" | grep -o '"num_gc":[0-9]*' | grep -o '[0-9]*')
        
        # 更新统计
        if [ $GOROUTINES -gt $MAX_GOROUTINES ]; then
            MAX_GOROUTINES=$GOROUTINES
        fi
        if [ $GOROUTINES -lt $MIN_GOROUTINES ]; then
            MIN_GOROUTINES=$GOROUTINES
        fi
        
        # 清屏并显示（可选，注释掉则不清屏）
        # clear
        
        # 显示标题
        echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
        echo "  实时监控 - [$CURRENT_TIME] 运行时长: ${ELAPSED_MIN}分${ELAPSED_SEC}秒"
        echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
        echo ""
        
        # 根据goroutine数量显示不同颜色
        if [ $GOROUTINES -gt 5000 ]; then
            COLOR=$RED
            STATUS="🔴 危险"
        elif [ $GOROUTINES -gt 2000 ]; then
            COLOR=$YELLOW
            STATUS="🟡 警告"
        else
            COLOR=$GREEN
            STATUS="🟢 正常"
        fi
        
        echo -e "${COLOR}📊 Goroutines: ${GOROUTINES} ${STATUS}${NC}"
        echo "💾 内存分配: ${ALLOC_MB} MB"
        echo "💽 系统内存: ${SYS_MB} MB"
        echo "🔄 GC次数: ${NUM_GC}"
        echo ""
        echo "📈 统计信息:"
        echo "   最大值: $MAX_GOROUTINES"
        echo "   最小值: $MIN_GOROUTINES"
        echo "   当前值: $GOROUTINES"
        echo ""
        
        # 显示健康建议
        if [ $GOROUTINES -gt 10000 ]; then
            echo "⚠️  严重警告: Goroutine 数量超过 10,000！"
            echo "   建议立即检查是否有新的泄漏问题"
            echo "   可能需要重启服务"
        elif [ $GOROUTINES -gt 5000 ]; then
            echo "⚠️  警告: Goroutine 数量超过 5,000"
            echo "   请检查日志，可能存在异常"
        elif [ $GOROUTINES -gt 2000 ]; then
            echo "ℹ️  提示: Goroutine 数量略高，属于高负载情况"
        else
            echo "✅ Goroutine 数量正常"
        fi
        
    else
        echo "❌ 无法连接到API: $API_URL"
        echo "   请检查："
        echo "   1. 服务是否运行"
        echo "   2. URL是否正确"
        echo "   3. 网络是否通畅"
    fi
    
    echo ""
    echo "下次刷新: ${INTERVAL}秒后... (Ctrl+C 停止)"
    echo ""
    
    # 等待下一次检查
    sleep $INTERVAL
done

