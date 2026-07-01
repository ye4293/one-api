# 更新记录 (CHANGELOG)

所有通过 Claude Code 辅助完成的代码变更必须记录在此文件中。

格式要求：每条记录包含日期、分支、变更类型、涉及文件和简要说明。

---

## 2026-07-01

### feat(gemini): Omni 视频结果接口透传上游 usage

- **分支**: `gemini-omini`
- **类型**: feat
- **涉及文件**:
  - `relay/model/general.go`
  - `relay/channel/gemini/video_adaptor.go`
- **说明**: `/v1/video/generations/result` 对 gemini-omni 的响应新增 `usage` 字段，透传上游 Interactions API 返回的完整 token 用量（total_tokens / input / output / cached / thought / tool_use 及按模态拆分明细）。扩展 `VideoUsage` 结构承载 token 字段；`HandleVideoResult` 成功路径与缓存命中路径（`buildCachedVideoResponse`）均从 `result` JSON 还原 usage，保持响应格式一致。

### feat(gemini): Omni 视频改为按真实 token 用量计费

- **分支**: `gemini-omini`
- **类型**: feat
- **涉及文件**:
  - `common/video-pricing.go`
  - `relay/channel/gemini/video_adaptor.go`
  - `relay/channel/gemini/billing.go`（新增）
  - `relay/model/general.go`
  - `model/video.go`
  - `relay/controller/video.go`
  - `controller/gemini_video_poller.go`
- **说明**: 废弃创建时固定扣 $0.20 的占位计费，改为按 Gemini 官方 token 定价（输入 $1.50/M、输出文本 $9/M、输出视频 $17.50/M）。创建任务不扣费不记消费 log，任务成功完成后从上游 `usage` 解析真实 token 计数、计算 quota 并异步扣费记 log。并发安全参考 flux：`Video.UpdateIfNotTerminal` CAS（`WHERE status NOT IN ('succeed','failed')`）保证后台 poller 与用户主动查询两条路径只扣一次；CAS 赢得竞争后 goroutine 异步执行 `PostConsumeTokenQuota` 扣费（记入 Token 维度，token_id 来自 `videos.token_id`，创建时落库）并记消费 log（真实 PromptTokens/CompletionTokens）。`videos.quota` 记总费用、`videos.result` 记完整上游 JSON。失败任务不扣不退。创建时保留 $0.2 最低余额门槛（`GetPrePaymentQuota` 返回 $0.2 用于余额校验但不实际预扣）防透支；无 TokenId 时降级为只扣用户余额。
- **关联计划**: `docs/plans/2026-07-01-gemini-omni-usage-based-billing.md`
- **运维提示**: 已部署实例 DB 中旧的 `gemini-omni-flash-preview` fixed $0.20 定价规则不会自动删除（虽不再生效），建议在管理后台清理。

### fix(gemini): Omni 视频任务正确保存 prompt 至 videos 表
- **分支**: `gemini-omini`
- **类型**: fix
- **涉及文件**:
  - `relay/channel/interface.go`
  - `relay/channel/gemini/video_adaptor.go`
  - `relay/controller/video.go`
  - `relay/controller/directvideo.go`
  - `relay/controller/directvideo_xai.go`
- **说明**: `VideoTaskResult` 新增 `Prompt` 字段，Gemini adaptor 提交任务时回填 `req.Prompt`，`invokeVideoAdaptorRequest` 将其透传给 `CreateVideoLog`，使 `videos.prompt` 列记录真实用户输入（此前硬编码为字面量 `"prompt"`）。其余 9 处非 adaptor 调用点补 `""` 占位以修复因 `CreateVideoLog` 签名变更导致的编译失败。
- **关联计划**: `docs/plans/2026-07-01-gemini-prompt-persist.md`

### feat(gemini): Omni 视频查询结果落库到 videos.result 字段
- **分支**: `gemini-omini`
- **类型**: feat
- **涉及文件**:
  - `model/video.go`
  - `relay/channel/gemini/video_adaptor.go`
  - `controller/gemini_video_poller.go`
- **说明**: `FetchAndStoreVideoResult` 增加返回上游完整响应体 `rawJSON`；用户主动查询（`HandleVideoResult`）与后台 poller 各自将其写入 `videos.result` 字段。新增 `model.UpdateVideoResult` 方法。每次查询覆盖为最新一次上游响应。
- **关联计划**: `docs/plans/2026-07-01-gemini-prompt-persist.md`

