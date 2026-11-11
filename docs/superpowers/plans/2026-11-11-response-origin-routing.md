# Response Origin Routing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复来源区域/类型约束、日志响应链和响应/推理条目 ID 登记。

**Architecture:** Redis 保存不可被子请求改绑的响应来源快照；请求进入时恢复统一约束并沿重试链携带；响应先登记后发送，日志分别保存当前/上一轮 ID。

**Tech Stack:** Go 1.25、Gin、GORM、Redis v8、SQLite、miniredis、httptest。

**Spec:** docs/superpowers/specs/2026-11-11-response-origin-routing-design.md

## Global Constraints

- 保留现有 API 协议与旧 Redis 值读取兼容。
- 历史来源未知不随机猜测；已知区域/类型候选耗尽结束请求。
- 父映射不得被子请求改绑；当前与上一轮响应 ID 分开记录。
- 每个独立功能提交前 go build ./... && go vet ./...。
- 不夹带基线中已存在的用量禁用、视频放大修复。
- 基于 1b4ee436，保留 Gemini 签名来源类型检查和 Gemini/Claude 缓存格式/TTL 兼容。

## Review Focus

- 旧格式缓存且原渠道删除：无法确认类型时返回明确来源错误。
- Header 与 body 指向不同父响应：拒绝冲突并记录实际输入父 ID。
- 缓存区域来自 x-ms-region、渠道 region 配置不同：所有重试使用来源快照。
- 纯工具调用或立即续聊：发送 ID 前映射可见。
- Redis 短暂失败：完成事件可补写，不能错误地保持已缓存标记。

### Task 1: 来源快照和严格选渠/重试

**Files:** model/response_region_cache.go、model/response_id_cache.go、model/response_region_select.go、model/cache.go、middleware/response_correlation.go、middleware/distributor.go、middleware/gemini_resource_affinity.go、controller/retry_policy.go；新增对应回归测试。

**Interfaces:** model.ResponseOrigin {ResponseID string, ChannelID int, KeyIndex int, Region string, ChannelType int}；model.LookupResponseOrigin(id string) (ResponseOrigin, error)；model.CacheResponseOrigin(id string, origin ResponseOrigin) error；上下文保存同一 ResponseOrigin 和规范父 ID，现有读取/写入包装保留兼容。

- [ ] 写并运行失败测试：复用审计的跨类型、区域耗尽、实际区域被配置覆盖案例；增加旧缓存原渠道删除、Header/body 冲突、失效 Key 和 compact 请求案例。
- [ ] 实现带版本来源值和兼容解析；原渠道有效时补齐旧值的类型/区域；来源不明返回错误。
- [ ] 统一首选与重试的类型/区域过滤，保留原 Key 复用；候选耗尽停止，覆盖静态与动态优先级。
- [ ] 保留 Gemini 签名检查；当签名来源与 Response-ID 类型约束冲突时返回明确错误，不能替换到约束之外的类型。
- [ ] Run: go test ./model ./middleware ./controller -run 'ResponseOrigin|ResponseCorrelation|Retry|Affinity' -count=1。Expected: 新回归和已有重试测试通过。
- [ ] Run: go build ./... && go vet ./...。Expected: exit 0；提交来源路由修复。

### Task 2: 日志当前/上一轮响应 ID

**Files:** model/log.go、controller/log.go、controller/retry_log.go、middleware/response_correlation.go、relay/controller/helper.go、relay/controller/opeai_response.go、controller/relay.go；新增日志写入/查询测试。

**Interfaces:** Log 表结构不变；Other.responseCorrelation 保存 previous_response_id 和来源信息；Log.XResponseID 继续表示本次顶层 id。请求进入时携带规范上一轮 ID；消费与错误写入入口读取该上下文，错误入口显式接收已生成当前 ID。

- [ ] 写并运行失败测试：Responses A→B 和 Chat Header A→B 在实际 logs 记录中同时保留两个 ID；流式失败保留已生成 ID；从 Other 取父 ID，再按 existing x_response_id 查询对应日志。
- [ ] 复用 Other 存关联 metadata，保留 existing x_response_id 查询，不修改表结构或添加 DDL；更新成功、最终失败及提前拒绝的日志调用，保持既有调用兼容。
- [ ] 删除成功请求对入站父 ID 的改绑；写失败测试证明子请求成功后原映射的区域、类型、渠道不变。
- [ ] Run: go test ./model ./controller ./middleware ./relay/controller -run 'Response|Correlation|Log|Retry' -count=1。Expected: 相关测试通过。
- [ ] Run: go build ./... && go vet ./...。Expected: exit 0；提交日志与父映射修复。

### Task 3: 响应和输出条目登记时机

**Files:** relay/channel/openai/main.go、relay/controller/opeai_response.go、relay/controller/azure_responses.go、model/response_region_cache.go；新增非流式/流式登记测试；更新 AGENTS.md 记录教训。

**Interfaces:** 登记入口接受顶层响应 ID、输出条目 ID 和同一来源快照，返回写入结果；只有写入成功才标记已缓存，完成事件允许补写。

- [ ] 写并运行失败测试：首次带 id 的工具调用事件、非流式顶层输出、reasoning/compaction 条目在发布前可查询；Redis 首次写失败后完成事件补写。
- [ ] 实现先解析登记后转发，覆盖顶层响应 ID 与输出条目 ID；同一响应登记保持来源一致，输出 payload 原样透传。
- [ ] Run: go test ./model ./middleware ./relay/channel/openai ./relay/controller ./controller -run 'Response|Origin|Correlation|Cache|Retry' -count=1。Expected: 所有新增相关测试通过。
- [ ] Run: go build ./... && go vet ./...。Expected: exit 0。
- [ ] Run: go test ./...；保存完整输出并对照基线，报告每个既有失败名称，要求无新增失败。
- [ ] 更新 AGENTS.md，提交登记修复；进行全分支独立审阅，按回归测试修复重要发现后交付。
