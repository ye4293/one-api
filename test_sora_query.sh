#!/bin/bash

# Sora 视频查询功能测试脚本

API_ENDPOINT="${API_ENDPOINT:-http://localhost:3000}"
API_KEY="${API_KEY:-your_api_key_here}"
TASK_ID="${TASK_ID:-video_123}"

echo "========================================="
echo "Sora 视频查询功能测试"
echo "========================================="
echo "API Endpoint: $API_ENDPOINT"
echo "Task ID: $TASK_ID"
echo ""
echo "统一查询地址: /v1/video/generations/result"
echo ""

# 测试 1: 查询视频状态
echo "测试 1: 查询 Sora 视频状态"
echo "-----------------------------------------"
echo "请求: 查询任务 ${TASK_ID} 的状态"
curl -X POST "${API_ENDPOINT}/v1/video/generations/result" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${API_KEY}" \
  -d "{
    \"task_id\": \"${TASK_ID}\"
  }" | jq .
echo ""
echo ""

# 测试 2: 查询另一个视频
echo "测试 2: 查询另一个视频（可能已完成）"
echo "-----------------------------------------"
TASK_ID_2="${TASK_ID_2:-video_456}"
curl -X POST "${API_ENDPOINT}/v1/video/generations/result" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${API_KEY}" \
  -d "{
    \"task_id\": \"${TASK_ID_2}\"
  }" | jq .
echo ""
echo ""

# 测试 3: 查询不存在的视频
echo "测试 3: 错误处理 - 查询不存在的视频"
echo "-----------------------------------------"
curl -X POST "${API_ENDPOINT}/v1/video/generations/result" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${API_KEY}" \
  -d '{
    "task_id": "video_nonexistent_999"
  }' | jq .
echo ""
echo ""

echo "========================================="
echo "测试完成"
echo "========================================="
echo ""
echo "响应格式说明："
echo ""
echo "进行中的视频："
echo "{"
echo "  \"task_id\": \"video_123\","
echo "  \"task_status\": \"processing\","
echo "  \"message\": \"Video generation in progress (50%)\","
echo "  \"duration\": \"5\""
echo "}"
echo ""
echo "已完成的视频（首次查询）："
echo "{"
echo "  \"task_id\": \"video_123\","
echo "  \"video_result\": \"https://file.ezlinkai.com/123_video.mp4\","
echo "  \"task_status\": \"succeed\","
echo "  \"message\": \"Video generation completed and uploaded to R2\","
echo "  \"duration\": \"5\""
echo "}"
echo ""
echo "已完成的视频（缓存）："
echo "{"
echo "  \"task_id\": \"video_123\","
echo "  \"video_result\": \"https://file.ezlinkai.com/123_video.mp4\","
echo "  \"task_status\": \"succeed\","
echo "  \"message\": \"Video retrieved from cache\","
echo "  \"duration\": \"5\""
echo "}"
echo ""
echo "处理流程："
echo "1. 检查数据库 storeurl - 如有缓存直接返回"
echo "2. 调用 OpenAI GET /v1/videos/{id} 查询状态"
echo "3. 如果 status = 'completed':"
echo "   a. 调用 GET /v1/videos/{id}/content 下载视频"
echo "   b. 上传到 Cloudflare R2"
echo "   c. 保存 URL 到数据库 storeurl"
echo "   d. 返回 URL"
echo "4. 如果未完成，返回当前状态和进度"

