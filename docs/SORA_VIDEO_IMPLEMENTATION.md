# Sora 视频生成实现文档

## 概述

本文档描述了 Sora 视频生成功能的实现，该实现参考了可灵和阿里云视频处理的统一流程，支持透传请求体、自动计费和统一响应格式。

## 功能特性

1. **透传请求体**：直接转发客户端请求到 OpenAI Sora API
2. **自动计费**：根据模型名称、时长和分辨率自动计算费用
3. **统一响应**：返回 `GeneralVideoResponse` 统一格式
4. **余额检查**：请求前检查用户余额
5. **日志记录**：记录视频生成任务和消费日志

## API 端点

### 请求格式

```
POST /v1/videos/generations
Content-Type: application/json
Authorization: Bearer YOUR_API_KEY
```

### 请求体示例

```json
{
  "model": "sora-2-pro",
  "prompt": "一只可爱的小猫在草地上玩耍",
  "size": "1280x720",
  "duration": 5
}
```

### 响应格式

```json
{
  "task_id": "vid_abc123",
  "task_status": "succeed",
  "message": "Video generation request submitted successfully, task_id: vid_abc123"
}
```

## 定价策略

根据 OpenAI 官方定价文档：

| 模型 | 分辨率 | 价格（美元/秒） |
|------|--------|----------------|
| sora-2 | Portrait: 720x1280<br>Landscape: 1280x720 | $0.10 |
| sora-2-pro | Portrait: 720x1280<br>Landscape: 1280x720 | $0.30 |
| sora-2-pro | Portrait: 1024x1792<br>Landscape: 1792x1024 | $0.50 |

### 计费示例

- **sora-2, 720x1280, 5秒**：5 × $0.10 = $0.50
- **sora-2-pro, 1280x720, 10秒**：10 × $0.30 = $3.00
- **sora-2-pro, 1792x1024, 10秒**：10 × $0.50 = $5.00

## 实现细节

### 1. 模型结构

在 `relay/channel/openai/model.go` 中定义：

```go
type SoraVideoRequest struct {
    Model       string `json:"model" binding:"required"`
    Prompt      string `json:"prompt" binding:"required"`
    Size        string `json:"size,omitempty"`
    Duration    int    `json:"duration,omitempty"`
    AspectRatio string `json:"aspect_ratio,omitempty"`
    Loop        bool   `json:"loop,omitempty"`
}

type SoraVideoResponse struct {
    ID         string `json:"id"`
    Object     string `json:"object"`
    Created    int64  `json:"created"`
    Model      string `json:"model"`
    Status     string `json:"status"`
    Size       string `json:"size,omitempty"`
    Duration   int    `json:"duration,omitempty"`
    VideoURL   string `json:"video_url,omitempty"`
    Error      *struct {
        Message string `json:"message"`
        Type    string `json:"type"`
        Code    string `json:"code"`
    } `json:"error,omitempty"`
    StatusCode int `json:"status_code,omitempty"`
}
```

### 2. 处理流程

#### handleSoraVideoRequest
- 读取并解析请求体
- 提取 `duration`、`size` 和 `model` 参数
- 设置默认值（duration=5秒, size=720x1280）
- 调用 `sendRequestAndHandleSoraVideoResponse`

#### sendRequestAndHandleSoraVideoResponse
- 获取渠道信息
- 根据模型和分辨率计算费用
- 检查用户余额
- 构建请求 URL：`{baseUrl}/v1/videos/generations`
- 发送请求到 OpenAI
- 解析响应
- 调用 `handleSoraVideoResponse`

#### handleSoraVideoResponse
- 检查响应状态
- 如果成功（200）：
  - 扣除用户额度
  - 创建视频日志
  - 记录消费日志
- 返回统一的 `GeneralVideoResponse` 格式

### 3. 费用计算逻辑

```go
var pricePerSecond float64
isHighRes := size == "1024x1792" || size == "1792x1024"

if modelName == "sora-2" {
    pricePerSecond = 0.10
} else if modelName == "sora-2-pro" {
    if isHighRes {
        pricePerSecond = 0.50
    } else {
        pricePerSecond = 0.30
    }
}

totalPriceUSD := float64(duration) * pricePerSecond
quota := int64(totalPriceUSD * config.QuotaPerUnit)
```

## 错误处理

### 余额不足
```json
{
  "error": {
    "message": "用户余额不足",
    "type": "User balance is not enough",
    "code": "insufficient_balance"
  }
}
```

### API 错误
```json
{
  "task_id": "vid_abc123",
  "task_status": "failed",
  "message": "Error: Invalid prompt (type: invalid_request_error, code: invalid_prompt)"
}
```

## 日志记录

系统会记录以下信息：
- 请求体内容（开发模式）
- 模型、时长、分辨率参数
- 定价计算详情
- 响应状态（开发模式）
- 错误信息

## 使用示例

### 基础文本生成视频

```bash
curl -X POST https://your-api-endpoint/v1/videos/generations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "sora-2",
    "prompt": "一只可爱的小猫在草地上玩耍",
    "duration": 5,
    "size": "720x1280"
  }'
```

### 高分辨率视频生成

```bash
curl -X POST https://your-api-endpoint/v1/videos/generations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "sora-2-pro",
    "prompt": "壮丽的山脉日出景色",
    "duration": 10,
    "size": "1792x1024"
  }'
```

## 与其他视频服务的对比

| 特性 | Sora | 阿里云视频 | 可灵 |
|------|------|-----------|------|
| 请求透传 | ✓ | ✓ | ✓ |
| 统一响应格式 | ✓ | ✓ | ✓ |
| 自动计费 | ✓ | ✓ | ✓ |
| 余额检查 | ✓ | ✓ | ✓ |
| 日志记录 | ✓ | ✓ | ✓ |

## 技术栈

- **语言**：Go
- **框架**：Gin
- **HTTP 客户端**：标准库 `net/http`
- **JSON 处理**：标准库 `encoding/json`

## 注意事项

1. **默认值**：如果请求中未指定 `duration` 或 `size`，系统会使用默认值（5秒，720x1280）
2. **分辨率验证**：系统会根据分辨率自动判断是否为高分辨率以确定定价
3. **余额检查**：在发送请求前会检查用户余额，余额不足会直接返回错误
4. **异步处理**：视频生成是异步的，响应中返回 `task_id` 供后续查询
5. **错误不扣费**：如果 API 返回错误，系统不会扣除用户费用

## 未来优化方向

1. 支持视频查询接口（根据 task_id 查询生成状态）
2. 支持 webhook 回调
3. 添加速率限制
4. 支持批量生成
5. 添加更多分辨率选项

## 测试

定价逻辑已通过以下测试用例：
- ✓ sora-2, 720x1280, 5秒 → $0.50
- ✓ sora-2, 1280x720, 10秒 → $1.00
- ✓ sora-2-pro, 720x1280, 5秒 → $1.50
- ✓ sora-2-pro, 1280x720, 10秒 → $3.00
- ✓ sora-2-pro, 1024x1792, 5秒 → $2.50
- ✓ sora-2-pro, 1792x1024, 10秒 → $5.00

## 相关文件

- `relay/controller/video.go` - 主要处理逻辑
- `relay/channel/openai/model.go` - 请求和响应模型
- `relay/model/general.go` - 通用响应格式
- `docs/SORA_VIDEO_IMPLEMENTATION.md` - 本文档

## 参考文档

- [OpenAI Sora API 文档](https://platform.openai.com/docs/api-reference/videos/create)
- [OpenAI 定价页面](https://openai.com/api/pricing/)

