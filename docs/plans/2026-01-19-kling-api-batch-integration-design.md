# Kling API 批量接入设计方案

**创建时间**: 2026-01-19
**版本**: 1.0
**接口数量**: 13个（视频6个 + 音频3个 + 图片4个）

---

## 1. 概述

本次接入 13 个新的 Kling API 接口，采用与现有 Kling 接口完全一致的架构模式，通过最小化修改、最大化复用的原则快速实施。

### 1.1 接口列表

**视频类（6个）- 使用 Video 表**
1. `/v1/videos/motion-control` - 镜头动作控制
2. `/v1/videos/multi-elements/init-selection` - 多元素初始化选择
3. `/v1/videos/video-extend` - 视频延长
4. `/v1/videos/avatar/image2video` - 数字人图生视频
5. `/v1/videos/effects` - 视频效果应用
6. `/v1/videos/image-recognize` - 图像识别

**音频类（3个）- 使用 Video 表**
1. `/v1/audio/text-to-audio` - 文本转音频
2. `/v1/audio/video-to-audio` - 视频提取音频
3. `/v1/audio/tts` - 文本转语音

**图片类（4个）- 使用 Image 表**
1. `/v1/images/generations` - 图片生成
2. `/v1/images/omni-image` - 全能图片
3. `/v1/images/multi-image2image` - 多图转图
4. `/v1/images/editing/expand` - 图片扩展编辑

### 1.2 核心设计原则

- **统一处理器**: 所有 13 个接口复用 `RelayKlingVideo` 控制器
- **类型识别**: 通过 `DetermineRequestType` 自动识别接口类型
- **表分离**: 视频/音频用 `Video` 表，图片用 `Image` 表
- **Fallback 查询**: 回调处理时自动查询正确的表
- **统一计费**: 复用 `common.CalculateVideoQuota`，通过后台配置差异化定价

---

## 2. 架构设计

### 2.1 整体流程

```
客户端请求
    ↓
TokenAuth → Distribute → RelayKlingVideo
    ↓
DetermineRequestType（识别类型）
    ↓
计算预估费用 → 验证余额
    ↓
根据类型判断：
  - 图片类 → 创建 Image 记录
  - 视频/音频类 → 创建 Video 记录
    ↓
调用 Kling API（带 callback_url + external_task_id）
    ↓
返回 task_id 给客户端
    ↓
[异步] Kling 回调 → HandleKlingCallback
    ↓
Fallback 查询机制：
  1. 先查 Video 表
  2. 未找到则查 Image 表
    ↓
更新状态 + 结果 → 成功时实际扣费
```

### 2.2 关键组件

| 组件 | 文件 | 职责 |
|------|------|------|
| 常量定义 | `relay/channel/kling/constants.go` | 定义 13 个新的 `RequestType` 常量 |
| 路由识别 | `relay/channel/kling/util.go` | `DetermineRequestType` 识别请求类型 |
| 数据结构 | `relay/channel/kling/model.go` | 定义 13 个接口的请求结构体 |
| 控制器 | `controller/kling_video.go` | `RelayKlingVideo` 统一处理所有接口 |
| 回调处理 | `controller/kling_video.go` | `HandleKlingCallback` + fallback 查询 |
| 路由注册 | `router/relay-router.go` | 注册 13 个新路由 |

---

## 3. 详细设计

### 3.1 常量定义

**文件**: `relay/channel/kling/constants.go`

新增 13 个请求类型常量：

```go
const (
    // 现有常量...

    // 视频相关（新增6个）
    RequestTypeMotionControl      = "motion-control"
    RequestTypeMultiElements      = "multi-elements"
    RequestTypeVideoExtend        = "video-extend"
    RequestTypeAvatarI2V          = "avatar-image2video"
    RequestTypeVideoEffects       = "video-effects"
    RequestTypeImageRecognize     = "image-recognize"

    // 音频相关（新增3个）
    RequestTypeTextToAudio        = "text-to-audio"
    RequestTypeVideoToAudio       = "video-to-audio"
    RequestTypeTTS                = "tts"

    // 图片相关（新增4个）
    RequestTypeImageGeneration    = "image-generation"
    RequestTypeOmniImage          = "omni-image"
    RequestTypeMultiImage2Image   = "multi-image2image"
    RequestTypeImageExpand        = "image-expand"
)
```

