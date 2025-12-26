# Kling API 接入使用指南

## 概述

本文档介绍如何在 one-api 系统中使用可灵 (Kling) AI 视频生成 API。Kling API 支持四种视频生成模式：

1. **文生视频** (text2video) - 根据文本描述生成视频
2. **全能视频** (omni-video) - 支持文本+图片组合生成视频
3. **图生视频** (image2video) - 根据图片生成视频
4. **多图生视频** (multi-image2video) - 根据多张图片生成视频

## 系统架构

### 工作流程

```
客户端 -> one-api 网关 -> Kling API
   ↓           ↓              ↓
提交任务  验证余额/转发   异步处理
   ↓           ↓              ↓
获取task_id 创建记录    生成视频
   ↓           ↓              ↓
轮询查询   ← 回调通知 ←  完成/失败
   ↓           ↓
获取结果   扣除费用
```

### 计费模式

采用**后扣费模式**：
- 提交任务时：仅验证用户余额是否充足，不实际扣费
- 任务成功时：通过回调或轮询确认成功后才扣除费用
- 任务失败时：不扣费，无需退款操作

## 配置说明

### 1. 渠道配置

在 one-api 管理后台添加 Kling 渠道：

- **渠道类型**: Keling (ChannelType=41)
- **Base URL**: `https://api.klingai.com` (默认) 或 `https://api-singapore.klingai.com`
- **API Key**: 从可灵开放平台获取的 API 密钥
- **回调域名**: 配置系统的公网访问地址（用于接收回调通知）

### 2. 模型定价配置

当前系统已配置以下模型（临时占位值，需根据官方定价调整）：

| 模型名称 | 定价倍率 | 说明 |
|---------|---------|------|
| kling-v1-5-std | 50 | Kling 1.5 标准版 |
| kling-v1-5-pro | 100 | Kling 1.5 专业版 |
| kling-v1-6-std | 60 | Kling 1.6 标准版 |
| kling-v1-6-pro | 120 | Kling 1.6 专业版 |

## API 接口说明

### 基础路径

所有 Kling API 请求的基础路径为：`/kling/v1/videos`

### 1. 文生视频 (Text2Video)

**端点**: `POST /kling/v1/videos/text2video`

**请求头**:
```
Authorization: Bearer YOUR_TOKEN
Content-Type: application/json
```

**请求体**:
```json
{
  "model": "kling-v1-5-std",
  "prompt": "一只可爱的小猫在草地上玩耍",
  "negative_prompt": "模糊,低质量",
  "duration": 5,
  "aspect_ratio": "16:9",
  "cfg_scale": 0.5,
  "mode": "std"
}
```

**参数说明**:
- `model` (必填): 模型名称
- `prompt` (必填): 视频描述文本
- `negative_prompt` (可选): 负面提示词
- `duration` (可选): 视频时长（秒），默认 5
- `aspect_ratio` (可选): 宽高比，支持 16:9, 9:16, 1:1 等
- `cfg_scale` (可选): CFG 强度，范围 0-1
- `mode` (可选): 生成模式

**响应示例**:
```json
{
  "task_id": "kling_abc123def456",
  "kling_task_id": "kt_789xyz",
  "status": "submitted",
  "message": "任务已提交，请通过查询接口获取结果"
}
```

### 2. 全能视频 (OmniVideo)

**端点**: `POST /kling/v1/videos/omni-video`

**请求体**:
```json
{
  "model": "kling-v1-5-pro",
  "prompt": "镜头缓慢推进",
  "image": "https://example.com/image.jpg",
  "image_tail": "https://example.com/end_image.jpg",
  "duration": 10,
  "aspect_ratio": "16:9",
  "camera_control": {
    "type": "zoom",
    "config": {
      "zoom": 1.2,
      "horizontal": 0.5
    }
  }
}
```

**参数说明**:
- `image` (可选): 首帧图片 URL 或 base64
- `image_tail` (可选): 尾帧图片 URL 或 base64
- `camera_control` (可选): 镜头控制参数
  - `type`: 镜头类型
  - `config`: 镜头配置（zoom, pan, tilt, roll 等）

### 3. 图生视频 (Image2Video)

**端点**: `POST /kling/v1/videos/image2video`

**请求体**:
```json
{
  "model": "kling-v1-6-std",
  "image": "https://example.com/photo.jpg",
  "prompt": "让图片中的人物动起来",
  "duration": 5,
  "cfg_scale": 0.7
}
```

**参数说明**:
- `image` (必填): 输入图片 URL 或 base64
- `prompt` (可选): 动作描述
- 其他参数同文生视频

### 4. 多图生视频 (MultiImage2Video)

**端点**: `POST /kling/v1/videos/multi-image2video`

**请求体**:
```json
{
  "model": "kling-v1-6-pro",
  "image_list": [
    {"image": "https://example.com/img1.jpg"},
    {"image": "https://example.com/img2.jpg"},
    {"image": "https://example.com/img3.jpg"}
  ],
  "prompt": "将这些图片连接成流畅的视频",
  "duration": 10,
  "aspect_ratio": "16:9"
}
```

**参数说明**:
- `image_list` (必填): 图片列表，每个元素包含 `image` 字段
- 其他参数同文生视频

### 5. 查询任务结果

**端点**: `GET /kling/v1/videos/{task_id}`

**请求头**:
```
Authorization: Bearer YOUR_TOKEN
```

**响应示例 - 处理中**:
```json
{
  "task_id": "kling_abc123def456",
  "kling_task_id": "kt_789xyz",
  "status": "processing",
  "model": "kling-v1-5-std",
  "provider": "kling",
  "type": "text2video",
  "created_at": 1703001234
}
```

