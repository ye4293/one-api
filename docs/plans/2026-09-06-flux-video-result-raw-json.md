# flux-3-video 持久化上游完整原始 JSON 到 result 列

## Context（背景与目标）

现状:flux-3-video 成功时只把视频 URL 存进 `videos.store_url`,**上游 get_result 的完整原始响应被丢弃**——`FluxVideoPollingResponse` 只解析 `id/status/result.sample/detail`,`videos.result` 列(定义为"保存完整上游 JSON")始终为空。

问题:
1. **无审计留痕**——上游 BFL get_result 响应实际带 `cost`(美分,已由图片侧 `FluxPollingResponse.Cost` 证实同端点返回),但视频侧既不解析也不留存,事后无法核对上游真实费用、无法排障还原上游返回。
2. 与 gemini/kling 口径不一致——它们都把上游原文写入 `result` 列。

目标:flux-3-video(BFL + Replicate 两条上游)在拿到终态响应时,把**上游完整原始 body** 存入 `videos.result` 列,与客户端查询、服务端对账器两条完成路径都对齐。

## 方案设计

**复用现有基础设施,零新增**:
- 复用 `GeneralFinalVideoResponse.RawResult string`(`relay/model/general.go:148`)——gemini 已用此字段承载上游原文。
- 复用 `dbmodel.UpdateVideoResult(taskId, result)`(`model/video.go:314`)——已存在,写 `result` 列。

### 改动 1:`relay/channel/flux/video_adaptor.go` —— 在适配器捕获原文

`HandleVideoResult`(BFL 分支)与 `handleReplicateVideoResult`(Replicate 分支)中,凡是要返回 `generalResponse` 且手里有 `body` 的位置,设 `generalResponse.RawResult = string(body)`:
- BFL:404 早返回分支(216-220)前 + `json.Unmarshal(body, &pollResp)` 成功后(233 之后)。
- Replicate:404 早返回分支(271-275)前 + `json.Unmarshal(body, &predResp)` 成功后(286 之后)。

覆盖 succeed 与 failed 两种终态(有 body 即留存)。

### 改动 2:`relay/controller/video.go` —— 客户端查询路径落库

`invokeVideoAdaptorResult`(800-847),在 833-838 存 `store_url` 之后,补一段:

```go
if result.RawResult != "" {
    if err := dbmodel.UpdateVideoResult(taskId, result.RawResult); err != nil {
        log.Printf("Failed to update result for task %s: %v", taskId, err)
    }
}
```

与该路径既有的分离 helper 风格一致(`UpdateVideoStoreUrl` + `UpdateVideoTaskStatus`)。此路径为通用 VideoAdaptor 路径,仅 flux 会带非空 `RawResult`,其它 provider 不受影响。

### 改动 3:`controller/flux_video_reconciler.go` —— 对账器路径落库

对账器用 CAS `Updates(map)` 原子落库,直接把 `result` 并入同一 map(而非二次非 CAS 写):
- `succeedFluxVideoTask`:增加形参 `rawResult string`,`if rawResult != "" { updates["result"] = rawResult }`;调用点(148)传 `result.RawResult`。
- `failFluxVideoTask`:增加形参 `rawResult string`,写入 map;调用点两处——超时段(85,无上游查询,传 `""`)、对账段(150 附近,传 `result.RawResult`)。

## 影响范围

- **无 schema 变更**:`videos.result`(text)列已存在。
- **不影响真实扣费**:本次仅新增 `result` 列写入,不触碰 `quota`/`store_url`/`status` 逻辑。
- **不影响其它 provider**:改动 2 的通用路径仅对带 `RawResult` 的响应生效(当前只有 gemini/flux 设此字段,gemini 走独立分支不经过此处)。
- 存量任务 `result` 仍为空,仅对改动后新完成的任务生效(可接受)。

## 验证方式

1. `go build ./... && go vet ./...` 通过(提交前必跑)。
2. test2 环境跑一单 flux-3-video,查 DB:`SELECT task_id, status, store_url, length(result) FROM videos WHERE task_id='...'`,确认 `result` 非空且为合法上游 JSON(含 `status`/`result.sample`,若上游返回则含 `cost`)。
3. 从 Loki 核对无新增报错。
4. 更新 `docs/CHANGELOG.md` + 归档本计划到 `docs/plans/`。

## 后续(不在本次范围,单独决策)

拿到 `result` 里的上游原文后,可评估"视频计费改为优先上游 `cost`、缺失回退按秒表",与图片侧口径统一——本次先只做留存,不改计费口径。
