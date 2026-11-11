# Responses 流内错误修复

用户授权：修复本次排障确认的问题。原始 response.created 单行 72,043 字节，超过前缀 Scanner 的 65,536 字节上限；扫描错误被忽略，额度不足未进入失败流程。

## 方案

仅扩大缓冲区不能覆盖扫描失败和前缀之后的错误。采用最小完整修复：前缀使用公共流扫描上限并检查读取错误；前缀与后续处理共用错误解析，公共扫描器新增保留 event 名称的兼容入口；后续错误返回失败且保留原始 SSE；已写出的响应记录失败后停止重试和追加 JSON。成功结算异步 context 使用既有 DetachCancel。

## 约束

- 保留现有前缀错误 429 语义及渠道错误处理、重试、禁用配置。
- 保留正常 SSE 的字节内容、usage 和计费行为。
- 解析 event 行和 JSON type，兼容 error 与 response.failed，保留上游 type/code/message。
- 超过公共 50 MiB 单行上限或读取异常必须返回 502，不得视为成功。
- 已发送 SSE 后发生错误不能拼接重试响应，不能写成功消费日志。
- 更新 AGENTS.md 排障教训；Go 修改提交前运行 go build ./... && go vet ./...。

## 验证

覆盖超过 64 KiB 的创建/进度事件后额度不足、正常超长事件无损重放、读取异常、公共上限溢出、data-only 错误、前缀之后错误无成功结算、已写响应不重试、取消后的成功结算指标。

基线 go test ./... 已失败：common/image TestDecode（外网 TLS 超时后空指针）、controller 用量禁用及 FluxVideoUpscaleLifecycle、anthropic TestFilterBetaFlags、kling TestGetModelNameFromRequest。修复不扩展到这些独立模块。
