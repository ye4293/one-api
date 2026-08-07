# Bedrock 渠道模型 diff 命名空间归一化

## 背景与目标

AWS Bedrock 渠道通过 `ListFoundationModels` 同步模型列表，返回 Bedrock 原生 ID（`anthropic.claude-opus-4-6-v1`）；本地渠道用规范短名（`claude-opus-4-6`）。relay 层 `AwsModelIDMap` 在请求时翻译，但 diff 函数做裸字符串比较，完全不知道这张映射表。结果：同一模型同时出现在「新增」和「待删」两侧。

顺带修复 Vertex AI 渠道只从 `publishers/google/models` 拉取、导致所有 `claude-*` 模型每轮巡检被判「待删」的安全隐患。

## 方案设计

1. 在 `relay/channel/aws/claude/canonical.go` 新增归一化函数 `CanonicalAwsModelID`、展示名函数 `AwsDisplayModelName`、辅助函数 `StripAwsRegionPrefix`，以及包级反向表 `awsCanonicalToDisplay`。
2. 在 `controller/channel_upstream_update.go` 新增 `upstreamModelCanonicalizers(channelType)` 和 `upstreamRemovalProtected(channelType, model)` 两个路由函数。
3. 重写 `upstreamCollectPendingChangesFromModels` 签名加首参 `channelType int`，内部用 canonical 域做集合比较，pendingAdd 用 display 名上报，pendingRemove 保持原始本地名。

## 影响范围

- diff 逻辑变更仅在 `channelType == ChannelTypeAwsClaude` 时生效，其余渠道走 identity 函数，行为逐字节一致。
- Vertex AI 渠道新增 `claude-*` 模型的删除豁免，不影响 gemini-* 模型的正常同步。
- 不修改数据库 schema，不改现有 API 签名，不影响前端。

## 验证方式

- `go build ./... && go vet ./...` 编译通过
- `go test ./relay/channel/aws/... ./controller/...` 全绿（含 20+ 新增 AWS/Vertex 用例）
- 部署后对 channel 59 执行 detect，确认 `remove_models` 为空，`add_models` 用短名形式

## 已知遗留

- `-thinking` 本地模型会抑制基础短名的新增（设计如此，不是 bug）
- Vertex AI 只拉 Google 发布者仍未真修（本次只加兜底，真修需同时请求 `publishers/anthropic/models`，留后续 PR）
- 表外模型（`anthropic.claude-opus-5` 等）仍会出现在 pendingAdd，靠探针的 403 verdict 拦住自动加入
