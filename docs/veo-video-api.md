# Veo 视频生成 API 文档

## 概述

本文档描述了如何使用 EzLinkAI 的视频生成 API 来调用 Google Veo 系列模型生成视频。请求体与 Google Vertex AI 官方 API 保持一致（透传），仅需额外添加 `model` 字段用于路由。

## 支持的模型

| 模型名称 | 系列 | 说明 |
|---------|------|------|
| `veo-2.0-generate-001` | Veo 2 | Veo 2.0 标准版 |
| `veo-3.0-generate-001` | Veo 3 | Veo 3.0 标准版 |
| `veo-3.0-fast-generate-001` | Veo 3 | Veo 3.0 快速版 |
| `veo-3.0-generate-preview` | Veo 3 | Veo 3.0 预览版 |
| `veo-3.0-fast-generate-preview` | Veo 3 | Veo 3.0 快速预览版 |
| `veo-3.1-generate-preview` | Veo 3.1 | Veo 3.1 预览版 |
| `veo-3.1-fast-generate-preview` | Veo 3.1 | Veo 3.1 快速预览版 |
| `veo-3.1-generate-001` | Veo 3.1 | Veo 3.1 标准版 |
| `veo-3.1-fast-generate-001` | Veo 3.1 | Veo 3.1 快速版 |

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

### 请求体格式

请求体与 [Google Vertex AI Veo 官方 API](https://docs.cloud.google.com/vertex-ai/generative-ai/docs/model-reference/veo-video-generation?hl=zh-cn) 保持一致，额外添加 `model` 字段。

```json
{
  "model": "MODEL_ID",
  "instances": [
    {
      "prompt": "TEXT_PROMPT",
      "image": {
        "bytesBase64Encoded": "BASE64_STRING",
        "mimeType": "MIME_TYPE"
      },
      "lastFrame": {
        "bytesBase64Encoded": "BASE64_STRING",
        "mimeType": "MIME_TYPE"
      },
      "video": {
        "bytesBase64Encoded": "BASE64_STRING",
        "mimeType": "MIME_TYPE"
      },
      "mask": {
        "bytesBase64Encoded": "BASE64_STRING",
        "mimeType": "MIME_TYPE",
        "maskMode": "MASK_MODE"
      },
      "referenceImages": [
        {
          "image": {
            "bytesBase64Encoded": "BASE64_STRING",
            "mimeType": "MIME_TYPE"
          },
          "referenceType": "asset"
        }
      ]
    }
  ],
  "parameters": {
    "aspectRatio": "16:9",
    "durationSeconds": 8,
    "generateAudio": true,
    "sampleCount": 1
  }
}
```

---

## 参数详解

### 顶层参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| model | string | 是 | 使用的模型名称 |
| instances | array | 是 | 实例数组，包含提示词和输入媒体 |
| parameters | object | 否 | 生成参数配置 |

### instances 数组对象

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| prompt | string | 文生视频必填，图生视频可选 | 视频生成的文本提示词 |
| image | object | 否 | 起始帧图片（用于图生视频） |
| lastFrame | object | 否 | 结束帧图片（模型生成首帧到末帧之间的过渡视频） |
| video | object | 否 | 输入视频（用于视频延长） |
| mask | object | 否 | 遮罩图片（用于视频中添加/移除对象） |
| referenceImages | array | 否 | 参考图片列表（最多 3 张素材图或 1 张风格图） |

#### 功能支持矩阵

| 功能 | Veo 2 | Veo 3.0 | Veo 3.1 |
|------|-------|---------|---------|
| 文生视频 (prompt) | ✅ | ✅ | ✅ |
| 图生视频 (image) | ✅ | ✅ | ✅ |
| 末帧控制 (lastFrame) | ✅ `veo-2.0-generate-001` | ❌ | ✅ |
| 视频延长 (video) | ✅ `veo-2.0-generate-001` | ❌ | ❌ |
| 遮罩编辑 (mask) | ✅ `veo-2.0-generate-preview` | ❌ | ❌ |
| 素材参考图 (referenceImages) | ✅ `veo-2.0-generate-exp` | ❌ | ✅ `veo-3.1-generate-preview` |

### image / lastFrame / video 对象

媒体对象是**联合字段**，`bytesBase64Encoded` 和 `gcsUri` 二选一：

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| bytesBase64Encoded | string | 二选一 | Base64 编码的媒体数据 |
| gcsUri | string | 二选一 | Google Cloud Storage URI，如 `gs://bucket/image.jpg` |
| mimeType | string | 是 | 媒体类型 |

**支持的 MIME 类型：**
- 图片：`image/jpeg`、`image/png`、`image/webp`
- 视频：`video/mp4`、`video/mov`、`video/mpeg`、`video/avi`、`video/wmv`、`video/mpegps`、`video/flv`

> **重要提示：** 图片字段使用 `bytesBase64Encoded` + `mimeType` 的扁平结构，**不要**使用 `inlineData` 嵌套格式（那是 Gemini API 的格式，VEO API 不支持）。

### mask 对象

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| bytesBase64Encoded | string | 二选一 | Base64 编码的遮罩图片 |
| gcsUri | string | 二选一 | Google Cloud Storage URI |
| mimeType | string | 是 | 媒体类型 |
| maskMode | string | 否 | 遮罩模式 |

### referenceImages 数组对象

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| image | object | 是 | 参考图片对象（结构同上，含 `bytesBase64Encoded`/`gcsUri` + `mimeType`） |
| referenceType | string | 是 | `"asset"`（素材参考，最多 3 张）或 `"style"`（风格参考，最多 1 张） |

### parameters 对象

| 参数名 | 类型 | 必填 | 说明 | 适用模型 |
|--------|------|------|------|----------|
| aspectRatio | string | 否 | 视频宽高比：`"16:9"`（默认）、`"9:16"` | 所有 |
| compressionQuality | string | 否 | 压缩质量：`"optimized"`（默认）、`"lossless"` | 所有 |
| durationSeconds | integer | 是 | 视频时长（秒） | 所有 |
| enhancePrompt | boolean | 否 | 使用 Gemini 优化提示词，默认 `true` | 仅 Veo 2 |
| generateAudio | boolean | Veo 3 必填 | 是否生成音频 | Veo 3 / Veo 3.1 |
| negativePrompt | string | 否 | 反向提示词，描述不希望出现的内容 | 所有 |
| personGeneration | string | 否 | 人物生成：`"allow_adult"`（默认）、`"dont_allow"` | 所有 |
| resizeMode | string | 否 | 图生视频调整模式：`"pad"`（默认）、`"crop"` | 仅 Veo 3 图生视频 |
| resolution | string | 否 | 分辨率：`"720p"`（默认）、`"1080p"` | 仅 Veo 3 / Veo 3.1 |
| sampleCount | integer | 否 | 生成样本数量（1-4） | 所有 |
| seed | uint32 | 否 | 随机种子（0-4294967295），用于复现结果 | 所有 |

#### durationSeconds 取值范围

| 模型 | 可选值 | 默认值 |
|------|--------|--------|
| Veo 2 | 5、6、7、8 | 8 |
| Veo 3 / Veo 3.1 | 4、6、8 | 8 |
| 使用 referenceImages 时 | 8 | 8 |

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
    "generateAudio": true,
    "sampleCount": 1
  }
}
```

### 示例 2：图片生成视频（图生视频）

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
    "durationSeconds": 8,
    "generateAudio": true,
    "resizeMode": "pad"
  }
}
```