### 3.2 路由识别扩展

**文件**: `relay/channel/kling/util.go`

在 `DetermineRequestType` 函数中添加 13 个新路径匹配：

```go
func DetermineRequestType(path string) string {
    // 现有逻辑...

    // 视频类（新增6个）
    if strings.Contains(path, "/motion-control") {
        return RequestTypeMotionControl
    }
    if strings.Contains(path, "/multi-elements") {
        return RequestTypeMultiElements
    }
    if strings.Contains(path, "/video-extend") {
        return RequestTypeVideoExtend
    }
    if strings.Contains(path, "/avatar/image2video") {
        return RequestTypeAvatarI2V
    }
    if strings.Contains(path, "/effects") {
        return RequestTypeVideoEffects
    }
    if strings.Contains(path, "/image-recognize") {
        return RequestTypeImageRecognize
    }

    // 音频类（新增3个）
    if strings.Contains(path, "/text-to-audio") {
        return RequestTypeTextToAudio
    }
    if strings.Contains(path, "/video-to-audio") {
        return RequestTypeVideoToAudio
    }
    if strings.Contains(path, "/tts") {
        return RequestTypeTTS
    }

    // 图片类（新增4个）
    if strings.Contains(path, "/generations") {
        return RequestTypeImageGeneration
    }
    if strings.Contains(path, "/omni-image") {
        return RequestTypeOmniImage
    }
    if strings.Contains(path, "/multi-image2image") {
        return RequestTypeMultiImage2Image
    }
    if strings.Contains(path, "/editing/expand") {
        return RequestTypeImageExpand
    }

    return ""
}
```

### 3.3 数据结构定义

**文件**: `relay/channel/kling/model.go`

为 13 个接口定义请求结构体（示例，具体字段需参考 Kling API 文档）：

```go
// ============ 视频类接口 ============

// 镜头控制请求
type MotionControlRequest struct {
    KlingBaseRequest
    VideoID       string         `json:"video_id"`
    CameraControl *CameraControl `json:"camera_control,omitempty"`
    Duration      int            `json:"duration,omitempty"`
    Mode          string         `json:"mode,omitempty"`
}

// 多元素初始化选择请求
type MultiElementsRequest struct {
    KlingBaseRequest
    VideoID    string   `json:"video_id"`
    ElementIDs []string `json:"element_ids,omitempty"`
}

// 视频延长请求
type VideoExtendRequest struct {
    KlingBaseRequest
    VideoID   string `json:"video_id"`
    Duration  int    `json:"duration,omitempty"`
    Direction string `json:"direction,omitempty"` // before/after
}

// 数字人图生视频请求
type AvatarImage2VideoRequest struct {
    KlingBaseRequest
    Image       string `json:"image"`
    AudioFile   string `json:"audio_file,omitempty"`
    AudioID     string `json:"audio_id,omitempty"`
    Duration    int    `json:"duration,omitempty"`
    AspectRatio string `json:"aspect_ratio,omitempty"`
}

// 视频效果应用请求
type VideoEffectsRequest struct {
    KlingBaseRequest
    VideoID    string                 `json:"video_id"`
    EffectType string                 `json:"effect_type"`
    EffectParams map[string]interface{} `json:"effect_params,omitempty"`
}

// 图像识别请求
type ImageRecognizeRequest struct {
    KlingBaseRequest
    VideoID  string `json:"video_id,omitempty"`
    VideoURL string `json:"video_url,omitempty"`
    Image    string `json:"image,omitempty"`
}

// ============ 音频类接口 ============

// 文本转音频请求
type TextToAudioRequest struct {
    KlingBaseRequest
    Text     string  `json:"text"`
    Voice    string  `json:"voice,omitempty"`
    Speed    float64 `json:"speed,omitempty"`
    Volume   float64 `json:"volume,omitempty"`
    Duration int     `json:"duration,omitempty"`
}

// 视频提取音频请求
type VideoToAudioRequest struct {
    KlingBaseRequest
    VideoID  string `json:"video_id,omitempty"`
    VideoURL string `json:"video_url,omitempty"`
}

// 文本转语音请求
type TTSRequest struct {
    KlingBaseRequest
    Text     string  `json:"text"`
    Voice    string  `json:"voice,omitempty"`
    Speed    float64 `json:"speed,omitempty"`
    Pitch    float64 `json:"pitch,omitempty"`
}

// ============ 图片类接口 ============

// 图片生成请求
type ImageGenerationRequest struct {
    KlingBaseRequest
    Prompt         string  `json:"prompt"`
    NegativePrompt string  `json:"negative_prompt,omitempty"`
    AspectRatio    string  `json:"aspect_ratio,omitempty"`
    N              int     `json:"n,omitempty"`
    Style          string  `json:"style,omitempty"`
    CfgScale       float64 `json:"cfg_scale,omitempty"`
}

// 全能图片请求
type OmniImageRequest struct {
    KlingBaseRequest
    Prompt         string `json:"prompt"`
    Image          string `json:"image,omitempty"`
    NegativePrompt string `json:"negative_prompt,omitempty"`
    AspectRatio    string `json:"aspect_ratio,omitempty"`
    Style          string `json:"style,omitempty"`
}

// 多图转图请求
type MultiImage2ImageRequest struct {
    KlingBaseRequest
    Images         []ImageItem `json:"images"`
    Prompt         string      `json:"prompt"`
    NegativePrompt string      `json:"negative_prompt,omitempty"`
    AspectRatio    string      `json:"aspect_ratio,omitempty"`
}

// 图片扩展编辑请求
type ImageExpandRequest struct {
    KlingBaseRequest
    Image       string `json:"image"`
    Prompt      string `json:"prompt,omitempty"`
    Direction   string `json:"direction,omitempty"` // top/bottom/left/right
    ExpandRatio float64 `json:"expand_ratio,omitempty"`
}
```

