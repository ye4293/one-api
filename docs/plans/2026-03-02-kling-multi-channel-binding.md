# Kling 多渠道支持 - 渠道绑定完整方案

## 背景

多个 Kling 渠道通过 `middleware.Distribute()` 自动负载均衡，但部分接口依赖前序任务的输出（如 `video_id`、`session_id`、`element_id`），必须路由到创建该资源的原渠道，否则 Kling API 报 404。

---

## 渠道绑定规则总览

### POST 创建类路由

| 路由 | 依赖字段 | 查表 | 渠道策略 |
|------|----------|------|----------|
| `/videos/text2video` | - | - | 随机 |
| `/videos/omni-video` | - | - | 随机 |
| `/videos/image2video` | - | - | 随机 |
| `/videos/multi-image2video` | - | - | 随机 |
| `/videos/motion-control` | - | - | 随机（参数为 `image_url`+`video_url`，不含 `video_id`） |
| `/videos/multi-elements/init-selection` | `video_id` | Video.video_id → channel_id | 绑定原渠道 |
| `/videos/video-extend` | `video_id`（必填） | Video.video_id → channel_id | 绑定原渠道 |
| `/videos/avatar/image2video` | - | - | 随机 |
| `/videos/effects` | - | - | 随机（参数为 `effect_scene`+`input.image`，不含 `video_id`） |
| `/videos/image-recognize` | - | - | 随机 |
| `/videos/identify-face` | `video_id`（可选，与 `video_url` 二选一） | Video.video_id → channel_id | 有 `video_id` 时绑定，有 `video_url` 时随机 |
| `/videos/advanced-lip-sync` | `session_id`（必填，等于 identify-face 的 task_id） | Video.task_id → channel_id | 绑定原渠道 |
| `/audio/text-to-audio` | - | - | 随机 |
| `/audio/video-to-audio` | `video_id` | Video.video_id → channel_id | 绑定原渠道 |
| `/audio/tts` | `voice_id`（可选） | Video.task_id → channel_id | 有 `voice_id` 时绑定 |
| `/images/generations` | `element_id`（可选） | Video.task_id → channel_id | 有 `element_id` 时绑定 |
| `/images/omni-image` | - | - | 随机 |
| `/images/multi-image2image` | - | - | 随机 |
| `/images/editing/expand` | - | - | 随机 |
| `/images/kolors-virtual-try-on` | - | - | 随机 |
| `/general/custom-elements` | - | - | 随机（创建资源） |
| `/general/custom-voices` | - | - | 随机（创建资源） |

> **API 文档勘误 1**：`/videos/motion-control` 参数为 `image_url`+`video_url`+`character_orientation`+`mode`，**不含 `video_id`**，无需渠道绑定。
>
> **API 文档勘误 2**：`/videos/effects` 参数为 `effect_scene`+`input`（含 `image`/`images` URL），**不含 `video_id`**，无需渠道绑定。
>
> **API 文档勘误 3**：`/videos/identify-face` 的 `video_id` 与 `video_url` **二选一**，传 `video_url`（外部 URL）时无需绑定。
>
> **新发现**：`/videos/advanced-lip-sync` 传入的是 `session_id`（= identify-face 返回的 task_id），需通过 `Video.task_id` 查找原渠道，而非 `Video.video_id`。

### GET 查询类路由

| 路由 | 渠道策略 |
|------|----------|
| `GET /videos/*path` | 从 URL 末段提取 task_id，查 Video/Image 表绑定原渠道 |
| `GET /audio/*path` | 同上 |
| `GET /images/*path` | 同上 |
| `GET /general/*path` | 同上 |

---

## 实现状态（截至 2026-03-02）

### 已完成

#### 1. `resolveChannelForBoundResource`（POST body 中的资源 ID 绑定）

位于 `controller/kling_video.go`，按优先级检查三个字段：

```go
func resolveChannelForBoundResource(params map[string]interface{}) *dbmodel.Channel {
    // 1. video_id → 查 Video.video_id 字段
    if videoID := extractResourceID(params, "video_id"); videoID != "" {
        if video, err := dbmodel.GetVideoTaskByVideoId(videoID); err == nil && video != nil && video.ChannelId != 0 {
            if ch, err := dbmodel.GetChannelById(video.ChannelId, true); err == nil {
                return ch
            }
        }
    }
    // 2. element_id / voice_id → 查 Video.task_id 字段
    taskID := extractResourceID(params, "element_id")
    if taskID == "" {
        taskID = extractResourceID(params, "voice_id")
    }
    if taskID == "" {
        return nil
    }
    if video, err := dbmodel.GetVideoTaskById(taskID); err == nil && video != nil && video.ChannelId != 0 {
        if ch, err := dbmodel.GetChannelById(video.ChannelId, true); err == nil {
            return ch
        }
    }
    return nil
}
```

**缺口**：`session_id` 字段未处理（见待解决问题 2）。

#### 2. `resolveChannelForTaskQuery` + `looksLikeTaskID`（GET URL 路径绑定）

位于 `controller/kling_video.go`，从 URL 末段提取 task_id，依次查 Video / Image 表。

#### 3. GET 请求渠道覆盖（已注入 `RelayKlingTransparent`）

---

## 待解决问题

### 问题 1：POST 请求渠道绑定缺失

`RelayKlingTransparent` 中只对 GET 请求做了渠道覆盖，**POST 请求未调用 `resolveChannelForBoundResource`**。

