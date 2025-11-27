#!/bin/bash

# Sora Remix 功能测试脚本 (使用 model 参数识别)

API_ENDPOINT="${API_ENDPOINT:-http://localhost:3000}"
API_KEY="${API_KEY:-your_api_key_here}"
VIDEO_ID="${VIDEO_ID:-video_123}"

echo "========================================="
echo "Sora Remix 功能测试（model参数方式）"
echo "========================================="
echo "API Endpoint: $API_ENDPOINT"
echo "Original Video ID: $VIDEO_ID"
echo ""
echo "说明："
echo "- 使用 model: 'sora-2-remix' 来识别 remix 请求"
echo "- 系统会自动去掉 model 参数后发送给 OpenAI"
echo "- 只有 prompt 会被发送到 OpenAI API"
echo ""

# 测试 1: 使用 sora-2-remix 模型
echo "测试 1: 基础 Remix 请求 (model: sora-2-remix)"
echo "-----------------------------------------"
echo "请求: 延长场景，猫向观众鞠躬"
curl -X POST "${API_ENDPOINT}/v1/videos" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${API_KEY}" \
  -d "{
    \"model\": \"sora-2-remix\",
    \"video_id\": \"${VIDEO_ID}\",
    \"prompt\": \"Extend the scene with the cat taking a bow to the cheering audience\"
  }" | jq .
echo ""
echo ""

# 测试 2: 使用 sora-2-pro-remix 模型
echo "测试 2: Pro 版本 Remix (model: sora-2-pro-remix)"
echo "-----------------------------------------"
echo "请求: 转换为夜晚霓虹灯效果"
curl -X POST "${API_ENDPOINT}/v1/videos" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${API_KEY}" \
  -d "{
    \"model\": \"sora-2-pro-remix\",
    \"video_id\": \"${VIDEO_ID}\",
    \"prompt\": \"Transform to nighttime with neon lights and vibrant colors\"
  }" | jq .
echo ""
echo ""

# 测试 3: 添加新元素
echo "测试 3: 添加新元素"
echo "-----------------------------------------"
echo "请求: 添加奔跑的小狗"
curl -X POST "${API_ENDPOINT}/v1/videos" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${API_KEY}" \
  -d "{
    \"model\": \"sora-2-remix\",
    \"video_id\": \"${VIDEO_ID}\",
    \"prompt\": \"Add a playful puppy running across the field\"
  }" | jq .
echo ""
echo ""

# 测试 4: 错误处理 - 不存在的 video_id
echo "测试 4: 错误处理 - 不存在的 video_id"
echo "-----------------------------------------"
curl -X POST "${API_ENDPOINT}/v1/videos" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${API_KEY}" \
  -d '{
    "model": "sora-2-remix",
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
echo "   API_ENDPOINT='http://localhost:3000' API_KEY='sk-xxx' VIDEO_ID='video_123' bash test_sora_remix_updated.sh"
echo ""
echo "请求格式："
echo "- 必需参数："
echo "  * model: 'sora-2-remix' 或 'sora-2-pro-remix'"
echo "  * video_id: 原视频的任务ID"
echo "  * prompt: 新的描述"
echo ""
echo "处理流程："
echo "1. 系统根据 model 参数识别这是 remix 请求"
echo "2. 查找原视频的渠道信息"
echo "3. 构建请求时去掉 model 和 video_id 参数"
echo "4. 只发送 prompt 到 OpenAI: POST /v1/videos/{video_id}/remix"
echo "5. 根据响应的 model, size, seconds 进行计费"


