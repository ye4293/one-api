# Kling API 批量接入实施计划

## 概述

接入 16 个新的 Kling API 接口，分为 4 个类别按优先级实施：

- **第一批：视频类（6个接口）**
- **第二批：音频类（3个接口）**
- **第三批：图片类（5个接口）**  
- **第四批：通用类（2个接口）**

所有接口采用统一的架构模式，复用现有的 `relay/channel/kling` 适配器和回调机制。

---

## 第一批：视频类接口（优先实施）

### 新增接口列表

1. `/v1/videos/motion-control` - 镜头动作控制
2. `/v1/videos/multi-elements/init-selection` - 多元素初始化选择
3. `/v1/videos/video-extend` - 视频延长
4. `/v1/videos/avatar/image2video` - 数字人图生视频
5. `/v1/videos/effects` - 视频效果应用
6. `/v1/videos/image-recognize` - 图像识别

### 实施步骤

#### 1. 扩展常量和类型定义

**文件**: `relay/channel/kling/constants.go`

添加新的请求类型常量：

```go
// 视频相关请求类型
RequestTypeMotionControl    = "motion-control"
RequestTypeMultiElements    = "multi-elements"  
RequestTypeVideoExtend      = "video-extend"
RequestTypeAvatarI2V        = "avatar-image2video"
RequestTypeVideoEffects     = "video-effects"
RequestTypeImageRecognize   = "image-recognize"
```

#### 2. 更新路由识别逻辑

**文件**: `relay/channel/kling/util.go`

在 `DetermineRequestType` 函数中添加新路径识别：

```go
else if strings.Contains(path, "/motion-control") {
    return RequestTypeMotionControl
} else if strings.Contains(path, "/multi-elements") {
    return RequestTypeMultiElements
}
// ... 其他类型识别
```

#### 3. 定义数据结构

**文件**: `relay/channel/kling/model.go`

为每个接口定义请求和响应结构体，参考官方 API 文档：

```go
// 镜头控制请求
type MotionControlRequest struct {
    KlingBaseRequest
    VideoID       string                 `json:"video_id"`
    CameraControl *CameraControl         `json:"camera_control"`
    // 其他参数根据官方文档补充
}

// 多元素请求
type MultiElementsRequest struct {
    KlingBaseRequest
    VideoID    string   `json:"video_id"`
    ElementIDs []string `json:"element_ids,omitempty"`
    // 其他参数
}

// 视频延长请求
type VideoExtendRequest struct {
    KlingBaseRequest
    VideoID       string `json:"video_id"`
    ExtendSeconds int    `json:"extend_seconds"`
    Direction     string `json:"direction"` // before/after
}

// ... 其他结构体
```

#### 4. 实现控制器函数

**文件**: `controller/kling_video.go`

为每个接口实现独立的处理函数（参考 `DoAdvancedLipSync` 模式）：

- `DoMotionControl(c *gin.Context)`
- `DoMultiElementsInit(c *gin.Context)`
- `DoVideoExtend(c *gin.Context)`
- `DoAvatarImage2Video(c *gin.Context)`
- `DoVideoEffects(c *gin.Context)`
- `DoImageRecognize(c *gin.Context)`

**通用处理流程**：

1. 解析请求参数
2. 计算预估费用（使用 `common.CalculateVideoQuota`）
3. 验证用户余额
4. 创建 `Video` 数据库记录
5. 构建回调 URL
6. 调用 Kling API（注入 `callback_url` 和 `external_task_id`）
7. 更新任务 ID 和状态
8. 返回响应

#### 5. 注册路由

**文件**: `router/relay-router.go`

在现有的 `klingRouter` 路由组中添加：

```go
klingRouter.POST("/motion-control", controller.DoMotionControl)
klingRouter.POST("/multi-elements/init-selection", controller.DoMultiElementsInit)
klingRouter.POST("/video-extend", controller.DoVideoExtend)
klingRouter.POST("/avatar/image2video", controller.DoAvatarImage2Video)
klingRouter.POST("/effects", controller.DoVideoEffects)
klingRouter.POST("/image-recognize", controller.DoImageRecognize)
```

