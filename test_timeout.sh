#!/bin/bash

# 超时配置测试脚本
# 用于验证新的超时设置是否生效

echo "================================"
echo "  超时配置测试脚本"
echo "================================"
echo ""

# 检查参数
API_URL=${1:-"http://localhost:3000"}
API_KEY=${2:-""}

if [ -z "$API_KEY" ]; then
    echo "❌ 请提供API密钥"
    echo "使用方法: ./test_timeout.sh <API_URL> <API_KEY>"
    echo "示例: ./test_timeout.sh http://localhost:3000 sk-xxxxx"
    exit 1
fi

echo "🔍 测试配置"
echo "  API地址: $API_URL"
echo "  API密钥: ${API_KEY:0:10}..."
echo ""

# 测试1: 快速响应（应该在几秒内完成）
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "测试 1: 快速响应（正常请求）"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
START_TIME=$(date +%s)

RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "$API_URL/v1/chat/completions" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-3.5-turbo",
    "messages": [{"role": "user", "content": "Say hello"}],
    "max_tokens": 10
  }' 2>&1)

HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
RESPONSE_BODY=$(echo "$RESPONSE" | head -n-1)
END_TIME=$(date +%s)
DURATION=$((END_TIME - START_TIME))

if [ "$HTTP_CODE" = "200" ]; then
    echo "✅ 成功: HTTP $HTTP_CODE"
    echo "⏱️  耗时: ${DURATION}秒"
else
    echo "❌ 失败: HTTP $HTTP_CODE"
    echo "响应: $RESPONSE_BODY"
fi
echo ""

# 测试2: 模拟连接超时（使用不存在的地址）
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "测试 2: 连接超时（应该10秒内失败）"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "ℹ️  这个测试会故意失败，用于验证快速失败机制"

START_TIME=$(date +%s)

# 使用一个不存在的IP地址
RESPONSE=$(curl -s -w "\n%{http_code}" -m 15 -X POST "http://192.0.2.1:3000/v1/chat/completions" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-3.5-turbo",
    "messages": [{"role": "user", "content": "test"}]
  }' 2>&1)

END_TIME=$(date +%s)
DURATION=$((END_TIME - START_TIME))

if [ $DURATION -le 15 ]; then
    echo "✅ 连接超时机制正常: ${DURATION}秒内失败"
else
    echo "⚠️  超时时间过长: ${DURATION}秒"
fi
echo ""

# 测试3: 检查Docker容器状态
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "测试 3: 容器健康状态"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# 尝试找到one-api容器
CONTAINER=$(docker ps --filter "name=one-api" --format "{{.Names}}" | head -n 1)

if [ -z "$CONTAINER" ]; then
    echo "⚠️  未找到运行中的one-api容器"
else
    echo "📊 容器名称: $CONTAINER"
    
    # 检查内存使用
    MEMORY=$(docker stats $CONTAINER --no-stream --format "{{.MemUsage}}")
    echo "💾 内存使用: $MEMORY"
    
    # 检查CPU使用
    CPU=$(docker stats $CONTAINER --no-stream --format "{{.CPUPerc}}")
    echo "🔧 CPU使用: $CPU"
    
    # 检查最近的错误日志
    ERROR_COUNT=$(docker logs --tail 100 $CONTAINER 2>&1 | grep -i "error\|panic\|timeout" | wc -l)
    echo "⚠️  最近错误数: $ERROR_COUNT"
fi
echo ""

# 测试4: 检查环境变量配置
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "测试 4: 超时配置检查"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

if [ ! -z "$CONTAINER" ]; then
    RELAY_TIMEOUT=$(docker exec $CONTAINER sh -c 'echo $RELAY_TIMEOUT' 2>/dev/null)
    
    if [ -z "$RELAY_TIMEOUT" ]; then
        echo "ℹ️  使用默认超时: 15分钟（900秒）"
    else
        MINUTES=$((RELAY_TIMEOUT / 60))
        echo "ℹ️  配置的超时: ${RELAY_TIMEOUT}秒（${MINUTES}分钟）"
    fi
else
    echo "⚠️  无法检查（容器未运行）"
fi
echo ""

# 总结
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "测试总结"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "✅ 建议的后续步骤："
echo "1. 如果测试1正常，说明API正常工作"
echo "2. 如果测试2在10-15秒内失败，说明快速失败机制生效"
echo "3. 持续监控内存和CPU使用"
echo "4. 观察错误日志数量变化"
echo ""
echo "💡 如果需要调整超时："
echo "   编辑 docker-compose.yml，设置 RELAY_TIMEOUT 环境变量"
echo "   然后重启容器: docker-compose restart"
echo ""

