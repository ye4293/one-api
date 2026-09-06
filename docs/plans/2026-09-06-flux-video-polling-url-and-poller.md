# flux-3-video 返回 polling_url + 服务端兜底轮询器

## 背景与目标

两项需求：

1. **创建响应增加 `polling_url`**：`POST /v1/flux-3-video`（及 `/v1/video/generations`）创建成功后，响应体增加 `polling_url`，供客户端轮询结果。
2. **服务端兜底轮询**：现状 flux video 状态推进 100% 依赖客户端主动查询（`GetVideoResult`），`flux_reconciler.go` 只扫 images 表。若客户端提交后不再查询，任务永远卡 `processing`、不结算、不退款。需参考现有 video poller（`gemini_video_poller.go` / `xai_video_poller.go` / `ali_video.go`）为 flux video 增加服务端定时轮询，在用户不请求时也能把任务走完（succeed / failed + 退款）。

## 关键区分（两个 polling_url，用途不同，不可混用）

- **服务端轮询用**：BFL 上游返回的原始 `polling_url`（含正确集群地址），已在上一改动落库到 `videos.credentials`。one-api 服务端轮询必须用它才能命中集群。**不能返给客户端**（客户端无 BFL key）。
- **返给客户端**：one-api 自己的代理端点 `{config.ServerAddress}/flux/v1/get_result?id={taskId}`（客户端持 one-api token 查，命中 `controller.GetFlux` → 转 `GetVideoResult`）。与图片 Replicate 分支 `adaptor.go:584` 完全一致。

## 方案设计

### 需求 1：响应增加 polling_url

1. `relay/model/general.go` — `GeneralVideoResponse` 增加 `PollingUrl string \`json:"polling_url,omitempty"\``（omitempty，不影响其他 provider）。
2. `relay/channel/interface.go` — `VideoTaskResult` 增加 `PollingUrl string`（客户端代理端点，与承载 BFL URL 的 `Credentials` 分开，语义清晰）。
3. `relay/channel/flux/video_adaptor.go` — `HandleVideoRequest` 用 `config.ServerAddress` 拼 `{ServerAddress}/flux/v1/get_result?id={taskId}` 填入 `VideoTaskResult.PollingUrl`（BFL / Replicate 分支都填，客户端查询端点统一）。
4. `relay/controller/video.go` — 提交响应（当前 784 行 `c.JSON`）把 `taskResult.PollingUrl` 带进 `GeneralVideoResponse.PollingUrl`。

### 需求 2：flux video 服务端兜底对账器（仿 StartFluxReconciler）

新建 `controller/flux_video_reconciler.go`，**对齐图片侧 `flux_reconciler.go` 的结构**（用户明确指定参考 `StartFluxReconciler`）：

- 开关：复用 `isFluxReconcilerEnabled()`（`ENABLE_VIDEO_TASK_POLLER`，同包已定义，与图片对账/其他 poller 共用）。
- 间隔：`fluxVideoReconcileInterval = 30 * time.Second`（与图片对账一致，利于在 BFL 结果 URL 失效前抓到 sample）。
- 单实例内并发保护：`fluxVideoReconcilerMu sync.Mutex` `TryLock`（上轮未完成跳过本轮）。
- 上游查询并发上限：`fluxVideoQuerySem = make(chan struct{}, 50)` 信号量。
- **超时：`fluxVideoExpireSecs = 4 * 60 * 60`（4 小时）**，超时任务判 `failed` **并退款**。
- 两段式 `runFluxVideoReconcile`：
  1. **① 超时段**：查 `provider="flux" AND status="processing" AND created_at < now-4h`，逐个 `failFluxVideoTask`（不查上游，直接判失败退款）。
  2. **② 对账段**：查 `created_at >= now-4h` 的 processing 任务，每个起 goroutine（占信号量）→ `reconcileSingleFluxVideo`。
- 单任务 `reconcileSingleFluxVideo`：`GetChannelById` → `LoadConfig` → `adaptor := &flux.VideoAdaptor{}; adaptor.Init(nil)` → `adaptor.HandleVideoResult(nil, task, channel, &cfg)`（flux 的 `HandleVideoResult` 全程不解引用 `c`，传 nil 安全；内部按 baseURL 区分 BFL/Replicate，BFL 用落库 polling_url 命中集群）；`recover()` 防单任务 panic。
  - `succeed` → `succeedFluxVideoTask`；`failed` → `failFluxVideoTask`；`processing` → 跳过。
- `main.go` 注册：紧邻 `StartFluxReconciler`，`common.SafeGoroutine(func(){ controller.StartFluxVideoReconciler(context.Background()) })`。

### 结算逻辑（CAS + RowsAffected 门控退款）

`controller/flux_video_reconciler.go` 位于顶层 `controller` 包，无法直接调用 `relay/controller` 的 `UpdateVideoTaskStatus`/`CompensateVideoTask`（跨包）。仿 `gemini_video_poller.go` 内联直接 DB 操作，但**改进其重复退款隐患**：

- `failFluxVideoTask` / `succeedFluxVideoTask` 均用 `Where("task_id = ? AND status = ?", taskId, "processing").Updates(...)` 做 **CAS**（仅 processing→终态生效）。
- 失败退款以 `res.RowsAffected > 0` 门控：只有赢得终态转换的那一次调 `CompensateVideoTaskQuota` + `CompensateChannelQuota`，杜绝多实例/超时与对账双路径重复退款（gemini poller 失败段无此门控，是潜在隐患，本对账器规避）。

## 影响范围

- `GeneralVideoResponse` 加 omitempty 字段：向后兼容，其他 provider 响应不变。
- 新 poller 默认由 `ENABLE_VIDEO_TASK_POLLER` 控制，未开启时零影响。
- 并发正确性：多实例同时轮询时，靠 `UpdateVideoTaskStatus` 的 CAS（`status NOT IN (succeed,failed)`）+ `needRefund` 幂等保证**不重复退款**；重复轮询仅浪费上游请求。本期先不加 Redis 分布式锁（与 gemini poller 一致，保持简单）；如后续上游查询频控紧张再补锁（参考 xai poller）。
- 无 schema 变更。
- 存量卡死任务：poller 上线后会被扫到，用 `credentials`（若有 BFL polling_url）命中正确集群推进；无 polling_url 的存量任务回退自拼 URL，404 则超时/即时判 failed 退款。

## 验证方式

1. `go build ./... && go vet ./...` 通过。
2. 响应走查：创建 flux-3-video 返回体含 `polling_url` = `{ServerAddress}/flux/v1/get_result?id=<taskId>`。
3. poller 走查：`ENABLE_VIDEO_TASK_POLLER=true` 下，提交任务后不主动查询，30s~1min 内对账器扫到并推进到终态；成功写 store_url，失败退款；超 4 小时未终态直接判失败并退款。
4. 幂等：对已 succeed/failed 任务重复轮询不重复退款、不改状态。
5. 传 nil gin.Context 调 `HandleVideoResult` 不 panic。
