# Sora Remix 功能 - 使用 model 参数识别

## 概述

通过在请求中添加特殊的 `model` 参数（如 `sora-2-remix` 或 `sora-2-pro-remix`）来识别和路由 Remix 请求。系统会自动去掉这个参数后再发送给 OpenAI。

## 为什么需要 model 参数？

由于系统需要通过统一的入口来识别不同类型的请求，使用 `model` 参数可以：
1. 让系统自动识别这是 Remix 请求
2. 保持 API 接口的一致性
3. 方便前端调用（统一的端点）

## 支持的 model 值

| Model 值 | 说明 | 用途 |
|----------|------|------|
| `sora-2-remix` | 标准版 Remix | 基于已有视频创建变体 |
| `sora-2-pro-remix` | 专业版 Remix | 基于已有视频创建高质量变体 |
| 包含 `remix` 的任何值 | 通用 Remix | 自动识别为 Remix 请求 |

## API 调用方式

### 请求端点

```
POST /v1/videos
```

**注意**：使用普通视频生成的端点，但通过 `model` 参数来区分。

### 请求参数

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| model | string | 是 | `sora-2-remix` 或 `sora-2-pro-remix` |
| video_id | string | 是 | 原视频的任务ID |
| prompt | string | 是 | 新的视频描述 |

### 请求示例

```bash
curl -X POST http://localhost:3000/v1/videos \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "sora-2-remix",
    "video_id": "video_123",
    "prompt": "Extend the scene with the cat taking a bow to the cheering audience"
  }'
```

## 处理流程

```
用户请求
    ↓
{
  "model": "sora-2-remix",      ← 用于路由识别
  "video_id": "video_123",
  "prompt": "..."
}
    ↓
系统检测到 model 包含 "remix"
    ↓
路由到 handleSoraRemixRequest
    ↓
查找原视频记录 (video_id)
    ↓
获取原渠道配置
    ↓
构建请求 - 去掉 model 和 video_id
    ↓
发送到 OpenAI:
POST /v1/videos/video_123/remix
{
  "prompt": "..."              ← 只保留 prompt
}
    ↓
接收响应 → 计费 → 返回结果
```

## 参数处理说明

### 请求中的参数

用户发送：
```json
{
  "model": "sora-2-remix",
  "video_id": "video_123",
  "prompt": "Extend the scene..."
}
```

### 发送给 OpenAI 的参数

系统处理后：
```json
{
  "prompt": "Extend the scene..."
}
```

**去掉的参数**：
- ✅ `model` - 仅用于系统路由识别
- ✅ `video_id` - 已在 URL 中使用

**保留的参数**：
- ✅ `prompt` - 发送给 OpenAI

## 路由逻辑

在 `relay/controller/video.go` 中：

```go
if strings.Contains(modelName, "remix") || 
   modelName == "sora-2-remix" || 
   modelName == "sora-2-pro-remix" {
    // Sora Remix 请求
    return handleSoraRemixRequest(c, ctx, meta)
} else if strings.HasPrefix(modelName, "sora") {
    // 普通 Sora 请求
    return handleSoraVideoRequest(c, ctx, videoRequest, meta)
}
```

## 完整示例

### 示例 1: 标准版 Remix

```bash
curl -X POST http://localhost:3000/v1/videos \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "sora-2-remix",
    "video_id": "video_original_cat",
    "prompt": "Extend the scene with the cat taking a bow to the cheering audience"
  }'
```

**系统处理**：
1. 识别 `model: "sora-2-remix"` → 路由到 Remix 处理
2. 查找 `video_original_cat` 的原渠道
3. 发送到 OpenAI：`POST /v1/videos/video_original_cat/remix`
4. 请求体：`{"prompt": "Extend the scene..."}`

### 示例 2: Pro 版本 Remix

```bash
curl -X POST http://localhost:3000/v1/videos \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "sora-2-pro-remix",
    "video_id": "video_city",
    "prompt": "Transform to nighttime with neon lights and vibrant colors"
  }'
```

