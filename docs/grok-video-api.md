# Grok Video API 调用文档

## 接口总览

| 操作 | 方法 | 路径 |
|------|------|------|
| 生成视频 | POST | `/v1/video/generations` |
| 编辑视频 | POST | `/v1/video/generations` |
| 延长视频 | POST | `/v1/video/generations` |
| 查询结果 | GET  | `/v1/video/generations/result?taskid={task_id}` |

三种操作共用同一提交端点，通过 **model 名称** 和 **是否传入 video.url** 区分路由：

| 条件 | 路由到 xAI 端点 |
|------|-----------------|
| model = `grok-imagine-video`，无 video.url | `/v1/videos/generations` |
| model = `grok-imagine-video`，有 video.url | `/v1/videos/edits` |
| model = `grok-imagine-video-extensions` | `/v1/videos/extensions` |

---

## 认证

所有请求需要在 Header 中携带 API Key：

```
Authorization: Bearer {YOUR_API_KEY}
Content-Type: application/json
```

---

## 1. 生成视频（文字/图片生成视频）

### 请求

```bash
curl -X POST http://your-api-host/v1/video/generations \
  -H "Authorization: Bearer sk-xxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "grok-imagine-video",
    "prompt": "一只猫在公园里散步，阳光明媚",
    "duration": 8,
    "resolution": "480p"
  }'
```

### 参数说明

| 参数 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| model | string | 是 | — | 固定为 `grok-imagine-video` |
| prompt | string | 是 | — | 视频描述文本 |
| duration | int | 否 | 8 | 视频时长（秒），范围 1-15 |
| resolution | string | 否 | "480p" | 分辨率：`480p` 或 `720p` |
| image | object | 否 | — | 图片输入，结构：`{"url": "https://..."}` |

### 图片生成视频 示例

```bash
curl -X POST http://your-api-host/v1/video/generations \
  -H "Authorization: Bearer sk-xxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "grok-imagine-video",
    "prompt": "让图片中的人物跳舞",
    "duration": 10,
    "resolution": "720p",
    "image": {
      "url": "https://example.com/photo.jpg"
    }
  }'
```

### 响应

```json
{
  "task_id": "req_abc123def456",
  "task_status": "succeed",
  "message": ""
}
```

### 计费

```
费用 = duration × 输出单价 + (有图片 ? $0.002 : $0)

输出单价: 480p = $0.05/秒, 720p = $0.07/秒
```

示例：480p、8 秒、纯文字 → 8 × $0.05 = **$0.40**
示例：720p、10 秒、带图片 → 10 × $0.07 + $0.002 = **$0.702**

---

## 2. 编辑视频

对已有视频进行编辑修改。**注意：此接口没有 duration 参数**，传入会被自动移除。

### 请求

```bash
curl -X POST http://your-api-host/v1/video/generations \
  -H "Authorization: Bearer sk-xxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "grok-imagine-video",
    "prompt": "将视频中的背景替换为海滩场景",
    "video": {
      "url": "https://example.com/source-video.mp4"
    }
  }'
```

### 参数说明

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| model | string | 是 | 固定为 `grok-imagine-video` |
| prompt | string | 是 | 编辑指令 |
| video | object | 是 | 源视频，结构：`{"url": "https://...mp4"}` |

### 响应

```json
{
  "task_id": "req_edit789xyz",
  "task_status": "succeed",
  "message": "",
  "video_duration": 12.5
}
```

> `video_duration`：服务端自动解析输入视频的时长（秒），用于客户端统计。仅在有 video.url 时返回。

### 计费

```
费用 = 输入视频时长 × (输出单价 + $0.01)
```

示例：480p、输入视频 10 秒 → 10 × ($0.05 + $0.01) = **$0.60**
示例：720p、输入视频 15 秒 → 15 × ($0.07 + $0.01) = **$1.20**

> 如果服务端无法解析输入视频时长，将使用 $0.20 预扣费兜底。

---

## 3. 延长视频

对已有视频进行续写延长。

### 请求

```bash
curl -X POST http://your-api-host/v1/video/generations \
  -H "Authorization: Bearer sk-xxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "grok-imagine-video-extensions",
    "prompt": "继续场景，镜头缓慢拉远，展现全景",
    "duration": 6,
    "video": {
      "url": "https://example.com/source-video.mp4"
    }
  }'
```

### 参数说明

| 参数 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| model | string | 是 | — | 固定为 `grok-imagine-video-extensions` |
| prompt | string | 是 | — | 续写描述 |
| duration | int | 否 | 6 | 延长时长（秒），范围 1-10 |
| video | object | **是** | — | 源视频（必填），结构：`{"url": "https://...mp4"}` |

