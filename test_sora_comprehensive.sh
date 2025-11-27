#!/bin/bash

# Sora 视频生成综合测试脚本
# 测试原生 form-data 和 JSON 两种格式，以及 input_reference 的不同用法

API_ENDPOINT="${API_ENDPOINT:-http://localhost:3000}"
API_KEY="${API_KEY:-your_api_key_here}"

echo "========================================="
echo "Sora 视频生成 - 综合测试"
echo "========================================="
echo "API Endpoint: $API_ENDPOINT"
echo ""

# 测试 1: JSON 格式 - 基础文本生成视频
echo "测试 1: JSON 格式 - 基础文本生成视频"
echo "-----------------------------------------"
curl -X POST "${API_ENDPOINT}/v1/videos/generations" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${API_KEY}" \
  -d '{
    "model": "sora-2",
    "prompt": "一只可爱的小猫在草地上玩耍",
    "seconds": 5,
    "size": "720x1280"
  }' | jq .
echo ""
echo ""

# 测试 2: JSON 格式 - 使用 URL 参考图片
echo "测试 2: JSON 格式 - 使用 URL 参考图片"
echo "-----------------------------------------"
curl -X POST "${API_ENDPOINT}/v1/videos/generations" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${API_KEY}" \
  -d '{
    "model": "sora-2-pro",
    "prompt": "基于这张图片生成动态视频",
    "seconds": 5,
    "size": "1280x720",
    "input_reference": "https://example.com/sample-image.jpg"
  }' | jq .
echo ""
echo ""

# 测试 3: JSON 格式 - 高清分辨率
echo "测试 3: JSON 格式 - 高清分辨率"
echo "-----------------------------------------"
curl -X POST "${API_ENDPOINT}/v1/videos/generations" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${API_KEY}" \
  -d '{
    "model": "sora-2-pro",
    "prompt": "壮丽的山脉日出景色，云雾缭绕，金色阳光",
    "seconds": 10,
    "size": "1792x1024"
  }' | jq .
echo ""
echo ""

# 测试 4: Form-data 格式 - 基础文本生成
echo "测试 4: Form-data 格式 - 基础文本生成"
echo "-----------------------------------------"
curl -X POST "${API_ENDPOINT}/v1/videos/generations" \
  -H "Authorization: Bearer ${API_KEY}" \
  -F "model=sora-2" \
  -F "prompt=城市夜景，车水马龙，霓虹灯闪烁" \
  -F "seconds=5" \
  -F "size=1280x720" | jq .
echo ""
echo ""

# 测试 5: Form-data 格式 - 带文件上传（需要本地图片）
if [ -f "test_image.jpg" ]; then
  echo "测试 5: Form-data 格式 - 带文件上传"
  echo "-----------------------------------------"
  curl -X POST "${API_ENDPOINT}/v1/videos/generations" \
    -H "Authorization: Bearer ${API_KEY}" \
    -F "model=sora-2-pro" \
    -F "prompt=基于这张图片生成动态视频" \
    -F "seconds=5" \
    -F "size=1280x720" \
    -F "input_reference=@test_image.jpg" | jq .
  echo ""
  echo ""
else
  echo "测试 5: 跳过 (缺少 test_image.jpg)"
  echo ""
fi

echo "========================================="
echo "测试完成"
echo "========================================="
echo ""
echo "注意事项："
echo "1. 请确保设置了正确的 API_ENDPOINT 和 API_KEY"
echo "2. seconds 字段使用的是官方字段名（不是 duration）"
echo "3. input_reference 支持 URL、base64、data URL 三种格式"
echo "4. form-data 格式是原生格式，性能更好"
echo "5. JSON 格式会自动转换为 form-data 发送"