## 响应格式

```json
{
  "task_id": "video_456",
  "task_status": "succeed",
  "message": "Video remix request submitted successfully, task_id: video_456, remixed_from: video_123"
}
```

## 日志输出

系统会输出以下日志：

```
sora-remix-request: model=sora-2-remix, video_id=video_123, prompt=Extend the scene...
sora-remix: using original channel_id=5, channel_name=OpenAI-Main
sora-remix: sending to OpenAI - URL: https://api.openai.com/v1/videos/video_123/remix, body: {"prompt":"Extend the scene..."} (model param removed)
Sora video pricing: model=sora-2-pro, seconds=8, size=1280x720, pricePerSecond=0.30, totalUSD=2.400000, quota=240
```

## 代码示例

### Python

```python
import requests

def remix_video(video_id, prompt, model="sora-2-remix"):
    url = "http://localhost:3000/v1/videos"
    headers = {
        "Content-Type": "application/json",
        "Authorization": f"Bearer {YOUR_API_KEY}"
    }
    data = {
        "model": model,  # 用于识别 Remix 请求
        "video_id": video_id,
        "prompt": prompt
    }
    
    response = requests.post(url, headers=headers, json=data)
    return response.json()

# 使用示例
result = remix_video(
    video_id="video_123",
    prompt="Extend the scene with the cat taking a bow",
    model="sora-2-remix"
)
print(result)
```

### JavaScript/Node.js

```javascript
const axios = require('axios');

async function remixVideo(videoId, prompt, model = 'sora-2-remix') {
  const url = 'http://localhost:3000/v1/videos';
  const headers = {
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${YOUR_API_KEY}`
  };
  const data = {
    model: model,  // 用于识别 Remix 请求
    video_id: videoId,
    prompt: prompt
  };
  
  const response = await axios.post(url, data, { headers });
  return response.data;
}

// 使用示例
remixVideo('video_123', 'Extend the scene with the cat taking a bow', 'sora-2-remix')
  .then(result => console.log(result));
```

## 与普通 Remix 接口的区别

| 特性 | model 参数方式 | 专用端点方式 |
|------|---------------|-------------|
| 请求端点 | `/v1/videos` | `/v1/videos/remix` |
| 识别方式 | `model` 参数 | 端点路径 |
| 参数 | model, video_id, prompt | video_id, prompt |
| 优点 | 统一入口，前端方便 | 语义更清晰 |

## 注意事项

1. **model 参数必需**：必须包含 `remix` 字符串或明确指定为 `sora-2-remix`/`sora-2-pro-remix`
2. **自动去除**：`model` 参数在发送给 OpenAI 前会被自动去除
3. **video_id 验证**：必须是系统中已存在的视频任务ID
4. **渠道使用**：自动使用原视频的渠道和密钥
5. **计费方式**：根据 OpenAI 响应中的 model、size、seconds 计费

## 错误处理

### 错误：video_id 不存在

```json
{
  "error": {
    "message": "video_id not found: video_999",
    "type": "video_not_found"
  }
}
```

### 错误：model 参数错误

如果使用 `sora-2` 而不是 `sora-2-remix`，会被路由到普通视频生成，而不是 Remix。

## 测试脚本

运行以下命令测试：

```bash
# Bash
bash test_sora_remix_updated.sh

# PowerShell
.\test_sora_remix_updated.ps1 -ApiEndpoint 'http://localhost:3000' -ApiKey 'your_key' -VideoId 'video_123'
```

## 总结

使用 `model` 参数方式的优势：
- ✅ 统一的 API 端点
- ✅ 前端调用方便
- ✅ 自动识别和路由
- ✅ 参数自动清理
- ✅ 完整的错误处理

---

**实现日期**: 2025-10-19  
**版本**: v1.1  
**状态**: ✅ 已实现并测试