**注意**: 具体字段需要根据 Kling 官方 API 文档逐个确认。

### 3.4 控制器扩展

**文件**: `controller/kling_video.go`

#### 3.4.1 添加辅助函数

```go
// 判断是否为图片类请求
func isImageRequestType(requestType string) bool {
    return requestType == kling.RequestTypeImageGeneration ||
           requestType == kling.RequestTypeOmniImage ||
           requestType == kling.RequestTypeMultiImage2Image ||
           requestType == kling.RequestTypeImageExpand
}
```

#### 3.4.2 修改 RelayKlingVideo 函数

在现有的 `RelayKlingVideo` 函数中，修改创建任务记录的部分：

```go
func RelayKlingVideo(c *gin.Context) {
    meta := util.GetRelayMeta(c)

    // 1. 确定请求类型
    requestType := kling.DetermineRequestType(c.Request.URL.Path)
    if requestType == "" {
        err := openai.ErrorWrapper(fmt.Errorf("unsupported endpoint"), "invalid_endpoint", http.StatusBadRequest)
        c.JSON(err.StatusCode, err.Error)
        return
    }

    // 2. 解析请求、计算费用、验证余额（现有逻辑不变）
    bodyBytes, _ := c.GetRawData()
    var requestParams map[string]interface{}
    json.Unmarshal(bodyBytes, &requestParams)

    model := kling.GetModelNameFromRequest(requestParams)
    duration := fmt.Sprintf("%d", kling.GetDurationFromRequest(requestParams))
    mode := kling.GetModeFromRequest(requestParams)
    quota := common.CalculateVideoQuota(model, requestType, mode, duration, "")

    // 验证余额...

    // 3. 根据类型创建不同的任务记录
    var externalTaskId int64

    if isImageRequestType(requestType) {
        // 图片类：创建 Image 记录
        image := &dbmodel.Image{
            TaskId:    "",
            UserId:    meta.UserId,
            Username:  user.Username,
            ChannelId: meta.ChannelId,
            Model:     model,
            Provider:  "kling",
            Status:    "",
            Quota:     quota,
            Mode:      mode,
            CreatedAt: time.Now().Unix(),
        }
        if err := image.Insert(); err != nil {
            errResp := openai.ErrorWrapper(err, "create_image_error", http.StatusInternalServerError)
            c.JSON(errResp.StatusCode, errResp.Error)
            return
        }
        externalTaskId = image.Id
    } else {
        // 视频/音频类：创建 Video 记录
        video := &dbmodel.Video{
            TaskId:    "",
            UserId:    meta.UserId,
            Username:  user.Username,
            ChannelId: meta.ChannelId,
            Model:     model,
            Provider:  "kling",
            Type:      requestType,
            Status:    "",
            Quota:     quota,
            Mode:      mode,
            Duration:  kling.GetDurationFromRequest(requestParams),
            CreatedAt: time.Now().Unix(),
        }
        if err := video.Insert(); err != nil {
            errResp := openai.ErrorWrapper(err, "create_video_error", http.StatusInternalServerError)
            c.JSON(errResp.StatusCode, errResp.Error)
            return
        }
        externalTaskId = video.Id
    }

    // 4. 调用 Kling API、返回响应（现有逻辑不变）
    // 注意：需要将 externalTaskId 注入到请求参数中
    // ...
}
```

