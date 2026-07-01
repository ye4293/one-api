# 更新记录 (CHANGELOG)

所有通过 Claude Code 辅助完成的代码变更必须记录在此文件中。

格式要求：每条记录包含日期、分支、变更类型、涉及文件和简要说明。

---

## 2026-07-01

### feat(audit): 为 /v1/messages (Claude 原生) 添加 audit 埋点
- **分支**: `AthenaQuery`
- **类型**: 新功能
- **涉及文件**: `relay/controller/claude.go`
- **说明**: 引入 `common/audit` 包，在 `RelayClaudeNative` 中调用 `SetMeta`（记录 isStream 和实际模型名）和 `SetConvertedBody`（记录转发给上游的请求体）；非流式路径在 `doNativeClaudeResponse` 中调用 `SetUpstreamResponse`；流式路径在 `doNativeClaudeStreamResponse` 中调用 `WrapUpstreamBody`，通过 TeeReader 透明捕获上游 SSE 数据。

---

## 2026-06-29

### feat(audit): 审计配置热重载，保存后无需重启服务
- **分支**: `AthenaQuery`
- **类型**: 新功能
- **涉及文件**: `common/audit/manager.go`, `model/option.go`, `common/audit/worker_test.go`
- **说明**: `manager.go` 用 mutex+`hasStarted` 替换 `sync.Once`，提取 `doStart`/`doStop`，新增 `Reload()`。`model/option.go` 在 `updateOptionMap` 里对 key `auditConfig` 触发 `go audit.Reload()`（goroutine 避免锁重入）。保存配置后审计模块自动停止旧实例并以新配置重启，无需重启进程。

### feat(audit): 将 9 个性能配置字段纳入 auditConfig JSON，支持前端覆盖
- **分支**: `AthenaQuery`
- **类型**: 新功能
- **涉及文件**: `common/audit/config.go`, `d:/my/ezlinkai-web/sections/setting/view/settingPage.tsx`
- **说明**: `loadConfig()` 新增解析 `channelSize`、`maxBufferMB`、`diskBufferDir`、`diskBufferMaxGB`、`batchSize`、`flushIntervalSec`、`maxBodyKB`、`maxRespKB`、`retentionDays` 9 个字段；整数 > 0 / 字符串非空时覆盖环境变量默认值。前端新增"高级性能配置"区块，留空则沿用默认值。



### fix(audit): 修复代码审查发现的 7 项数据完整性、竞态和性能问题
- **分支**: `AthenaQuery`
- **类型**: 修复
- **涉及文件**: `common/audit/awsclient.go`, `common/audit/compaction.go`, `common/audit/config.go`, `common/audit/query.go`, `common/audit/spill.go`, `common/audit/worker.go`, `main.go`
- **说明**: 代码审查后集中修复：(1) putRecordBatch 部分成功时只 spill 未发送记录防重复写入；(2) compaction goroutine 改用可取消 context；(3) XRequestID 正则放宽兼容非十六进制 ID；(4) spillStore 加 mutex + 原子 rename 防竞态；(5) AthenaDatabase/Table 标识符校验防 SQL 注入；(6) QueryLogs 用 COUNT(*) OVER() 合并为单次查询减半延迟；(7) 新增 AUDIT_RETENTION_DAYS 数据留存策略。

### fix(audit): 修复审计中间件吞没 panic 及 compaction 30 秒超时必败
- **分支**: `AthenaQuery`
- **类型**: 修复
- **涉及文件**: `middleware/audit.go`, `common/audit/athena.go`
- **说明**: (1) 审计中间件 defer 中捕获 panic 后未 re-panic，导致 Gin 的 RelayPanicRecover 无法触发——改为先完成审计采集再 re-panic；(2) compaction OPTIMIZE 查询需数分钟但 Athena 硬编码 30s 超时——改为从 context deadline 取值，compaction 传入 15 分钟超时。

### refactor(audit): compaction 从进程内定时器改为外部调度
- **分支**: `AthenaQuery`
- **类型**: 重构
- **涉及文件**: `common/audit/compaction.go`, `common/audit/manager.go`, `common/audit/config.go`, `main.go`
- **说明**: 将 Iceberg BIN_PACK compaction 从 audit 模块内部 goroutine 剥离，改为在 main.go 中由 `ENABLE_VIDEO_TASK_POLLER` 环境变量守卫的独立定时任务。保证多实例部署时只在一台机器上执行，消除 `AUDIT_COMPACTION_ENABLED` 配置项。
- **关联计划**: `docs/plans/2026-06-23-audit-compaction-externalize.md`

## 2026-06-22

### refactor(audit): 写入/查询层从 BigQuery 迁移到 Firehose + Iceberg + Athena
- **分支**: `AthenaQuery`
- **类型**: 重构
- **涉及文件**: `common/audit/config.go`、`common/audit/awsclient.go`（新）、`common/audit/athena.go`（新）、`common/audit/compaction.go`（新）、`common/audit/query.go`、`common/audit/serialize.go`、`common/audit/worker.go`、`common/audit/manager.go`、`go.mod`、`go.sum`；删除 `bqclient.go`、`bqclient_test.go`
- **说明**: 将审计模块整体从 GCP BigQuery 迁移到 AWS 原生栈（Firehose PutRecordBatch → Iceberg Table → S3 → Athena），消除跨云 egress 费用（~$184-368/月）。写入改用 JSON + Firehose PutRecordBatch（自动分片 500 条/4MB，部分失败重试）；查询改用 Athena 异步轮询（500ms 间隔，30s 超时）；SQL 注入防护用严格白名单正则校验；新增 Glue 自动建表（Iceberg 格式，day 分区 + x_request_id 排序）；新增每日 OPTIMIZE compaction。移除全部 GCP/BigQuery/protobuf 依赖。
- **关联计划**: `docs/plans/2026-06-22-audit-athena-migration.md`

