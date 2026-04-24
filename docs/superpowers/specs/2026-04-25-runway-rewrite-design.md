# Runway 渠道重写设计

日期：2026-04-25
状态：待实施

## 目标

对 ezlinkai 现有 Runway 集成进行完整重写，达成：

1. 接入模式统一为 `/runway/v1/<官方路径>` 纯透传代理
2. 请求体、响应体 100% 透传给客户，**唯一改动是响应中的 `id` 字段添加 `video-`/`image-` 前缀**（用于下游任务查询路由）
3. 补齐官方核心接口 `POST /v1/text_to_video`
4. 消除现有实现的脆弱点（mode 由 URL 驱动而非 body 猜测）、魔数、死代码、图像计费日志 10× bug
5. 对外契约（URL、前缀、响应字段）完全向后兼容

## 背景

### 官方核心接口（Runway docs.dev.runwayml.com）

| 官方路径 | 方法 | 功能 |
|---|---|---|
| `/v1/text_to_video` | POST | 文生视频（**现未接入**） |
| `/v1/image_to_video` | POST | 图生视频 |
| `/v1/video_to_video` | POST | 视频转视频 |
| `/v1/text_to_image` | POST | 文生图 |
| `/v1/character_performance` | POST | 角色表演动画 |
| `/v1/video_upscale` | POST | 视频高清化 |
| `/v1/tasks/{id}` | GET | 任务状态查询 |

### 现状问题

1. `determineVideoMode`（`relay/controller/directvideo.go:210`）靠 body 字符串匹配判定 mode，`text_to_video` 会被误判为 `upscalevideo`
2. `text_to_video` 路由缺失
3. `DirectRelayRunway`（`relay/controller/directvideo.go:42-202`）单函数 160 行，混合了透传、解析、改写、分支计费、日志
4. `relay/channel/runway/video_adaptor.go`（186 行）实现了老 VideoAdaptor 接口，但 router 仅调用 `DirectRelayRunway`——**死代码**
5. `constant.go:ModelDetails` 仅声明 `gen3a_turbo`，但计费代码已支持 `gen4_turbo/gen4_aleph/gen4_image/act_two/upscale_v1`
6. `video-`/`image-` 前缀字符串操作散落于创建、查询、退款 3 处
7. 计费日志 **bug**：图像日志用 `quota/5000000` 计算展示金额，视频用 `quota/500000`，差 10 倍（实际扣费金额正确，仅日志展示错误）
8. `X-Runway-Version: 2024-11-06` 在多处硬编码
9. 图像退款用 `log.content LIKE '%provider%'` + 时间窗口模糊匹配反查 quota，脆弱
10. `log.Printf`/`fmt.Printf` 与 `logger` 混用

## 非目标

- **不**抽取跨渠道（xAI/Sora）的通用重试框架
- **不**修改 Video/Image 数据库表的既有列（只允许 `ALTER TABLE ADD COLUMN`）
- **不**接入 Runway 音频、Avatar、Knowledge、Realtime、Workflow、Organization 类接口
- **不**改动任何客户可见的 URL、前缀、响应字段名

## 架构

### 新增 package：`relay/channel/runway/`

```
relay/channel/runway/
├── constant.go    模型清单、API 版本、endpoint → Mode 映射、计费常量
├── taskid.go      EncodeTaskID / DecodeTaskID / KindOf
├── mode.go        ModeFromPath(path) → Mode 枚举；Mode 自带 Kind、LogName
├── billing.go     ComputeQuota(mode, body) / BillImage / BillVideo
├── refund.go      RefundImage(taskID) / RefundVideo(taskID)
├── status.go      MapStatus(runwayStatus) → dbStatus
├── proxy.go       Proxy(c, meta, body) → upstreamResult：纯 HTTP 透传
├── result.go      FetchTaskResult(c, taskID)：查询 + DB 同步
└── handler.go     Handler(c, meta) / HandleResult(c, taskID)：组合入口
```

### `controller/relay.go` 瘦身

保留 `RelayRunway` / `RelayRunwayResult` 作为路由入口，仅负责**重试外壳**；内部委托给 `runway.Handler` / `runway.HandleResult`。

删除：
- `DirectRelayRunway`（从 `relay/controller/directvideo.go` 移除）
- `GetRunwayResult`（从 `relay/controller/directvideo.go` 移除）
- `determineVideoMode` / `extractDurationFromRequest` / `calculateRunwayQuota` / `calculateImageCredits` / `getDurationSeconds`
- `updateTaskStatus` / `mapRunwayStatusToDbStatus`
- `compensateRunwayImageTask` / `compensateRunwayVideoTask` / `compensateWithQuota` / `calculateDefaultImageQuota`
- `handleRunwayImageBilling` / `handleRunwayVideoBilling`

（`compensateWithQuota` 若仍被 Sora / 其它调用，则保留在共用位置，仅移除 Runway 专用包装）

### `router/relay-router.go` 新增

```go
runwayRouter.POST("/text_to_video", controller.RelayRunway)
```