**响应示例 - 成功**:
```json
{
  "task_id": "kling_abc123def456",
  "kling_task_id": "kt_789xyz",
  "status": "succeed",
  "model": "kling-v1-5-std",
  "provider": "kling",
  "type": "text2video",
  "created_at": 1703001234,
  "video_url": "https://kling-cdn.com/videos/xxx.mp4",
  "duration": "5"
}
```

**响应示例 - 失败**:
```json
{
  "task_id": "kling_abc123def456",
  "kling_task_id": "kt_789xyz",
  "status": "failed",
  "model": "kling-v1-5-std",
  "provider": "kling",
  "type": "text2video",
  "created_at": 1703001234,
  "fail_reason": "内容不符合规范"
}
```

**任务状态说明**:
- `pending`: 任务已创建，等待提交
- `submitted`: 已提交到 Kling API
- `processing`: 正在处理中
- `succeed`: 生成成功
- `failed`: 生成失败

## 回调机制

### 回调 URL 格式

系统自动生成回调 URL：`https://your-domain/kling/callback/{task_id}`

### 回调通知结构

Kling API 会在任务完成时向回调 URL 发送 POST 请求：

```json
{
  "task_id": "kt_789xyz",
  "task_status": "succeed",
  "task_result": {
    "videos": [
      {
        "id": "video_123",
        "url": "https://kling-cdn.com/videos/xxx.mp4",
        "duration": "5"
      }
    ]
  },
  "external_task_id": "kling_abc123def456"
}
```

### 回调处理流程

1. 接收回调通知
2. 验证任务 ID
3. 原子更新任务状态（防止并发冲突）
4. 如果成功，扣除用户费用
5. 返回 200 OK 给 Kling

## 计费说明

### 计费公式

```
总费用 = 基础价格 × 时长倍率 × 分辨率倍率 × 请求类型倍率 × QuotaPerUnit
```

**时长倍率**: `duration / 5`（每 5 秒为一个计费单位）

**分辨率倍率**:
- 16:9 或 9:16: 1.2
- 1:1: 1.0
- 21:9 或 9:21: 1.3

**请求类型倍率**:
- text2video: 1.0
- image2video: 1.1
- omni-video: 1.2
- multi-image2video: 1.3

### 扣费时机

- **提交任务时**: 仅验证余额，不扣费
- **回调成功时**: 自动扣除费用
- **轮询查询时**: 如果发现任务成功且未扣费，则扣费

## 错误处理

### 常见错误码

| 错误码 | 说明 | 处理建议 |
|-------|------|---------|
| 400 | 请求参数错误 | 检查请求体格式和必填参数 |
| 401 | 认证失败 | 检查 Token 是否有效 |
| 402 | 余额不足 | 充值后重试 |
| 404 | 任务不存在 | 检查 task_id 是否正确 |
| 500 | 服务器错误 | 稍后重试或联系管理员 |

### 错误响应示例

```json
{
  "error": {
    "message": "余额不足",
    "type": "insufficient_quota",
    "code": "insufficient_quota"
  }
}
```

## 最佳实践

### 1. 轮询策略

建议使用指数退避策略查询任务状态：

```python
import time

def poll_task_result(task_id, max_retries=30):
    for i in range(max_retries):
        result = query_task(task_id)
        if result['status'] in ['succeed', 'failed']:
            return result
        # 指数退避：2秒, 4秒, 8秒, 16秒...最多60秒
        wait_time = min(2 ** i, 60)
        time.sleep(wait_time)
    raise TimeoutError("任务超时")
```

### 2. 错误重试

对于网络错误或临时故障，建议实现重试机制：

```python
from tenacity import retry, stop_after_attempt, wait_exponential

@retry(
    stop=stop_after_attempt(3),
    wait=wait_exponential(multiplier=1, min=2, max=10)
)
def submit_video_task(payload):
    return requests.post(
        "https://your-api/kling/v1/videos/text2video",
        json=payload,
        headers={"Authorization": f"Bearer {token}"}
    )
```

### 3. 批量处理

对于大量任务，建议：
- 控制并发数量（避免超过 API 限流）
- 使用队列管理任务
- 实现任务优先级机制

## 监控与日志

### 关键指标

- API 调用成功率
- 平均任务完成时间
- 回调成功率
- 用户余额扣费准确性

### 日志查看

系统日志记录在 `logs/oneapi.log`，可以通过以下方式查看：

```bash
# 查看 Kling 相关日志
tail -f logs/oneapi.log | grep -i kling

# 查看特定任务日志
grep "task_id=kling_abc123" logs/oneapi.log
```

## 常见问题

### Q1: 回调没有收到怎么办？

**A**: 回调可能因网络问题丢失，建议：
1. 使用轮询作为备选方案
2. 检查回调域名是否可公网访问
3. 查看系统日志确认是否收到回调

### Q2: 任务失败但扣费了怎么办？

**A**: 系统采用后扣费模式，只有任务成功才扣费。如果发现异常，请联系管理员核查日志。

### Q3: 如何修改模型定价？

**A**: 编辑 `common/model-ratio.go` 文件，修改对应模型的定价倍率，然后重启服务。

### Q4: 支持哪些图片格式？

**A**: 支持 JPG、PNG、WebP 等常见格式，建议图片大小不超过 10MB。

### Q5: 视频生成需要多长时间？

**A**: 通常 5-10 分钟，具体取决于视频时长和复杂度。建议设置合理的超时时间。

## 技术支持

如有问题，请：
1. 查看系统日志：`logs/oneapi.log`
2. 查看 Kling 官方文档：https://app.klingai.com/cn/dev/document-api
3. 联系系统管理员

## 更新日志

- **2025-12-25**: 初始版本，支持四种视频生成模式
- 后续更新请关注系统发布说明

