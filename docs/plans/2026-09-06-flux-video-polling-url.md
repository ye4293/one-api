# flux-3-video 轮询用上游 polling_url，修复 "Task not found" 误判

## 背景与目标

BFL FLUX 3 Video 为异步任务：提交后返回 `{id, polling_url}`。BFL 官方文档明确要求查询结果**必须**使用响应返回的 `polling_url`——全局/区域端点做多集群路由，任务被分配到某个具体集群，`polling_url` 编码了正确的集群地址。

当前实现（`relay/channel/flux/video_adaptor.go`）丢弃 `polling_url`，改用 `id` 自拼 `baseURL + /v1/get_result?id=`（`video_model.go:30` 注释自承认），轮询打到不持有该任务的集群 → 上游返回 HTTP 404 `Task not found`。

直接后果：
- BFL 原生 flux-3-video 结构性几乎全挂（仅任务碰巧落全局集群时才通）。
- 上一提交 `39d7e0f`（404→判失败→退款）掩盖了根因，且把**上游已正常生成的视频误判为 failed 扔掉并退款**，用户拿不到结果、上游成本白烧。

目标：轮询时优先使用上游返回的 `polling_url`；保留 404→退款作为兜底。不影响 Replicate 分支。

## 方案设计

**持久化载体**：复用 `videos.credentials` 字段（未加索引 text；flux 不用 VertexAI/xai 凭证，无冲突；adaptor 结果流已接好 `UpdateVideoCredentials` 保存路径）。

改动文件（仅 flux 包）：

1. `relay/channel/flux/video_adaptor.go`
   - `submitBFLVideo` 返回值增加 `pollingURL`，捕获 `submitResp.PollingURL`。
   - `submitReplicateVideo` 对齐签名，`pollingURL` 返回空串（Replicate 用 `predictions/{id}`，渠道自有 baseURL，无多集群问题）。
   - `HandleVideoRequest`：把 BFL 的 `pollingURL` 放进 `VideoTaskResult.Credentials`（video.go:777 已有 `Credentials != "" → UpdateVideoCredentials` 保存逻辑，无需改动）。
   - `HandleVideoResult` 的 BFL 分支：若 `videoTask.Credentials` 以 `http` 开头，直接用其作 queryURL；否则回退到旧的自拼 URL（兼容存量任务 / Replicate）。

2. `relay/channel/flux/video_model.go`
   - 更新 `FluxVideoSubmitResponse.PollingURL` 注释（不再"仅留存"）。

**不改动**：`interface.go`（`VideoTaskResult.Credentials` 已存在）、`relay/controller/video.go`（保存路径已有）、DB schema（无迁移）。

## 影响范围

- 仅 flux-3-video BFL 原生分支行为变化：轮询命中正确集群，正常返回 Pending/Processing/Ready。
- Replicate 分支不受影响（Credentials 为空 → 走原逻辑）。
- 存量卡死任务：Credentials 为空 → 回退自拼 URL → 仍可能 404 → 现有退款兜底照常生效，行为不回退。
- 404→退款逻辑保留，作为真·失败（任务过期/无效 id）的兜底。
- 图片侧同类自拼 URL（adaptor.go:369、859）本次**不处理**，仅标记为后续隐患。

## 验证方式

1. `go build ./... && go vet ./...` 通过。
2. 逻辑走查：BFL 提交后 Credentials 落库为 polling_url；轮询读取并命中；Replicate 分支 Credentials 空、走回退。
3. 真实/构造场景：新提交一个 BFL flux-3-video 任务，确认轮询不再立即 404，能走到 Ready 并返回 sample URL。
4. 存量任务（Credentials 空）轮询仍走回退，404 时退款照常。
