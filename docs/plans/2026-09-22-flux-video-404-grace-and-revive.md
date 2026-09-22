# flux-video：404 grace 窗口 + 成功覆盖失败并正确计费

- **日期**：2026-09-22
- **分支**：`flux-video`
- **类型**：fix（计费关键路径）

## 1. 背景与目标

测试环境高清（upscale）任务 `bf110e64-...` 复现出严重 bug：

1. 任务创建后 18s，reconciler 轮询 BFL `get_result` 拿到**瞬时 404 "Task not found"**（新建任务早期窗口不可查），`HandleVideoResult` 把**任意 404 一律判为终态 failed**，reconciler 随即退款。
2. 26s 后上游回调 `Ready`（真实 mp4，时长 36.76s）到达，但任务已是 `failed`，`handleVideoCallback` 首行终态短路 `already processed`，`ApplyVideoSuccess` 的 CAS 又要求 `status='processing'` —— **成功结果被静默丢弃，任务永久 failed**，且回调回 200 使上游不再重推。

在此之前该 404 分支的注释显示：曾从「404→processing→永久卡死不退款」改成「404→立即失败退款」，是一次从一个极端荡到另一个极端的过度修正，缺了「任务年龄 / 存活证据」维度。

**目标**（用户已确认）：
- (A) reconciler 与 get 两条路径，在 **10 分钟 grace 窗口内不把 404 "Task not found" 写成失败**（不落库该错误），视为瞬时 → 返回 processing 等待收敛。
- (B) **成功结果可以覆盖失败结果**（`failed→succeed` 复活），并**正确计费**（撤销失败退款后按上游口径重新结算）。

## 2. 方案设计

### 2.1 grace 窗口（覆盖 A）

关键点：reconciler 和 get **都** funnel 进 `flux.VideoAdaptor.HandleVideoResult`，故只需在这一处改，两条路径同时生效。

- 新增常量 `FluxVideoNotFoundGraceSecs = 600`（10 分钟），支持 env `FLUX_VIDEO_NOTFOUND_GRACE_SECS` 覆盖。选值远大于视频最大生成耗时、远小于 4h expire 兜底。
- 改 `relay/channel/flux/video_adaptor.go`：
  - BFL 分支（现 `:241` 404 处）：`if now - videoTask.CreatedAt < grace` → `TaskStatus="processing"`，**不写 Message/RawResult 的 404 body**，直接返回；否则维持原「404→failed+审计 body」。
  - Replicate 分支（现 `:309`）同样处理。
- 校验 get 的 processing 分支不会把 404 body 落库（`ApplyVideoProgress` 只在 RawResult 非空时写 result，grace 内返回空 RawResult 即可）。

行为：grace 内 404 → 任务保持 processing，等回调 / 下一轮轮询；grace 外 404 → 仍判失败退款；4h expire 兜底不变。

### 2.2 成功覆盖失败并正确计费（覆盖 B）

**账务基线**（Q0=预扣额，Q_final=最终结算额）：
- 创建：余额-Q0、token-Q0、used+Q0、req+1、channel+Q0。
- 失败退款后：余额0、token **-Q0（未还）**、used0、req0、channel0。
- 目标（=从未失败的成功路径）：余额-Q_final、token-Q_final、used+Q_final、req+1、channel+Q_final。

**复活 = 撤销退款回到预扣基线 + 复用现有差额结算**，逐项对齐目标（含 token）：
- Step A 撤销退款（`CompensateVideoTaskQuota` 的精确逆操作，不动 token）：余额-Q0、used+Q0、req+1、channel+Q0 → 回到预扣基线。
- Step B `settleVideoQuotaDiff(task, Q_final)`（此时内存 `task.Quota` 仍=Q0）：`PostConsumeTokenQuota(diff)` 令余额、token 各 -diff；used、channel 各 +diff。
- 合并后：余额-Q_final ✓ token-Q_final ✓ used+Q_final ✓ req+1 ✓ channel+Q_final ✓。Q_final==Q0 时 diff=0，退化为仅 Step A，同样对齐。

**代码改动**：
- `model/user.go`：新增 `ChargeVideoTaskQuota(userId, quota)` = Compensate 的逆（余额-Q、used+Q、req+1）。channel 侧复用现有 `UpdateChannelUsedQuota(channelId, +Q)`。
- `relay/channel/flux/video_billing.go`：`ApplyVideoSuccess` 改两段 CAS：
  1. `WHERE status='processing'` 命中 → `settleVideoQuotaDiff`（现有正常路径）。
  2. 否则 `WHERE status='failed'` 命中 → 复活：`ChargeVideoTaskQuota + UpdateChannelUsedQuota(+Q0)` 撤销退款，再 `settleVideoQuotaDiff(Q_final)`，追加「失败后复活」结算日志。
  3. 两次都未命中 → 读 DB 终态返回 `applied=false`（竞争落败，幂等）。
  幂等性由 `RowsAffected==1` 门控：failed→succeed 只发生一次，重复回调见 succeed 后早退。
- `relay/channel/flux/video_callback.go`：`handleVideoCallback` 首行短路由 `succeed || failed` 收窄为**仅 `succeed`**。failed 任务收到 Ready → 进 `ApplyVideoSuccess` 复活；收到 failed/progress 回调 → 各自 CAS(`status='processing'`) 落空，no-op，failed 不被改写。

## 3. 影响范围

- 无数据库 schema 变更（复用 `created_at`/`status`/`quota` 现有列）。
- 改变退款/结算时序行为：grace 内不再误退款；已 failed 任务可被成功回调/轮询复活并重新扣费。
- 存量已 failed 任务（如 bf110e64）：上游已回 200 不再重推，除非再次被查询触发轮询，否则维持 failed（本方案前向修复，不主动回捞历史）。
- 竞态：processing→failed 与 failed→succeed 并发时，两段 CAS 互斥，最终收敛「用户净付 Q_final」。

## 4. 待确认的既有问题（本次不改，仅记录）

失败退款 `CompensateVideoTaskQuota` 只还用户余额、**不还 token 余额**（限额 token 的 remaining 在视频失败后被永久少记）。这是既有行为，不在本次范围；本方案的复活路径已能正确对齐 token 终态，故不受影响。建议后续单独评估纯失败路径是否需补还 token。

## 5. 验证方式

- `go build ./... && go vet ./...`。
- 单测（`relay/channel/flux/video_billing_test.go` 同构补充）：
  - grace 内 404 → HandleVideoResult 返回 processing、不落库 404。
  - grace 外 404 → 返回 failed。
  - `ApplyVideoSuccess` 对 `failed` 任务复活：断言 status=succeed、store_url 写入，且余额/used/req/channel/token 五项终态 == 从未失败的成功路径。
  - 复活幂等：连续两次成功回调只扣一次。
- 复盘 bf110e64 时序：grace=10min 下，reconciler 在 18s 的 404 不再判失败 → Ready 回调正常结算。