### 3.5 回调处理扩展

**文件**: `controller/kling_video.go`

修改 `HandleKlingCallback` 函数，添加 fallback 查询机制：

```go
func HandleKlingCallback(c *gin.Context) {
    // 1. 解析回调数据
    bodyBytes, err := c.GetRawData()
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid callback data"})
        return
    }

    var callback kling.CallbackNotification
    if err := json.Unmarshal(bodyBytes, &callback); err != nil {
        logger.Error(fmt.Sprintf("解析回调数据失败: %v", err))
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
        return
    }

    externalTaskId := callback.ExternalTaskID
    if externalTaskId == "" {
        logger.Error("回调缺少 external_task_id")
        c.JSON(http.StatusBadRequest, gin.H{"error": "missing external_task_id"})
        return
    }

    // 2. Fallback 查询机制：先 Video，后 Image
    video, err := dbmodel.GetVideoByTaskId(externalTaskId)
    if err == nil {
        // 找到 Video 记录
        handleVideoCallback(video, &callback)
        c.JSON(http.StatusOK, gin.H{"message": "success"})
        return
    }

    image, err := dbmodel.GetImageByTaskId(externalTaskId)
    if err == nil {
        // 找到 Image 记录
        handleImageCallback(image, &callback)
        c.JSON(http.StatusOK, gin.H{"message": "success"})
        return
    }

    // 3. 未找到记录
    logger.Error(fmt.Sprintf("任务未找到: external_task_id=%s", externalTaskId))
    c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
}

// Video 回调处理（提取现有逻辑）
func handleVideoCallback(video *dbmodel.Video, callback *kling.CallbackNotification) {
    video.TaskId = callback.TaskID
    video.Status = callback.TaskStatus

    if callback.TaskStatus == kling.TaskStatusSucceed {
        // 成功：保存结果，实际扣费
        resultJSON, _ := json.Marshal(callback.TaskResult)
        video.Result = string(resultJSON)

        // 从结果中提取 duration 并更新
        if len(callback.TaskResult.Videos) > 0 {
            durationStr := callback.TaskResult.Videos[0].Duration
            if duration, err := parseDuration(durationStr); err == nil {
                video.Duration = duration
            }
        }

        // 实际扣费
        err := dbmodel.DecreaseUserQuota(video.UserId, video.Quota)
        if err != nil {
            logger.Error(fmt.Sprintf("扣费失败: user_id=%d, quota=%d, error=%v",
                video.UserId, video.Quota, err))
        }

    } else if callback.TaskStatus == kling.TaskStatusFailed {
        // 失败：记录原因，不扣费
        video.FailReason = callback.TaskStatusMsg
        logger.Warn(fmt.Sprintf("任务失败: task_id=%s, reason=%s",
            video.TaskId, video.FailReason))
    }

    video.Update()
}

// Image 回调处理（新增，逻辑与 Video 类似）
func handleImageCallback(image *dbmodel.Image, callback *kling.CallbackNotification) {
    image.TaskId = callback.TaskID
    image.Status = callback.TaskStatus

    if callback.TaskStatus == kling.TaskStatusSucceed {
        // 成功：保存结果，实际扣费
        resultJSON, _ := json.Marshal(callback.TaskResult)
        image.Result = string(resultJSON)

        // 实际扣费
        err := dbmodel.DecreaseUserQuota(image.UserId, image.Quota)
        if err != nil {
            logger.Error(fmt.Sprintf("扣费失败: user_id=%d, quota=%d, error=%v",
                image.UserId, image.Quota, err))
        }

    } else if callback.TaskStatus == kling.TaskStatusFailed {
        // 失败：记录原因，不扣费
        image.FailReason = callback.TaskStatusMsg
        logger.Warn(fmt.Sprintf("任务失败: task_id=%s, reason=%s",
            image.TaskId, image.FailReason))
    }

    image.Update()
}

// 解析时长字符串（如 "5.0s" -> 5）
func parseDuration(durationStr string) (int, error) {
    durationStr = strings.TrimSuffix(durationStr, "s")
    duration, err := strconv.ParseFloat(durationStr, 64)
    if err != nil {
        return 0, err
    }
    return int(duration), nil
}
```

