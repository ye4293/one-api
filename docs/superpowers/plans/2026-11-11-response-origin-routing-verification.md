# 验证记录（2026-11-11）

- Base: 1b4ee436；修复分支 fix/response-origin-routing。
- go build ./... && go vet ./...：通过。
- 相关包 Response/Origin/Correlation/Log/Retry/ScanSSEOwned 回归，含 race：通过。
- go test ./...：未全绿；以下失败在修复前的 1b4ee436 已复现，无新增失败。

- `github.com/songquanpeng/one-api/common/image` / `TestDecode`
- `github.com/songquanpeng/one-api/common/image` / `TestDecode/Decode:jpeg`
- `github.com/songquanpeng/one-api/controller` / `TestEvaluateUsageBasedChannelDisable_MixedChannels`
- `github.com/songquanpeng/one-api/controller` / `TestEvaluateUsageBasedChannelDisable_SingleChannelTriggered`
- `github.com/songquanpeng/one-api/controller` / `TestFluxVideoUpscaleLifecycle`
- `github.com/songquanpeng/one-api/controller` / `TestFluxVideoUpscaleLifecycle/后台失败退款`
- `github.com/songquanpeng/one-api/controller` / `TestFluxVideoUpscaleLifecycle/后台完成`
- `github.com/songquanpeng/one-api/controller` / `TestFluxVideoUpscaleLifecycle/回调提交无轮询地址时后台查询`
- `github.com/songquanpeng/one-api/controller` / `TestFluxVideoUpscaleLifecycle/回调提交无轮询地址时客户端查询`
- `github.com/songquanpeng/one-api/controller` / `TestFluxVideoUpscaleLifecycle/客户端失败退款`
- `github.com/songquanpeng/one-api/controller` / `TestFluxVideoUpscaleLifecycle/客户端完成`
- `github.com/songquanpeng/one-api/controller` / `TestFluxVideoUpscaleLifecycle/本站回调失败退款`
- `github.com/songquanpeng/one-api/controller` / `TestFluxVideoUpscaleLifecycle/本站回调完成`
- `github.com/songquanpeng/one-api/relay/channel/anthropic` / `TestFilterBetaFlags`
- `github.com/songquanpeng/one-api/relay/channel/anthropic` / `TestFilterBetaFlags/vertex_allows_files-api`
- `github.com/songquanpeng/one-api/relay/channel/kling` / `TestGetModelNameFromRequest`
- `github.com/songquanpeng/one-api/relay/channel/kling` / `TestGetModelNameFromRequest/有model`

## 独立审阅与最后修复

独立审阅发现 5 项：已发布 Chat 流仍会重试、Redis 恢复后使用配置区域、登记开关失效、区域耗尽未写日志、前缀失败丢失已生成 ID。均已用独立失败测试复现，并修复至通过；相关 race、全量 build/vet 再次通过。最后一次全量测试仍仅包含上列基线失败。

## 行为边界与实施决定

- 历史来源未知或已知区域/类型候选耗尽时返回明确错误；不会退回跨区/跨类型随机选择。旧缓存且原渠道删除、无法补齐类型时可能要求重新开始会话。
- 首轮非 Azure 请求未固定区域时保留循环重试，但始终限定已选类型；携带父响应或固定区域时候选耗尽结束。
- 原渠道/Key 仍有效时，实际响应区域快照优先于后来改动的配置；其他候选使用声明的区域，配置需要准确描述后端。缓存不保存重配置前的 endpoint/API Key。
- 当前 ID 使用 XResponseID；上一轮及来源信息使用 Other.responseCorrelation；遵从用户最后指示，不修改表结构，不提供/执行 DDL，不新增全表 Other 检索。
- 流在发布后失败保留日志与可见 ID，不启动第二个上游响应。既有 late response.failed 解析与无关基线失败未夹带修改。
- OpenAI Chat/Responses 的发送前登记已修复；Gemini 原生既有异步窗口仍保留，签名来源约束和旧缓存格式读取保持兼容。
- 原渠道的模型能力禁用后不能绕过 abilities；service_tier 事件没有响应 ID 时仍独立更新计费状态。
- RESPONSES_AFFINITY_ENABLED 控制来源登记；关闭时不会创建新来源，读取已有来源和严格历史约束仍适用。
- 工作流脚本未标记可执行，使用 bash 而未改动技能文件；被忽略的 AGENTS.md 保留在本地，没有强制加入版本控制。

## 合并 main 的流处理冲突

- 合并目标：main `d9c736fd`，包含 `50d14d3a` 的大 SSE 前缀和流内错误修复。
- 新增 event-aware owned 扫描入口，保留 `event:` 名称与空行重置，同时保持所有 Gin、用量及发送回调在请求调用栈内运行。原 data-only 入口继续兼容 Chat 调用。
- Responses 同时保留 terminal/error 检测、发送前来源登记和失败登记补写；事件 JSON 缺少 type 时使用 SSE event 名称。
- 前缀元数据扫描与原始前缀缓冲共用 50 MiB 上限，避免大于 64 KiB 的合法 response.created 在错误日志中丢失已生成 ID。
- event-only 流内错误与大前缀 ID 丢失均 RED → GREEN；common、relay/controller 针对性回归通过。
- `go build ./... && go vet ./...` 通过；相关 7 包的 Response/Origin/Correlation/Retry/ScanSSEOwned/BufferStreamPrefix race 回归通过。
- `go test ./...` 仍只出现上列六组既有失败；本次合并未新增失败。