#### 6. 回调处理

**文件**: `controller/kling_video.go` 中的 `HandleKlingCallback`

确认现有回调处理器已支持新类型（应无需修改，因为回调逻辑统一）。

#### 7. 计费配置

在后台系统中为每个新类型添加计费规则示例：

```json
{
  "model": "kling-*",
  "type": "motion-control",
  "mode": "*",
  "duration": "*",
  "pricing_type": "fixed",
  "price": 0.05,
  "currency": "USD",
  "priority": 10
}
```

---

## 第二批：音频类接口

### 新增接口列表

1. `/v1/audio/text-to-audio` - 文本转音频
2. `/v1/audio/video-to-audio` - 视频提取音频
3. `/v1/audio/tts` - 文本转语音

### 实施要点

#### 数据库扩展

可能需要新建 `Audio` 表或复用 `Video` 表，字段包括：

- `task_id`
- `user_id`, `channel_id`
- `type` (text-to-audio / video-to-audio / tts)
- `status`, `result`, `fail_reason`
- `quota`, `duration`

#### 计费逻辑

音频计费可能按时长或固定价格，需要扩展 `common/video-pricing.go` 或创建 `audio-pricing.go`。

#### 路由分组

创建新的 `klingAudioRouter` 路由组：

```go
klingAudioRouter := router.Group("/kling/v1/audio")
klingAudioRouter.Use(middleware.RelayPanicRecover(), middleware.TokenAuth(), middleware.Distribute())
{
    klingAudioRouter.POST("/text-to-audio", controller.DoTextToAudio)
    klingAudioRouter.POST("/video-to-audio", controller.DoVideoToAudio)
    klingAudioRouter.POST("/tts", controller.DoTTS)
}
```

---

## 第三批：图片类接口

### 新增接口列表

1. `/v1/images/generations` - 图片生成
2. `/v1/images/omni-image` - 全能图片
3. `/v1/images/multi-image2image` - 多图转图
4. `/v1/images/editing/expand` - 图片扩展编辑
5. `/v1/images/kolors-virtual-try-on` - Kolors 虚拟试穿

### 实施要点

#### 数据库表

新建 `Image` 表或扩展现有 `dbmodel.Image`：

- 与 Video 表类似结构
- 特有字段：`resolution`, `format`, `style`

#### 适配器扩展

创建 `relay/channel/kling/image_adaptor.go`，参考视频适配器模式。

#### 路由分组

```go
klingImageRouter := router.Group("/kling/v1/images")
klingImageRouter.Use(middleware.RelayPanicRecover(), middleware.TokenAuth(), middleware.Distribute())
{
    klingImageRouter.POST("/generations", controller.DoImageGeneration)
    klingImageRouter.POST("/omni-image", controller.DoOmniImage)
    klingImageRouter.POST("/multi-image2image", controller.DoMultiImage2Image)
    klingImageRouter.POST("/editing/expand", controller.DoImageExpand)
    klingImageRouter.POST("/kolors-virtual-try-on", controller.DoVirtualTryOn)
}
```

---

## 第四批：通用类接口

### 新增接口列表

1. `/v1/general/custom-elements` - 自定义元素训练
2. `/v1/general/custom-voices` - 自定义声音训练

### 实施要点

#### 特殊性

这两个接口可能是长时任务（训练模型），需要：

- 更长的超时时间
- 进度查询接口
- 训练结果管理

#### 数据库表

新建 `CustomTask` 表：

- `task_id`, `type` (custom-element / custom-voice)
- `training_status` (queued / training / completed / failed)
- `progress` (训练进度 0-100)
- `model_id` (训练完成后的模型 ID)

#### 路由分组

```go
klingGeneralRouter := router.Group("/kling/v1/general")
klingGeneralRouter.Use(middleware.RelayPanicRecover(), middleware.TokenAuth(), middleware.Distribute())
{
    klingGeneralRouter.POST("/custom-elements", controller.DoCustomElements)
    klingGeneralRouter.POST("/custom-voices", controller.DoCustomVoices)
    klingGeneralRouter.GET("/custom-tasks/:id", controller.GetCustomTaskStatus)
}
```