### perf(audit): Clustering 首列改为 x_request_id
- **分支**: `bigQuery`
- **类型**: 性能优化
- **涉及文件**: `common/audit/bqclient.go`
- **说明**: 将 BigQuery 表 Clustering 字段顺序调整为 `[x_request_id, actual_model, channel_id, user_id]`，x_request_id 排首位以优化按请求 ID 精确查询的性能。

### feat(audit): 新增 BigQuery 审计查看器（后端 API + 前端页面）
- **分支**: `bigQuery`
- **类型**: 新功能
- **涉及文件**: `common/audit/query.go`、`controller/audit_viewer.go`、`router/api-router.go`、前端 `~/code/ezlinkai-web` 下 `app/dashboard/bigquery/`、`sections/bigquery/`、`constants/data.ts`、`components/icons.tsx`、`lib/searchparams.ts`
- **说明**: 新增管理后台审计查看页面。后端提供 `GET /api/audit/logs`（分页列表）和 `GET /api/audit/detail`（完整详情）两个 API，通过 BigQuery 参数化查询实现，强制日期范围（≤31天）确保分区裁剪控制成本。前端新增 Audit 页面（仅管理员可见），支持按 x_request_id 精确搜索、按日期范围分页浏览、查看完整请求/响应详情。
- **关联计划**: `docs/plans/2026-06-22-audit-bigquery-viewer.md`

### refactor(audit): 写入层从 GCS Load Job 迁移到 Storage Write API
- **分支**: `bigQuery`
- **类型**: 重构
- **涉及文件**: `common/audit/bqclient.go`、`common/audit/worker.go`、`common/audit/config.go`、`common/audit/manager.go`、`common/audit/bqclient_test.go`、`common/audit/worker_test.go`、`go.mod`、`go.sum`、`docs/plans/2026-06-10-audit-bigquery-design.md`、`docs/plans/2026-06-10-audit-bigquery-implementation.md`
- **说明**: 将审计数据写入通道从 GCS 上传 + BigQuery Load Job 替换为 BigQuery Storage Write API（`managedwriter` 包 DefaultStream + `EnableWriteRetries`），消除 Load Job 1,500 次/天/表的硬性配额限制和 `job.Wait()` 同步阻塞瓶颈。新增 protobuf 序列化（`dynamicpb` + `adapt` 包，无需 .proto codegen）；spill 落盘仍为 gzip NDJSON（调试友好），重放时转 proto→AppendRows。移除 GCS 直接依赖（`cloud.google.com/go/storage` 降级为 indirect）。建表新增 Clustering（`actual_model`、`channel_id`、`user_id`）免费优化查询。新增 4 个测试覆盖 proto 序列化和 spill 重放。
- **关联计划**: `docs/plans/2026-06-10-audit-bigquery-design.md`

## 2026-06-11

### fix(audit): Shutdown 等待 flush + 透传埋点改用 bytes 缓冲
- **分支**: `bigQuery`
- **类型**: fix
- **涉及文件**: `common/audit/manager.go`、`common/audit/worker_test.go`、`relay/controller/text.go`
- **说明**: 集成层评审遗留项收口。`Shutdown()` 关闭 `recordChan` 后阻塞等待 `ingestLoop` flush 完成（新增 `ingestDone` 信号），避免将来真有 graceful shutdown 时丢失内存尾批。经评估，审计为 best-effort 旁路功能，未引入进程级 SIGTERM 处理（blast radius 过大，与收益不匹配）——重启丢失最多 ~10s（默认 FlushInterval）内存尾批属可接受。`text.go` 透传分支改为仅在 `audit.Enabled()` 时读取原始体并用 `bytes.NewBuffer(raw)` 下发，保持审计关闭时零开销，且不依赖 `c.Request.Body` 读取后的状态。
- **关联计划**: `docs/plans/2026-06-10-audit-bigquery-implementation.md`

## 2026-06-10

### feat(audit): 模型调用全链路审计 → BigQuery
- **分支**: `bigQuery`
- **类型**: 新功能
- **涉及文件**: `common/audit/*`（config.go、record.go、redact.go、truncate.go、context.go、serialize.go、spill.go、bqclient.go、manager.go、worker.go、assemble.go 及对应测试）、`middleware/audit.go`、`middleware/recover.go`、`relay/controller/text.go`、`relay/channel/common.go`、`main.go`、`router/relay-router.go`
- **说明**: 新增与主业务解耦的审计模块，记录模型调用 6 类全链路数据（原始请求头/体、转换后请求头/体、上游响应、返回客户端响应），经脱敏（Authorization/Api-Key 等凭证）、截断（请求 10MB/响应 4MB）后批量写入 BigQuery。两级缓冲：内存（默认 1GB）满则落盘 NDJSON gzip（默认 40GB）经 GCS load job 入库。全程非阻塞 channel 投递 + 哑操作埋点，审计未启用（`AUDIT_ENABLED` 默认关闭）时零开销，任何初始化/运行失败自动降级，绝不阻断主请求。一期仅覆盖 `/completions`、`/chat/completions`。顺带修复 `middleware/recover.go` 既有的 non-constant format string vet 报错。
- **关联计划**: `docs/plans/2026-06-10-audit-bigquery-design.md`、`docs/plans/2026-06-10-audit-bigquery-implementation.md`

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
