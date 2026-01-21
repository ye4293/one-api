# Kling API Model Name 定义规范

## 📋 按次计费接口的 model_name 定义

以下接口为**按次计费**（固定费用），客户端调用时必须传递对应的 `model_name`：

### 1. 人脸识别 `/v1/videos/identify-face`
```json
{
  "model_name": "kling-identify-face",
  "video_id": "xxx",
  // 或
  "video_url": "https://..."
}
```
- **费用**: 0.05元/次
- **说明**: 同步接口，立即返回结果

---

### 2. 图像识别 `/v1/videos/image-recognize`
```json
{
  "model_name": "kling-image-recognize",
  "image_url": "https://..."
}
```
- **费用**: 0.1元/次
- **说明**: 同步接口，立即返回结果，一次访问可得图片中所有类型元素的识别结果

---

### 3. 自定义音色训练 `/v1/general/custom-voices`
```json
{
  "model_name": "kling-custom-voices",
  "voice_name": "我的音色",
  "audio_url": "https://...",
  "callback_url": "https://..."
}
```
- **费用**: 0.05元/次
- **说明**: 异步接口，任务成功后扣费（固定费用）

---

### 4. 语音合成/TTS `/v1/audio/tts`
```json
{
  "model_name": "kling-tts",
  "text": "要合成的文本",
  "voice_id": "xxx"
}
```
- **费用**: 0.05元/次
- **说明**: 同步接口，立即返回结果

---

### 5. 文生音效 `/v1/audio/text-to-audio`
```json
{
  "model_name": "kling-text-to-audio",
  "prompt": "音效描述",
  "duration": 5
}
```
- **费用**: 0.25元/次
- **说明**: 固定费用，不按时长计费

---

### 6. 视频配音效 `/v1/audio/video-to-audio`
```json
{
  "model_name": "kling-video-to-audio",
  "video_id": "xxx",
  // 或
  "video_url": "https://..."
}
```
- **费用**: 0.25元/次
- **说明**: 固定费用，不按时长计费

---

### 7. 自定义元素训练 `/v1/general/custom-elements`
```json
{
  "model_name": "kling-custom-elements",
  "element_name": "自定义主体-001",
  "element_frontal_image": "https://...",
  "element_refer_list": [
    {"image_url": "https://..."}
  ]
}
```
- **费用**: 待确认
- **说明**: 同步接口，成功后立即扣费

---

## 📊 按时长/按张计费接口的 model_name

这些接口费用根据实际生成的时长或张数计算：

### 视频生成类
- `kling-video-o1` - Video O1 模型（按秒计费）
- `kling-v1` - V1 模型
- `kling-v1-5` - V1.5 模型
- `kling-v1-6` - V1.6 模型
- `kling-v2-0` - V2.0 模型
- `kling-v2-1` - V2.1 模型
- `kling-v2-5-turbo` - V2.5 Turbo 模型
- `kling-v2-6` - V2.6 模型

### 图片生成类
- `kling-image-o1` - Image O1 模型
- `kling-v1-0` - V1.0 图片模型
- `kling-v1-5` - V1.5 图片模型
- `kling-v2-0` - V2.0 图片模型
- `kling-v2-1` - V2.1 图片模型

---

## 🔧 服务端处理逻辑

### 1. 请求参数处理
服务端会自动将 `model_name` 复制给 `model` 字段（如果 model 不存在）：

```go
// adaptor.go: ConvertRequest()
if _, exists := requestBody["model_name"]; !exists {
    if modelValue, ok := c.Get("model"); ok {
        if modelStr, isString := modelValue.(string); isString && modelStr != "" {
            requestBody["model_name"] = modelStr
        }
    }
}
// 删除 model 字段（Kling API 使用 model_name）
delete(requestBody, "model")
```

### 2. 计费处理
所有接口统一通过 `CalculateVideoQuota(model, type, mode, duration, resolution)` 计算费用：

- **按次计费接口**: 根据 model 名称返回固定价格，忽略 duration 参数
- **按时长计费接口**: 根据 model、mode 和实际时长计算价格
- **按张计费接口**: 根据 model 和生成张数计算价格

---

## ⚠️ 重要说明

### 必填字段
客户端调用时**必须传递 `model_name`**，否则计费将使用默认值，可能导致费用错误。

### 参数优先级
如果同时传递了 `model` 和 `model_name`：
- 服务端会**删除 `model` 字段**
- 仅使用 `model_name` 调用 Kling API
- 计费时使用从 gin.Context 中获取的 model 值（由中间件设置）

### 测试建议
调用新接口前，建议先在测试环境验证 model_name 和费用是否正确：

```bash
# 测试人脸识别
curl -X POST http://your-api/kling/v1/videos/identify-face \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "model_name": "kling-identify-face",
    "video_url": "https://..."
  }'
```

---

**更新时间**: 2026-01-20
**文档版本**: v1.0
