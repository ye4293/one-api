# flux-3-video 计费改为「以上游 cost 为准」+ 修 detail/details 字段名 bug

> 状态：已实现（分支 `flux-video`）。日志检索采用方案 B：`model/log.go` 从 ctx 通用
> `RequestIdKey` 读取 x_request_id，flux 提交预扣与完成差额两处在调 log 前用
> `context.WithValue` 把 task id 覆盖进 ctx，使同一任务多条日志共享检索键。

## Context（背景与目标）

flux-3-video 是**提交时预扣费**架构：提交时按 `CalculateVideoQuota`（按秒预估，如 HD 5s=$0.85）预扣，成功不再结算、失败全额退。现状两个问题：

1. **计费不以上游为准**：预扣是本地按秒预估。实测固定 duration 下预估==上游 cost，但 `duration:"auto"`/浮动时长场景，上游实际 cost 与预估会有差额，当前永不修正。上游 get_result **已返回权威 cost**（BFL 顶层 `cost:85.0` 美分；部分 Replicate 代理同样返回顶层 cost）。
2. **detail/details 字段名 bug**：`FluxVideoPollingResponse.Detail` 用 `json:"detail"`（单数），但上游返回 `details`（复数），导致审核原因（如 `Moderation Reasons`）丢失、客户端 message 显示 `<nil>`。

目标：任务完成时按上游权威 cost **多退少补**，差额记一条 ±quota 消费日志；并修 detail/details bug 让审核原因正确回显。

**关键前提（已核实）**：
- 视频侧预扣**不含 groupRatio**（`CalculateVideoQuota` 返回 `price×QuotaPerUnit`），故完成路径换算 cost→quota 同样**不乘 groupRatio**。
- `PostConsumeTokenQuota(tokenId, quota)` 原生支持正负：正=扣，负=退，一次调用搞定用户余额+token 双维度。
- `UpdateChannelUsedQuota` 用 `gorm.Expr("used_quota + ?")`，支持负数。

## 方案设计（实现结果）

### 1. 上游 cost 提取
- `relay/channel/flux/video_model.go`：`FluxVideoPollingResponse` 加 `Cost float64`（美分）+ `Details interface{}`。
- `relay/channel/flux/model.go`：`ReplicateResponse` 加 `Cost float64`（标准 replicate.com 无此字段→为 0）。
- `relay/model/general.go`：`GeneralFinalVideoResponse` 加内部字段 `UpstreamCost float64 json:"-"`。
- `relay/channel/flux/video_adaptor.go`：succeed 分支填 `UpstreamCost`（BFL `pollResp.Cost` / Replicate `predResp.Cost`）。**UpstreamCost>0 才触发多退少补；==0 保持预扣**。

### 2. detail/details 修复
- `video_adaptor.go` BFL 失败分支：message 优先取非 nil 的 `Details`，回退 `Detail`。

### 3. 差额结算函数（两路径共用，`flux/billing.go`）
`flux.SettleVideoCostDiff(ctx, videoTask, upstreamCostCents)`：
- `diff = VideoQuotaFromUpstreamCost(cost) - videoTask.Quota`；diff==0 或 cost<=0 直接返回。
- `PostConsumeTokenQuota(TokenId, diff)`（用户余额+token，正负通吃）。
- `UpdateUserUsedQuota(UserId, diff)`（不动 request_count，提交时已计一次）。
- `UpdateChannelUsedQuota(ChannelId, diff)`（支持负数）。
- 追加一条差额消费日志：`quota=±diff`，logContent 形如「上游 cost 结算：少补 +N / 多退 -N」。

### 4. 成功路径 CAS 化 + 结算（幂等核心）
差额结算只在「赢得 processing→succeed 转换」的唯一一次执行。
- **对账器** `succeedFluxVideoTask`：签名加 `upstreamCost`，`UpstreamCost>0` 时把 `quota=newQuota` 并入 CAS `Updates`，`RowsAffected==1` 后调 `SettleVideoCostDiff`。
- **客户端查询** `invokeVideoAdaptorResult`：succeed 且 `UpstreamCost>0` 时走 CAS（`Where status=processing`.Updates status+quota+store_url+result），赢家调 `SettleVideoCostDiff`。

### 5. logContent 显示修正
`handleSuccessfulResponseWithQuota`：`"模型固定价格"` 写死 `DefaultModelPrice` 改为按实际 quota 反算（`quota/QuotaPerUnit`）。

### 6. 日志按 task id 可检索（方案 B）
- `model/log.go` `recordVideoConsumeLog`：`x_request_id` 从 ctx 的 `RequestIdKey` 读取（取不到为空，行为与改动前一致）。
- flux 提交预扣（`video.go`，仅 `GetProviderName()=="flux"`）与完成差额（`billing.go`）在调 log 前 `context.WithValue(ctx, logger.RequestIdKey, taskId)`，使两条日志 `x_request_id=task id`，现有日志搜索框按 task id 即可一并搜出。

## 影响范围
- **无 schema 变更**：`videos.quota`/`result` 列已存在。
- **触碰真实扣费**：新增完成差额结算——最高风险项。缓解：UpstreamCost>0 且 diff!=0 才动，固定 duration 主路径 diff==0 完全不动；账户调整全走现有已验证 helper。
- **日志 x_request_id 连带**：`log.go` 改为从通用 `RequestIdKey` 读后，走通用视频预扣入口且 ctx 带真实 HTTP request id 的**其余 VideoAdaptor provider**，预扣日志 x_request_id 从空变为真实 HTTP request id（正向补齐，不改计费/状态）；`context.Background` 路径（directvideo 系）保持空。

## 验证方式
1. `go build ./... && go vet ./...` 通过。
2. test2 环境：固定 duration（diff=0 无多余日志）；`duration:"auto"`（DB quota→newQuota、logs 有 ±diff 日志、余额/渠道 used_quota 变动==diff）；对账器路径只结算一次；审核失败 message 带 Moderation Reasons、退 oldQuota；按 task id 搜出预扣+差额两条；Loki 无新增报错。
