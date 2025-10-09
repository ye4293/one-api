# Sora API 实现文档

## 概述

本文档描述了 OpenAI Sora 视频生成 API 的实现，参考了 Runway 的处理方式。

## 实现的功能

### 1. API 端点

#### 创建视频任务
- **端点**: `POST /v1/videos`
- **功能**: 创建 Sora 视频生成任务
- **处理流程**:
  1. 透传请求到 OpenAI Sora API
  2. 解析响应获取视频 ID
  3. 计算并扣除配额
  4. 创建数据库记录
  5. 支持失败重试机制

#### 查询视频状态
- **端点**: `GET /v1/videos/{videoId}`
- **功能**: 查询视频生成任务状态
- **处理流程**:
  1. 从数据库查询任务信息
  2. 向 OpenAI API 获取最新状态
  3. 更新数据库状态
  4. 失败任务自动退款

## 核心实现

### 1. 主要函数

#### `DirectRelaySoraVideo` (relay/controller/directvideo.go)
- 处理 Sora API 的创建请求
- 透传请求到 OpenAI Sora API
- 执行计费和日志记录

#### `GetSoraVideoResult` (relay/controller/directvideo.go)
- 处理 Sora API 的查询请求
- 更新数据库状态
- 处理失败退款

#### `RelaySoraVideo` (controller/relay.go)
- 处理请求并支持重试机制
- 记录渠道历史
- 错误处理和日志记录

#### `RelaySoraVideoResult` (controller/relay.go)
- 简单的查询结果代理

### 2. 辅助函数

#### `calculateSoraQuota`
- 根据请求参数计算配额
- 支持 sora-2 和 sora-2-pro 模型
- 根据 model 和 size 参数确定每秒价格
- 支持 string 类型的 seconds 参数
- 默认值：seconds=4, size="720x1280", model="sora-2"

#### `extractSecondsFromRequest`
- 从请求体中提取 seconds 字段
- Sora API 使用 seconds 而不是 duration
- 返回字符串格式的秒数

#### `handleSoraVideoBilling`
- 处理 Sora 视频任务扣费
- 更新用户配额
- 记录消费日志

#### `updateSoraTaskStatus`
- 更新任务状态到数据库
- 处理成功/失败状态
- 触发失败退款

#### `compensateSoraVideoTask`
- 补偿失败任务的配额
- 退还用户和渠道配额

## 定价模型

根据 OpenAI Sora 官方定价：

| 模型 | 分辨率 | 每秒价格 |
|------|--------|---------|
| sora-2 | Portrait: 720x1280<br>Landscape: 1280x720 | $0.10 |
| sora-2-pro | Portrait: 720x1280<br>Landscape: 1280x720 | $0.30 |
| sora-2-pro | Portrait: 1024x1792<br>Landscape: 1792x1024 | $0.50 |

**参数说明**：
- `seconds` (string): 视频时长（秒），默认值为 "4"
- `size` (string): 输出分辨率，格式为 "width x height"，默认值为 "720x1280"
- `model` (string): 模型名称，默认为 "sora-2"

配额转换率: 1美元 = 500,000 quota

## 状态映射

| Sora API 状态 | 数据库状态 |
|--------------|----------|
| queued, pending | pending |
| processing, running | running |
| completed, succeeded | succeeded |
| failed, error | failed |
| cancelled | cancelled |

## 重试机制

与 Runway 相同的重试机制：
1. 首次请求失败后，根据配置进行重试
2. 每次重试选择不同优先级的渠道
3. 记录详细的重试日志
4. 支持渠道历史记录

## 路由配置

```go
// Sora 视频生成路由 - 需要 Distribute 中间件进行渠道选择
soraRouter := router.Group("/v1")
soraRouter.Use(middleware.RelayPanicRecover(), middleware.TokenAuth(), middleware.Distribute())
{
    soraRouter.POST("/videos", controller.RelaySoraVideo)
}

// Sora 查询路由 - 不需要 Distribute 中间件
soraResultRouter := router.Group("/v1")
soraResultRouter.Use(middleware.RelayPanicRecover(), middleware.TokenAuth())
{
    soraResultRouter.GET("/videos/:videoId", controller.RelaySoraVideoResult)
}
```

## 数据库记录

使用 `CreateVideoLog` 函数创建视频日志：
- Provider: "sora"
- Task ID: 视频 ID
- Mode: "sora"
- Duration: 从请求中提取
- Quota: 根据定价计算

## 失败处理

1. **自动重试**: 根据状态码决定是否重试
2. **状态更新**: 实时同步任务状态
3. **自动退款**: 失败任务自动补偿配额
4. **详细日志**: 记录所有错误和重试信息

## 使用示例

### 创建视频任务
```bash
curl -X POST https://your-api.com/v1/videos \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "A cat playing piano",
    "model": "sora-1.0-turbo",
    "duration": 5,
    "resolution": "1080p"
  }'
```

### 查询视频状态
```bash
curl -X GET https://your-api.com/v1/videos/{videoId} \
  -H "Authorization: Bearer YOUR_TOKEN"
```

## 注意事项

1. 确保渠道配置中包含有效的 OpenAI API 密钥
2. 根据实际的 OpenAI Sora API 定价调整 `calculateSoraQuota` 函数
3. 根据实际的 API 响应格式调整状态映射逻辑
4. 监控配额使用和退款情况

## 参考实现

本实现参考了 Runway API 的处理方式，包括：
- 请求透传机制
- 重试逻辑
- 状态管理
- 计费系统
- 退款机制

## 文件变更

1. `relay/controller/directvideo.go`: 添加 Sora 相关的核心处理函数
2. `controller/relay.go`: 添加 Sora 的重试和错误处理逻辑
3. `router/relay-router.go`: 添加 Sora API 路由配置

## 未来改进

1. 支持更多的 Sora 参数（如 aspect_ratio、quality 等）
2. 优化定价计算逻辑
3. 添加更详细的错误类型处理
4. 支持批量查询任务状态

