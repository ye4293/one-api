# Responses 来源未命中的最小修复

目标：避免自带消息及加密 reasoning 的 Responses 请求因为 Redis 未命中，在进入上游前永久重复 409；保留已有来源约束和历史数据。

## 方案

- 删除旧映射或所有未命中都随机选渠：会破坏服务端引用和压缩历史，不采用。
- 引入持久化来源库、复杂故障转移：超出本次最小修复范围。
- 本次采用：区分确定未命中、读取故障、无效记录；仅对有限的消息回放形态放宽未命中，原样送上游验证。

## 边界

- 仅 `/v1/responses`，无 `previous_response_id` 或服务端 conversation，input 包含消息及非空加密 reasoning，才具备放宽条件。允许消息、reasoning、function_call、function_call_output、custom_tool_call、custom_tool_call_output；拒绝 item_reference、compaction、未知项和只有 ID 的引用。存在消息不代表历史完整，网关不推断或补造缺失历史。
- X-Response-ID 仍参与查找及日志关联；header 单独出现不满足放宽条件。
- 只在 Redis 明确返回不存在时放宽。Redis 未启用、客户端缺失、连接/读取故障返回 503；无效或不完整来源仍拒绝。
- 放宽只清除当前选渠的查找参数，不改请求正文，不覆盖旧 ID 的来源。正常 relay 成功后沿用现有逻辑登记新 ID。
- 重试代码为当前渠道生成的无 ResponseID 约束快照，不得在关联日志中冒充非空历史 ID 的已知来源。
- 命中来源时保持渠道、区域、key 约束。禁用渠道、429 不删除来源映射；旧映射继续按已有 TTL 自然过期。
- Azure 明确返回加密内容 400 时沿用已有的一次降级重试；不新增重试机制，不扩展 compact/chat 行为。
- 本次不改线上 Redis、部署配置或历史数据，不处理独立的旧格式迁移和 key 索引问题。

## 验证

用真实 Gin 分发、SQLite 渠道及 miniredis 验证重复未命中可到达 relay、正文和关联 ID 保留、旧来源不被伪造；覆盖服务端引用、压缩项、header、Redis 故障、无效记录和已知来源。跑相关测试、全量测试、build、vet，提交独立分支。
