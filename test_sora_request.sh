#!/bin/bash

# Sora 视频生成测试脚本
# 使用方法: bash test_sora_request.sh

# 配置
API_ENDPOINT="http://localhost:3000"  # 修改为你的 API 地址
API_KEY="your_api_key_here"           # 修改为你的 API Key

echo "========================================="
echo "Sora 视频生成 API 测试"
echo "========================================="
echo ""

# 测试 1: sora-2 标准分辨率
echo "测试 1: sora-2 模型，720x1280 分辨率，5秒"
echo "-----------------------------------------"
curl -X POST "${API_ENDPOINT}/v1/videos/generations" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${API_KEY}" \
  -d '{
    "model": "sora-2",
    "prompt": "一只可爱的小猫在草地上玩耍",
    "duration": 5,
    "size": "720x1280"
  }' | jq .

echo ""
echo ""

# 测试 2: sora-2-pro 标准分辨率
echo "测试 2: sora-2-pro 模型，1280x720 分辨率，10秒"
echo "-----------------------------------------"
curl -X POST "${API_ENDPOINT}/v1/videos/generations" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${API_KEY}" \
  -d '{
    "model": "sora-2-pro",
    "prompt": "壮丽的山脉日出景色，云雾缭绕",
    "duration": 10,
    "size": "1280x720"
  }' | jq .

echo ""
echo ""

# 测试 3: sora-2-pro 高分辨率
echo "测试 3: sora-2-pro 模型，1792x1024 分辨率，5秒"
echo "-----------------------------------------"
curl -X POST "${API_ENDPOINT}/v1/videos/generations" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${API_KEY}" \
  -d '{
    "model": "sora-2-pro",
    "prompt": "城市夜景，车水马龙，霓虹灯闪烁",
    "duration": 5,
    "size": "1792x1024"
  }' | jq .

echo ""
echo ""
echo "========================================="
echo "测试完成"
echo "========================================="