### 示例 3：首帧 + 末帧控制

> 仅 Veo 2 (`veo-2.0-generate-001`) 和 Veo 3.1 系列支持 `lastFrame`

```json
{
  "model": "veo-3.1-generate-preview",
  "instances": [
    {
      "prompt": "小狗在草地上奔跑，叼起了一张海报，海报上印着一只猫咪",
      "image": {
        "bytesBase64Encoded": "/9j/4AAQSkZJRg...(首帧图片Base64)",
        "mimeType": "image/jpeg"
      },
      "lastFrame": {
        "bytesBase64Encoded": "iVBORw0KGgo...(末帧图片Base64)",
        "mimeType": "image/png"
      }
    }
  ],
  "parameters": {
    "aspectRatio": "16:9",
    "durationSeconds": 8,
    "generateAudio": true
  }
}
```

### 示例 4：使用素材参考图片

> 仅 `veo-2.0-generate-exp` 和 `veo-3.1-generate-preview` 支持

```json
{
  "model": "veo-3.1-generate-preview",
  "instances": [
    {
      "prompt": "一个人在海边漫步，夕阳西下",
      "referenceImages": [
        {
          "image": {
            "bytesBase64Encoded": "/9j/4AAQSkZJRg...(素材图片Base64)",
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

### 示例 5：使用风格参考图片

> 仅 `veo-2.0-generate-exp` 支持

```json
{
  "model": "veo-2.0-generate-001",
  "instances": [
    {
      "prompt": "城市街道上车流穿梭",
      "referenceImages": [
        {
          "image": {
            "bytesBase64Encoded": "/9j/4AAQSkZJRg...(风格图片Base64)",
            "mimeType": "image/jpeg"
          },
          "referenceType": "style"
        }
      ]
    }
  ],
  "parameters": {
    "aspectRatio": "16:9",
    "durationSeconds": 8
  }
}
```

---

## cURL 请求示例

### 文生视频

```bash
curl -X POST "https://api.ezlinkai.com/v1/video/generations" \
  -H "Authorization: Bearer YOUR_API_KEY" \
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
      "generateAudio": true,
      "sampleCount": 1
    }
  }'