### 3.6 路由注册

**文件**: `router/relay-router.go`

在现有的 `klingRouter` 路由组中添加 13 个新路由：

```go
klingRouter := router.Group("/kling/v1")
klingRouter.Use(middleware.RelayPanicRecover(), middleware.TokenAuth(), middleware.Distribute())
{
    // 现有视频接口...
    klingRouter.POST("/videos/text2video", controller.RelayKlingVideo)
    klingRouter.POST("/videos/omni-video", controller.RelayKlingVideo)
    // ...

    // ========== 新增视频类（6个） ==========
    klingRouter.POST("/videos/motion-control", controller.RelayKlingVideo)
    klingRouter.POST("/videos/multi-elements/init-selection", controller.RelayKlingVideo)
    klingRouter.POST("/videos/video-extend", controller.RelayKlingVideo)
    klingRouter.POST("/videos/avatar/image2video", controller.RelayKlingVideo)
    klingRouter.POST("/videos/effects", controller.RelayKlingVideo)
    klingRouter.POST("/videos/image-recognize", controller.RelayKlingVideo)

    // ========== 新增音频类（3个） ==========
    klingRouter.POST("/audio/text-to-audio", controller.RelayKlingVideo)
    klingRouter.POST("/audio/video-to-audio", controller.RelayKlingVideo)
    klingRouter.POST("/audio/tts", controller.RelayKlingVideo)

    // ========== 新增图片类（4个） ==========
    klingRouter.POST("/images/generations", controller.RelayKlingVideo)
    klingRouter.POST("/images/omni-image", controller.RelayKlingVideo)
    klingRouter.POST("/images/multi-image2image", controller.RelayKlingVideo)
    klingRouter.POST("/images/editing/expand", controller.RelayKlingVideo)
}
```

---

## 4. 计费配置

### 4.1 计费策略

- **视频类**：按时长计费（`pricing_type: per_second`）
- **音频类**：按时长计费（`pricing_type: per_second`）
- **图片类**：按次计费（`pricing_type: fixed`）

### 4.2 后台配置示例

在后台管理界面为每个 `type` 添加定价规则：

```json
// 视频类
{
  "model": "kling-*",
  "type": "motion-control",
  "mode": "*",
  "duration": "*",
  "pricing_type": "per_second",
  "price": 0.001,
  "currency": "USD"
}

// 音频类
{
  "model": "kling-*",
  "type": "text-to-audio",
  "mode": "*",
  "duration": "*",
  "pricing_type": "per_second",
  "price": 0.0008,
  "currency": "USD"
}

// 图片类
{
  "model": "kling-*",
  "type": "image-generation",
  "mode": "*",
  "duration": "0",
  "pricing_type": "fixed",
  "price": 0.02,
  "currency": "USD"
}
```

### 4.3 需要配置的类型

| 类型 | Type 值 | 建议定价类型 |
|------|---------|--------------|
| 镜头控制 | motion-control | per_second |
| 多元素选择 | multi-elements | fixed |
| 视频延长 | video-extend | per_second |
| 数字人图生视频 | avatar-image2video | per_second |
| 视频效果 | video-effects | fixed |
| 图像识别 | image-recognize | fixed |
| 文本转音频 | text-to-audio | per_second |
| 视频提取音频 | video-to-audio | fixed |
| 文本转语音 | tts | per_second |
| 图片生成 | image-generation | fixed |
| 全能图片 | omni-image | fixed |
| 多图转图 | multi-image2image | fixed |
| 图片扩展 | image-expand | fixed |

