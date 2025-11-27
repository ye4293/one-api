# Sora Remix 功能实现文档

## 概述

Sora Remix 功能允许用户基于已生成的视频创建新的变体视频。系统会自动使用原视频的渠道密钥，并根据响应参数进行计费。

## 功能特性

- ✅ 基于现有视频ID创建 remix
- ✅ 自动查找并使用原视频的渠道
- ✅ 根据响应中的 model、size、seconds 自动计费
- ✅ 统一的响应格式
- ✅ 完整的错误处理和日志记录

## API 端点

### Remix 请求

```
POST /v1/videos/{video_id}/remix
```

**注意**: 实际使用时，`video_id` 需要通过请求体中的 `video_id` 字段传递。

## 请求格式

### 请求参数

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| video_id | string | 是 | 原视频的任务ID |
| prompt | string | 是 | 新的视频描述 |

### 请求示例

```bash
curl -X POST http://localhost:3000/v1/videos/remix \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "video_id": "video_123",
    "prompt": "Extend the scene with the cat taking a bow to the cheering audience"
  }'
```

## 响应格式

### 成功响应

```json
{
  "task_id": "video_456",
  "task_status": "succeed",
  "message": "Video remix request submitted successfully, task_id: video_456, remixed_from: video_123"
}
```

### 错误响应 - 视频不存在

```json
{
  "error": {
    "message": "video_id not found: video_999",
    "type": "video_not_found",
    "code": ""
  }
}
```

### 错误响应 - 余额不足

```json
{
  "error": {
    "message": "用户余额不足",
    "type": "User balance is not enough",
    "code": ""
  }
}
```

## OpenAI 原始响应格式

系统会接收到来自 OpenAI 的响应：

```json
{
  "id": "video_456",
  "object": "video",
  "model": "sora-2",
  "status": "queued",
  "progress": 0,
  "created_at": 1712698600,
  "size": "720x1280",
  "seconds": 8,
  "remixed_from_video_id": "video_123"
}
```

系统会从响应中提取：
- **model**: 用于计费（sora-2 或 sora-2-pro）
- **size**: 用于确定分辨率定价
- **seconds**: 用于计算时长费用

## 处理流程

```
用户请求 (video_id + prompt)
    ↓
查询数据库获取原视频记录
    ↓
提取原视频的渠道ID
    ↓
获取原渠道配置（BaseURL + Key）
    ↓
构建请求: POST {baseUrl}/v1/videos/{video_id}/remix
    ↓
使用原渠道的 Key 发送请求
    ↓
接收 OpenAI 响应
    ↓
从响应提取 model、size、seconds
    ↓
计算费用 (根据 Sora 定价)
    ↓
检查用户余额
    ↓
扣费 + 记录日志
    ↓
返回统一响应 (GeneralVideoResponse)
```

## 关键实现

### 1. 查找原视频记录

```go
videoTask, err := dbmodel.GetVideoTaskByVideoId(remixReq.VideoID)
if err != nil {
    return openai.ErrorWrapper(
        fmt.Errorf("video_id not found: %s", remixReq.VideoID),
        "video_not_found",
        http.StatusNotFound,
    )
}
```

### 2. 获取原渠道配置

```go
originalChannel, err := dbmodel.GetChannelById(videoTask.ChannelId, true)
if err != nil {
    return openai.ErrorWrapper(err, "get_original_channel_error", http.StatusInternalServerError)
}
```

### 3. 使用原渠道的 Key

```go
req.Header.Set("Authorization", "Bearer "+originalChannel.Key)
```

### 4. 从响应提取计费参数

```go
modelName := soraResponse.Model    // 从响应获取
seconds := soraResponse.Seconds     // 从响应获取
size := soraResponse.Size           // 从响应获取

quota := calculateSoraQuota(modelName, seconds, size)
```

### 5. 记录日志

```go
videoType := "remix"
err = CreateVideoLog("sora", taskId, meta, size, secondsStr, videoType, originalVideoID, quota)
```

## 定价说明

Remix 功能使用与普通 Sora 视频生成相同的定价策略：

| 模型 | 分辨率 | 价格（美元/秒） |
|------|--------|----------------|
| sora-2 | 720x1280, 1280x720 | $0.10 |
| sora-2-pro | 720x1280, 1280x720 | $0.30 |
| sora-2-pro | 1024x1792, 1792x1024 | $0.50 |

**示例**：
- 如果响应返回 `model: "sora-2-pro"`, `size: "1280x720"`, `seconds: 8`
- 费用 = 8 × $0.30 = $2.40

## 使用场景

### 场景 1: 延长视频场景

```bash
# 原视频: 一只猫在玩耍
# Remix: 延长场景，猫向观众鞠躬

curl -X POST http://localhost:3000/v1/videos/remix \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "video_id": "video_original_cat",
    "prompt": "Extend the scene with the cat taking a bow to the cheering audience"
  }'
```

### 场景 2: 改变视频风格

```bash
# 原视频: 白天的城市街景
# Remix: 改为夜晚霓虹灯效果

curl -X POST http://localhost:3000/v1/videos/remix \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "video_id": "video_city_day",
    "prompt": "Transform to nighttime with neon lights and vibrant colors"
  }'
```