影响路由：`/videos/video-extend`、`/videos/multi-elements/init-selection`、`/videos/identify-face`（传 `video_id` 时）、`/videos/advanced-lip-sync`、`/audio/video-to-audio`、`/audio/tts`（传 `voice_id` 时）、`/images/generations`（传 `element_id` 时）。

**修复方案**（在 `RelayKlingTransparent` 中 GET 覆盖逻辑的同级处添加）：

```go
// POST 请求：从 body 中的资源 ID 路由到该资源所属渠道
if c.Request.Method == http.MethodPost {
    bodyBytes, err := io.ReadAll(c.Request.Body)
    if err == nil {
        c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // 必须重置，否则后续转发时 body 为空
        var params map[string]interface{}
        if json.Unmarshal(bodyBytes, &params) == nil {
            if boundChannel := resolveChannelForBoundResource(params); boundChannel != nil {
                if boundChannel.Id != channel.Id {
                    logger.Info(c, fmt.Sprintf("Transparent channel override (POST): path=%s, original=%d, bound=%d",
                        c.Request.URL.Path, channel.Id, boundChannel.Id))
                    channel = boundChannel
                    meta.ChannelId = boundChannel.Id
                }
            }
        }
    }
}
```

---

### 问题 2：`session_id` 绑定缺失（`/videos/advanced-lip-sync`）

**场景**：
```
Step 1: POST /videos/identify-face → Channel A
  → 返回 session_id = "850508686686064678"（= identify-face 的 task_id，存于 Video.task_id）
  → Video{task_id: "850508686686064678", channel_id: 1}

Step 2: POST /videos/advanced-lip-sync  body: {"session_id": "850508686686064678", ...}
  → middleware 随机选 Channel B
  → resolveChannelForBoundResource 未检查 session_id → 无法绑定
  → Kling API 使用 Channel B token → 404
```

**修复方案**：在 `resolveChannelForBoundResource` 中增加 `session_id` 的处理，将其与 `element_id` / `voice_id` 同等对待（查 `Video.task_id`）：

```go
func resolveChannelForBoundResource(params map[string]interface{}) *dbmodel.Channel {
    // 1. video_id → 查 Video.video_id 字段
    if videoID := extractResourceID(params, "video_id"); videoID != "" {
        if video, err := dbmodel.GetVideoTaskByVideoId(videoID); err == nil && video != nil && video.ChannelId != 0 {
            if ch, err := dbmodel.GetChannelById(video.ChannelId, true); err == nil {
                return ch
            }
        }
    }

    // 2. session_id / element_id / voice_id → 查 Video.task_id 字段
    taskID := extractResourceID(params, "session_id")  // ← 新增
    if taskID == "" {
        taskID = extractResourceID(params, "element_id")
    }
    if taskID == "" {
        taskID = extractResourceID(params, "voice_id")
    }
    if taskID == "" {
        return nil
    }
    if video, err := dbmodel.GetVideoTaskById(taskID); err == nil && video != nil && video.ChannelId != 0 {
        if ch, err := dbmodel.GetChannelById(video.ChannelId, true); err == nil {
            return ch
        }
    }
    return nil
}
```

---

## 数据流示例

### 场景 1：video-extend 依赖 video_id

```
POST /kling/v1/videos/text2video → Channel A (id=1)
→ Video{task_id: "task_001", video_id: "vid_abc", channel_id: 1}

POST /kling/v1/videos/video-extend  body: {"video_id": "vid_abc"}
→ resolveChannelForBoundResource: video_id="vid_abc"
  → GetVideoTaskByVideoId("vid_abc") → Video{channel_id: 1}
  → 覆盖 channel = Channel A → 成功
```

### 场景 2：advanced-lip-sync 依赖 session_id

```
POST /kling/v1/videos/identify-face  body: {"video_id": "vid_abc"} → Channel A (id=1)
→ Video{task_id: "session_001", channel_id: 1}
→ 返回 {"session_id": "session_001"}

POST /kling/v1/videos/advanced-lip-sync  body: {"session_id": "session_001", ...}
→ resolveChannelForBoundResource: session_id="session_001"
  → GetVideoTaskById("session_001") → Video{channel_id: 1}
  → 覆盖 channel = Channel A → 成功
```

### 场景 3：GET 查询任务

```
GET /kling/v1/videos/text2video/task_001
→ resolveChannelForTaskQuery: task_id="task_001"
  → GetVideoTaskById("task_001") → Video{channel_id: 1}
  → 覆盖 channel = Channel A → 成功
```

---

## To-do

- [x] 扩展 `resolveChannelForBoundResource` 增加 `video_id` 查找逻辑（查 `Video.video_id` 字段）
- [x] 新增 `resolveChannelForTaskQuery` 和 `looksLikeTaskID` 函数
- [x] 在 `RelayKlingTransparent` 注入 GET 查询的渠道覆盖逻辑
- [x] 验证 `GetVideoTaskByVideoId` 已有索引（`idx_video_id`）
- [x] **在 `resolveChannelForBoundResource` 增加 `session_id` 处理**（查 `Video.task_id`，与 `element_id`/`voice_id` 同等处理）
- [x] **在 `RelayKlingTransparent` 注入 POST 请求的渠道绑定逻辑**（读取 body → `resolveChannelForBoundResource` → 重置 body）

---

**创建时间**: 2026-03-02
**关联文件**: `controller/kling_video.go`