---

## 5. 实施步骤

### 5.1 实施顺序

| 步骤 | 文件 | 任务 | 预计时间 |
|------|------|------|----------|
| 1 | `relay/channel/kling/constants.go` | 添加 13 个新常量 | 10分钟 |
| 2 | `relay/channel/kling/util.go` | 扩展 `DetermineRequestType` | 15分钟 |
| 3 | `relay/channel/kling/model.go` | 定义 13 个请求结构体 | 1-2小时 |
| 4 | `controller/kling_video.go` | 添加 `isImageRequestType` 和修改 `RelayKlingVideo` | 30分钟 |
| 5 | `controller/kling_video.go` | 修改 `HandleKlingCallback` + 提取回调处理函数 | 30分钟 |
| 6 | `router/relay-router.go` | 注册 13 个新路由 | 10分钟 |
| 7 | 后台管理界面 | 配置 13 个类型的定价规则 | 20分钟 |
| 8 | 测试验证 | 分批测试所有接口 | 2-3小时 |

**总计**: 约 6-9 小时（一个工作日内完成）

### 5.2 测试策略

**分批测试**：
1. 第一批：测试 1-2 个视频接口（如 `image-recognize`）
2. 第二批：测试 1 个音频接口（如 `text-to-audio`）
3. 第三批：测试 1 个图片接口（如 `image-generation`）
4. 第四批：快速测试其余接口

**关键验证点**：
- ✅ 请求参数正确传递
- ✅ 回调 URL 正确生成
- ✅ Video/Image 表正确写入
- ✅ Fallback 查询机制正确
- ✅ 成功时正确扣费，失败时不扣费

---

## 6. 风险和注意事项

### 6.1 技术风险

| 风险 | 影响 | 应对措施 |
|------|------|----------|
| API 文档不完整 | 请求参数可能不正确 | 逐个接口查看文档，测试验证 |
| 回调格式差异 | 回调解析失败 | 第一批测试时重点验证 |
| Image 表字段不足 | 无法存储特定信息 | 使用 `Result` 字段（JSON）存储扩展信息 |
| 计费规则不明确 | 定价可能不合理 | 先按估算配置，后续根据实际调整 |

### 6.2 实施注意事项

1. **数据库兼容性**
   - 确保 `Video` 和 `Image` 表的 `Type` 字段长度足够（varchar(255)）
   - 新的 `Type` 值不与现有值冲突

2. **回调 URL 配置**
   - 确保 `KLING_CALLBACK_DOMAIN` 环境变量正确
   - 回调 URL 必须公网可访问

3. **错误日志**
   - 在关键节点添加日志（API 调用、回调处理、扣费）
   - 便于生产环境问题排查

4. **向后兼容**
   - 新增代码不影响现有 6 个视频接口
   - 测试时优先验证现有接口正常工作

---

## 7. 验收标准

### 7.1 功能验收

- ✅ 所有 13 个接口可正常调用
- ✅ 异步任务状态正确更新
- ✅ 回调处理正确扣费
- ✅ 失败情况不扣费
- ✅ 支持查询任务状态和结果
- ✅ Video 和 Image 表的 fallback 查询正确

### 7.2 质量验收

- ✅ 代码通过 linter 检查
- ✅ 关键日志完整
- ✅ 接口响应时间 < 2s（不含 Kling API 耗时）
- ✅ 错误处理完善

---

## 8. 后续优化

1. **批量处理**: 支持批量提交任务
2. **监控告警**: 任务失败率、回调延迟监控
3. **结果缓存**: 相同参数缓存结果
4. **成本优化**: 智能路由到低成本渠道

---

## 9. 参考文档

- Kling API 官方文档: https://app.klingai.com/cn/dev/document-api/
- 现有实施计划: `/docs/KLING_API_INTEGRATION_PLAN.md`

---

**设计完成时间**: 2026-01-19
**预计实施完成**: 2026-01-19（同日）
