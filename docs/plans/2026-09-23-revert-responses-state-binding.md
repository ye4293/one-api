# 2026-09-23 紧急回滚：Responses Provider 状态绑定

## 背景与目标

上线 `413abc23`（合并 `fix/azure-responses-encrypted-content` 到 main，功能 commit `aace13ef`）后，
生产（main 分支，多副本 + 有 Redis）出现大面积 `openai response API /v1/responses` 报错：

> 状态索引暂时不可用，请稍后重试（HTTP 503，code=`responses_state_store_unavailable`）

现象：同一用户、同一渠道，`Consumption`（成功）与 `Error` 交替反复出现，token 多为 0。

目标：立即止血，恢复 `/v1/responses` 正常服务。

## 根因分析（止血阶段的初步结论，待深挖）

新功能在 `middleware/responses_binding.go` 对**每个** `/v1/responses` 请求强制执行
`ResolveResponsesConstraint`：凡带 `previous_response_id` / `conversation` /
`encrypted_content` / `item_reference` 的请求，都要在状态索引里查到其来源，
**查不到或查询出错即拒绝请求**（fail-closed）：

- `Lookup` 出错 → 503 `responses_state_store_unavailable`（`service/responses_state.go:172`）
- 非加密引用查不到 → 409 `responses_state_unknown`（`:180`）
- 写索引失败 → 503（`RegisterResponseOutput`，`:298`）

这是把"路由亲和优化"错误地做成了"请求准入硬门槛"，且**全程无 config 开关**可关闭。

生产为多副本 + 有 Redis（走 `redisResponseStateStore`，本应跨副本共享），
故根因不是"内存 store 多副本不共享"，更可能是以下之一，**需在修复分支上继续验证**：

- 登记（`RegisterResponseOutput`）与查询（`ResolveResponsesConstraint`）的 key 口径不一致，
  导致上一轮登记的状态下一轮永远查不到；
- Redis `Eval` 2s 超时 / 写入失败在高并发下频发；
- 登记时机（流式响应结束后）与下一轮请求存在竞态，状态尚未写入即被查询。

## 方案设计

`git revert -m 1 413abc23` —— 整体撤销该 merge，退回 merge 前状态。
删除全部状态绑定文件（`service/responses_state*.go`、`middleware/responses_binding.go`、
`model/responses_binding.go`、`model/channel_provider.go`、`relay/controller/responses_stream.go` 等），
并回退被其重写的核心链路文件（`opeai_response.go`、`distributor.go`、`relay.go`、`cache.go` 等）。

**代价（已知并接受）**：
1. `a308b5a0`（Azure 加密历史验证失败后清理并重试）随功能一并回退。
2. revert merge commit 后，`fix/azure-responses-encrypted-content` 修好后若想再合回 main，
   被 revert 的改动不会自动回来，需先 "revert the revert" 或重建分支。

## 影响范围

- 不涉及数据库 schema 变更，不涉及数据迁移。
- `/v1/responses` 退回无 Provider 约束的旧路由行为（与故障前一致）。
- 后续 flux-video 相关功能不受影响（revert 无冲突，`go build`/`go vet` 通过）。

## 验证方式

- [x] `git revert -m 1 413abc23` 无冲突。
- [x] `go build ./...` 通过。
- [x] `go vet ./...` 通过。
- [ ] push origin/main 后手动触发 main 构建/部署（CI 仅在 dev 触发，需确认 main 部署链路）。
- [ ] 部署后观察 `/v1/responses` 503/409 报错消失。

## 待办（根因修复）

在 `fix/azure-responses-encrypted-content` 或新分支上重做时，至少满足：
1. 加 config/env 开关，默认关闭，可灰度。
2. 改为 fail-open：状态索引查不到/查失败时放行（仅丢失路由亲和），不拒绝请求。
3. 补齐登记/查询 key 一致性与竞态的测试。
