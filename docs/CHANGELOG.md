# 更新记录 (CHANGELOG)

所有通过 Claude Code 辅助完成的代码变更必须记录在此文件中。

格式要求：每条记录包含日期、分支、变更类型、涉及文件和简要说明。

---

## 2026-06-10

### feat(audit): 模型调用全链路审计 → BigQuery
- **分支**: `bigQuery`
- **类型**: 新功能
- **涉及文件**: `common/audit/*`（config.go、record.go、redact.go、truncate.go、context.go、serialize.go、spill.go、bqclient.go、manager.go、worker.go、assemble.go 及对应测试）、`middleware/audit.go`、`middleware/recover.go`、`relay/controller/text.go`、`relay/channel/common.go`、`main.go`、`router/relay-router.go`
- **说明**: 新增与主业务解耦的审计模块，记录模型调用 6 类全链路数据（原始请求头/体、转换后请求头/体、上游响应、返回客户端响应），经脱敏（Authorization/Api-Key 等凭证）、截断（请求 10MB/响应 4MB）后批量写入 BigQuery。两级缓冲：内存（默认 1GB）满则落盘 NDJSON gzip（默认 40GB）经 GCS load job 入库。全程非阻塞 channel 投递 + 哑操作埋点，审计未启用（`AUDIT_ENABLED` 默认关闭）时零开销，任何初始化/运行失败自动降级，绝不阻断主请求。一期仅覆盖 `/completions`、`/chat/completions`。顺带修复 `middleware/recover.go` 既有的 non-constant format string vet 报错。
- **关联计划**: `docs/plans/2026-06-10-audit-bigquery-design.md`、`docs/plans/2026-06-10-audit-bigquery-implementation.md`

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