---

## 2026-06-25

### fix(stream): 修复 wg.Add 竞态、跳过空 EndReason、补充 None 测试
- **分支**: `stream-status-port`
- **类型**: fix
- **涉及文件**:
  - `relay/helper/stream_scanner.go`
  - `relay/util/stream_status.go`
  - `relay/util/stream_status_test.go`
- **说明**: 将 `wg.Add(1)` 移至 `RelayCtxGo` 调用前，消除调度延迟导致的竞态；
  `AppendStreamStatusOther` 在 `EndReason == ""` 时提前返回，避免写入误导性 `"status":"error"` 记录；
  测试新增 `StreamEndReasonNone` 用例及 `TestAppendStreamStatusOther_NoneReasonSkipped`。

### feat(stream): 移植 StreamStatus 机制，持久化流式结束原因
- **分支**: `stream-status-port`
- **类型**: 新功能
- **涉及文件**:
  - `relay/util/stream_status.go`（新建）
  - `relay/util/stream_status_test.go`（新建）
  - `relay/util/relay_meta.go`
  - `relay/helper/stream_scanner.go`
  - `relay/controller/helper.go`
- **说明**: 从 new-api 完整移植 StreamStatus 机制。流式请求的 `logs.Other` 字段现在包含
  `streamStatus:{status, end_reason, end_error, errors}` 段，支持 done/timeout/client_gone/
  scanner_error/handler_stop/eof/panic/ping_fail 共 8 种结束原因。
- **关联计划**: `docs/superpowers/plans/2026-06-25-stream-status-port.md`

## 2026-06-11

### fix(anthropic): 更新 Vertex AI beta flags 白名单
- **分支**: `main`
- **类型**: fix
- **涉及文件**: `relay/channel/anthropic/beta.go`
- **说明**: 移除 Vertex 白名单中 5 个对应功能在 Vertex 上不支持的 flag（`mcp-client` x2、`files-api`、`code-execution`、`skills`），新增 3 个已验证支持的 flag（`compaction`、`context-editing`、`fallback-credit`）。经官方文档交叉验证。
- **关联计划**: 无

## 2026-06-09

### fix(streaming): SSE ping 格式改为 Claude 官方格式
- **分支**: `main`
- **类型**: fix
- **涉及文件**: `relay/channel/common.go`, `relay/helper/common.go`, `relay/helper/stream_scanner.go`
- **说明**: 将 ping 心跳从 SSE 注释格式 (`: PING`) 改为 Claude 官方格式 (`event: ping\ndata: {"type": "ping"}`)，与上游 Claude API 透传的 ping 保持一致。同时将 stream_scanner 中部分 println 调试日志改为 logger 正式日志。
- **关联计划**: 无

### feat(streaming): 等待上游响应期间发送 SSE ping 保活
- **分支**: `stream-ping`
- **类型**: 新功能
- **涉及文件**: `relay/channel/common.go`
- **说明**: 借鉴 new-api 实现，在 `DoRequest` 中增加 pre-request ping 机制。当流式请求等待上游（如 Claude thinking）响应时，定期发送 SSE 注释 (`: PING`) 防止中间代理层（ALB/nginx）误判连接空闲并断开。stop 函数同步等待 goroutine 退出，避免与后续 StreamScannerHandler 产生并发写入竞态。
- **关联计划**: 无（小功能，直接实现）

### refactor(logging): 改用原始 JSON 记录 message_delta 事件
- **分支**: `main`
- **类型**: 重构
- **涉及文件**: `relay/controller/claude.go`
- **说明**: 将 Claude 流式响应中 `message_delta` 的日志从仅记录 `stop_reason` 改为打印完整原始 JSON，便于排查 usage、output_tokens_details 等信息。

### feat(logging): Claude 流式响应增加 stop_reason 日志
- **分支**: `main`
- **类型**: 新功能
- **涉及文件**: `relay/controller/claude.go`
- **说明**: 在流式处理中记录 Claude 响应的 stop_reason 和 OutputTokens，用于排查客户反馈的 output_token 异常问题。
