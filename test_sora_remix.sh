#!/bin/bash

# Sora Remix 功能测试脚本

API_ENDPOINT="${API_ENDPOINT:-http://localhost:3000}"
API_KEY="${API_KEY:-your_api_key_here}"
VIDEO_ID="${VIDEO_ID:-video_123}"

echo "========================================="
echo "Sora Remix 功能测试"
echo "========================================="
echo "API Endpoint: $API_ENDPOINT"
echo "Original Video ID: $VIDEO_ID"
echo ""

# 测试 1: 基础 Remix 请求
echo "测试 1: 基础 Remix 请求"
echo "-----------------------------------------"
echo "请求: 延长场景，猫向观众鞠躬"
curl -X POST "${API_ENDPOINT}/v1/videos/remix" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${API_KEY}" \
  -d "{
    \"video_id\": \"${VIDEO_ID}\",
    \"prompt\": \"Extend the scene with the cat taking a bow to the cheering audience\"
  }" | jq .
echo ""
echo ""

# 测试 2: 不同的 Remix 描述
echo "测试 2: 改变视频风格"
echo "-----------------------------------------"
echo "请求: 转换为夜晚霓虹灯效果"
curl -X POST "${API_ENDPOINT}/v1/videos/remix" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${API_KEY}" \
  -d "{
    \"video_id\": \"${VIDEO_ID}\",
    \"prompt\": \"Transform to nighttime with neon lights and vibrant colors\"
  }" | jq .
echo ""
echo ""

# 测试 3: 添加新元素
echo "测试 3: 添加新元素"
echo "-----------------------------------------"
echo "请求: 添加奔跑的小狗"
curl -X POST "${API_ENDPOINT}/v1/videos/remix" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${API_KEY}" \
  -d "{
    \"video_id\": \"${VIDEO_ID}\",
    \"prompt\": \"Add a playful puppy running across the field\"
  }" | jq .
echo ""
echo ""

# 测试 4: 错误处理 - 不存在的 video_id
echo "测试 4: 错误处理 - 不存在的 video_id"
echo "-----------------------------------------"
curl -X POST "${API_ENDPOINT}/v1/videos/remix" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${API_KEY}" \
  -d '{
    "video_id": "video_nonexistent_999",
    "prompt": "Try to remix a non-existent video"
  }' | jq .
echo ""
echo ""

echo "========================================="
echo "测试完成"
echo "========================================="
echo ""
echo "使用说明："
echo "1. 设置环境变量："
echo "   export API_ENDPOINT='http://your-server:3000'"
echo "   export API_KEY='your_api_key'"
echo "   export VIDEO_ID='your_video_id'"
echo ""
echo "2. 或直接运行："
echo "   API_ENDPOINT='http://localhost:3000' API_KEY='sk-xxx' VIDEO_ID='video_123' bash test_sora_remix.sh"
echo ""
echo "注意事项："
echo "- video_id 必须是系统中已存在的视频任务ID"
echo "- 系统会自动使用原视频的渠道和密钥"
echo "- 费用根据响应中的 model、size、seconds 计算"
echo "- 只有成功响应（200）才会扣费"

