# Veo 视频生成 API 文档

## 概述

本文档描述了如何使用 EzLinkAI 的视频生成 API 来调用 Google Veo 系列模型生成视频。

## 支持的模型

| 模型名称 | 说明 |
|---------|------|
| `veo-3.0-generate-001` | Veo 3.0 标准版 |
| `veo-3.0-fast-generate-001` | Veo 3.0 快速版 |
| `veo-3.0-generate-preview` | Veo 3.0 预览版 |
| `veo-3.0-fast-generate-preview` | Veo 3.0 快速预览版 |
| `veo-3.1-generate-preview` | Veo 3.1 预览版 |
| `veo-3.1-fast-generate-preview` | Veo 3.1 快速预览版 |

---

## 提交视频生成任务

### 请求地址

```
POST https://api.ezlinkai.com/v1/video/generations
```

### 请求头 (Headers)

| 参数名 | 参数值 | 必填 | 说明 |
|--------|--------|------|------|
| Authorization | Bearer {your_api_key} | 是 | API 密钥认证 |
| Content-Type | application/json | 是 | 请求体格式 |

### 请求体 (Request Body)

```json
{
  "model": "veo-3.1-generate-preview",
  "instances": [
    {
      "prompt": "string"
    }
  ],
  "parameters": {
    "aspectRatio": "16:9",
    "durationSeconds": 8,
    "sampleCount": 1
  }
}
```

### 完整请求体参数说明

#### 顶层参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| model | string | 是 | 使用的模型名称 |
| instances | array | 是 | 实例数组，包含提示词和输入媒体 |
| parameters | object | 否 | 生成参数配置 |

#### instances 数组对象

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| prompt | string | 是 | 视频生成的文本提示词 |
| image | object | 否 | 起始图片（用于图生视频） |
| lastFrame | object | 否 | 结束帧图片 |
| video | object | 否 | 输入视频（用于视频编辑/扩展） |
| mask | object | 否 | 遮罩图片 |
| referenceImages | array | 否 | 参考图片列表（最多3张资产图或1张风格图） |

#### image / lastFrame / video 对象

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| bytesBase64Encoded | string | 是 | Base64 编码的媒体数据 |
| mimeType | string | 是 | 媒体类型，如 `image/png`、`image/jpeg`、`video/mp4` |

#### mask 对象

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| bytesBase64Encoded | string | 是 | Base64 编码的遮罩图片 |
| mimeType | string | 是 | 媒体类型 |
| maskMode | string | 否 | 遮罩模式 |

#### referenceImages 数组对象

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| image | object | 是 | 参考图片对象 |
| image.bytesBase64Encoded | string | 是 | Base64 编码的图片 |
| image.mimeType | string | 是 | 图片类型 |
| referenceType | string | 是 | 参考类型：`style`（风格参考）或 `asset`（资产参考） |

#### parameters 对象

| 参数名 | 类型 | 必填 | 说明 | 适用模型 |
|--------|------|------|------|----------|
| aspectRatio | string | 否 | 视频宽高比，支持：`16:9`、`9:16`、`1:1` | 所有 |
| compressionQuality | string | 否 | 压缩质量 | 所有 |
| durationSeconds | integer | 否 | 视频时长（秒），范围 5-8 | 所有 |
| enhancePrompt | boolean | 否 | 是否增强提示词 | 仅 Veo 2 |
| generateAudio | boolean | 否 | 是否生成音频 | 所有 |
| negativePrompt | string | 否 | 负面提示词（不希望出现的内容） | 所有 |
| personGeneration | string | 否 | 人物生成设置：`dont_allow`、`allow_adult`、`allow_all` | 所有 |
| resizeMode | string | 否 | 调整大小模式 | 仅 Veo 3 图生视频 |
| resolution | string | 否 | 分辨率：`720p`、`1080p` | 仅 Veo 3 |
| sampleCount | integer | 否 | 生成样本数量（1-4） | 所有 |
| seed | uint32 | 否 | 随机种子（用于复现结果） | 所有 |

---

## 请求示例

### 示例 1：文本生成视频（最简单）

```json
{
  "model": "veo-3.1-generate-preview",
  "instances": [
    {
      "prompt": "一只可爱的猫咪在阳光下慵懒地打哈欠，背景是温馨的客厅"
    }
  ],
  "parameters": {
    "aspectRatio": "16:9",
    "durationSeconds": 8,
    "sampleCount": 1
  }
}
```

### 示例 2：图片生成视频

```json
{
  "model": "veo-3.1-generate-preview",
  "instances": [
    {
      "prompt": "让画面中的人物缓缓转头微笑",
      "image": {
        "bytesBase64Encoded": "/9j/4AAQSkZJRg...(Base64编码的图片数据)",
        "mimeType": "image/jpeg"
      }
    }
  ],
  "parameters": {
    "aspectRatio": "16:9",
    "durationSeconds": 5,
    "resizeMode": "RESIZE_MODE_FIT"
  }
}
```