### 删除

- `relay/channel/runway/video_adaptor.go`（死代码）
- `relay/channel/runway/model.go`（老结构体，新包不再使用）
- `relay/channel/runway/constant.go` 旧内容由新 `constant.go` 完全替换

## 关键模块设计

### taskid.go

```go
type Kind string
const (
    KindVideo Kind = "video"
    KindImage Kind = "image"
)

// EncodeTaskID("image", "abc123") → "image-abc123"
func EncodeTaskID(k Kind, rawID string) string

// DecodeTaskID("video-abc") → (KindVideo, "abc", true)
// DecodeTaskID("abc")       → (KindVideo, "abc", false)  // 无前缀，默认视频，向后兼容
func DecodeTaskID(taskID string) (Kind, string, bool)
```

**所有前缀操作仅在此文件发生**。

### mode.go / constant.go

```go
const (
    APIVersion      = "2024-11-06"
    HeaderVersion   = "X-Runway-Version"
    RoutePrefix     = "/runway"  // 透传时剥离
)

type Mode struct {
    Name string // "imagetovideo" | "texttovideo" | ...（用于日志 / mode 字段）
    Kind Kind   // image | video，决定分表与计费分支
}

var pathToMode = map[string]Mode{
    "/v1/text_to_video":         {Name: "texttovideo",          Kind: KindVideo},
    "/v1/image_to_video":        {Name: "imagetovideo",         Kind: KindVideo},
    "/v1/video_to_video":        {Name: "videotovideo",         Kind: KindVideo},
    "/v1/text_to_image":         {Name: "texttoimage",          Kind: KindImage},
    "/v1/character_performance": {Name: "characterperformance", Kind: KindVideo},
    "/v1/video_upscale":         {Name: "upscalevideo",         Kind: KindVideo},
}

// ModeFromPath 根据请求路径（已剥离 /runway 前缀）返回 Mode
func ModeFromPath(urlPath string) (Mode, bool)
```

费率**完全由请求 body 里的 `model` 字段决定**（与路径无关），在 `billing.go` 查表：
`gen4_turbo`/`gen3a_turbo`/`act_two` = 5 c/s；`gen4_aleph` = 15 c/s；`upscale_v1` = 2 c/s；`gen4_image` 按 ratio 5/8 credits。Mode 只负责 Kind 分流，不承载费率。

### billing.go

集中处理计费金额计算，消除原代码的 `500000 / 100` 和 `0.05 * duration * 500000` 魔数：

```go
// $1 = QuotaPerUnit quota。Runway credits 官方定价：1 credit = $0.01
const creditToUSD = 0.01

// ComputeVideoQuota: credits/s × duration × $/credit × quota/$
func ComputeVideoQuota(model string, durationSec float64) int64 {
    creditsPerSec := videoCreditRate(model) // 5 / 15 / 2
    usd := creditsPerSec * durationSec * creditToUSD
    return int64(usd * config.QuotaPerUnit)
}

// ComputeImageQuota: 按 ratio 区分 5/8 credits
func ComputeImageQuota(ratio string) int64

// BillImage / BillVideo：扣费 + 日志 + 更新缓存
```

**修正图像日志 bug**：`logContent` 的 `$%.6f` 参数统一除以 `config.QuotaPerUnit`，不再写死 `5000000` / `500000`。

### proxy.go

**唯一职责**：透传 HTTP，不解析 body、不改写响应体。

```go
type UpstreamResult struct {
    Status int
    Header http.Header
    Body   []byte
}

// Proxy 发起对 Runway 的 HTTP 请求
// - URL = meta.BaseURL + strings.TrimPrefix(c.Request.URL.Path, RoutePrefix)
// - 注入 Authorization: Bearer <channel.Key>
// - 注入 X-Runway-Version
// - body 原样透传
func Proxy(c *gin.Context, meta *util.RelayMeta, body []byte) (*UpstreamResult, error)
```

### handler.go

组合层：

```go
func Handler(c *gin.Context, meta *util.RelayMeta) {
    body, _ := io.ReadAll(c.Request.Body)

    mode, ok := ModeFromPath(trimRoutePrefix(c.Request.URL.Path))
    if !ok { writeError(c, 404, "unknown runway endpoint"); return }

    upstream, err := Proxy(c, meta, body)
    if err != nil { writeError(c, 500, err.Error()); return }

    if upstream.Status == 200 {
        rawID := extractID(upstream.Body)               // 仅读
        taskID := EncodeTaskID(mode.Kind, rawID)
        quota := ComputeQuota(mode, body)

        if mode.Kind == KindImage {
            BillImage(c, meta, mode, taskID, quota)
            CreateImageLog(...)
        } else {
            BillVideo(c, meta, mode, taskID, quota, body)
            CreateVideoLog(...)
        }

        upstream.Body = rewriteResponseID(upstream.Body, taskID) // 唯一改动
    }

    writeUpstream(c, upstream)
}
```

`HandleResult` 类似：提取 kind/rawID → 向 Runway 查询 → 更新 DB → 仅改 id 前缀后写回。