### 响应

```json
{
  "task_id": "req_ext456abc",
  "task_status": "succeed",
  "message": "",
  "video_duration": 8.0
}
```

> `video_duration`：输入视频的时长（秒）。

### 计费

```
费用 = duration × 输出单价 + 输入视频时长 × $0.01
```

示例：480p、延长 6 秒、输入视频 8 秒 → 6 × $0.05 + 8 × $0.01 = **$0.38**
示例：720p、延长 10 秒、输入视频 15 秒 → 10 × $0.07 + 15 × $0.01 = **$0.85**

---

## 4. 查询任务结果

所有视频任务提交后返回 `task_id`，通过此接口轮询结果。

### 请求

```bash
curl -X GET "http://your-api-host/v1/video/generations/result?taskid=req_abc123def456" \
  -H "Authorization: Bearer sk-xxx"
```

### 响应 — 处理中

```json
{
  "task_id": "req_abc123def456",
  "video_id": "req_abc123def456",
  "task_status": "processing",
  "message": "Video generation in progress",
  "duration": "8"
}
```

### 响应 — 完成

```json
{
  "task_id": "req_abc123def456",
  "video_id": "req_abc123def456",
  "task_status": "succeed",
  "message": "Video generation completed",
  "video_result": "https://vidgen.x.ai/xai-vidgen-bucket/video-xxxxx.mp4",
  "video_results": [
    { "url": "https://vidgen.x.ai/xai-vidgen-bucket/video-xxxxx.mp4" }
  ],
  "duration": "8",
  "usage": {
    "cost_in_usd": 0.05
  }
}
```

### 响应 — 失败

```json
{
  "task_id": "req_abc123def456",
  "video_id": "req_abc123def456",
  "task_status": "failed",
  "message": "INVALID_REQUEST_ERROR: Invalid prompt",
  "duration": "8"
}
```

### 响应字段说明

| 字段 | 类型 | 说明 |
|------|------|------|
| task_id | string | 任务 ID |
| video_id | string | 视频 ID（与 task_id 相同） |
| task_status | string | `processing` / `succeed` / `failed` |
| message | string | 状态描述信息 |
| video_result | string | 视频下载 URL（完成时） |
| video_results | array | 视频 URL 数组（完成时） |
| duration | string | 视频时长（秒） |
| usage | object | 费用信息（完成时），`cost_in_usd` 为美元费用 |

> `usage.cost_in_usd` 由 xAI 返回的 `cost_in_usd_ticks` 转换而来（1 美元 = 10,000,000,000 ticks）。

---

## 定价参考

| 项目 | 价格 |
|------|------|
| 文本输入 | 免费 |
| 图片输入 | $0.002 / 张 |
| 视频输入 | $0.01 / 秒 |
| 输出 480p | $0.05 / 秒 |
| 输出 720p | $0.07 / 秒 |

---

## 完整调用流程示例

```bash
# 步骤 1：提交生成任务
TASK_ID=$(curl -s -X POST http://your-api-host/v1/video/generations \
  -H "Authorization: Bearer sk-xxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "grok-imagine-video",
    "prompt": "一片宁静的湖面，倒映着远山",
    "duration": 8,
    "resolution": "480p"
  }' | jq -r '.task_id')

echo "Task ID: $TASK_ID"

# 步骤 2：轮询结果（建议间隔 5-10 秒）
while true; do
  RESULT=$(curl -s "http://your-api-host/v1/video/generations/result?taskid=$TASK_ID" \
    -H "Authorization: Bearer sk-xxx")

  STATUS=$(echo $RESULT | jq -r '.task_status')
  echo "Status: $STATUS"

  if [ "$STATUS" = "succeed" ]; then
    echo "Video URL: $(echo $RESULT | jq -r '.video_result')"
    echo "Cost: \$$(echo $RESULT | jq -r '.usage.cost_in_usd')"
    break
  elif [ "$STATUS" = "failed" ]; then
    echo "Failed: $(echo $RESULT | jq -r '.message')"
    break
  fi

  sleep 10
done

# 步骤 3（可选）：延长视频
curl -X POST http://your-api-host/v1/video/generations \
  -H "Authorization: Bearer sk-xxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "grok-imagine-video-extensions",
    "prompt": "镜头继续向前推进，穿过湖面到达远山",
    "duration": 6,
    "video": {
      "url": "'"$(echo $RESULT | jq -r '.video_result')"'"
    }
  }'
```
