# Grok Video 通用接口改进 — 变更说明

## 概述

本次修改完善了 xAI Grok 视频通用接口，新增视频延长（extensions）端点支持，优化了三种视频操作（生成/编辑/延长）的 duration 处理和计费逻辑，并在响应中增加了 `video_duration` 和 `usage` 字段。

---

## 一、新增文件

### `relay/channel/xai/mp4_duration.go`

远程 MP4 文件头部解析工具，用于获取用户传入视频的精确时长。

- **`GetRemoteMP4Duration(videoURL string) (float64, error)`** — 入口函数
  - 通过 HTTP Range 请求只下载 MP4 头部（32KB）或尾部（256KB），不下载整个文件
  - 解析 moov > mvhd atom 提取 timescale 和 duration
  - 支持 mvhd v0（32-bit）和 v1（64-bit）两种格式
  - 使用 `io.LimitReader` 防止服务器忽略 Range 时 OOM
  - 失败返回 `(0, error)`，不阻断主流程

---

## 二、修改文件

### 1. `relay/channel/xai/model.go`

| 改动 | 说明 |
|------|------|
| 新增 `GrokVideoUsage` 结构体 | `CostInUsdTicks int64` — 对应 xAI API 返回的费用字段 |
| `GrokVideoResult` 增加 `Usage` 字段 | `*GrokVideoUsage` — 用于解析查询响应中的费用 |
| `GrokVideoResult` 增加 `Progress` 字段 | `int` — 对应 xAI API 返回的进度 0-100 |

### 2. `relay/model/general.go`

| 改动 | 说明 |
|------|------|
| `GeneralVideoResponse` 增加 `VideoDuration` | `float64` — 提交任务时返回输入视频的时长（秒），仅编辑/延长时有值 |
| 新增 `VideoUsage` 结构体 | `CostInUsd float64` — 转换后的美元费用 |
| `GeneralFinalVideoResponse` 增加 `Usage` | `*VideoUsage` — 查询结果时返回实际费用 |

### 3. `relay/channel/interface.go`

| 改动 | 说明 |
|------|------|
| `VideoTaskResult` 增加 `VideoDuration` | `float64` — 适配器内部传递解析出的输入视频时长 |

### 4. `relay/controller/video.go`

| 改动 | 说明 |
|------|------|
| `invokeVideoAdaptorRequest` 响应体增加 `VideoDuration` | 将 `taskResult.VideoDuration` 传入 `GeneralVideoResponse` 返回给客户端 |

### 5. `relay/channel/xai/video_adaptor.go`（核心改动）

#### 5a. 支持视频延长

- `GetSupportedModels()` 新增 `"grok-imagine-video-extensions"`
- model 名以 `-extensions` 结尾时路由到 `/v1/videos/extensions`
- 发送到 xAI 前将 model 替换为 `"grok-imagine-video"`（xAI API 不认识带后缀的名称）
- extensions 的 `duration` 默认 6，范围 1-10，必须提供 `video.url`

#### 5b. 三端点 duration 差异化处理

| 端点 | duration 处理 |
|------|--------------|
| **generations** | 默认 8，范围 1-15，原样发送 |
| **edits** | 无此字段，从请求体中删除后发送 |
| **extensions** | 默认 6，范围 1-10，原样发送 |

#### 5c. 计费逻辑重构

删除了旧的 `computeGrokQuota()` 函数，内联到 `HandleVideoRequest` 中，按端点类型分别计算：

| 端点 | 计费公式 |
|------|---------|
| **generations** | `duration × outputPrice + (image ? $0.002 : 0)` |
| **edits** | `videoDuration × (outputPrice + $0.01)`，解析失败用 $0.20 兜底 |
| **extensions** | `duration × outputPrice + videoDuration × $0.01` |

其中 outputPrice: 480p = $0.05/s, 720p = $0.07/s

#### 5d. 输入视频时长解析

当请求包含 `video.url`（编辑或延长）时，调用 `GetRemoteMP4Duration()` 解析输入视频时长：
- 解析成功：用于计费和响应 `video_duration` 字段
- 解析失败：edits 用 $0.20 预扣费兜底，extensions 仅按输出时长计费，`video_duration` 不返回

#### 5e. 查询响应增加 usage

视频完成时解析 xAI 返回的 `usage.cost_in_usd_ticks`，转换为美元：

```
cost_in_usd = cost_in_usd_ticks / 10,000,000,000
```

写入 `GeneralFinalVideoResponse.Usage.CostInUsd` 返回给客户端。

---

## 三、Bug 修复（代码审查）

| # | 文件 | 问题 | 修复 |
|---|------|------|------|
| 1 | `mp4_duration.go:149` | mvhd v1 长度检查 `< 28` 错误，28-31 字节时 `data[24:32]` 越界 panic | 改为 `< 32` |
| 2 | `video_adaptor.go:110` | extensions 把 `grok-imagine-video-extensions` 原样发给 xAI 导致 400 | 请求体中 model 替换为 `grok-imagine-video` |
| 3 | `mp4_duration.go:66` | `io.ReadAll` 无限制，Range 不被支持时整个视频读入内存 | 改用 `io.LimitReader` |