### 场景 3: 添加新元素

```bash
# 原视频: 空旷的草地
# Remix: 添加奔跑的小狗

curl -X POST http://localhost:3000/v1/videos/remix \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "video_id": "video_field",
    "prompt": "Add a playful puppy running across the field"
  }'
```

## 代码示例

### Python

```python
import requests
import json

API_ENDPOINT = "http://localhost:3000"
API_KEY = "your_api_key_here"

def remix_video(video_id, prompt):
    url = f"{API_ENDPOINT}/v1/videos/remix"
    headers = {
        "Content-Type": "application/json",
        "Authorization": f"Bearer {API_KEY}"
    }
    data = {
        "video_id": video_id,
        "prompt": prompt
    }
    
    response = requests.post(url, headers=headers, json=data)
    return response.json()

# 使用示例
result = remix_video(
    video_id="video_123",
    prompt="Extend the scene with the cat taking a bow"
)

print(json.dumps(result, indent=2))
```

### JavaScript/Node.js

```javascript
const axios = require('axios');

const API_ENDPOINT = 'http://localhost:3000';
const API_KEY = 'your_api_key_here';

async function remixVideo(videoId, prompt) {
  const url = `${API_ENDPOINT}/v1/videos/remix`;
  const headers = {
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${API_KEY}`
  };
  const data = {
    video_id: videoId,
    prompt: prompt
  };
  
  try {
    const response = await axios.post(url, data, { headers });
    return response.data;
  } catch (error) {
    console.error('Error:', error.response?.data || error.message);
    throw error;
  }
}

// 使用示例
remixVideo('video_123', 'Extend the scene with the cat taking a bow')
  .then(result => {
    console.log(JSON.stringify(result, null, 2));
  });
```

## 错误处理

### 常见错误

| 错误码 | 原因 | 解决方法 |
|--------|------|----------|
| video_not_found | video_id 不存在 | 检查 video_id 是否正确 |
| get_original_channel_error | 原渠道不存在或已删除 | 联系管理员 |
| User balance is not enough | 余额不足 | 充值账户 |
| parse_remix_request_failed | 请求格式错误 | 检查 JSON 格式 |
| request_error | 网络错误 | 稍后重试 |

## 日志记录

系统会记录以下信息：

```
sora-remix-request: video_id=video_123, prompt=Extend the scene...
sora-remix: using original channel_id=5, channel_name=OpenAI-Main
Sora video pricing: model=sora-2-pro, seconds=8, size=1280x720, pricePerSecond=0.30, totalUSD=2.400000, quota=240
[DEBUG] Sora remix response: status=200, body={"id":"video_456",...}
```

## 数据库记录

Remix 请求会在数据库中记录：

- **provider**: "sora"
- **task_id**: 新视频的ID (如 "video_456")
- **video_type**: "remix"
- **video_id**: 原视频ID (如 "video_123")
- **mode**: size (分辨率)
- **duration**: seconds (时长)
- **quota**: 扣费金额

## 注意事项

1. **原渠道依赖**: Remix 必须使用原视频的渠道和密钥，确保渠道仍然有效
2. **余额检查**: 在发送请求前会检查用户余额
3. **计费时机**: 只有当 OpenAI 返回 200 状态码时才会扣费
4. **视频ID验证**: 必须是系统中已存在的 video_id
5. **渠道权限**: 确保原渠道的 API Key 仍然有效

## 与普通视频生成的区别

| 特性 | 普通生成 | Remix |
|------|---------|-------|
| 请求地址 | `/v1/videos` | `/v1/videos/{id}/remix` |
| 必需参数 | model, prompt | video_id, prompt |
| 渠道选择 | 当前用户的渠道 | 原视频的渠道 |
| input_reference | 支持 | 不支持 |
| size/seconds | 请求中指定 | 从响应中获取 |

## 技术细节

### 请求构建

```go
fullRequestUrl := fmt.Sprintf("%s/v1/videos/%s/remix", baseUrl, remixReq.VideoID)

requestBody := map[string]string{
    "prompt": remixReq.Prompt,
}
```

### 响应解析

```go
var soraResponse openai.SoraVideoResponse
json.Unmarshal(respBody, &soraResponse)

// 提取计费参数
modelName := soraResponse.Model
seconds := soraResponse.Seconds
size := soraResponse.Size
```

### 费用计算

```go
quota := calculateSoraQuota(modelName, seconds, size)
```

使用与普通 Sora 视频相同的计费函数。

## 相关文件

- `relay/channel/openai/model.go` - SoraRemixRequest 和响应结构
- `relay/controller/video.go` - handleSoraRemixRequest 和 handleSoraRemixResponse
- `docs/SORA_REMIX_IMPLEMENTATION.md` - 本文档

## 参考文档

- [OpenAI Sora API - Remix](https://platform.openai.com/docs/api-reference/videos/remix)
- [Sora 完整实现文档](./SORA_UPDATED_IMPLEMENTATION.md)

---

**实现日期**: 2025-10-19  
**版本**: v1.0