```

### 图生视频

```bash
curl -X POST "https://api.ezlinkai.com/v1/video/generations" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "veo-3.1-generate-preview",
    "instances": [
      {
        "prompt": "让画面动起来，微风吹过",
        "image": {
          "bytesBase64Encoded": "'$(base64 -w0 input.jpg)'",
          "mimeType": "image/jpeg"
        }
      }
    ],
    "parameters": {
      "aspectRatio": "16:9",
      "durationSeconds": 8,
      "generateAudio": true
    }
  }'
```

---

## 响应说明

### 提交任务成功响应

```json
{
  "task_id": "aec81bcb-fd2e-423d-a5ab-0bbe6350fbfd",
  "task_status": "succeed",
  "message": ""
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| task_id | string | 视频生成任务 ID，用于后续查询任务状态 |
| task_status | string | 任务提交状态，提交成功时为 `succeed` |
| message | string | 状态消息 |

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
| taskid | string | 是 | 视频生成任务 ID |
| response_format | string | 是 | 响应格式，使用 `url` 返回视频链接 |

### 请求示例

```
GET https://api.ezlinkai.com/v1/video/generations/result?taskid=aec81bcb-fd2e-423d-a5ab-0bbe6350fbfd&response_format=url
```

### cURL 请求示例

```bash
curl -X GET "https://api.ezlinkai.com/v1/video/generations/result?taskid=aec81bcb-fd2e-423d-a5ab-0bbe6350fbfd&response_format=url" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json"
```

### 响应说明

#### 任务完成

```json
{
  "task_id": "aec81bcb-fd2e-423d-a5ab-0bbe6350fbfd",
  "video_results": [
    {
      "url": "https://storage.googleapis.com/bucket/sample_0.mp4"
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
  "message": "Operation in progress"
}
```

#### 任务失败

```json
{
  "task_id": "aec81bcb-fd2e-423d-a5ab-0bbe6350fbfd",
  "task_status": "failed",
  "message": "错误信息描述"
}
```

### 响应字段说明

| 字段 | 类型 | 说明 |
|------|------|------|
| task_id | string | 视频生成任务 ID |
| task_status | string | 任务状态：`processing`（处理中）、`succeed`（成功）、`failed`（失败） |
| message | string | 状态消息 |
| video_results | array | 生成的视频列表（仅 `succeed` 时返回） |
| video_results[].url | string | 视频的下载/访问链接 |
| video_id | string | 视频 ID |
| duration | string | 视频时长（秒） |

---

## 常见错误

| 错误信息 | 原因 | 解决方案 |
|---------|------|---------|
| `image is empty` | image 字段格式不正确，使用了 Gemini 的 `inlineData` 格式 | 使用 `bytesBase64Encoded` + `mimeType` 扁平结构 |
| `unsupported_model` | 模型名称不在支持列表中 | 检查模型名称拼写 |
| `User balance is not enough` | 用户余额不足 | 充值后重试 |
| Content filtered | 生成内容被安全策略过滤 | 调整提示词，避免敏感内容 |

### 常见格式错误对比

**错误格式（Gemini API 风格，VEO 不支持）：**

```json
{
  "image": {
    "inlineData": {
      "mimeType": "image/jpeg",
      "data": "/9j/4AAQSkZJRg..."
    }
  }
}
```

**正确格式（Vertex AI VEO API）：**

```json
{
  "image": {
    "bytesBase64Encoded": "/9j/4AAQSkZJRg...",
    "mimeType": "image/jpeg"
  }
}
```

---

## 注意事项

1. **请求体透传**：本 API 的 `instances` 和 `parameters` 字段与 Google Vertex AI 官方 API 完全一致，仅额外添加 `model` 字段用于模型路由
2. **图片格式**：支持 `image/jpeg`、`image/png`、`image/webp`
3. **视频格式**：支持 `video/mp4`、`video/mov`、`video/mpeg` 等
4. **Base64 编码**：所有媒体文件必须使用 Base64 编码后传入 `bytesBase64Encoded` 字段，**不要**使用 `inlineData` 嵌套格式
5. **联合字段**：`bytesBase64Encoded` 和 `gcsUri` 二选一，不能同时传递
6. **参考图片限制**：最多 3 张素材参考图（`asset`）或 1 张风格参考图（`style`）
7. **视频时长**：Veo 2 支持 5-8 秒，Veo 3/3.1 支持 4、6、8 秒
8. **分辨率**：Veo 3/3.1 系列支持 720p（默认）和 1080p
9. **音频生成**：Veo 3/3.1 模型必须传 `generateAudio` 参数；Veo 2 不支持此参数
10. **`lastFrame` 支持**：仅 `veo-2.0-generate-001` 和 Veo 3.1 系列支持末帧控制

---

## 官方文档参考

- [Google Vertex AI Veo 视频生成 API 文档](https://docs.cloud.google.com/vertex-ai/generative-ai/docs/model-reference/veo-video-generation?hl=zh-cn)
- [Veo 模型概览](https://cloud.google.com/vertex-ai/generative-ai/docs/video/overview)