## 数据流

### 创建（`POST /runway/v1/image_to_video`）

```
[Client]
   │ POST /runway/v1/image_to_video  {body}
   ▼
[RelayRunway 外壳]
   │  - 重试控制（保留现有逻辑；Recorder 下沉到 handler）
   ▼
[runway.Handler]
   │  - body 原样透传
   │  - 从 URL 判 mode
   │  - 上游返回后：EncodeTaskID、扣费、写日志
   │  - 仅改写响应 id
   ▼
[Client]  收到：上游响应体（id 加前缀）
```

### 查询（`GET /runway/v1/tasks/video-abc`）

```
[Client]
   │ GET /runway/v1/tasks/video-abc
   ▼
[RelayRunwayResult]
   │
   ▼
[runway.HandleResult]
   │  - DecodeTaskID → (KindVideo, "abc")
   │  - 查 DB 找渠道
   │  - GET <base>/v1/tasks/abc
   │  - SyncDB: 更新 status / failReason / storeUrl；
   │             若状态转为 failed/cancelled 且此前未失败 → 退款
   │  - 仅改写响应 id（加回前缀）
   ▼
[Client]  收到：上游响应体（id 加前缀）
```

## 错误与失败路径

| 情况 | 行为 |
|---|---|
| Runway 返回非 200 | 原样透传（body + status），不扣费、不记计费日志、不创建任务记录 |
| 网络错误 | 500 JSON `{error: "<msg>"}`；重试由外壳处理 |
| 上游返回 200 但无 `id` 字段 | 记 error 级日志；原样透传响应（不加前缀）；不扣费 |
| 查询时 DB 无此任务 | 404 `{error: "task not found"}` |
| 查询时状态变 FAILED | DB 更新 + 退款（幂等：仅 `oldStatus ∉ {failed,cancelled}` 时退） |

## 兼容性

| 维度 | 保证 |
|---|---|
| 对外 URL | `/runway/v1/*` 全部保留；新增 `text_to_video` |
| 请求体 | 纯透传，无字段校验/补全 |
| 响应体 | 纯透传，**仅 `id` 字段添加前缀** |
| 任务 ID 前缀 | 继续 `video-` / `image-` |
| 查询旧任务 | `DecodeTaskID` 容忍无前缀输入（默认视频），向后兼容 |
| DB schema | 只允许 `ALTER TABLE ADD COLUMN`，不改/删旧列 |
| 重试行为 | 对客户端透明不变 |

## DB 变更（可选）

现有图像退款依赖 `log.content LIKE '%provider%' + 时间窗口` 反查 quota，脆弱。

**建议**：在 `images` 表添加 `quota` 列（`BIGINT`，默认 0），创建任务时直接写入；退款时直接读。

若不允许改表，退款降级为"不退图像失败的费用"或保留现有模糊查询逻辑。此决策留到实施阶段，由用户确认。

## 测试策略

1. **单元测试**（新增）：
   - `taskid_test.go`：Encode/Decode 双向 + 无前缀兼容
   - `mode_test.go`：所有 endpoint → Mode 映射
   - `billing_test.go`：各模型 × 各 duration 的 quota 计算，覆盖 $/credit 链路
   - `status_test.go`：Runway status → dbStatus 映射

2. **集成测试**（手动）：
   - text_to_video 首次创建 + 查询走通
   - image_to_video 现有流程无回归
   - 任务失败场景退款幂等

3. **回归冒烟**：
   - 调用所有 6 个创建端点 + 1 个查询端点，确认响应 body 与上游一致（仅 id 前缀差异）

## 实施顺序（粗）

1. 新建 `relay/channel/runway/` 包的 9 个文件（constant / taskid / mode / status 先行，纯函数易测）
2. 实现 `billing.go` + 单元测试
3. 实现 `proxy.go` + `handler.go` / `result.go`
4. 在 `controller/relay.go` 中把 `RelayRunway` 的内部调用切换到新 handler
5. 删 `relay/controller/directvideo.go` 中的 Runway 相关函数
6. 删 `relay/channel/runway/video_adaptor.go` / 旧 `model.go`
7. `router/relay-router.go` 新增 `text_to_video`
8. `go build ./... && go vet ./...`
9. 手动跑 6 条创建 + 1 条查询的冒烟路径

## 风险

| 风险 | 缓解 |
|---|---|
| `compensateWithQuota` 被 Sora 等共用 | 实施前 grep 确认；若共用则保留在 `relay/controller` 共用位置 |
| `Image` 表无 `quota` 字段 | 实施时先确认；若无则本次不改退款路径，仅消除 fuzzy 查询的 provider 依赖 |
| 重试逻辑与新 handler 集成复杂 | `tryRunwayRequest` 的 `responseRecorder` 模式保留不变，只是目标函数换成 `runway.Handler` |
| 现有客户端硬编码了 `video-`/`image-` 前缀解析 | 前缀保留，规避 |

## 开放问题

无（决策已在 brainstorming 锁定）。
