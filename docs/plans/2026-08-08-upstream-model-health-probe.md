# 健康巡检：对本地已有模型做周期性可达性探测

## 背景与目标

现有探针只探 diff 出来的模型（`pending_add` / `pending_remove`），对稳定待在 `channel.Models` 里的模型从不做可达性检查。AWS Bedrock 的 `ListFoundationModels` 是目录 API，返回 region 内全部模型不看 IAM 权限，导致无权调用的模型永远不会进 `pendingRemove`、自动删除永远不会碰它。

**目标**：新增第三个探针场景 `health`，对本地已有模型按节奏做周期性真实请求；连续 N 次失败即自动删除；全部模型都失败时判为渠道级故障，禁用渠道但保留模型配置。

## 方案设计

### 状态机

每个模型存 `{Fails, Successes, LastProbe}`。`Successes < 3` → fast 间隔（10min）；`Successes >= 3` → steady 间隔（60min）。`alive` 加成功清失败，`not_found` 加失败清成功，其他 verdict 不动计数器。`Fails >= threshold` 进删除集。

### 全军覆没

tracked 全部达阈值 → 禁用渠道、不删模型、清零计数器。tracked = localModels 减去 ignored / removal-protected / pendingAdd / pendingRemove。

### 核心文件

- `controller/channel_upstream_health.go`（新建）：状态机纯函数 + 编排
- `controller/channel_upstream_health_test.go`（新建）：表驱动测试
- `common/config/channel_other_settings.go`：加 `ModelHealthState` 和 map 字段
- `common/config/config.go`：4 个新配置项
- `model/option.go`：InitOptionMap + updateOptionMap 注册
- `controller/channel_upstream_update.go`：hoist budget、插入健康巡检、合并删除集
- `controller/channel_upstream_probe.go`：加 `probeSceneHealth` 常量

## 影响范围

- 默认关闭（`UpstreamModelHealthProbeEnabled=false`），不影响现有行为
- 零数据库迁移（复用 `channels.settings` 列）
- 健康删除合并进 `approvedRemove`，继承全部现有 guard

## 验证方式

1. `go build ./... && go vet ./...`
2. `go test ./controller/... ./common/... ./model/...` 全绿
3. 测试服 channel 59 端到端验证