---

## 统一架构模式

### 架构图

```mermaid
flowchart TB
    Client[客户端请求]
    
    subgraph Router[路由层]
        KlingVideo[/kling/v1/videos/*]
        KlingAudio[/kling/v1/audio/*]
        KlingImage[/kling/v1/images/*]
        KlingGeneral[/kling/v1/general/*]
    end
    
    subgraph Middleware[中间件层]
        Auth[TokenAuth]
        Distribute[Distribute渠道分发]
    end
    
    subgraph Controller[控制器层]
        DoMotion[DoMotionControl]
        DoAudio[DoTextToAudio]
        DoImage[DoImageGeneration]
        DoCustom[DoCustomElements]
    end
    
    subgraph Service[服务层]
        ParseReq[解析请求]
        CalcQuota[计算费用]
        CheckBalance[验证余额]
        CreateTask[创建任务记录]
        CallAPI[调用KlingAPI]
    end
    
    subgraph Database[数据库]
        VideoTable[(Video表)]
        AudioTable[(Audio表)]
        ImageTable[(Image表)]
        CustomTable[(CustomTask表)]
    end
    
    subgraph External[外部服务]
        KlingAPI[Kling API]
        CallbackURL[回调地址]
    end
    
    Client --> Router
    Router --> Auth
    Auth --> Distribute
    Distribute --> Controller
    Controller --> Service
    Service --> Database
    Service --> KlingAPI
    KlingAPI -.异步回调.-> CallbackURL
    CallbackURL --> HandleCallback[HandleKlingCallback]
    HandleCallback --> Database
```

### 通用处理流程

所有新接口遵循相同的处理模式：

1. **请求验证**：参数解析、格式验证
2. **费用计算**：根据配置计算预估费用
3. **余额检查**：验证用户余额是否充足
4. **任务创建**：插入数据库记录，状态为 `pending`
5. **API 调用**：向 Kling 发送请求（包含回调 URL）
6. **响应返回**：返回 `task_id` 给客户端
7. **回调处理**：接收 Kling 回调，更新状态和结果
8. **实际计费**：成功时扣费，失败不扣费

---

## 文件结构

实施完成后，新增/修改的文件：

```
relay/channel/kling/
├── constants.go           [修改] 新增常量
├── model.go               [修改] 新增数据结构
├── util.go                [修改] 路由识别
├── adaptor.go             [复用] 通用适配器
├── audio_adaptor.go       [新建] 音频适配器
└── image_adaptor.go       [新建] 图片适配器

controller/
├── kling_video.go         [修改] 新增视频处理函数
├── kling_audio.go         [新建] 音频处理函数
├── kling_image.go         [新建] 图片处理函数
└── kling_general.go       [新建] 通用功能处理

model/
├── video.go               [修改] 可能扩展字段
├── audio.go               [新建] 音频任务表
├── image.go               [修改] 扩展图片表
└── custom_task.go         [新建] 自定义任务表

router/
└── relay-router.go        [修改] 注册新路由

common/
├── video-pricing.go       [修改] 扩展计费规则
└── audio-pricing.go       [新建] 音频计费

docs/
├── KLING_VIDEO_API.md     [新建] 视频接口文档
├── KLING_AUDIO_API.md     [新建] 音频接口文档
├── KLING_IMAGE_API.md     [新建] 图片接口文档
└── KLING_GENERAL_API.md   [新建] 通用接口文档
```

---

## 实施时间线

| 批次 | 接口数 | 预计工期 | 关键里程碑 |
|------|--------|----------|------------|
| **第一批：视频类** | 6个 | 3-4天 | 完成所有视频接口、测试、文档 |
| **第二批：音频类** | 3个 | 2-3天 | 音频表设计、计费逻辑、接口实现 |
| **第三批：图片类** | 5个 | 3-4天 | 图片适配器、多种生成模式支持 |
| **第四批：通用类** | 2个 | 2-3天 | 训练任务管理、进度查询 |
| **总计** | 16个 | 10-14天 | 全部接口上线、集成测试 |

---

## 风险和注意事项

### 技术风险