### 示例 3：使用参考图片

```json
{
  "model": "veo-3.1-generate-preview",
  "instances": [
    {
      "prompt": "一个人在海边漫步，夕阳西下",
      "referenceImages": [
        {
          "image": {
            "bytesBase64Encoded": "/9j/4AAQSkZJRg...(Base64编码的风格参考图)",
            "mimeType": "image/jpeg"
          },
          "referenceType": "asset"
        }
      ]
    }
  ],
  "parameters": {
    "aspectRatio": "16:9",
    "durationSeconds": 8,
    "generateAudio": true
  }
}
```

---

## 响应说明

### 提交任务成功响应

```json
{
  "task_id": "aec81bcb-fd2e-423d-a5ab-0bbe6350fbfd",
  "task_status": "succeed",
  "message": "Request submitted successfully"
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| task_id | string | 视频生成任务ID，用于后续查询任务状态 |
| task_status | string | 任务状态，提交成功时为 `succeed` |
| message | string | 状态消息 |

---

## cURL 请求示例

```bash
curl -X POST "https://api.ezlinkai.com/v1/video/generations" \
  -H "Authorization: Bearer aW99iQKPTLOzay5R3aD82fC513B444De90F495Df6f9f9641" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "veo-3.1-generate-preview",
    "instances": [
      {
        "prompt": "一只可爱的猫咪在阳光下慵懒地打哈欠"
      }
    ],
    "parameters": {
      "aspectRatio": "16:9",
      "durationSeconds": 8,
      "sampleCount": 1
    }
  }'
```

---

## 注意事项

1. **图片格式**：支持 `image/png`、`image/jpeg`、`image/gif`、`image/webp`
2. **视频格式**：支持 `video/mp4`
3. **Base64 编码**：所有媒体文件必须使用 Base64 编码后传入 `bytesBase64Encoded` 字段
4. **参考图片限制**：最多可使用 3 张资产参考图片（`REFERENCE_TYPE_ASSET`）或 1 张风格参考图片（`REFERENCE_TYPE_STYLE`）
5. **视频时长**：目前支持 5-8 秒的视频生成
6. **分辨率**：Veo 3 系列支持 720p 和 1080p 分辨率

---

## 查询任务结果

### 请求地址

```
GET https://api.ezlinkai.com/v1/video/generations/result
```

### 请求头 (Headers)

| 参数名 | 参数值 | 必填 | 说明 |
|--------|--------|------|------|
| Authorization | Bearer {your_api_key} | 是 | API 密钥认证 |
| Content-Type | application/json | 是 | 请求体格式 |

### Query 参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| taskid | string | 是 | 视频生成任务ID，即提交视频生成请求时返回的 task_id |
| response_format | string | 是 | 响应格式，通常为 `url` 格式返回视频链接 |

### 请求示例

```
GET https://api.ezlinkai.com/v1/video/generations/result?taskid=cgt-20250714221020-668xc&response_format=url
```

### cURL 请求示例

```bash
curl -X GET "https://api.ezlinkai.com/v1/video/generations/result?taskid=cgt-20250714221020-668xc&response_format=url" \
  -H "Authorization: Bearer aW99iQKPTLOzay5R3aD82fC513B444De90F495Df6f9f9641" \
  -H "Content-Type: application/json"
```

### 响应说明

#### 任务完成

```json
{
  "task_id": "aec81bcb-fd2e-423d-a5ab-0bbe6350fbfd",
  "video_results": [
    {
      "url": "https://storage.googleapis.com/ezlinkai22/7929512627208621553/sample_0.mp4"
    }
  ],
  "video_id": "aec81bcb-fd2e-423d-a5ab-0bbe6350fbfd",
  "task_status": "succeed",
  "message": "Video generated successfully.",
  "duration": "8"
}
```

#### 任务进行中

```json
{
  "task_id": "aec81bcb-fd2e-423d-a5ab-0bbe6350fbfd",
  "task_status": "processing",
  "message": "Video is being generated, please wait..."
}
```

#### 任务失败

```json
{
  "task_id": "aec81bcb-fd2e-423d-a5ab-0bbe6350fbfd",
  "task_status": "failed",
  "message": "错误信息"
}
```

### 响应字段说明

| 字段 | 类型 | 说明 |
|------|------|------|
| task_id | string | 视频生成任务ID |
| task_status | string | 任务状态：`processing`（处理中）、`succeed`（成功）、`failed`（失败） |
| message | string | 状态消息 |
| video_results | array | 生成的视频列表（仅当 task_status 为 `succeed` 时返回） |
| video_results[].url | string | 视频的下载/访问链接 |
| video_id | string | 视频ID（与 task_id 相同） |
| duration | string | 视频时长（秒） |

---

## 官方文档参考

- [Google Vertex AI Veo 视频生成文档](https://docs.cloud.google.com/vertex-ai/generative-ai/docs/model-reference/veo-video-generation?hl=zh-cn)
