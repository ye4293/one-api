# flux-3-video get_result 增加 DB 优先返回

## 背景与目标

`GET /flux/v1/get_result?id=<uuid>` 命中视频任务时，当前路径为：

```
GetFlux (controller/flux.go:201)
  → relaycontroller.GetVideoResult (video.go:1054)
    → invokeVideoAdaptorResult (video.go:829)
      → flux.VideoAdaptor.HandleVideoResult (video_adaptor.go:182)
        → SendVideoResultQuery(queryURL)   ← 每次都直连 BFL/Replicate
```

**问题**：
1. **无 DB 优先**：任务已 `succeed` 且 `store_url`/`result` 已落库，客户端每次轮询仍回源官方，浪费上游配额、增加延迟、可能触发限流。
2. **已成功任务可能被误退款**（最危险）：BFL 结果 URL 有有效期，过期后 `get_result` 返回 HTTP 404 → `HandleVideoResult` 判 `failed`（video_adaptor.go:216）→ `UpdateVideoTaskStatus` 中 `oldStatus="succeed" != "failed"` 且新状态为 `failed` → `needRefund=true` → 对已计费的成功任务退款。
3. 图片路径本就是 DB 优先（flux.go:214-228），视频路径行为不一致；`from_source` 参数对视频完全无效。

**目标**：视频任务已 `succeed` 且原始结果已落库时，直接用 DB 数据组装 BFL 原生 7 字段响应返回，不回源。

## 方案设计

### 放置位置：客户端专属的 `invokeVideoAdaptorResult`

`HandleVideoResult` 被客户端查询与服务端对账器（flux_video_reconciler.go:138）共用。对账器**只处理 `processing` 任务**（reconciler.go:92），因此：
- **不改** `HandleVideoResult`（保持对账器回源语义不变）。
- 短路加在 `invokeVideoAdaptorResult` 顶部，仅客户端路径生效。

### 改动点

**1. `relay/channel/flux/adaptor.go`**：导出判定函数
```go
func IsReplicate(baseURL string) bool { return isReplicate(baseURL) }
```

**2. `relay/channel/flux/video_adaptor.go`**：新增导出函数，从 DB 组装终态响应（succeed + failed，复用现有映射）：
```go
// BuildTerminalResultFromDB 在任务已终态（succeed/failed）时，用库里数据组装
// GeneralFinalVideoResponse（不回源）。TaskStatus 以 DB status 为权威，
// FluxStatus/result/details 尽力从 videoTask.Result 原始 JSON 提取，缺失则兜底。
// 返回 (gr, true) 命中；(nil, false) 表示非终态，需回源。
func BuildTerminalResultFromDB(videoTask *dbmodel.Video, baseURL string) (*model.GeneralFinalVideoResponse, bool)
```
内部逻辑：
- `Status` 非 `succeed`/`failed` → `false`（processing 回源）
- `gr.TaskStatus = videoTask.Status`（**以 DB 为权威**，不由 raw 反推——避免 404/error body 被误判为 processing）
- 若 `Result != ""`，按 `IsReplicate(baseURL)` 尽力提取展示字段：
  - BFL：`fillFluxNativeFields`（result/details/preview/progress）+ 取顶层 `status` → `FluxStatus`、`result.sample` → `VideoResult`
  - Replicate：解析 `ReplicateResponse`，`replicateStatusToBFL(status)` → `FluxStatus`，`output` → 构造 `{"sample": output}`，`error` → details
- 兜底：
  - `FluxStatus == ""` → succeed 填 `UpstreamStatusReady`，failed 填 `UpstreamStatusError`
  - succeed 且 `VideoResult == ""` 但 `StoreUrl != ""` → 用 `StoreUrl` 构造 `{"sample": store_url}`（覆盖 result 列为空的存量成功任务）
  - failed 且无 details 但 `FailReason != ""` → 构造 `{"detail": fail_reason}`
- 不设置 `UpstreamCost`（DB 优先不做结算）

**3. `relay/controller/video.go`**：`invokeVideoAdaptorResult` 顶部（`adaptor.Init` 后、`HandleVideoResult` 前）：
```go
if videoTask.Provider == "flux" {
    baseURL := "https://api.bfl.ai"
    if channel.BaseURL != nil && *channel.BaseURL != "" { baseURL = *channel.BaseURL }
    if gr, ok := flux.BuildTerminalResultFromDB(videoTask, baseURL); ok {
        if videoTask.VideoDuration > 0 { gr.VideoDuration = videoTask.VideoDuration }
        cost := 0.0
        if gr.TaskStatus == "succeed" { cost = videoCostCents(videoTask.Quota) } // 已结算 quota → 权威费用
        c.JSON(http.StatusOK, buildFluxVideoResult(gr, cost))
        return nil
    }
}
```

**4. `relay/controller/video.go` — 误退款根因修复**：`UpdateVideoTaskStatus`（1683 行）退款门控由
```go
needRefund := (oldStatus != "failed" && status == "failed")
```
改为
```go
needRefund := (oldStatus == "processing" && status == "failed")
```
只允许 `processing → failed` 退款，与对账器 `Where status=processing` 的 CAS 语义对齐。杜绝“已 succeed 任务被 404 翻成 failed 时退款”。`CreateVideoLog` 恒以 `processing` 建任务，故不存在 `"" → failed` 的正常退款需求；同步修正该函数上方关于空字符串的过时注释。

### 为什么 cost 用 `videoCostCents(videoTask.Quota)`
succeed 任务的 `quota` 列已是结算后的最终值（对账器/客户端 CAS 已写入 `VideoQuotaFromUpstreamCost`）。用它换算与 endpoint 既定口径（“最终实际计费 quota ÷ QuotaPerUnit × 100”）一致，对 BFL / Replicate 两条路径统一。不信任 raw 里的 cost 字段（Replicate raw 无顶层 cost）。failed 退款后 cost=0。

## 影响范围

- **影响** flux-3-video 客户端查询的 **succeed + failed** 分支：终态直接由 DB 组装返回，不回源。
- `processing`：不命中短路，仍回源（正确，需推进状态）。
- 对账器（`reconcileSingleFluxVideo`）：只喂 `processing` 任务且直接调 `HandleVideoResult`（未改），行为不变。
- 其他 provider（zhipu/runway/minimax/...）：`invokeVideoAdaptorResult` 短路仅对 `provider=="flux"` 生效，其余不受影响。
- **`UpdateVideoTaskStatus` 退款门控变更为全局共享逻辑**：所有 provider 的 `succeed→failed` / `""→failed` 不再退款。正常失败链路是 `processing→failed`，退款不受影响。此为期望内的正确性收紧。
- 无数据库 schema 变更。

## 验证方式

1. `go build ./... && go vet ./...`
2. 构造/选取一条 `provider=flux, status=succeed, result=<BFL raw>` 记录，请求 `get_result`：
   - 断言响应为 BFL 7 字段结构，`result.sample` 与库中一致，`cost` == `videoCostCents(quota)`；
   - 抓包/日志确认**无对上游的 HTTP 请求**。
3. 同样验证一条 `result=<Replicate raw>` 记录：响应 `status=="Ready"`、`result=={"sample": <output url>}`。
4. `processing` 记录：确认仍回源、状态能推进。
5. 回归误退款场景：succeed 任务反复查询，确认不再产生 404→failed→退款。
