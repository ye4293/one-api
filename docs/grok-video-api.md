# Grok 视频生成 API 调用文档

## 概述

Grok 视频 API 支持两种模式：
- **视频生成**：根据文本提示生成视频
- **视频编辑**：基于已有视频进行编辑

模型名称：`grok-imagine-video`

---

## API 端点

### 创建视频任务

**POST** `/v1/video/generations`

### 查询任务结果

**GET** `/v1/video/generations/result?taskid={request_id}`

---

## 请求格式

### 1. 纯文本生成视频

```json
{
  "prompt": "A cat playing with a ball in a sunny garden",
  "model": "grok-imagine-video",
  "duration": 6,
  "resolution": "480p"
}
```

### 2. 图片 + 文本生成视频

```json
{
  "prompt": "Make the character wave hello",
  "model": "grok-imagine-video",
  "image": {
    "url": "https://example.com/image.jpg"
  },
  "duration": 6,
  "resolution": "480p"
}
```

### 3. 视频编辑（基于已有视频）

```json
{
  "prompt": "Add snow falling to the scene",
  "model": "grok-imagine-video",
  "video": {
    "url": "https://example.com/video.mp4"
  }
}
```

---

## 请求参数说明

| 参数 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| `prompt` | string | 是 | - | 视频生成/编辑的文本描述 |
| `model` | string | 是 | - | 固定为 `grok-imagine-video` |
| `duration` | integer | 否 | 6 | 视频时长（秒），范围：1-15 |
| `resolution` | string | 否 | "480p" | 分辨率，可选：`480p`、`720p` |
| `image` | object | 否 | - | 输入图片（图生视频模式） |
| `image.url` | string | 否 | - | 图片 URL |
| `video` | object | 否 | - | 输入视频（视频编辑模式） |
| `video.url` | string | 否 | - | 视频 URL |

**路由规则：**
- 包含 `video` 参数 → 视频编辑模式
- 包含 `image` 参数（无 `video`）→ 图生视频模式
- 仅 `prompt` → 纯文本生成模式

---

## 响应格式

### 任务创建成功

```json
{
  "task_id": "xai-video-14d3e83e-b09a-413d-b5d2-ab0fdc3fa5a4",
  "task_status": "succeed",
  "message": "Video generation task created successfully"
}
```

### 任务创建失败

```json
{
  "task_id": "",
  "task_status": "failed",
  "message": "Client specified an invalid argument: Duration must be between 1 and 15 seconds"
}
```

### 查询结果 - 处理中

```json
{
  "task_id": "xai-video-14d3e83e-b09a-413d-b5d2-ab0fdc3fa5a4",
  "task_status": "processing",
  "message": ""
}
```

### 查询结果 - 完成

```json
{
  "task_id": "xai-video-14d3e83e-b09a-413d-b5d2-ab0fdc3fa5a4",
  "task_status": "succeed",
  "message": "",
  "video_url": ["https://vidgen.x.ai/xai-vidgen-bucket/xai-video-xxx.mp4"]
}
```

### 查询结果 - 失败

```json
{
  "task_id": "xai-video-14d3e83e-b09a-413d-b5d2-ab0fdc3fa5a4",
  "task_status": "failed",
  "message": "Client specified an invalid argument: Generated video rejected by content moderation."
}
```

---

## 计费标准

### 输出费用

| 分辨率 | 价格 |
|--------|------|
| 480p | $0.05/秒 |
| 720p | $0.07/秒 |

### 输入费用

| 输入类型 | 价格 |
|----------|------|
| 图片 | $0.002/张 |
| 视频 | $0.01/秒（按输出时长估算） |

### 计费示例

| 场景 | 时长 | 分辨率 | 输入 | 费用计算 |
|------|------|--------|------|----------|
| 纯文本生成 | 6秒 | 480p | 无 | 6 × $0.05 = $0.30 |
| 图生视频 | 6秒 | 480p | 1张图 | 6 × $0.05 + $0.002 = $0.302 |
| 视频编辑 | 10秒 | 720p | 视频 | 10 × $0.07 + 10 × $0.01 = $0.80 |

---

## 调用示例

> 端点：`https://api.ezlinkai.com`  
> 认证：`Authorization: Bearer YOUR_API_KEY`

---

### 1. 纯文本生成视频（默认参数）

```bash
curl -X POST "https://api.ezlinkai.com/v1/video/generations" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"prompt": "A cat playing with a ball in a sunny garden", "model": "grok-imagine-video"}'
```

### 2. 文本生成视频（自定义时长和分辨率）

```bash
curl -X POST "https://api.ezlinkai.com/v1/video/generations" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"prompt": "A futuristic city with flying cars at sunset", "model": "grok-imagine-video", "duration": 10, "resolution": "720p"}'
```

### 3. 图片生成视频

```bash
curl -X POST "https://api.ezlinkai.com/v1/video/generations" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"prompt": "Make the character dance", "model": "grok-imagine-video", "image": {"url": "https://example.com/image.jpg"}, "duration": 6}'
```

### 4. 视频编辑

```bash
curl -X POST "https://api.ezlinkai.com/v1/video/generations" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"prompt": "Add snow falling to the scene", "model": "grok-imagine-video", "video": {"url": "https://example.com/video.mp4"}}'
```

### 5. 查询任务结果

```bash
curl -X GET "https://api.ezlinkai.com/v1/video/generations/result?taskid=YOUR_TASK_ID" \
  -H "Authorization: Bearer YOUR_API_KEY"
```

---

**快速测试流程：**
1. 执行创建任务的 curl，获取返回的 `task_id`
2. 将 `task_id` 替换到查询命令中
3. 每隔 10-30 秒执行查询命令，直到 `task_status` 变为 `succeed` 或 `failed`

---

### Python 示例

```python
import requests
import time

API_BASE = "https://api.ezlinkai.com"
API_KEY = "YOUR_API_KEY"

headers = {
    "Authorization": f"Bearer {API_KEY}",
    "Content-Type": "application/json"
}

# 1. 创建视频任务
response = requests.post(
    f"{API_BASE}/v1/video/generations",
    headers=headers,
    json={
        "prompt": "A cat playing with a ball",
        "model": "grok-imagine-video",
        "duration": 6,
        "resolution": "480p"
    }
)

result = response.json()
task_id = result["task_id"]
print(f"Task created: {task_id}")

# 2. 轮询查询结果
while True:
    response = requests.get(
        f"{API_BASE}/v1/video/generations/result",
        headers=headers,
        params={"taskid": task_id}
    )
    result = response.json()
    status = result["task_status"]
    
    if status == "succeed":
        print(f"Video URL: {result['video_url']}")
        break
    elif status == "failed":
        print(f"Failed: {result['message']}")
        break
    else:
        print("Processing... waiting 10 seconds")
        time.sleep(10)
```

---

## 注意事项

1. **异步任务**：视频生成是异步的，创建任务后需轮询查询结果
2. **轮询间隔**：建议 10-30 秒轮询一次，避免频繁请求
3. **视频时长限制**：1-15 秒
4. **多 Key 渠道**：系统会自动使用创建任务时的 Key 进行后续查询，支持多 Key 负载均衡
5. **自动重试**：任务创建失败时会自动重试其他渠道
6. **自动禁用**：遇到认证错误等会自动禁用对应 Key

---

## 任务类型标识

系统会根据输入自动标记任务类型，用于日志和统计：

| 输入 | 任务类型 |
|------|----------|
| 纯文本 | `video-generation` |
| 文本 + 图片 | `video-generation+image` |
| 文本 + 视频 | `video-edit` |
