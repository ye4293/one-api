# 响应来源关联、区域约束与日志修复

## 目标与已确认要求

基于 main@1b4ee436，修复审计指出的响应来源信息丢失、跨区域/类型选渠、日志无法串联和响应 ID 登记遗漏。客户继续使用已有协议，不增加必传会话字段。保留新合入的 Gemini 签名来源类型检查，更新 Response-ID 读写时兼容 Gemini/Claude 的既有缓存格式与 TTL；本次先登记后发送针对 OpenAI Chat/Responses 处理链路。

- 来源缓存保存本轮顶层响应 ID、原渠道 ID、原 Key 索引、实际区域和渠道类型。
- 已知来源的后续请求和所有重试只能选择同区域、同类型渠道；原区域候选耗尽立即结束。
- logs 同时保存当前顶层响应 ID 和上一轮 ID；Responses 的上一轮 ID 来自请求体 previous_response_id，Chat 来自请求头 X-Response-ID。
- 本轮新 ID 登记本轮来源，父响应映射不得被子请求成功回写覆盖。
- 在向客户发送含 ID 的响应前登记映射，覆盖普通文本、纯工具调用、非流式与流式响应。
- 无状态回放的 reasoning/compaction 条目 ID 同时登记，并关联其顶层响应 ID。
- /v1/responses/compact 采用相同关联解析。

## 方案与取舍

沿用现有 Redis 命名空间，值改为带版本的 JSON 来源快照，读取兼容历史单段、两段、三段格式。旧缓存缺少类型/区域时，只从仍存在的原渠道配置补齐；原渠道删除且来源无法恢复时不猜测类型。

采用统一的请求来源约束，首选和重试使用同一份快照。独立修改每个重试循环容易遗漏；继续只绑定 channel_id 则无法满足原渠道禁用后的同区域/类型兜底。

响应处理先解析、登记，再发布对应正文/事件。Redis 写入失败不把未写入标记为成功，完成事件再次尝试登记。失败不应被伪装为成功关联；下一轮无法确认来源时返回明确错误。

## 请求与日志语义

- /v1/responses 和 /compact：body.previous_response_id 是服务端历史引用；若 Header 同时存在且不同，拒绝冲突，避免路由和历史分别指向不同来源。
- /v1/chat/completions：入站 X-Response-ID 作为 previous_response_id 记录，当前顶层 id 继续存 x_response_id。
- 无状态回放：条目 ID 用于查来源，日志里的 previous_response_id 优先记录该来源的顶层响应 ID；旧条目映射未携带顶层 ID 时保留实际关联 ID，不凭前缀改造 ID。
- 成功和失败日志都记录上一轮 ID。流式已经生成当前响应 ID 后失败时保留当前 ID；完全没有生成响应则当前 ID 为空。
- 日志表结构不变：本轮顶层 ID 使用 XResponseID，其余信息保存到 Other 的 responseCorrelation JSON 段，包含 previous_response_id、origin_region、origin_channel_type、origin_channel_id、origin_key_index。通过现有 x_response_id 索引逐条回溯，不新增全表 Other 搜索。

## 来源未知与配置边界（方案中的行为选择）

已携带历史关联 ID，但缓存/原渠道无法恢复必要来源信息时返回明确来源错误；不退回无限制随机选渠。已知区域/类型却没有可用候选时返回渠道不可用错误，普通重试不能重置失败列表越界。

无历史引用的首轮可正常选渠；OpenAI Chat/Responses 选定后重试固定类型以及已知区域。Azure 缺少区域时停止跨渠道重试。无区域概念的类型固定类型即可。Gemini 签名约束继续使用已有来源类型检查，与新的 Response-ID 来源约束必须一致。区域候选判断基于渠道声明的 region；配置应准确描述渠道所代理的区域。

## 验证与交付

使用 SQLite、miniredis 和 httptest 对真实选择/登记/日志入口做回归测试。覆盖静态和动态优先级、来源渠道禁用/删除、配置区域与实际区域不同、非 Azure 类型约束、父映射保持、纯工具调用、快速续聊、登记失败恢复、rs_/compaction 回放及 compact 路由。

提交前执行 go build ./... && go vet ./...，运行针对性测试和 go test ./...。已有用量禁用、视频放大测试失败已在 66f9198e 复现，记录基线并报告，不夹带无关修复。按独立功能提交，最终进行独立代码审阅。