1. **API 文档不完整**：部分接口可能缺少详细文档，需要联系 Kling 官方确认
2. **计费模式差异**：不同类型接口的计费方式可能不同（按时长/按次数/按分辨率）
3. **回调延迟**：长时任务（如自定义训练）回调可能延迟很久

### 数据库迁移

- 新增表需要编写迁移脚本
- 考虑向后兼容性
- 生产环境迁移需要停机时间评估

### 测试策略

1. **单元测试**：每个控制器函数
2. **集成测试**：完整工作流（创建 → 查询 → 回调）
3. **压力测试**：并发请求、回调处理能力
4. **真实环境测试**：使用 Kling 测试账号验证

---

## 配置示例

### 环境变量

```bash
# Kling API 配置
KLING_API_BASE_URL=https://api-beijing.klingai.com
KLING_API_TIMEOUT=30s
KLING_CALLBACK_DOMAIN=https://your-domain.com

# 计费配置（后台管理界面配置）
```

### 计费规则配置

参考 `common/video-pricing.go` 格式，在后台添加：

```json
[
  {
    "model": "kling-*",
    "type": "motion-control",
    "pricing_type": "fixed",
    "price": 0.05,
    "currency": "USD"
  },
  {
    "model": "kling-*",
    "type": "text-to-audio",
    "pricing_type": "per_second",
    "price": 0.001,
    "currency": "USD"
  }
]
```

---

## 验收标准

### 功能验收

- ✅ 所有 16 个接口可正常调用
- ✅ 异步任务状态正确更新
- ✅ 回调处理正确扣费
- ✅ 失败情况不扣费
- ✅ 支持查询任务状态和结果

### 质量验收

- ✅ 通过所有 linter 检查
- ✅ 单元测试覆盖率 > 80%
- ✅ 接口响应时间 < 2s（不含 Kling API 耗时）
- ✅ 错误日志完整、可追溯

### 文档验收

- ✅ 每个接口都有详细 API 文档
- ✅ 包含请求/响应示例
- ✅ curl 和代码示例
- ✅ 错误码说明

---

## 后续优化

1. **批量处理**：支持批量提交任务
2. **优先级队列**：付费用户优先处理
3. **结果缓存**：相同参数缓存结果
4. **监控告警**：任务失败率、回调延迟监控
5. **成本优化**：智能路由到低成本渠道

---

## TODO 清单

### 第一批：视频类接口（6个）

- [ ] 扩展 constants.go 添加 6 个视频类请求类型常量
- [ ] 更新 util.go 的 DetermineRequestType 函数识别新路径
- [ ] 在 model.go 中定义 6 个视频接口的请求响应结构体
- [ ] 实现 6 个视频接口的控制器函数（DoMotionControl 等）
- [ ] 在 relay-router.go 注册 6 个视频接口路由
- [ ] 创建视频接口文档 KLING_VIDEO_API.md

### 第二批：音频类接口（3个）

- [ ] 添加 3 个音频类请求类型常量
- [ ] 定义音频接口数据结构和数据库表
- [ ] 创建 audio-pricing.go 实现音频计费逻辑
- [ ] 实现 3 个音频接口控制器函数（DoTextToAudio 等）
- [ ] 注册音频接口路由组
- [ ] 创建音频接口文档 KLING_AUDIO_API.md

### 第三批：图片类接口（5个）

- [ ] 添加 5 个图片类请求类型常量
- [ ] 扩展 Image 表和定义图片接口数据结构
- [ ] 创建 image_adaptor.go 图片适配器
- [ ] 实现 5 个图片接口控制器函数
- [ ] 注册图片接口路由组
- [ ] 创建图片接口文档 KLING_IMAGE_API.md

### 第四批：通用类接口（2个）

- [ ] 添加 2 个通用类请求类型常量
- [ ] 创建 CustomTask 表和数据结构
- [ ] 实现自定义元素和声音接口及进度查询
- [ ] 注册通用接口路由组
- [ ] 创建通用接口文档 KLING_GENERAL_API.md

### 集成与测试

- [ ] 编写集成测试验证所有接口工作流

---

**创建时间**: 2026-01-19  
**计划版本**: 1.0.0
